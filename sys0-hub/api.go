package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/transport"
	"github.com/fakecrowd/sys0/internal/wire"
)

//go:embed all:web
var webFS embed.FS

// Router builds the hub HTTP handler: REST API, console WS, agent WS, static UI.
func (h *Hub) Router() http.Handler {
	mux := http.NewServeMux()

	// --- agent WebSocket gateway ---
	agentWS := transport.NewWSListener()
	mux.HandleFunc("/agent", agentWS.Handler)
	go func() {
		for {
			conn, err := agentWS.Accept()
			if err != nil {
				return
			}
			go h.serveAgent(conn)
		}
	}()

	// --- console WebSocket ---
	consoleWS := transport.NewWSListener()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.actorFromRequest(r); !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		consoleWS.Handler(w, r)
	})
	go func() {
		for {
			conn, err := consoleWS.Accept()
			if err != nil {
				return
			}
			go h.serveConsole(conn)
		}
	}()

	// --- REST API v1 ---
	mux.HandleFunc("POST /api/v1/auth/login", h.apiLogin)
	mux.HandleFunc("GET /api/v1/methods", h.apiMethods)
	mux.HandleFunc("GET /api/v1/nodes", h.guard(h.apiNodes))
	mux.HandleFunc("GET /api/v1/nodes/{id}", h.guard(h.apiNode))
	mux.HandleFunc("POST /api/v1/dispatch", h.guard(h.apiDispatch))
	mux.HandleFunc("GET /api/v1/metrics", h.guard(h.apiMetrics))
	mux.HandleFunc("GET /api/v1/audit", h.guard(h.apiAudit))
	mux.HandleFunc("GET /api/v1/events", h.guard(h.apiEvents))
	mux.HandleFunc("GET /api/v1/keys", h.guardAdmin(h.apiListKeys))
	mux.HandleFunc("POST /api/v1/keys", h.guardAdmin(h.apiCreateKey))
	mux.HandleFunc("DELETE /api/v1/keys/{id}", h.guardAdmin(h.apiRevokeKey))

	// --- MCP ---
	mux.HandleFunc("/mcp", h.mcpHandler)

	// --- static console (SPA) ---
	sub, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(sub))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/")); err != nil && r.URL.Path != "/" {
			// SPA fallback to index.html
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	return withCORS(mux)
}

// guard wraps a handler requiring any authenticated actor.
func (h *Hub) guard(next func(http.ResponseWriter, *http.Request, Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actorFromRequest(r)
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r, actor)
	}
}

// guardAdmin requires an admin user.
func (h *Hub) guardAdmin(next func(http.ResponseWriter, *http.Request, Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := h.actorFromRequest(r)
		if !ok || actor.Role != "admin" {
			writeErr(w, http.StatusForbidden, "admin required")
			return
		}
		next(w, r, actor)
	}
}

func (h *Hub) apiLogin(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	role, ok := h.store.AuthUser(body.Username, body.Password)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	tok := h.signToken(body.Username, role, 12*time.Hour)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": tok, "role": role, "username": body.Username})
}

func (h *Hub) apiMethods(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "methods": wire.NodeMethods})
}

func (h *Hub) apiNodes(w http.ResponseWriter, r *http.Request, _ Actor) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "nodes": h.ListNodes()})
}

func (h *Hub) apiNode(w http.ResponseWriter, r *http.Request, _ Actor) {
	s := h.reg.get(r.PathValue("id"))
	if s == nil {
		writeErr(w, http.StatusNotFound, "node not found or offline")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "node": s.view()})
}

func (h *Hub) apiDispatch(w http.ResponseWriter, r *http.Request, actor Actor) {
	var p wire.DispatchParams
	if json.NewDecoder(r.Body).Decode(&p) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, rerr := h.Dispatch(ctx, actor, p)
	if rerr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": rerr.Message, "code": rerr.Code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "items": res.Items})
}

func (h *Hub) apiMetrics(w http.ResponseWriter, r *http.Request, _ Actor) {
	node := r.URL.Query().Get("node")
	if node == "" {
		writeErr(w, http.StatusBadRequest, "node required")
		return
	}
	from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
	to, _ := strconv.ParseInt(r.URL.Query().Get("to"), 10, 64)
	samples, err := h.store.QuerySamples(node, from, to)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "samples": samples})
}

func (h *Hub) apiAudit(w http.ResponseWriter, r *http.Request, _ Actor) {
	limit := 100
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	rows, err := h.store.ListAudit(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "audit": rows})
}

// apiEvents is a Server-Sent Events stream of live node/metrics events.
func (h *Hub) apiEvents(w http.ResponseWriter, r *http.Request, _ Actor) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	topics := strings.Split(orDefault(r.URL.Query().Get("topics"), "node,metrics"), ",")
	sub := h.reg.subscribeBus(topics)
	defer h.reg.unsubscribeBus(sub)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			w.Write([]byte(": ping\n\n"))
			flusher.Flush()
		case m := <-sub.ch:
			w.Write([]byte("event: " + m.Method + "\ndata: "))
			w.Write(m.Data)
			w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}

func (h *Hub) apiListKeys(w http.ResponseWriter, r *http.Request, _ Actor) {
	keys, err := h.store.ListKeys()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "keys": keys})
}

func (h *Hub) apiCreateKey(w http.ResponseWriter, r *http.Request, _ Actor) {
	var body struct {
		Name           string   `json:"name"`
		Role           string   `json:"role"`
		NodeScope      []string `json:"nodeScope"`
		MethodScope    []string `json:"methodScope"`
		AllowDangerous bool     `json:"allowDangerous"`
		RateLimit      int      `json:"rateLimit"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if body.Role == "" {
		body.Role = "operator"
	}
	secret, rec, err := h.store.CreateKey(body.Name, body.Role, body.NodeScope, body.MethodScope, body.AllowDangerous, body.RateLimit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "key": secret, "id": rec.ID, "note": "store this key now; it will not be shown again"})
}

func (h *Hub) apiRevokeKey(w http.ResponseWriter, r *http.Request, _ Actor) {
	if err := h.store.RevokeKey(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusInternalServerError, "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- console WS handler ---

func (h *Hub) serveConsole(conn transport.Conn) {
	sess := &consoleSession{topics: map[string]bool{}}
	actor := Actor{Kind: "user", ID: "console", Role: "admin", AllowDangerous: true}
	sess.peer = rpc.NewPeer(conn, h.consoleHandler(sess, actor), nil)
	h.reg.addConsole(sess)
	defer h.reg.removeConsole(sess)
	sess.peer.Run(context.Background())
}

func (h *Hub) consoleHandler(sess *consoleSession, actor Actor) rpc.Handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, *rpc.Error) {
		switch method {
		case "dispatch":
			var p wire.DispatchParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, rpc.Errorf(rpc.CodeBadParams, "%v", err)
			}
			return h.Dispatch(ctx, actor, p)
		case "hub.nodes":
			return map[string]any{"nodes": h.ListNodes()}, nil
		case "hub.node":
			var p struct {
				Node string `json:"node"`
			}
			json.Unmarshal(params, &p)
			s := h.reg.get(p.Node)
			if s == nil {
				return nil, rpc.Errorf(rpc.CodeOffline, "node offline")
			}
			return s.view(), nil
		case "hub.label":
			var p struct {
				Node  string   `json:"node"`
				Label string   `json:"label"`
				Tags  []string `json:"tags"`
			}
			json.Unmarshal(params, &p)
			s := h.reg.get(p.Node)
			if s == nil {
				return nil, rpc.Errorf(rpc.CodeOffline, "node offline")
			}
			s.mu.Lock()
			if p.Label != "" {
				s.label = p.Label
			}
			s.tags = p.Tags
			s.mu.Unlock()
			h.store.SetNodeLabelTags(p.Node, s.label, strings.Join(p.Tags, ","))
			return wire.OKResult{OK: true}, nil
		case "hub.detach":
			var p struct {
				Node string `json:"node"`
			}
			json.Unmarshal(params, &p)
			if s := h.reg.get(p.Node); s != nil {
				s.peer.Close()
			}
			return wire.OKResult{OK: true}, nil
		case "hub.subscribe":
			var p struct {
				Topics []string `json:"topics"`
			}
			json.Unmarshal(params, &p)
			sess.subscribe(p.Topics)
			return wire.OKResult{OK: true}, nil
		default:
			return nil, rpc.Errorf(rpc.CodeNoMethod, "unknown method %q", method)
		}
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
