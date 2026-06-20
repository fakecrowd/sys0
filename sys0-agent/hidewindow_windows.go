//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow makes a child process run with NO visible console window. The agent
// runs in the background (often GUI-subsystem / no console), so spawning a
// console-subsystem helper like powershell.exe or cmd.exe would otherwise flash
// a black window on the user's screen. CREATE_NO_WINDOW + HideWindow suppress it.
func hideWindow(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000 // CREATE_NO_WINDOW
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
