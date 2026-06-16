package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// version is the build version (yyyyMMddhhmm), injected via -ldflags.
var version = "dev"

func main() {
	cfg := HubConfig{}
	var adminPass, jwtSecret string
	flag.StringVar(&cfg.HTTP, "http", ":8080", "HTTP listen addr (console + API + ws agents)")
	flag.StringVar(&cfg.AgentTCP, "agent-tcp", ":7000", "TCP listen addr for agents")
	flag.StringVar(&cfg.AccessKey, "key", "devkey", "pre-shared agent access key")
	flag.StringVar(&cfg.DBPath, "db", "sys0.db", "SQLite database path")
	flag.StringVar(&adminPass, "admin-pass", "admin", "initial admin password")
	flag.StringVar(&jwtSecret, "jwt-secret", "", "JWT signing secret (random if empty)")
	flag.Parse()

	if jwtSecret == "" {
		b := make([]byte, 16)
		rand.Read(b)
		jwtSecret = hex.EncodeToString(b)
	}
	cfg.JWTSecret = jwtSecret

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	hub, err := NewHub(cfg, log)
	if err != nil {
		log.Error("init hub", "err", err)
		os.Exit(1)
	}
	if err := hub.store.EnsureUser("admin", adminPass, "admin"); err != nil {
		log.Error("seed admin", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := hub.runAgentTCP(); err != nil && ctx.Err() == nil {
			log.Error("agent gateway", "err", err)
		}
	}()

	srv := &http.Server{Addr: cfg.HTTP, Handler: hub.Router()}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		srv.Shutdown(sctx)
	}()

	log.Info("sys0-hub listening", "version", version, "http", cfg.HTTP, "agentTCP", cfg.AgentTCP)
	log.Info("default login", "user", "admin", "pass", adminPass)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("http server", "err", err)
		os.Exit(1)
	}
}
