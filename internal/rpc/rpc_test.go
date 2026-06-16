package rpc

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fakecrowd/sys0/internal/transport"
)

// TestPeerCallAndNotify exercises request/response and notification delivery
// over a loopback transport.
func TestPeerCallAndNotify(t *testing.T) {
	ca, cb := transport.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var gotNotify atomic.Int64

	// server side: handles "add", records "ping" notifications.
	server := NewPeer(cb, func(ctx context.Context, method string, params json.RawMessage) (any, *Error) {
		switch method {
		case "add":
			var p struct{ A, B int }
			json.Unmarshal(params, &p)
			return map[string]int{"sum": p.A + p.B}, nil
		case "boom":
			return nil, Errorf(4242, "kaboom")
		default:
			return nil, Errorf(CodeNoMethod, "no method")
		}
	}, func(method string, params json.RawMessage) {
		if method == "ping" {
			gotNotify.Add(1)
		}
	})
	go server.Run(ctx)

	client := NewPeer(ca, nil, nil)
	go client.Run(ctx)

	// request/response
	cctx, c := context.WithTimeout(ctx, 2*time.Second)
	defer c()
	raw, rerr := client.Call(cctx, "add", map[string]int{"A": 2, "B": 3})
	if rerr != nil {
		t.Fatalf("call add: %v", rerr)
	}
	var res struct{ Sum int }
	json.Unmarshal(raw, &res)
	if res.Sum != 5 {
		t.Fatalf("sum = %d, want 5", res.Sum)
	}

	// error response
	if _, rerr := client.Call(cctx, "boom", nil); rerr == nil || rerr.Code != 4242 {
		t.Fatalf("expected error 4242, got %v", rerr)
	}

	// unknown method
	if _, rerr := client.Call(cctx, "nope", nil); rerr == nil || rerr.Code != CodeNoMethod {
		t.Fatalf("expected no-method error, got %v", rerr)
	}

	// notification
	if err := client.Notify("ping", nil); err != nil {
		t.Fatalf("notify: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if gotNotify.Load() != 1 {
		t.Fatalf("notify count = %d, want 1", gotNotify.Load())
	}
}

// TestPeerCallTimeout verifies that a call to an unhandled-but-slow peer times out.
func TestPeerCallTimeout(t *testing.T) {
	ca, cb := transport.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// server that never replies (blocks in handler)
	server := NewPeer(cb, func(ctx context.Context, method string, params json.RawMessage) (any, *Error) {
		<-ctx.Done()
		return nil, Errorf(CodeInternal, "ctx done")
	}, nil)
	go server.Run(ctx)

	client := NewPeer(ca, nil, nil)
	go client.Run(ctx)

	cctx, c := context.WithTimeout(ctx, 200*time.Millisecond)
	defer c()
	if _, rerr := client.Call(cctx, "slow", nil); rerr == nil || rerr.Code != CodeTimeout {
		t.Fatalf("expected timeout, got %v", rerr)
	}
}
