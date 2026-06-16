package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/transport"
	"github.com/fakecrowd/sys0/internal/wire"
)

const agentVersion = "0.1.0"

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
	shells      *shellManager
	tasks       *taskManager

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
		shells:      newShellManager(),
		tasks:       newTaskManager(),
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
	a.mu.Lock()
	a.peer = peer
	a.mu.Unlock()

	runErr := make(chan error, 1)
	go func() { runErr <- peer.Run(sctx) }()

	// handshake
	hi := hostInfo()
	hello := wire.Hello{
		Key: a.cfg.Key, Fingerprint: a.fingerprint, Label: a.label,
		Host:         wire.HostSummary{Name: hi.Hostname, OS: hi.OS, Arch: hi.Arch, Kernel: hi.Kernel, IP: hi.IP},
		AgentVersion: agentVersion,
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

// handle dispatches inbound requests from the hub.
func (a *Agent) handle(ctx context.Context, method string, params json.RawMessage) (any, *rpc.Error) {
	switch method {
	case wire.MethodShellRun:
		return a.doShellRun(ctx, params)
	case wire.MethodShellOpen:
		return a.doShellOpen(params)
	case wire.MethodShellInput:
		return a.doShellInput(params)
	case wire.MethodShellResize:
		return a.doShellResize(params)
	case wire.MethodShellClose:
		return a.doShellClose(params)
	case wire.MethodTaskStart:
		return a.doTaskStart(params)
	case wire.MethodTaskInput:
		return a.doTaskInput(params)
	case wire.MethodTaskSignal:
		return a.doTaskSignal(params)
	case wire.MethodTaskList:
		return a.doTaskList(params)
	case wire.MethodTaskRemove:
		return a.doTaskRemove(params)
	case wire.MethodHostInfo:
		return hostInfo(), nil
	case wire.MethodHostMetrics:
		return sampleMetrics(), nil
	case wire.MethodHostWatch:
		return a.doHostWatch(params)
	case wire.MethodProcList:
		var p wire.ProcListParams
		decode(params, &p)
		return wire.ProcListResult{Procs: procList(p.Filter)}, nil
	case wire.MethodProcSignal:
		return a.doProcSignal(params)
	case wire.MethodFsLs:
		return a.doFsLs(params)
	case wire.MethodFsStat:
		return a.doFsStat(params)
	case wire.MethodFsGet:
		return a.doFsGet(params)
	case wire.MethodFsPut:
		return a.doFsPut(params)
	case wire.MethodFsRm:
		return a.doFsRm(params)
	case wire.MethodConfig:
		return a.doConfig(params)
	case wire.MethodReconnect:
		a.signalReconnect()
		return wire.OKResult{OK: true}, nil
	case wire.MethodShutdown:
		go a.doShutdown()
		return wire.OKResult{OK: true}, nil
	default:
		return nil, rpc.Errorf(rpc.CodeNoMethod, "unknown method %q", method)
	}
}

func (a *Agent) onNotify(method string, params json.RawMessage) {}

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
		return transport.WSDialer{URL: "ws://" + a.cfg.Hub + "/agent"}, nil
	default:
		return nil, fmt.Errorf("unknown transport %q", a.cfg.Transport)
	}
}

func capabilities() []string {
	caps := make([]string, 0, len(wire.NodeMethods))
	for _, m := range wire.NodeMethods {
		caps = append(caps, m.Name)
	}
	return caps
}
