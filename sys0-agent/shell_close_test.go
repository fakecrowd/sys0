package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/fakecrowd/sys0/internal/wire"
)

// newTestAgent builds an agent with no hub peer connected. currentPeer() returns
// nil, which the shell/task readers tolerate (they just buffer without emitting).
func newTestAgent() *Agent {
	return NewAgent(Config{}, "test-fingerprint", slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func openShell(t *testing.T, a *Agent) string {
	t.Helper()
	p, _ := json.Marshal(wire.ShellOpenParams{Cols: 80, Rows: 24})
	res, rerr := a.doShellOpen(p)
	if rerr != nil {
		t.Fatalf("doShellOpen: %v", rerr)
	}
	return res.(wire.ShellOpenResult).Session
}

// TestShellCloseNoRace is the regression guard for the agent-killing crash when
// closing a shell (notably the last one). The original bug: both closeShell and
// the reader goroutine called cmd.Wait()/ptmx.Close(), neither of which is
// concurrency-safe in go-pty, so two concurrent calls raced -> panic in an
// unrecovered goroutine -> the whole agent process died. With the fix the reader
// goroutine is the sole owner of cmd.Wait()+ptmx.Close() and closeShell only
// signals the process to exit. Run with -race to prove there is no data race.
func TestShellCloseNoRace(t *testing.T) {
	a := newTestAgent()

	// Open several shells, then close them all concurrently. Closing the LAST
	// shell while its reader is also tearing down is exactly the original crash
	// scenario; the -race detector catches any concurrent Wait/Close.
	const n = 8
	ids := make([]string, n)
	for i := range ids {
		ids[i] = openShell(t, a)
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			a.closeShell(id)
		}(id)
	}
	wg.Wait()

	// Give the reader goroutines a moment to run their (single) Wait()+Close()
	// and mark the sessions exited. No panic => the crash is fixed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		a.shells.mu.Lock()
		left := len(a.shells.sessions)
		a.shells.mu.Unlock()
		if left == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestShellCloseWhileReading closes a shell while its reader is actively
// streaming output (a busy loop), maximizing the overlap between the reader's
// Wait()/Close() and closeShell. This is the tightest reproduction of the
// double-Wait/double-Close window.
func TestShellCloseWhileReading(t *testing.T) {
	a := newTestAgent()
	id := openShell(t, a)

	// Drive some output through the PTY so the reader goroutine is busy.
	in, _ := json.Marshal(wire.ShellInputParams{Session: id, Data: "eWVzIHwgaGVhZCAtNTAwMAo="}) // "yes | head -5000\n"
	_, _ = a.doShellInput(in)

	time.Sleep(50 * time.Millisecond)
	a.closeShell(id) // overlaps the reader mid-stream

	time.Sleep(300 * time.Millisecond)
}
