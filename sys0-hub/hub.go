package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/wire"
)

// Hub ties together the registry, store and dispatch core.
type Hub struct {
	cfg   HubConfig
	log   *slog.Logger
	store *Store
	reg   *Registry
}

// HubConfig holds hub runtime configuration.
type HubConfig struct {
	AgentTCP  string // tcp listen addr for agents
	HTTP      string // http listen addr (console + ws agents + api)
	AccessKey string // pre-shared key agents must present
	DBPath    string
	JWTSecret string
}

// Actor identifies who is invoking dispatch and what they may do.
type Actor struct {
	Kind           string // user | key
	ID             string
	Role           string
	ScopeAll       bool     // true = may access every node (admins, unrestricted keys)
	NodeScope      []string // explicit allow-list (used when ScopeAll is false)
	MethodScope    []string // empty = all
	AllowDangerous bool
}

func (a Actor) nodeAllowed(id string) bool {
	if a.ScopeAll {
		return true
	}
	for _, n := range a.NodeScope {
		if n == id {
			return true
		}
	}
	return false
}

func (a Actor) methodAllowed(m string) bool {
	if len(a.MethodScope) == 0 {
		return true
	}
	for _, x := range a.MethodScope {
		if x == m {
			return true
		}
	}
	return false
}

// NewHub constructs a hub.
func NewHub(cfg HubConfig, log *slog.Logger) (*Hub, error) {
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	return &Hub{cfg: cfg, log: log, store: store, reg: NewRegistry()}, nil
}

// ListNodes returns the full known fleet: online nodes (live, from the
// registry) merged with persisted offline nodes (from the store), stably
// ordered by id.
func (h *Hub) ListNodes() []NodeView {
	records, _ := h.store.ListNodeRecords()
	out := make([]NodeView, 0, len(records))
	seen := map[string]bool{}
	for _, r := range records {
		seen[r.ID] = true
		if s := h.reg.get(r.ID); s != nil {
			out = append(out, s.view())
		} else {
			out = append(out, nodeViewFromRecord(r))
		}
	}
	// include any online node not yet persisted (shouldn't happen, but be safe)
	for _, s := range h.reg.listNodes() {
		if !seen[s.nodeID] {
			out = append(out, s.view())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListNodesFor returns the fleet visible to a given actor (admins/ScopeAll see
// all; members see only nodes in their allow-list).
func (h *Hub) ListNodesFor(actor Actor) []NodeView {
	all := h.ListNodes()
	if actor.ScopeAll {
		return all
	}
	out := make([]NodeView, 0, len(all))
	for _, v := range all {
		if actor.nodeAllowed(v.ID) {
			out = append(out, v)
		}
	}
	return out
}

// resolve turns a Select into target node sessions plus offline error items.
func (h *Hub) resolve(sel wire.Select, actor Actor) (targets []*nodeSession, offline []wire.DispatchItem) {
	switch {
	case len(sel.Nodes) > 0:
		for _, id := range sel.Nodes {
			if s := h.reg.get(id); s != nil {
				targets = append(targets, s)
			} else {
				offline = append(offline, wire.DispatchItem{Node: id, OK: false,
					Error: &wire.DispatchError{Code: rpc.CodeOffline, Message: "node offline"}})
			}
		}
	case len(sel.Tags) > 0:
		for _, s := range h.reg.listNodes() {
			s.mu.Lock()
			tags := s.tags
			s.mu.Unlock()
			if anyTag(tags, sel.Tags) {
				targets = append(targets, s)
			}
		}
	case sel.All:
		targets = h.reg.listNodes()
	}
	// apply node scope (members are restricted to their allow-list)
	if !actor.ScopeAll {
		filtered := targets[:0]
		for _, s := range targets {
			if actor.nodeAllowed(s.nodeID) {
				filtered = append(filtered, s)
			}
		}
		targets = filtered
	}
	return targets, offline
}

// Dispatch fans a call out to selected nodes and aggregates results.
func (h *Hub) Dispatch(ctx context.Context, actor Actor, p wire.DispatchParams) (wire.DispatchResult, *rpc.Error) {
	started := time.Now()
	method := p.Call.Method

	spec, known := wire.MethodIndex[method]
	if !known {
		return wire.DispatchResult{}, rpc.Errorf(rpc.CodeNoMethod, "unknown method %q", method)
	}
	if !actor.methodAllowed(method) {
		return wire.DispatchResult{}, rpc.Errorf(rpc.CodeForbidden, "method %q not permitted for this actor", method)
	}
	if spec.Dangerous && !actor.AllowDangerous {
		return wire.DispatchResult{}, rpc.Errorf(rpc.CodeForbidden, "dangerous method %q disabled for this actor", method)
	}

	targets, items := h.resolve(p.Select, actor)

	timeout := time.Duration(p.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 35 * time.Second
	}

	if p.DryRun {
		for _, s := range targets {
			items = append(items, wire.DispatchItem{Node: s.nodeID, OK: true, Value: json.RawMessage(`{"dryRun":true}`)})
		}
		h.audit(actor, p, len(targets), "dryRun", started)
		return wire.DispatchResult{Items: items}, nil
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, s := range targets {
		wg.Add(1)
		go func(s *nodeSession) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			res, rerr := s.peer.Call(cctx, method, p.Call.Params)
			item := wire.DispatchItem{Node: s.nodeID}
			if rerr != nil {
				item.Error = &wire.DispatchError{Code: rerr.Code, Message: rerr.Message}
			} else {
				item.OK = true
				item.Value = res
			}
			mu.Lock()
			items = append(items, item)
			mu.Unlock()
		}(s)
	}
	wg.Wait()

	h.audit(actor, p, len(targets), "ok", started)
	return wire.DispatchResult{Items: items}, nil
}

func (h *Hub) audit(actor Actor, p wire.DispatchParams, targets int, outcome string, started time.Time) {
	selJSON, _ := json.Marshal(p.Select)
	digest := sha256.Sum256(p.Call.Params)
	h.store.InsertAudit(actor.Kind, actor.ID, p.Call.Method, string(selJSON),
		hex.EncodeToString(digest[:])[:12], targets, p.DryRun, outcome,
		started.Unix(), time.Now().Unix())
}

func anyTag(have, want []string) bool {
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return true
			}
		}
	}
	return false
}

func splitScope(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}
