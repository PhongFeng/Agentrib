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
	browserSocket := flag.String("qa-browser-mcp", "", "Connect stdio to the current QA browser (internal)")
	godotTool := flag.String("godot-mcp", "", "Forward the development workspace to Godot MCP (internal)")
	godotEngine := flag.String("godot-engine", "", "Godot executable for the current MCP session (internal)")
	godotProject := flag.String("godot-project", "", "Godot workspace for the current MCP session (internal)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	if *browserSocket != "" {
		err = runner.ConnectBrowserTools(ctx, *browserSocket, os.Stdin, os.Stdout)
	} else if *godotTool != "" {
		err = runner.ServeGodotMCP(ctx, *godotTool, *godotEngine, *godotProject, os.Stdin, os.Stdout)
	} else {
		err = run(ctx, *path, *check)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
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
