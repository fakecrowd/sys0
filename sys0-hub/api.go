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
	consoleWS := transport.NewWSListener()
	go acceptLoop(consoleWS, h.serveConsole)
	r.GET("/ws", func(c *gin.Context) {
		if _, ok := h.actorFromRequest(c.Request); !ok {
			c.String(http.StatusUnauthorized, "unauthorized")
			return
		}
		consoleWS.Handler(c.Writer, c.Request)
	})

	// --- REST API v1 ---
	v1 := r.Group("/api/v1")
	v1.POST("/auth/login", h.apiLogin)
	v1.GET("/methods", h.apiMethods)

	auth := v1.Group("", h.authMW())
	auth.GET("/nodes", h.apiNodes)
	auth.GET("/nodes/:id", h.apiNode)
	auth.POST("/nodes/:id/label", h.apiNodeLabel)
	auth.POST("/nodes/:id/detach", h.apiNodeDetach)
	auth.DELETE("/nodes/:id", h.apiNodeDelete)
	auth.POST("/dispatch", h.apiDispatch)
	auth.GET("/metrics", h.apiMetrics)
	auth.GET("/audit", h.apiAudit)
	auth.GET("/events", h.apiEvents)

	admin := v1.Group("", h.adminMW())
	admin.GET("/keys", h.apiListKeys)
	admin.POST("/keys", h.apiCreateKey)
	admin.DELETE("/keys/:id", h.apiRevokeKey)

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
		if !ok || actor.Role != "admin" {
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
	role, ok := h.store.AuthUser(body.Username, body.Password)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"ok": false, "error": "invalid credentials"})
		return
	}
	tok := h.signToken(body.Username, role, 12*time.Hour)
	c.JSON(http.StatusOK, gin.H{"ok": true, "token": tok, "role": role, "username": body.Username})
}

func (h *Hub) apiMethods(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "methods": wire.NodeMethods})
}

func (h *Hub) apiNodes(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "nodes": h.ListNodes()})
}

func (h *Hub) apiNode(c *gin.Context) {
	s := h.reg.get(c.Param("id"))
	if s == nil {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "node not found or offline"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "node": s.view()})
}

func (h *Hub) apiNodeLabel(c *gin.Context) {
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
	if s := h.reg.get(c.Param("id")); s != nil {
		s.peer.Close()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Hub) apiNodeDelete(c *gin.Context) {
	id := c.Param("id")
	if s := h.reg.get(id); s != nil {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "node online; detach first"})
		return
	}
	if err := h.store.DeleteNode(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
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
			c.Writer.WriteString(": ping\n\n")
			c.Writer.Flush()
		case m := <-sub.ch:
			c.Writer.WriteString("event: " + m.Method + "\ndata: ")
			c.Writer.Write(m.Data)
			c.Writer.WriteString("\n\n")
			c.Writer.Flush()
		}
	}
}

func (h *Hub) apiListKeys(c *gin.Context) {
	keys, err := h.store.ListKeys()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "keys": keys})
}

func (h *Hub) apiCreateKey(c *gin.Context) {
	var body struct {
		Name           string   `json:"name"`
		Role           string   `json:"role"`
		NodeScope      []string `json:"nodeScope"`
		MethodScope    []string `json:"methodScope"`
		AllowDangerous bool     `json:"allowDangerous"`
		RateLimit      int      `json:"rateLimit"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	if body.Role == "" {
		body.Role = "operator"
	}
	secret, rec, err := h.store.CreateKey(body.Name, body.Role, body.NodeScope, body.MethodScope, body.AllowDangerous, body.RateLimit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "key": secret, "id": rec.ID, "note": "store this key now; it will not be shown again"})
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

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}
