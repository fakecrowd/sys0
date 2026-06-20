//go:build !windows

package main

import "os/exec"

// hideWindow is a no-op on non-Windows platforms (there's no console window to
// hide). Present so callers can invoke it unconditionally.
func hideWindow(cmd *exec.Cmd) {}
