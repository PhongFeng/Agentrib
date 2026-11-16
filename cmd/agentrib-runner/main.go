package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/PhongFeng/Agentrib/internal/runner"
)

func main() {
	path := flag.String("config", "runner.json", "Runner configuration path")
	check := flag.Bool("check", false, "Report discovered executors and exit")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *path, *check); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("runner stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, path string, check bool) error {
	cfg, err := runner.Load(path)
	if err != nil {
		return err
	}
	r := runner.New(cfg)
	if check {
		return json.NewEncoder(os.Stdout).Encode(r.Detect(ctx))
	}
	return r.Run(ctx)
}
