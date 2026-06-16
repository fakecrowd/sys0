package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	cfg := Config{}
	flag.StringVar(&cfg.Hub, "hub", "127.0.0.1:7000", "hub address host:port")
	flag.StringVar(&cfg.Transport, "transport", "tcp", "transport: tcp | ws")
	flag.StringVar(&cfg.Key, "key", "devkey", "pre-shared access key")
	flag.StringVar(&cfg.Label, "label", hostnameDefault(), "node label")
	flag.IntVar(&cfg.Heartbeat, "heartbeat", 15, "heartbeat interval seconds")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	log.Info("sys0-agent starting", "hub", cfg.Hub, "transport", cfg.Transport, "label", cfg.Label)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	NewAgent(cfg, log).Run(ctx)
}

func hostnameDefault() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "node"
	}
	return h
}
