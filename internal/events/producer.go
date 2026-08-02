package events

import (
	"context"
	"fmt"
	"strings"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

// This file is acquire's FIRST and ONLY Kafka producer. Until now the package
// was consume-only, by design and by comment.
//
// The taxonomy: <tenant-prefix>acquire.<domain>.<event>, three segments after
// the prefix. Facts are past tense (grabbed, imported, failed). Things we are
// asking someone to do end in .requested or .due — so a reader can tell from
// the name alone whether an event is a record or an instruction.
const (
	DomainRequest  = "request"
	DomainDownload = "download"
	DomainSearch   = "search"
	DomainSchedule = "schedule"
)

// Topic builds a fully-qualified topic name for the tenant prefix.
func Topic(prefix, domain, event string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "stube."
	}
	return prefix + "acquire." + domain + "." + event
}

// Producer publishes outbox rows. It is deliberately synchronous and fully
// acknowledged.
type Producer struct {
	w *kafka.Writer
}

// NewProducer builds the writer.
//
// Three settings here are load-bearing and all three differ from the library
// default:
//
//   - RequiredAcks: RequireAll. The kafka-go default is RequireNone, which
//     means Write returns success once the bytes leave the process. Every event
//     would be silently droppable — reproducing precisely the failure this
//     outbox exists to prevent, while looking like it worked.
//   - Async: false. An async writer reports success before the broker has the
//     record, so the relay would mark rows published that were never delivered.
//   - AllowAutoTopicCreation: false. Topics are created only by the
//     event-streaming pipeline. Auto-creating one here would produce a topic
//     with default partitioning and retention that nobody declared, and it
//     would mask a missing tenant CR until much later.
func NewProducer(brokers []string, certDir string) (*Producer, error) {
	if len(brokers) == 0 {
		return nil, nil // producing disabled; the relay no-ops
	}
	transport := &kafka.Transport{}
	if certDir != "" {
		tlsCfg, err := loadTLS(certDir)
		if err != nil {
			return nil, fmt.Errorf("kafka tls: %w", err)
		}
		transport.TLS = tlsCfg
	}
	return &Producer{w: &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Transport:              transport, // mTLS is required on the shared cluster
		Balancer:               &kafka.Hash{}, // key -> stable partition, so per-subject order holds
		RequiredAcks:           kafka.RequireAll,
		Async:                  false,
		AllowAutoTopicCreation: false,
		WriteTimeout:           15 * time.Second,
		BatchTimeout:           50 * time.Millisecond,
	}}, nil
}

// Publish writes a batch and returns only once the brokers have acknowledged
// all of it. A partial failure fails the whole batch: the relay leaves those
// rows pending and retries, which is safe because delivery is at-least-once and
// consumers are idempotent.
func (p *Producer) Publish(ctx context.Context, msgs []kafka.Message) error {
	if p == nil || p.w == nil || len(msgs) == 0 {
		return nil
	}
	return p.w.WriteMessages(ctx, msgs...)
}

func (p *Producer) Close() error {
	if p == nil || p.w == nil {
		return nil
	}
	return p.w.Close()
}
