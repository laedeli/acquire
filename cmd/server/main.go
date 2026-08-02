// Command server runs acquire: the requests + downloads addon for the zaentrum
// platform. Wires store + gateway + katalog + tmdb + Kafka consumer + SSE + HTTP.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"os/signal"
	"syscall"
	"time"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/clock"
	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/httpapi"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/prowlarr"
	"github.com/laedeli/acquire/internal/relay"
	"github.com/laedeli/acquire/internal/sse"
	"github.com/laedeli/acquire/internal/store"
	"github.com/laedeli/acquire/internal/tmdb"
)

func main() {
	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("acquire: store: %v", err)
	}
	defer st.Close()

	tokens := gateway.NewTokenSource(cfg.SvcTokenURL, cfg.SvcClientID, cfg.SvcSecret)
	gw := gateway.New(cfg.GatewayURL, tokens)
	kc := katalog.New(cfg.ManagerURL, cfg.KatalogURL, tokens)
	tm := tmdb.New(cfg.TMDBAPIKey, cfg.TMDBLanguage)
	pr := prowlarr.New(cfg.IndexerURL, cfg.IndexerAPIKey)
	br := sse.New()

	svc := app.New(cfg, st, gw, kc, tm, pr, br)

	// The consumer starts at the latest offset, so downloads that began while
	// acquire was down would be invisible. Ask the gateway what is in flight.
	go svc.Reconcile(ctx)

	// Kafka consumer (download.client.* + catalog.item.packaged → svc reactions).
	if cons, err := events.NewConsumer(cfg.KafkaBrokers, cfg.KafkaCertDir, cfg.KafkaTopicPrefix, cfg.KafkaGroupID); err != nil {
		log.Printf("acquire: kafka consumer init failed (status reactions disabled): %v", err)
	} else if cons != nil {
		go func() {
			log.Printf("acquire: kafka consumer active (group=%s prefix=%s)", cfg.KafkaGroupID, cfg.KafkaTopicPrefix)
			if err := cons.Run(ctx, svc.Handlers()); err != nil {
				log.Printf("acquire: kafka consumer stopped: %v", err)
			}
		}()
	} else {
		log.Printf("acquire: no Kafka brokers — status reactions disabled")
	}

	// ── substrate ──────────────────────────────────────────────────────────
	// The outbox relay runs on EVERY replica (claims use SKIP LOCKED, so they
	// partition rather than race); the clock runs on all of them too but a
	// transaction-scoped advisory lock admits exactly one per tick.
	prod, err := events.NewProducer(brokersOf(cfg.KafkaBrokers), cfg.KafkaCertDir)
	if err != nil {
		log.Printf("acquire: kafka producer init failed (events stay in the outbox): %v", err)
	}
	if prod != nil {
		defer func() { _ = prod.Close() }()
	}
	go relay.New(st, prod, 2*time.Second).Run(ctx)
	// Register the recurring jobs later phases hang off. EnsureSchedule never
	// disturbs an existing row, so an operator's disable or interval survives a
	// redeploy. Nothing consumes schedule.due yet — this establishes the
	// cadence and the alerting surface before the work exists.
	for _, sc := range []struct {
		name  string
		every time.Duration
	}{
		{"backlog-sweep", 30 * time.Minute},
		{"retry-failed", 15 * time.Minute},
		{"outbox-audit", 6 * time.Hour},
	} {
		if err := st.EnsureSchedule(ctx, sc.name, sc.every); err != nil {
			log.Printf("acquire: could not register schedule %s: %v", sc.name, err)
		}
	}
	go clock.New(st, app.NewClockEmitter(cfg.KafkaTopicPrefix), 10*time.Second).Run(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.NewServer(cfg, svc, st, br).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Printf("acquire listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("acquire: listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("acquire: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// brokersOf splits the comma-separated KAFKA_BROKERS value the deployment sets.
func brokersOf(v string) []string {
	var out []string
	for _, b := range strings.Split(v, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}
