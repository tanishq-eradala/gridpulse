package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"gridpulse/internal/telemetry"
)

func main() {
	path := os.Getenv("GRIDPULSE_DB_PATH")
	if path == "" {
		path = "gridpulse.db"
	}
	store, err := telemetry.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	api := telemetry.API{Store: store, Broker: telemetry.NewBroker(), AllowedOrigin: os.Getenv("GRIDPULSE_ALLOWED_ORIGIN")}
	addr := os.Getenv("GRIDPULSE_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	server := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("gridpulse API listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
