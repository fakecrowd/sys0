//go:build windows && (!modular || mod_screen)

package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	srcCopy           = 0x00CC0020
	captureBLT        = 0x40000000
	dibRGBColors      = 0
	biRGB             = 0
)

type bitmapInfoHeader struct {
	Size            uint32
	Width           int32
	Height          int32
	Planes          uint16
	BitCount        uint16
	Compression     uint32
	SizeImage       uint32
	XPelsPerMeter   int32
	YPelsPerMeter   int32
	ColorsUsed      uint32
	ColorsImportant uint32
}

type rgbQuad struct{ Blue, Green, Red, Reserved byte }
type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]rgbQuad
}

var (
	user32                            = windows.NewLazySystemDLL("user32.dll")
	gdi32                             = windows.NewLazySystemDLL("gdi32.dll")
	procSetThreadDpiAwarenessContext  = user32.NewProc("SetThreadDpiAwarenessContext")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC            = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC                      = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap        = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteObject                  = gdi32.NewProc("DeleteObject")
	procSelectObject                  = gdi32.NewProc("SelectObject")
	procBitBlt                        = gdi32.NewProc("BitBlt")
	procGetDIBits                     = gdi32.NewProc("GetDIBits")
)

func metric(index uintptr) int {
	value, _, _ := procGetSystemMetrics.Call(index)
	return int(int32(value))
}

func capturePNGWindows(ctx context.Context, _ string, _ int) ([]byte, string, error) {
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	default:
	}

	// DPI awareness is thread-scoped; keep all capture calls on that OS thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Per-monitor-v2 uses physical pixels. Prefer the thread-scoped API because
	// process awareness cannot be changed after another component has set it.
	const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	threadAware := false
	if err := procSetThreadDpiAwarenessContext.Find(); err == nil {
		previous, _, _ := procSetThreadDpiAwarenessContext.Call(perMonitorAwareV2)
		if previous != 0 {
			threadAware = true
			defer procSetThreadDpiAwarenessContext.Call(previous)
		}
	}
	if !threadAware {
		if err := procSetProcessDpiAwarenessContext.Find(); err == nil {
			procSetProcessDpiAwarenessContext.Call(perMonitorAwareV2)
		} else {
			procSetProcessDPIAware.Call()
		}
	}

	x, y := metric(smXVirtualScreen), metric(smYVirtualScreen)
	width, height := metric(smCXVirtualScreen), metric(smCYVirtualScreen)
	bufferSize, err := windowsPixelBufferSize(width, height)
	if err != nil {
		return nil, "", err
	}

	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return nil, "", fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, screenDC)

	memoryDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memoryDC == 0 {
		return nil, "", fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memoryDC)

	bitmap, _, _ := procCreateCompatibleBitmap.Call(screenDC, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, "", fmt.Errorf("CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(bitmap)

	previous, _, _ := procSelectObject.Call(memoryDC, bitmap)
	if previous == 0 {
		return nil, "", fmt.Errorf("SelectObject failed")
	}
	selected := true
	defer func() {
		if selected {
			procSelectObject.Call(memoryDC, previous)
		}
	}()

	copied, _, _ := procBitBlt.Call(
		memoryDC, 0, 0, uintptr(width), uintptr(height),
		screenDC, uintptr(x), uintptr(y), srcCopy|captureBLT,
	)
	if copied == 0 {
		return nil, "", fmt.Errorf("BitBlt failed")
	}
	// GetDIBits requires the bitmap not to be selected into a device context.
	restored, _, _ := procSelectObject.Call(memoryDC, previous)
	if restored == 0 {
		return nil, "", fmt.Errorf("restore SelectObject failed")
	}
	selected = false

	raw := make([]byte, bufferSize)
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(width), Height: -int32(height),
		Planes: 1, BitCount: 32, Compression: biRGB, SizeImage: uint32(len(raw)),
	}}
	lines, _, _ := procGetDIBits.Call(
		memoryDC, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&raw[0])),
		uintptr(unsafe.Pointer(&info)), dibRGBColors,
	)
	if int(lines) != height {
		return nil, "", fmt.Errorf("GetDIBits copied %d of %d rows", lines, height)
	}

	img := &image.NRGBA{Pix: windowsPixelsToRGBA(raw), Stride: width * 4, Rect: image.Rect(0, 0, width, height)}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, "", fmt.Errorf("encode capture: %w", err)
	}
	return out.Bytes(), "gdi32", nil
}
