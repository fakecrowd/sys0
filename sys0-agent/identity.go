package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const (
	idFile = "sys0-agent.id"
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
	// Create the id file ATOMICALLY. When several module agents (core/shell/fs/
	// screen) launch concurrently on a fresh, shared data-dir, a plain
	// read-then-write races: each reads the missing file and writes its OWN id,
	// so the modules end up with DIFFERENT fingerprints and the hub treats them
	// as separate nodes — defeating module aggregation. O_EXCL lets exactly one
	// writer win; the losers re-read the winner's id so every module converges
	// on the same fingerprint.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// Another module won the create race. It creates the (empty) file
			// THEN writes the id, so a loser racing in between can momentarily
			// read it empty — retry the read briefly until the winner's id lands.
			for i := 0; i < 50; i++ {
				if b, rerr := os.ReadFile(path); rerr == nil {
					if got := strings.TrimSpace(string(b)); len(got) >= 8 {
						return got, nil
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			return "", errors.New("id file exists but never became readable")
		}
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(id + "\n"); err != nil {
		return "", err
	}
	return id, nil
}

// acquireLock enforces a single instance PER MODULE per data dir. Modules share
// the same data dir (and the same sys0-agent.id identity) but each takes its own
// lock file (sys0-agent-<module>.lock), so core/shell/fs/screen can coexist in
// one data dir while still preventing two of the SAME module from running.
func acquireLock(dataDir, module string) (*flock.Flock, error) {
	if module == "" {
		module = "all"
	}
	fl := flock.New(filepath.Join(dataDir, "sys0-agent-"+module+".lock"))
	ok, err := fl.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("another sys0-agent instance is already running in this directory")
	}
	return fl, nil
}
