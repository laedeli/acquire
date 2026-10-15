// Command server runs acquire: the requests + downloads addon for the zaentrum
// platform. Wires store + gateway + katalog + tmdb + Kafka consumer + SSE + HTTP.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/clock"
	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/httpapi"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/relay"
	"github.com/laedeli/acquire/internal/secretbox"
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
	br := sse.New()
	if retired := config.RetiredSearchEnv(); len(retired) > 0 {
		log.Printf("acquire: %s set but no longer read — add search sources in the console instead",
			strings.Join(retired, ", "))
	}

	// Credentials acquire stores (download client secrets, search source keys)
	// are sealed with ACQUIRE_CONFIG_KEY. Without a usable key acquire still
	// serves; it refuses to store secrets and setup says why. The error never
	// contains the key.
	box, err := secretbox.FromEnv()
	if err != nil {
		log.Printf("acquire: %v — credentials cannot be stored until this is fixed", err)
	} else if !box.Enabled() {
		log.Printf("acquire: ACQUIRE_CONFIG_KEY is not set — credentials cannot be stored")
	}

	svc := app.New(cfg, st, gw, kc, tm, br, box)

	// The search and grab policy used to be environment only; the environment
	// now seeds the stored row once, and the console edits it from there.
	if err := svc.SeedSettings(ctx); err != nil {
		log.Printf("acquire: could not seed search settings (environment defaults apply): %v", err)
	}
	// The download clients the deployment runs itself (ACQUIRE_MANAGED_CLIENTS)
	// are stored before the first push, so the gateway gets them with the rest.
	if cfg.ManagedClientsSet {
		res, err := svc.ApplyManagedClients(ctx)
		switch {
		case err != nil:
			log.Printf("acquire: the deployment's download clients were not applied: %v", err)
		default:
			log.Printf("acquire: the deployment's download clients: %s", res)
			for _, s := range res.Skipped {
				log.Printf("acquire: a download client of the deployment was not stored — %s", s)
			}
		}
	}
	// The gateway holds download clients in memory only: push acquire's stored
	// set now, after every change, and whenever the gateway falls behind.
	go svc.RunConfigSync(ctx)
	// Search source caps are cached; refresh the ones older than a day.
	go svc.RunSourceMaintenance(ctx)
	// Finished downloads outside the library's arrivals are copied in, handed to
	// the catalog and released in their clients (ACQUIRE_ARRIVALS_ROOT); a
	// target grab's files go over by identity, file by file.
	go svc.RunImports(ctx)
	// Grabs asked for in the console's missing tab, one at a time.
	go svc.RunTargetGrabs(ctx)
	// What the library holds, asked of katalog-api on the holdings-reconcile
	// schedule and whenever titles are imported or derived.
	go svc.RunHoldings(ctx)

	// The consumer starts at the latest offset, so downloads that began while
	// acquire was down would be invisible. Ask the gateway what is in flight.
	go svc.Reconcile(ctx)

	// Kafka consumer (download.client.* + catalog.item.* → svc reactions).
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
	// Register the recurring jobs. EnsureSchedule never disturbs an existing
	// row, so an operator's disable or interval survives a redeploy. The
	// consumer acts on schedule.due (OnScheduleDue): the sweep searches the
	// backlog (and grabs with search.autoGrab), the reconcile learns what the
	// library holds, the grab recovery wants failed targets again after their
	// wait and gives up grabs stuck in flight.
	for _, sc := range []struct {
		name  string
		every time.Duration
	}{
		{"backlog-sweep", 30 * time.Minute},
		{"retry-failed", 15 * time.Minute},
		{"holdings-reconcile", 6 * time.Hour},
		{"grab-recovery", time.Hour},
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
