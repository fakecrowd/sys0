//go:build !windows && (!modular || mod_screen)

package main

import (
	"context"
	"fmt"
)

func capturePNGWindows(context.Context, string, int) ([]byte, string, error) {
	return nil, "", fmt.Errorf("Windows capture backend is unavailable on this platform")
}
