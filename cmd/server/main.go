// Command server runs acquire: the requests + downloads addon for the zaentrum
// platform. Wires store + gateway + katalog + tmdb + Kafka consumer + SSE + HTTP.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/laedeli/acquire/internal/app"
	"github.com/laedeli/acquire/internal/config"
	"github.com/laedeli/acquire/internal/events"
	"github.com/laedeli/acquire/internal/gateway"
	"github.com/laedeli/acquire/internal/httpapi"
	"github.com/laedeli/acquire/internal/katalog"
	"github.com/laedeli/acquire/internal/prowlarr"
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
	pr := prowlarr.New(cfg.ProwlarrURL, cfg.ProwlarrAPIKey)
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
