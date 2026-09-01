//go:build !modular || mod_screen

package main

import (
	"strings"
	"testing"
)

func TestWindowsScreenshotEnablesPhysicalPixelCoordinatesBeforeReadingBounds(t *testing.T) {
	script := windowsScreenshotPowerShell(`C:\Temp\shot.png`)

	awarenessAPI := strings.Index(script, "SetProcessDpiAwarenessContext")
	enableCall := strings.Index(script, "[Sys0Dpi]::Enable()")
	bounds := strings.Index(script, "SystemInformation]::VirtualScreen")
	if awarenessAPI < 0 || enableCall < 0 {
		t.Fatal("PowerShell capture must enable per-monitor DPI awareness")
	}
	if bounds < 0 {
		t.Fatal("PowerShell capture must read virtual-screen bounds")
	}
	if enableCall > bounds {
		t.Fatal("DPI awareness must be enabled before reading virtual-screen bounds")
	}
	if !strings.Contains(script, "SetProcessDPIAware") {
		t.Fatal("capture needs a fallback for Windows versions without per-monitor-v2 awareness")
	}
}
