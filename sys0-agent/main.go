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
	var dataDir string
	flag.StringVar(&cfg.Hub, "hub", "127.0.0.1:7000", "hub address host:port")
	flag.StringVar(&cfg.Transport, "transport", "tcp", "transport: tcp | ws")
	flag.StringVar(&cfg.Key, "key", "devkey", "pre-shared access key")
	flag.StringVar(&cfg.Label, "label", hostnameDefault(), "node label")
	flag.IntVar(&cfg.Heartbeat, "heartbeat", 15, "heartbeat interval seconds")
	flag.StringVar(&dataDir, "data-dir", ".", "run directory for the id/lock files")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Error("data dir", "err", err)
		os.Exit(1)
	}
	// single-instance lock: refuse to start if another agent owns this dir
	lock, err := acquireLock(dataDir)
	if err != nil {
		log.Error("startup", "err", err)
		os.Exit(1)
	}
	defer lock.Unlock()

	// stable per-host identity, generated once on first run
	fingerprint, err := loadOrCreateID(dataDir)
	if err != nil {
		log.Error("identity", "err", err)
		os.Exit(1)
	}

	log.Info("sys0-agent starting", "hub", cfg.Hub, "transport", cfg.Transport,
		"label", cfg.Label, "id", fingerprint[:8], "dataDir", dataDir)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	NewAgent(cfg, fingerprint, log).Run(ctx)
}

func hostnameDefault() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "node"
	}
	return h
}
