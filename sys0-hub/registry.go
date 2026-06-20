package main

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/transport"
	"github.com/fakecrowd/sys0/internal/wire"
)

// nodeSession is a single live connection from ONE agent module (core, shell,
// fs or screen — or "all" for the monolith). Multiple module connections that
// share a fingerprint are aggregated into one logical nodeGroup.
type nodeSession struct {
	hub    *Hub
	peer   *rpc.Peer
	conn   transport.Conn
	module string // which module this connection serves (core|shell|fs|screen|all)

	mu       sync.Mutex
	nodeID   string
	label    string
	tags     []string
	host     wire.HostSummary
	version  string
	agentCwd string
	agentPid int
	lastSeen time.Time
}

// nodeGroup aggregates the per-module connections of one logical node (keyed by
// shared fingerprint/nodeID). The console sees ONE node; the hub fans a
// dispatched call out to whichever module connection serves that method. A node
// is online as long as ANY module is connected, so AV-quarantining one module
// (e.g. screen) doesn't take the node offline — the others keep working.
type nodeGroup struct {
	nodeID string
	mu     sync.Mutex
	conns  map[string]*nodeSession // module -> live connection
	label  string                  // node-level, edited from the console
	tags   []string
}

// ModuleView reports the live state of one module connection on a node.
type ModuleView struct {
	Name    string `json:"name"`
	Online  bool   `json:"online"`
	Version string `json:"version,omitempty"`
}

// NodeView is the JSON shape returned to clients.
type NodeView struct {
	ID            string           `json:"id"`
	Label         string           `json:"label"`
	Tags          []string         `json:"tags"`
	Host          wire.HostSummary `json:"host"`
	Version       string           `json:"version"`
	State         string           `json:"state"`
	LastSeen      int64            `json:"lastSeen"`
	AgentCwd      string           `json:"agentCwd,omitempty"` // agent's working directory
	AgentPid      int              `json:"agentPid,omitempty"` // agent's own pid
	Modules       []ModuleView     `json:"modules,omitempty"`  // per-module connection state
	Rescue        bool             `json:"rescue"`        // a sys0-rescue is supervising this node
	RescueVersion string           `json:"rescueVersion"` // reported rescue build
	// RescueInfo is the full live rescue status (phase/detail/restarts/…) for
	// the console detail view; nil when no fresh rescue report exists.
	RescueInfo *rescueView `json:"rescueInfo,omitempty"`
}

// primaryConn picks the connection whose metadata represents the node best:
// core first (most representative), then the monolith, then any. Caller holds
// g.mu. Returns nil only if the group has no connections.
func (g *nodeGroup) primaryConn() *nodeSession {
	for _, pref := range []string{"core", "all"} {
		if c := g.conns[pref]; c != nil {
			return c
		}
	}
	for _, c := range g.conns {
		return c
	}
	return nil
}

// peerFor returns the peer of the module connection that serves the given
// method, or nil if that module isn't connected.
func (g *nodeGroup) peerFor(method string) *rpc.Peer {
	g.mu.Lock()
	defer g.mu.Unlock()
	// The monolith ("all") serves everything.
	if c := g.conns["all"]; c != nil {
		return c.peer
	}
	mod := wire.MethodModule(method)
	if c := g.conns[mod]; c != nil {
		return c.peer
	}
	return nil
}

// anyPeer returns any connected module's peer (for lifecycle/label nudges).
func (g *nodeGroup) anyPeer() *rpc.Peer {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c := g.primaryConn(); c != nil {
		return c.peer
	}
	return nil
}

// closeAll detaches every module connection of this node.
func (g *nodeGroup) closeAll() {
	g.mu.Lock()
	peers := make([]*rpc.Peer, 0, len(g.conns))
	for _, c := range g.conns {
		peers = append(peers, c.peer)
	}
	g.mu.Unlock()
	for _, p := range peers {
		p.Close()
	}
}

func (g *nodeGroup) view() NodeView {
	g.mu.Lock()
	tags := g.tags
	if tags == nil {
		tags = []string{}
	}
	label := g.label
	pc := g.primaryConn()
	var host wire.HostSummary
	var version, cwd string
	var pid int
	var lastSeen time.Time
	if pc != nil {
		pc.mu.Lock()
		host, version, cwd, pid, lastSeen = pc.host, pc.version, pc.agentCwd, pc.agentPid, pc.lastSeen
		pc.mu.Unlock()
	}
	mods := make([]ModuleView, 0, len(g.conns))
	for _, name := range wire.Modules {
		if c := g.conns[name]; c != nil {
			c.mu.Lock()
			v := c.version
			c.mu.Unlock()
			mods = append(mods, ModuleView{Name: name, Online: true, Version: v})
		} else {
			mods = append(mods, ModuleView{Name: name, Online: false})
		}
	}
	// A monolith connection lights up every module logically.
	if c := g.conns["all"]; c != nil {
		c.mu.Lock()
		v := c.version
		c.mu.Unlock()
		for i := range mods {
			mods[i].Online = true
			if mods[i].Version == "" {
				mods[i].Version = v
			}
		}
	}
	g.mu.Unlock()

	rs := rescueStatus(g.nodeID)
	var ri *rescueView
	if rs.Live {
		v := rs
		ri = &v
	}
	return NodeView{
		ID: g.nodeID, Label: label, Tags: tags, Host: host,
		Version: version, State: "online", LastSeen: lastSeen.Unix(),
		AgentCwd: cwd, AgentPid: pid, Modules: mods,
		Rescue: rs.Live, RescueVersion: rs.Version, RescueInfo: ri,
	}
}

// nodeViewFromRecord builds an offline NodeView from a persisted record.
func nodeViewFromRecord(r Node) NodeView {
	tags := []string{}
	if r.Tags != "" {
		tags = strings.Split(r.Tags, ",")
	}
	rs := rescueStatus(r.ID)
	var ri *rescueView
	if rs.Live {
		v := rs
		ri = &v
	}
	mods := make([]ModuleView, 0, len(wire.Modules))
	for _, name := range wire.Modules {
		mods = append(mods, ModuleView{Name: name, Online: false})
	}
	return NodeView{
		ID: r.ID, Label: r.Label, Tags: tags,
		Host:    wire.HostSummary{Name: r.Label, OS: r.OS, Arch: r.Arch, Kernel: r.Kernel, IP: r.IP},
		Version: r.AgentVersion, State: "offline", LastSeen: r.LastSeen, Modules: mods,
		Rescue: rs.Live, RescueVersion: rs.Version, RescueInfo: ri,
	}
}

// nodeViewFromRescue builds a synthetic "bootstrapping" NodeView for a node that
// has a live rescue report but no agent session/record yet — i.e. the rescue is
// downloading/starting the agent. State is "bootstrapping" so the console can
// distinguish it from online/offline.
func nodeViewFromRescue(id string, rs rescueView) NodeView {
	v := rs
	return NodeView{
		ID: id, Label: id, Tags: []string{},
		Host:          wire.HostSummary{OS: "", Arch: ""},
		State:         "bootstrapping",
		Rescue:        true,
		RescueVersion: rs.Version,
		RescueInfo:    &v,
	}
}

// consoleSession is a live console/operator connection over WebSocket.
type consoleSession struct {
	peer   *rpc.Peer
	mu     sync.Mutex
	topics map[string]bool
}

func (c *consoleSession) subscribe(topics []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.topics == nil {
		c.topics = map[string]bool{}
	}
	for _, t := range topics {
		c.topics[t] = true
	}
}

func (c *consoleSession) wants(topic string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.topics[topic]
}

// Registry tracks online nodes and connected consoles.
type Registry struct {
	mu       sync.RWMutex
	nodes    map[string]*nodeGroup
	consoles map[*consoleSession]bool
	subs     map[*busSub]bool
}

// busSub is a generic event subscriber (used by SSE).
type busSub struct {
	ch     chan busMsg
	topics map[string]bool
}

type busMsg struct {
	Method string
	Data   json.RawMessage
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		nodes:    map[string]*nodeGroup{},
		consoles: map[*consoleSession]bool{},
		subs:     map[*busSub]bool{},
	}
}

// addModule attaches a module connection to its node group (creating the group
// on the first module). It seeds the group's label/tags from the connection on
// first creation. Returns any prior connection for the SAME module (a stale
// duplicate to displace).
func (r *Registry) addModule(s *nodeSession) (old *nodeSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.nodes[s.nodeID]
	if g == nil {
		s.mu.Lock()
		lbl, tags := s.label, s.tags
		s.mu.Unlock()
		g = &nodeGroup{nodeID: s.nodeID, conns: map[string]*nodeSession{}, label: lbl, tags: tags}
		r.nodes[s.nodeID] = g
	}
	g.mu.Lock()
	old = g.conns[s.module]
	g.conns[s.module] = s
	g.mu.Unlock()
	return old
}

// removeModule detaches a module connection. When the group has no connections
// left, the node group itself is removed (node goes offline).
func (r *Registry) removeModule(s *nodeSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.nodes[s.nodeID]
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.conns[s.module] == s {
		delete(g.conns, s.module)
	}
	empty := len(g.conns) == 0
	g.mu.Unlock()
	if empty {
		delete(r.nodes, s.nodeID)
	}
}

func (r *Registry) get(id string) *nodeGroup {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nodes[id]
}

func (r *Registry) listNodes() []*nodeGroup {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*nodeGroup, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	return out
}

func (r *Registry) addConsole(c *consoleSession) {
	r.mu.Lock()
	r.consoles[c] = true
	r.mu.Unlock()
}

func (r *Registry) removeConsole(c *consoleSession) {
	r.mu.Lock()
	delete(r.consoles, c)
	r.mu.Unlock()
}

// broadcast pushes an event notification to consoles subscribed to topic and
// to any generic bus subscribers (SSE).
func (r *Registry) broadcast(topic, method string, payload any) {
	data, _ := json.Marshal(payload)
	r.mu.RLock()
	targets := make([]*consoleSession, 0)
	for c := range r.consoles {
		if c.wants(topic) {
			targets = append(targets, c)
		}
	}
	subs := make([]*busSub, 0)
	for s := range r.subs {
		if s.topics[topic] {
			subs = append(subs, s)
		}
	}
	r.mu.RUnlock()
	for _, c := range targets {
		c.peer.Notify(method, json.RawMessage(data))
	}
	for _, s := range subs {
		select {
		case s.ch <- busMsg{Method: method, Data: data}:
		default: // drop if subscriber is slow
		}
	}
}

func (r *Registry) subscribeBus(topics []string) *busSub {
	s := &busSub{ch: make(chan busMsg, 64), topics: map[string]bool{}}
	for _, t := range topics {
		s.topics[t] = true
	}
	r.mu.Lock()
	r.subs[s] = true
	r.mu.Unlock()
	return s
}

func (r *Registry) unsubscribeBus(s *busSub) {
	r.mu.Lock()
	delete(r.subs, s)
	r.mu.Unlock()
}
