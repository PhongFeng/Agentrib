package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/PhongFeng/Agentrib/internal/control"
	"github.com/PhongFeng/Agentrib/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	database := os.Getenv("AGENTRIB_DATABASE_URL")
	operator := strings.TrimSpace(os.Getenv("AGENTRIB_OPERATOR_TOKEN"))
	if database == "" || len(operator) < 32 {
		return errors.New("set AGENTRIB_DATABASE_URL and a 32+ character AGENTRIB_OPERATOR_TOKEN")
	}
	addr := os.Getenv("AGENTRIB_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:8765"
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	openCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	db, err := store.Open(openCtx, database)
	cancel()
	if err != nil {
		return err
	}
	defer db.Close()
	server := &http.Server{Addr: addr, Handler: control.New(db, operator), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10}
	go sweep(ctx, db)
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	slog.Info("control plane listening", "address", addr)
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func sweep(ctx context.Context, db *store.Store) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check, cancel := context.WithTimeout(ctx, 8*time.Second)
			if err := db.Sweep(check); err != nil && ctx.Err() == nil {
				slog.Error("sweep expired leases", "error", err)
			}
			if err := db.Schedule(check); err != nil && ctx.Err() == nil {
				slog.Error("advance workflows", "error", err)
			}
			cancel()
		}
	}
}
