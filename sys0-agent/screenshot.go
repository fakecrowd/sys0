package main

// screenshot.go — CGO-free screen capture for the agent.
//
// The agent is cross-compiled with CGO_ENABLED=0 for 6 platforms from one Linux
// runner, so we cannot use cgo-based capture libraries (kbinani/screenshot et al
// link X11/Cocoa via cgo). Instead we shell out to a capture tool that already
// ships with the OS (or is commonly present), write a PNG to a temp file, then
// do all resolution scaling + color compression in pure Go (image, image/jpeg,
// image/png). This keeps the static binary portable and the scaling/quality
// knobs identical across platforms.
//
//   Windows : PowerShell + System.Drawing (CopyFromScreen) — always available.
//   macOS   : /usr/sbin/screencapture -x — built in.
//   Linux   : first of grim (wlroots/Wayland), scrot, ImageMagick `import`,
//             gnome-screenshot, spectacle — whatever is installed.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"

	"golang.org/x/image/draw"
)

// doScreenshot captures the screen, optionally downscales to MaxWidth, and
// encodes as jpeg (with Quality) or png. All knobs are applied in pure Go so
// behaviour is identical regardless of which capture backend the OS used.
func (a *Agent) doScreenshot(params json.RawMessage) (any, *rpc.Error) {
	var p wire.ScreenshotParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}

	// Capture to a temp PNG (lossless source we then re-encode/scale ourselves).
	rawPNG, tool, err := capturePNG(p.Display)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "screenshot capture failed: %v", err)
	}

	img, _, derr := image.Decode(bytes.NewReader(rawPNG))
	if derr != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "decode capture: %v", derr)
	}

	// --- resolution: scale down to MaxWidth, preserving aspect ratio ---
	if p.MaxWidth > 0 {
		b := img.Bounds()
		if b.Dx() > p.MaxWidth {
			nh := int(float64(b.Dy()) * float64(p.MaxWidth) / float64(b.Dx()))
			if nh < 1 {
				nh = 1
			}
			dst := image.NewRGBA(image.Rect(0, 0, p.MaxWidth, nh))
			// CatmullRom = high quality downscale; good enough and fast for a screen.
			draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
			img = dst
		}
	}

	// --- color compression: encode jpeg(quality) or png ---
	format := p.Format
	if format == "" {
		format = "jpeg"
	}
	var buf bytes.Buffer
	switch format {
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, rpc.Errorf(rpc.CodeInternal, "png encode: %v", err)
		}
	case "jpeg", "jpg":
		format = "jpeg"
		q := p.Quality
		if q <= 0 {
			q = 80
		}
		if q > 100 {
			q = 100
		}
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
			return nil, rpc.Errorf(rpc.CodeInternal, "jpeg encode: %v", err)
		}
	default:
		return nil, rpc.Errorf(rpc.CodeBadParams, "unsupported format %q (jpeg|png)", p.Format)
	}

	bnd := img.Bounds()
	return wire.ScreenshotResult{
		Format: format,
		Width:  bnd.Dx(),
		Height: bnd.Dy(),
		Size:   int64(buf.Len()),
		Data:   base64.StdEncoding.EncodeToString(buf.Bytes()),
		Tool:   tool,
	}, nil
}

// capturePNG grabs the screen to PNG bytes using an OS-native tool. Returns the
// PNG bytes and the backend name used. display selects a monitor where the tool
// supports it (currently honoured on macOS via -D; other backends capture the
// full/virtual desktop and rely on MaxWidth for sizing).
func capturePNG(display int) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tmp, err := os.CreateTemp("", "sys0-shot-*.png")
	if err != nil {
		return nil, "", err
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	switch runtime.GOOS {
	case "windows":
		return capturePNGWindows(ctx, path, display)
	case "darwin":
		args := []string{"-x", "-t", "png"}
		if display >= 0 {
			args = append(args, "-D", strconv.Itoa(display+1)) // screencapture -D is 1-based
		}
		args = append(args, path)
		if out, err := exec.CommandContext(ctx, "/usr/sbin/screencapture", args...).CombinedOutput(); err != nil {
			return nil, "", fmt.Errorf("screencapture: %v: %s", err, out)
		}
		b, err := os.ReadFile(path)
		return b, "screencapture", err
	default: // linux & other unix
		return capturePNGLinux(ctx, path)
	}
}

// capturePNGWindows uses PowerShell + System.Drawing to grab the full virtual
// screen. No external install needed on any modern Windows.
func capturePNGWindows(ctx context.Context, path string, _ int) ([]byte, string, error) {
	ps := `Add-Type -AssemblyName System.Windows.Forms,System.Drawing;` +
		`$b=[System.Windows.Forms.SystemInformation]::VirtualScreen;` +
		`$bmp=New-Object System.Drawing.Bitmap($b.Width,$b.Height);` +
		`$g=[System.Drawing.Graphics]::FromImage($bmp);` +
		`$g.CopyFromScreen($b.X,$b.Y,0,0,$bmp.Size);` +
		`$bmp.Save('` + path + `',[System.Drawing.Imaging.ImageFormat]::Png);` +
		`$g.Dispose();$bmp.Dispose()`
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, "", fmt.Errorf("powershell capture: %v: %s", err, out)
	}
	b, err := os.ReadFile(path)
	return b, "powershell", err
}

// capturePNGLinux tries common CLI screenshot tools in order of preference,
// covering Wayland (grim) and X11 (scrot/import/gnome-screenshot/spectacle).
func capturePNGLinux(ctx context.Context, path string) ([]byte, string, error) {
	type backend struct {
		bin  string
		args []string
	}
	candidates := []backend{
		{"grim", []string{path}},
		{"scrot", []string{"-o", path}},
		{"import", []string{"-window", "root", path}}, // ImageMagick
		{"gnome-screenshot", []string{"-f", path}},
		{"spectacle", []string{"-b", "-n", "-o", path}},
		{"maim", []string{path}},
	}
	var tried string
	for _, c := range candidates {
		bin, err := exec.LookPath(c.bin)
		if err != nil {
			continue
		}
		tried = c.bin
		if out, err := exec.CommandContext(ctx, bin, c.args...).CombinedOutput(); err != nil {
			return nil, "", fmt.Errorf("%s: %v: %s", c.bin, err, out)
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, "", rerr
		}
		if len(b) == 0 {
			return nil, "", fmt.Errorf("%s produced an empty image", c.bin)
		}
		return b, c.bin, nil
	}
	if tried == "" {
		return nil, "", fmt.Errorf("no screenshot tool found (install one of: grim, scrot, imagemagick, gnome-screenshot, spectacle, maim)")
	}
	return nil, "", fmt.Errorf("screenshot failed via %s", tried)
}
