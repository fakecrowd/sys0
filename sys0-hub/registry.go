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

// nodeSession is a live connection to one agent.
type nodeSession struct {
	hub  *Hub
	peer *rpc.Peer
	conn transport.Conn

	mu       sync.Mutex
	nodeID   string
	label    string
	tags     []string
	host     wire.HostSummary
	version  string
	lastSeen time.Time
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
	Rescue        bool             `json:"rescue"`        // a sys0-rescue is supervising this node
	RescueVersion string           `json:"rescueVersion"` // reported rescue build
	// RescueInfo is the full live rescue status (phase/detail/restarts/…) for
	// the console detail view; nil when no fresh rescue report exists.
	RescueInfo *rescueView `json:"rescueInfo,omitempty"`
}

func (n *nodeSession) view() NodeView {
	n.mu.Lock()
	defer n.mu.Unlock()
	tags := n.tags
	if tags == nil {
		tags = []string{}
	}
	rs := rescueStatus(n.nodeID)
	var ri *rescueView
	if rs.Live {
		v := rs
		ri = &v
	}
	return NodeView{
		ID: n.nodeID, Label: n.label, Tags: tags, Host: n.host,
		Version: n.version, State: "online", LastSeen: n.lastSeen.Unix(),
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
	return NodeView{
		ID: r.ID, Label: r.Label, Tags: tags,
		Host:    wire.HostSummary{Name: r.Label, OS: r.OS, Arch: r.Arch, Kernel: r.Kernel, IP: r.IP},
		Version: r.AgentVersion, State: "offline", LastSeen: r.LastSeen,
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
	nodes    map[string]*nodeSession
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
		nodes:    map[string]*nodeSession{},
		consoles: map[*consoleSession]bool{},
		subs:     map[*busSub]bool{},
	}
}

func (r *Registry) addNode(s *nodeSession) (old *nodeSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	old = r.nodes[s.nodeID]
	r.nodes[s.nodeID] = s
	return old
}

func (r *Registry) removeNode(s *nodeSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.nodes[s.nodeID] == s {
		delete(r.nodes, s.nodeID)
	}
}

func (r *Registry) get(id string) *nodeSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.nodes[id]
}

func (r *Registry) listNodes() []*nodeSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*nodeSession, 0, len(r.nodes))
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
