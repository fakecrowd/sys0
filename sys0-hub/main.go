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

// envOr returns the value of env var key if set & non-empty, else fallback.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfg := HubConfig{}
	var jwtSecret string
	// Flags default to env vars (SYS0_*) so config can be set via environment;
	// an explicit flag still overrides the env value.
	flag.StringVar(&cfg.HTTP, "http", envOr("SYS0_HTTP", ":8080"), "HTTP listen addr (console + API + ws agents)")
	flag.StringVar(&cfg.AgentTCP, "agent-tcp", envOr("SYS0_AGENT_TCP", ":7000"), "TCP listen addr for agents")
	flag.StringVar(&cfg.AccessKey, "key", envOr("SYS0_AGENT_KEY", "devkey"), "pre-shared agent access key (env SYS0_AGENT_KEY)")
	flag.StringVar(&cfg.DBPath, "db", envOr("SYS0_DB", "sys0.db"), "SQLite database path")
	flag.StringVar(&jwtSecret, "jwt-secret", envOr("SYS0_JWT_SECRET", ""), "JWT signing secret, random if empty (env SYS0_JWT_SECRET)")
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

	// Account storage is DB-backed. By default the FIRST visit to the console
	// runs an interactive setup (create the first admin). For unattended
	// deploys you may optionally seed an admin via SYS0_ADMIN_USER/PASS — but
	// only if BOTH are set AND no user exists yet.
	if hub.store.CountUsers() == 0 {
		su, sp := os.Getenv("SYS0_ADMIN_USER"), os.Getenv("SYS0_ADMIN_PASS")
		if su != "" && sp != "" {
			if err := hub.store.EnsureUser(su, sp, "admin"); err != nil {
				log.Error("seed admin", "err", err)
				os.Exit(1)
			}
			log.Info("seeded admin from env", "user", su)
		} else {
			log.Info("no users yet — first-run setup required at the console")
		}
	}

	// Keep the release binaries hot in the hub's in-memory cache so node
	// downloads are served from memory, never blocked on a slow hub->GitHub pull.
	go startBinaryWarmer(log)

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
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("http server", "err", err)
		os.Exit(1)
	}
}
