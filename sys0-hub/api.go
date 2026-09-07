package main

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/transport"
	"github.com/fakecrowd/sys0/internal/wire"
	"github.com/gin-gonic/gin"
)

//go:embed all:web
var webFS embed.FS

// Router builds the hub HTTP handler with gin: REST API, console/agent WS,
// MCP server and the embedded static console.
func (h *Hub) Router() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), corsMW())

	// --- agent WebSocket gateway ---
	agentWS := transport.NewWSListener()
	go acceptLoop(agentWS, h.serveAgent)
	r.GET("/agent", func(c *gin.Context) { agentWS.Handler(c.Writer, c.Request) })

	// --- console WebSocket (auth required) ---
	r.GET("/ws", func(c *gin.Context) {
		actor, ok := h.actorFromRequest(c.Request)
		if !ok {
			c.String(http.StatusUnauthorized, "unauthorized")
			return
		}
		conn, err := transport.UpgradeWS(c.Writer, c.Request)
		if err != nil {
			return
		}
		// bind the authenticated actor to this console session
		go h.serveConsole(conn, actor)
	})

	// --- REST API v1 ---
	v1 := r.Group("/api/v1")
	v1.POST("/auth/login", h.apiLogin)
	v1.GET("/methods", h.authMW(), h.apiMethods)
	v1.GET("/setup/status", h.apiSetupStatus)
	v1.POST("/setup", h.apiSetup)
	v1.GET("/releases", h.apiReleases)           // public: agent download list (/dl page)
	v1.GET("/agent", h.apiAgentRedirect)         // public: 302 to latest matching agent binary
	v1.GET("/rescue", h.apiRescueRedirect)       // public: 302 to latest matching rescue binary
	v1.POST("/rescue/report", h.apiRescueReport) // public: rescue liveness report (pre-shared key)

	auth := v1.Group("", h.authMW())
	auth.GET("/me", h.apiMe)
	auth.POST("/me/password", h.apiChangeOwnPassword)
	auth.GET("/me/keys", h.apiListOwnKeys)
	auth.POST("/me/keys", h.apiCreateOwnKey)
	auth.DELETE("/me/keys/:id", h.apiRevokeOwnKey)
	auth.GET("/nodes", h.apiNodes)
	auth.POST("/nodes/:id/claim", h.apiNodeClaim)
	auth.GET("/nodes/:id/access", h.apiGetNodeAccess)
	auth.POST("/nodes/:id/access", h.apiSetNodeAccess)
	auth.GET("/nodes/:id", h.apiNode)
	auth.POST("/nodes/:id/label", h.apiNodeLabel)
	auth.POST("/nodes/:id/detach", h.apiNodeDetach)
	auth.POST("/nodes/:id/dismiss-rescue", h.apiNodeDismissRescue)
	auth.POST("/nodes/:id/rescue-command", h.apiNodeRescueCommand)
	auth.DELETE("/nodes/:id", h.apiNodeDelete)
	auth.POST("/dispatch", h.apiDispatch)
	auth.GET("/metrics", h.apiMetrics)
	auth.GET("/cache", h.requireHumanAdmin(), h.apiCacheStatus) // hub release-binary cache status
	auth.GET("/audit", h.requireHumanAdmin(), h.apiAudit)
	auth.GET("/events", h.apiEvents)

	admin := v1.Group("", h.adminMW())
	admin.GET("/keys", h.apiListKeys) // all-account key audit
	admin.DELETE("/keys/:id", h.apiRevokeKey)
	admin.GET("/users", h.apiListUsers)
	admin.POST("/users", h.apiCreateUser)
	admin.POST("/users/:id/scope", retiredAccessRoute)
	admin.POST("/users/:id/role", h.apiUserRole)
	admin.POST("/users/:id/password", h.apiUserPassword)
	admin.DELETE("/users/:id", h.apiDeleteUser)
	admin.GET("/settings/default-access", retiredAccessRoute)
	admin.POST("/settings/default-access", retiredAccessRoute)
	admin.POST("/cache/refresh", h.apiCacheRefresh) // force re-pull latest release binaries

	// --- MCP (reuse the net/http handler) ---
	r.Any("/mcp", gin.WrapF(h.mcpHandler))

	// --- static console (SPA) ---
	r.NoRoute(h.serveStatic)
	return r
}

func acceptLoop(l *transport.WSListener, serve func(transport.Conn)) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go serve(conn)
	}
}

// --- middleware ---

func corsMW() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (h *Hub) authMW() gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := h.actorFromRequest(c.Request)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "unauthorized"})
			return
		}
		c.Set("actor", actor)
		c.Next()
	}
}

func (h *Hub) adminMW() gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := h.actorFromRequest(c.Request)
		if !ok || actor.Role != "admin" || actor.Kind != "user" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"ok": false, "error": "admin required"})
			return
		}
		c.Set("actor", actor)
		c.Next()
	}
}

func actorOf(c *gin.Context) Actor {
	if a, ok := c.Get("actor"); ok {
		return a.(Actor)
	}
	return Actor{}
}

// --- handlers ---

func (h *Hub) apiLogin(c *gin.Context) {
	var body struct{ Username, Password string }
	if c.BindJSON(&body) != nil {
		return
	}
	u, ok := h.store.AuthUser(body.Username, body.Password)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "invalid credentials"})
		return
	}
	tok := h.signUserToken(u, 12*time.Hour)
	c.JSON(http.StatusOK, gin.H{"ok": true, "token": tok, "role": u.Role, "username": u.Username})
}

func (h *Hub) apiMethods(c *gin.Context) {
	methods := []wire.MethodSpec{}
	a := actorOf(c)
	for _, m := range wire.NodeMethods {
		if a.methodAllowed(m.Name) && (!m.Dangerous || a.AllowDangerous) {
			methods = append(methods, m)
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "methods": methods})
}

func (h *Hub) apiNodes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "nodes": h.ListNodesFor(actorOf(c))})
}

func (h *Hub) apiNode(c *gin.Context) {
	if !actorOf(c).nodeAllowed(c.Param("id")) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "node not permitted"})
		return
	}
	for _, v := range h.ListNodesFor(actorOf(c)) {
		if v.ID == c.Param("id") {
			c.JSON(http.StatusOK, gin.H{"ok": true, "node": v})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "node not found"})
}

func (h *Hub) apiNodeLabel(c *gin.Context) {
	if actorOf(c).Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return
	}
	if !actorOf(c).nodeAllowed(c.Param("id")) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "node not permitted"})
		return
	}
	var body struct {
		Label string   `json:"label"`
		Tags  []string `json:"tags"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	id := c.Param("id")
	if s := h.reg.get(id); s != nil {
		s.mu.Lock()
		if body.Label != "" {
			s.label = body.Label
		}
		if body.Tags != nil {
			s.tags = body.Tags
		}
		s.mu.Unlock()
	}
	h.store.SetNodeLabelTags(id, body.Label, strings.Join(body.Tags, ","))
	h.reg.broadcast("node", "event.node", gin.H{"event": "updated", "id": id})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Hub) apiNodeDetach(c *gin.Context) {
	if actorOf(c).Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return
	}
	if !actorOf(c).nodeAllowed(c.Param("id")) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "node not permitted"})
		return
	}
	if s := h.reg.get(c.Param("id")); s != nil {
		s.closeAll()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Hub) apiNodeDelete(c *gin.Context) {
	id := c.Param("id")
	if !h.canManageNode(actorOf(c), id) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "node not permitted"})
		return
	}
	if s := h.reg.get(id); s != nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "node online; detach first"})
		return
	}
	// Always suppress any rescue report for this id so a runaway rescue can't
	// keep the node alive as a zombie after deletion.
	dismissRescue(id)
	if err := h.store.DeleteNode(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// apiNodeDismissRescue suppresses a rescue-only (bootstrapping) zombie node:
// a rescue process reporting from somewhere keeps the synthesized node visible
// with a 90s TTL, so plain deletion can't clear it. Dismissing drops the report
// and ignores further reports for that id for rescueDismissWindow.
func (h *Hub) apiNodeDismissRescue(c *gin.Context) {
	if actorOf(c).Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return
	}
	id := c.Param("id")
	if !actorOf(c).nodeAllowed(id) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "node not permitted"})
		return
	}
	dismissRescue(id)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// apiNodeRescueCommand queues a control command for the node's supervising
// rescue (e.g. update-agent). The rescue talks HTTPS only, so the command is
// buffered and delivered on the rescue's next /rescue/report poll; the result
// comes back on a later report and surfaces in the node's rescueInfo.commands.
func (h *Hub) apiNodeRescueCommand(c *gin.Context) {
	if actorOf(c).Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return
	}
	id := c.Param("id")
	if !actorOf(c).nodeAllowed(id) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "node not permitted"})
		return
	}
	var body struct {
		Kind string `json:"kind"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	kind := rescueCmdKind(body.Kind)
	if !validRescueCmd(kind) {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown command"})
		return
	}
	// Only meaningful if a rescue is actually reporting for this node.
	if !rescueStatus(id).Live {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "no live rescue for this node"})
		return
	}
	cmd := enqueueRescueCommand(id, kind)
	// For update/restart, the rescue's own kill path only works on an agent it
	// SPAWNED (g_child). A decoupled, independently-started agent (or one the
	// rescue is merely standing by for) has g_child==nil, so the rescue could
	// never stop it and the update would silently no-op (old version kept
	// running). Since the hub holds the live agent peer(s), proactively tell the
	// agent to exit here: it releases its single-instance lock, the rescue sees
	// the node go offline on its next report, and re-downloads + relaunches the
	// fresh binary. uninstall does its own teardown, so skip it.
	if kind == rescueCmdUpdateAgent || kind == rescueCmdRestartAgent {
		if g := h.reg.get(id); g != nil {
			for _, peer := range g.allPeers() {
				go func(pr *rpc.Peer) {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_, _ = pr.Call(ctx, wire.MethodShutdown, json.RawMessage(`{}`))
				}(peer)
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "command": cmd})
}

func (h *Hub) apiDispatch(c *gin.Context) {
	var p wire.DispatchParams
	if c.BindJSON(&p) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	res, rerr := h.Dispatch(ctx, actorOf(c), p)
	if rerr != nil {
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": rerr.Message, "code": rerr.Code})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "items": res.Items})
}

func (h *Hub) apiMetrics(c *gin.Context) {
	node := c.Query("node")
	if node == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "node required"})
		return
	}
	if !actorOf(c).nodeAllowed(node) || !actorOf(c).methodAllowed(wire.MethodHostWatch) {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "node or method not permitted"})
		return
	}
	from, _ := strconv.ParseInt(c.Query("from"), 10, 64)
	to, _ := strconv.ParseInt(c.Query("to"), 10, 64)
	samples, err := h.store.QuerySamples(node, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "samples": samples})
}

func (h *Hub) apiAudit(c *gin.Context) {
	limit := 100
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 {
		limit = l
	}
	rows, err := h.store.ListAudit(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "audit": rows})
}

// apiEvents is a Server-Sent Events stream of live node/metrics/shell events.
func (h *Hub) apiEvents(c *gin.Context) {
	topics := strings.Split(orDefault(c.Query("topics"), "node,metrics"), ",")
	sub := h.reg.subscribeBus(topics)
	defer h.reg.unsubscribeBus(sub)

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-ping.C:
			if _, ok := actorOf(c).refreshed(); !ok {
				return
			}
			c.Writer.WriteString(": ping\n\n")
			c.Writer.Flush()
		case m := <-sub.ch:
			if _, ok := actorOf(c).refreshed(); !ok {
				return
			}
			data, allowed := h.filterEvent(actorOf(c), m.Method, m.Data)
			if !allowed {
				continue
			}
			m.Data = data
			c.Writer.WriteString("event: " + m.Method + "\ndata: ")
			c.Writer.Write(m.Data)
			c.Writer.WriteString("\n\n")
			c.Writer.Flush()
		}
	}
}

func (h *Hub) apiListOwnKeys(c *gin.Context) {
	a := actorOf(c)
	if a.Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return
	}
	keys, err := h.store.ListKeysForUserID(a.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "keys": keys})
}

func normalizedMethods(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" || len(m) > 96 || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

func (h *Hub) apiCreateOwnKey(c *gin.Context) {
	a := actorOf(c)
	if a.Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return
	}
	var body struct {
		Name        string   `json:"name"`
		MethodScope []string `json:"methodScope"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if len(body.Name) > 80 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "name too long"})
		return
	}
	methods := normalizedMethods(body.MethodScope)
	for _, method := range methods {
		if _, ok := wire.MethodIndex[method]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "unknown method in scope: " + method})
			return
		}
	}
	secret, rec, err := h.store.CreateAccountKeyByUserID(a.UserID, body.Name, methods, 0)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "key": secret, "record": rec, "note": "store this key now; it will not be shown again"})
}

func (h *Hub) apiRevokeOwnKey(c *gin.Context) {
	a := actorOf(c)
	if a.Kind != "user" {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "account login required"})
		return
	}
	changed, err := h.store.RevokeKeyForUserID(c.Param("id"), a.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if !changed {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "key not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// Admin audit: list every account's active keys and revoke compromised keys.
func (h *Hub) apiListKeys(c *gin.Context) {
	keys, err := h.store.ListKeys()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "keys": keys})
}

func (h *Hub) apiRevokeKey(c *gin.Context) {
	if err := h.store.RevokeKey(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// serveStatic serves the embedded SPA with index.html fallback (no redirects).
func (h *Hub) serveStatic(c *gin.Context) {
	sub, _ := fs.Sub(webFS, "web")
	p := strings.TrimPrefix(c.Request.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	data, err := fs.ReadFile(sub, p)
	if err != nil {
		p = "index.html" // SPA fallback
		data, err = fs.ReadFile(sub, p)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
	}
	ct := mime.TypeByExtension(path.Ext(p))
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.Data(http.StatusOK, ct, data)
}

// --- console WS handler (JSON-RPC over the persistent connection) ---

func (h *Hub) serveConsole(conn transport.Conn, actor Actor) {
	sess := &consoleSession{topics: map[string]bool{}, filter: func(method string, data json.RawMessage) (json.RawMessage, bool) {
		return h.filterEvent(actor, method, data)
	}}
	sess.peer = rpc.NewPeer(conn, h.consoleHandler(sess, actor), nil)
	// Preserve ordering of interactive input coming from the browser. Each
	// keystroke is a separate "dispatch" request wrapping an inner call (e.g.
	// task.input); serving those inline keeps them strictly ordered on the way
	// to the agent, so fast typing doesn't echo back scrambled. Non-interactive
	// or potentially slow dispatches still run on their own goroutine.
	sess.peer.SetAsyncFunc(consoleAsync)
	h.reg.addConsole(sess)
	defer h.reg.removeConsole(sess)
	sess.peer.Run(context.Background())
}

// consoleAsync decides which console requests run on their own goroutine.
// Interactive input dispatches are served inline (return false) to keep their
// arrival order; everything else stays async so a slow fan-out can't block the
// console connection.
func consoleAsync(method string, params json.RawMessage) bool {
	if method != "dispatch" {
		return true
	}
	var p wire.DispatchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return true
	}
	// Order-sensitive, fast methods: serve inline.
	switch p.Call.Method {
	case wire.MethodTaskInput, wire.MethodTaskResize,
		wire.MethodShellInput, wire.MethodShellResize:
		return false
	}
	return true
}

func (h *Hub) consoleHandler(sess *consoleSession, actor Actor) rpc.Handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, *rpc.Error) {
		actor, ok := actor.refreshed()
		if !ok {
			return nil, rpc.Errorf(rpc.CodeForbidden, "unauthorized")
		}
		if (method == "hub.label" || method == "hub.detach") && actor.Kind != "user" {
			return nil, rpc.Errorf(rpc.CodeForbidden, "method not permitted")
		}
		switch method {
		case "dispatch":
			var p wire.DispatchParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, rpc.Errorf(rpc.CodeBadParams, "%v", err)
			}
			return h.Dispatch(ctx, actor, p)
		case "hub.nodes":
			return map[string]any{"nodes": h.ListNodesFor(actor)}, nil
		case "hub.node":
			var p struct {
				Node string `json:"node"`
			}
			json.Unmarshal(params, &p)
			if !actor.nodeAllowed(p.Node) {
				return nil, rpc.Errorf(rpc.CodeForbidden, "node not permitted")
			}
			s := h.reg.get(p.Node)
			if s == nil {
				return nil, rpc.Errorf(rpc.CodeOffline, "node offline")
			}
			v, visible := h.nodeViewFor(actor, s.view())
			if !visible {
				return nil, rpc.Errorf(rpc.CodeForbidden, "node not permitted")
			}
			return v, nil
		case "hub.label":
			var p struct {
				Node  string   `json:"node"`
				Label string   `json:"label"`
				Tags  []string `json:"tags"`
			}
			json.Unmarshal(params, &p)
			if !actor.nodeAllowed(p.Node) {
				return nil, rpc.Errorf(rpc.CodeForbidden, "node not permitted")
			}
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
			if !actor.nodeAllowed(p.Node) {
				return nil, rpc.Errorf(rpc.CodeForbidden, "node not permitted")
			}
			if s := h.reg.get(p.Node); s != nil {
				s.closeAll()
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

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
