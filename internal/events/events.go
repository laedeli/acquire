// Package events is acquire's Kafka wiring on the shared cluster (mTLS). It is
// CONSUME-ONLY: download.client.completed/failed advance request status +
// trigger ingest, and catalog.item.packaged marks fulfillment. It does NOT emit
// catalog.item.discovered itself — katalog-manager's POST /api/ingest owns that
// (the item creator emits discovered, exactly as the scanner does). Topic names
// carry the tenant prefix (KAFKA_TOPIC_PREFIX).
package events

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

// Topics resolved from the tenant prefix.
type Topics struct {
	Started   string
	Progress  string
	Completed string
	Failed    string
	Packaged  string
}

func TopicsFor(prefix string) Topics {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "stube."
	}
	return Topics{
		Started:   prefix + "download.client.started",
		Progress:  prefix + "download.client.progress",
		Completed: prefix + "download.client.completed",
		Failed:    prefix + "download.client.failed",
		Packaged:  prefix + "catalog.item.packaged",
	}
}

// ItemEvent is the catalog pipeline envelope (mirrors the scanner/worker shape).
// Consumers require only itemId; producers emit the full shape.
type ItemEvent struct {
	EventID    string `json:"eventId"`
	ItemID     string `json:"itemId"`
	Type       string `json:"type,omitempty"`
	Step       string `json:"step,omitempty"`
	Status     string `json:"status,omitempty"`
	OccurredAt string `json:"occurredAt,omitempty"`
	Source     string `json:"source,omitempty"`
}

// DownloadEvent is the subset of the gateway's download.client.* payloads we
// read. One shape covers started/progress/completed/failed: the fields a given
// kind doesn't carry simply stay zero.
type DownloadEvent struct {
	ClientID string   `json:"client_id"`
	Adapter  string   `json:"adapter"`
	WantedID string   `json:"wanted_item_id"`
	Title    string   `json:"title"`
	Files    []string `json:"files"`
	Error    string   `json:"error"`

	// Progress telemetry. SpeedBps is a plain number because 0 means idle;
	// size/eta stay pointers because those can genuinely be unknown.
	State       string  `json:"state"`
	NativeState string  `json:"native_state"`
	ProgressPct float64 `json:"progress_pct"`
	Downloaded  int64   `json:"downloaded_bytes"`
	SizeBytes   *int64  `json:"size_bytes"`
	SpeedBps    int64   `json:"speed_bps"`
	EtaSec      *int32  `json:"eta_sec"`
	Seeders     *int32  `json:"seeders"`
	Leechers    *int32  `json:"leechers"`
	Health      *int32  `json:"health"`
}

func loadTLS(certDir string) (*tls.Config, error) {
	certDir = strings.TrimSpace(certDir)
	if certDir == "" {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(certDir, "user.crt"), filepath.Join(certDir, "user.key"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	ca, err := os.ReadFile(filepath.Join(certDir, "ca.crt"))
	if err != nil {
		return nil, err
	}
	if !pool.AppendCertsFromPEM(ca) {
		return nil, errors.New("append CA failed")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// Consumer reads download.client.completed/failed + catalog.item.packaged.
type Consumer struct {
	brokers []string
	tlsCfg  *tls.Config
	group   string
	topics  Topics
}

func NewConsumer(brokers, certDir, prefix, group string) (*Consumer, error) {
	if strings.TrimSpace(brokers) == "" {
		return nil, nil
	}
	tlsCfg, err := loadTLS(certDir)
	if err != nil {
		return nil, err
	}
	return &Consumer{
		brokers: strings.Split(brokers, ","),
		tlsCfg:  tlsCfg,
		group:   group,
		topics:  TopicsFor(prefix),
	}, nil
}

// Handlers is the domain callback set the consumer drives.
type Handlers struct {
	OnStarted   func(ctx context.Context, ev DownloadEvent) error
	OnProgress  func(ctx context.Context, ev DownloadEvent) error
	OnCompleted func(ctx context.Context, ev DownloadEvent) error
	OnFailed    func(ctx context.Context, ev DownloadEvent) error
	OnPackaged  func(ctx context.Context, ev ItemEvent) error
}

// Run consumes until ctx is cancelled. Per-message errors are logged by the
// caller's handlers; a decode failure is skipped (poison-safe).
func (c *Consumer) Run(ctx context.Context, h Handlers) error {
	dialer := &kafka.Dialer{Timeout: 10 * time.Second, DualStack: true, TLS: c.tlsCfg}
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers: c.brokers,
		GroupID: c.group,
		GroupTopics: []string{
			c.topics.Started, c.topics.Progress,
			c.topics.Completed, c.topics.Failed, c.topics.Packaged,
		},
		Dialer:      dialer,
		StartOffset: kafka.LastOffset, // only new events; history isn't replayable state
	})
	defer r.Close()
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			time.Sleep(time.Second)
			continue
		}
		switch msg.Topic {
		case c.topics.Started:
			var ev DownloadEvent
			if json.Unmarshal(msg.Value, &ev) == nil && h.OnStarted != nil {
				_ = h.OnStarted(ctx, ev)
			}
		case c.topics.Progress:
			var ev DownloadEvent
			if json.Unmarshal(msg.Value, &ev) == nil && h.OnProgress != nil {
				_ = h.OnProgress(ctx, ev)
			}
		case c.topics.Completed:
			var ev DownloadEvent
			if json.Unmarshal(msg.Value, &ev) == nil && h.OnCompleted != nil {
				_ = h.OnCompleted(ctx, ev)
			}
		case c.topics.Failed:
			var ev DownloadEvent
			if json.Unmarshal(msg.Value, &ev) == nil && h.OnFailed != nil {
				_ = h.OnFailed(ctx, ev)
			}
		case c.topics.Packaged:
			var ev ItemEvent
			if json.Unmarshal(msg.Value, &ev) == nil && h.OnPackaged != nil {
				_ = h.OnPackaged(ctx, ev)
			}
		}
	}
}
