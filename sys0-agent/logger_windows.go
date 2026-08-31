//go:build windows

package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// newLogger configures logging for Windows. The standalone /dl agent is linked
// with -H=windowsgui so it runs without a console window; in that mode writes
// to os.Stderr go nowhere. To keep the agent diagnosable, we always tee logs to
// <dataDir>/sys0-agent.log (rotated by truncation on each start to bound size),
// and also to stderr when a console happens to be attached (e.g. run from a
// shell or the console-subsystem bundled build).
func newLogger(dataDir string) *slog.Logger {
	var w io.Writer = os.Stderr

	logPath := filepath.Join(dataDir, "sys0-agent.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); err == nil {
		// Tee so that a console (if any) and the log file both receive output.
		w = io.MultiWriter(os.Stderr, f)
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
