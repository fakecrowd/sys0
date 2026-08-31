//go:build !windows

package main

import (
	"log/slog"
	"os"
)

// newLogger configures logging on non-Windows platforms: plain stderr. These
// builds always have a usable stderr (run from a shell or under a supervisor),
// so no log-file fallback is needed.
func newLogger(_ string) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
