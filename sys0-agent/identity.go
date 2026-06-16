package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofrs/flock"
)

const (
	idFile   = "sys0-agent.id"
	lockFile = "sys0-agent.lock"
)

// loadOrCreateID returns a stable per-host identifier stored in dataDir,
// generating and persisting one on first run.
func loadOrCreateID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, idFile)
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); len(id) >= 8 {
			return id, nil
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// acquireLock enforces a single agent instance per data dir. The returned lock
// is held for the process lifetime; another running instance makes this fail.
func acquireLock(dataDir string) (*flock.Flock, error) {
	fl := flock.New(filepath.Join(dataDir, lockFile))
	ok, err := fl.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("another sys0-agent instance is already running in this directory")
	}
	return fl, nil
}
