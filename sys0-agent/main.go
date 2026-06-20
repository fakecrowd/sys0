package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
)

// Build-time defaults. These are overridable at link time via
//   -ldflags "-X main.defaultHub=host -X main.defaultTransport=wss -X main.defaultKey=..."
// The bundled agent (shipped inside the hub release tarball) keeps the
// localhost/tcp values so self-hosters get a sane local default. The STANDALONE
// agent published on the hub's /dl page is built with the hosted hub's values
// baked in, so a zero-arg launch (e.g. a double-click) connects straight to it.
var (
	defaultHub       = "127.0.0.1:7000"
	defaultTransport = "tcp"
	defaultKey       = "devkey"
)

func main() {
	cfg := Config{}
	var dataDir string
	flag.StringVar(&cfg.Hub, "hub", defaultHub, "hub address host:port (or host for ws/wss)")
	flag.StringVar(&cfg.Transport, "transport", defaultTransport, "transport: tcp | ws | wss")
	flag.StringVar(&cfg.Key, "key", defaultKey, "pre-shared access key")
	flag.StringVar(&cfg.Label, "label", hostnameDefault(), "node label")
	flag.IntVar(&cfg.Heartbeat, "heartbeat", 15, "heartbeat interval seconds")
	flag.StringVar(&dataDir, "data-dir", defaultDataDir(), "run directory for the id/lock files")
	flag.Parse()

	// dataDir must exist before we can place a log file inside it (Windows GUI
	// builds have no console, so logs go to a file there — see newLogger).
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		// No logger yet; nothing we can usefully do but exit.
		os.Exit(1)
	}

	log := newLogger(dataDir)
	// single-instance lock: refuse to start if another agent owns this dir
	lock, err := acquireLock(dataDir, module)
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

	log.Info("sys0-agent starting", "version", version, "hub", cfg.Hub, "transport", cfg.Transport,
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

// defaultDataDir picks a stable, writable per-user location for the agent's
// identity/lock files. This matters for double-click launches, where the
// working directory may be read-only or non-obvious. Falls back to "." if the
// user config dir cannot be resolved.
func defaultDataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		return "."
	}
	return base + string(os.PathSeparator) + "sys0-agent"
}
