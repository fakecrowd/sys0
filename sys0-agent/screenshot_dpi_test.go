//go:build !modular || mod_screen

package main

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsPixelsConvertBGRAIntoOpaqueRGBA(t *testing.T) {
	got := windowsPixelsToRGBA([]byte{0x11, 0x22, 0x33, 0x00, 0xaa, 0xbb, 0xcc, 0x7f})
	want := []byte{0x33, 0x22, 0x11, 0xff, 0xcc, 0xbb, 0xaa, 0xff}
	if string(got) != string(want) {
		t.Fatalf("RGBA pixels = %v, want %v", got, want)
	}
}

func TestWindowsPixelBufferSizeRejectsInvalidBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height int
	}{
		{name: "zero width", width: 0, height: 1080},
		{name: "negative height", width: 1920, height: -1},
		{name: "overflow", width: int(^uint(0) >> 1), height: 2},
		{name: "DIB size overflow", width: 32768, height: 32768},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := windowsPixelBufferSize(tc.width, tc.height); err == nil {
				t.Fatalf("windowsPixelBufferSize(%d, %d) succeeded", tc.width, tc.height)
			}
		})
	}
}

func TestWindowsPixelBufferSizeAcceptsValidBounds(t *testing.T) {
	got, err := windowsPixelBufferSize(1920, 1080)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1920*1080*4 {
		t.Fatalf("buffer size = %d", got)
	}
}

func TestWindowsCaptureUsesNativeGDIWithoutPowerShell(t *testing.T) {
	source, err := os.ReadFile("screenshot_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{"GetSystemMetrics", "BitBlt", "GetDIBits", "SetThreadDpiAwarenessContext", "runtime.LockOSThread"} {
		if !strings.Contains(text, required) {
			t.Fatalf("native capture missing %s", required)
		}
	}
	if strings.Contains(strings.ToLower(text), "powershell") {
		t.Fatal("Windows screenshot capture must not depend on PowerShell")
	}
	if strings.Contains(text, "defer procSelectObject.Call(memoryDC, previous)") {
		t.Fatal("captured bitmap must be deselected before GetDIBits, not deferred")
	}
	restoreCall := "procSelectObject.Call(memoryDC, previous)"
	if strings.Count(text, restoreCall) < 2 {
		t.Fatal("captured bitmap needs both deferred cleanup and explicit deselection")
	}
	restore := strings.LastIndex(text, restoreCall)
	readback := strings.Index(text, "procGetDIBits.Call(")
	if restore < 0 || readback < 0 || restore > readback {
		t.Fatal("captured bitmap must be deselected before GetDIBits")
	}
}
