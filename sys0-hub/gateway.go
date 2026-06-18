package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/fakecrowd/sys0/internal/transport"
	"github.com/fakecrowd/sys0/internal/wire"
)

// serveAgent runs the JSON-RPC loop for one agent connection.
func (h *Hub) serveAgent(conn transport.Conn) {
	sess := &nodeSession{hub: h, conn: conn, lastSeen: time.Now()}
	peer := rpc.NewPeer(conn, sess.handleRequest, sess.handleNotify)
	sess.peer = peer

	err := peer.Run(context.Background())

	sess.mu.Lock()
	id := sess.nodeID
	sess.mu.Unlock()
	if id != "" {
		h.reg.removeNode(sess)
		h.store.SetNodeState(id, "offline")
		h.reg.broadcast("node", "event.node", map[string]any{"event": "offline", "id": id})
		sess.mu.Lock()
		label := sess.label
		sess.mu.Unlock()
		h.auditNode(id, "node.offline", label, conn.RemoteAddr())
		h.log.Info("node offline", "nodeId", id, "err", err)
	}
}

// auditNode records a node connect/disconnect event into the audit log.
func (h *Hub) auditNode(id, method, label, addr string) {
	sel, _ := json.Marshal(map[string]string{"label": label, "addr": addr})
	now := time.Now().Unix()
	outcome := "online"
	if method == "node.offline" {
		outcome = "offline"
	}
	h.store.InsertAudit("node", id, method, string(sel), "", 0, false, outcome, now, now)
}

// handleRequest answers inbound requests from the agent (only node.hello).
func (n *nodeSession) handleRequest(ctx context.Context, method string, params json.RawMessage) (any, *rpc.Error) {
	if method != wire.MethodHello {
		return nil, rpc.Errorf(rpc.CodeNoMethod, "unexpected method %q", method)
	}
	var hello wire.Hello
	if err := json.Unmarshal(params, &hello); err != nil {
		return nil, rpc.Errorf(rpc.CodeBadParams, "%v", err)
	}
	if hello.Key != n.hub.cfg.AccessKey {
		n.hub.log.Warn("rejected agent: bad key", "addr", n.conn.RemoteAddr())
		return nil, rpc.Errorf(rpc.CodeForbidden, "invalid access key")
	}
	if len(hello.Fingerprint) < 6 {
		return nil, rpc.Errorf(rpc.CodeBadParams, "invalid fingerprint")
	}

	id, isNew, effLabel, effTags, err := n.hub.store.UpsertNode(hello.Fingerprint, hello.Label, n.conn.RemoteAddr(), hello.Host, hello.AgentVersion)
	if err != nil {
		return nil, rpc.Errorf(rpc.CodeInternal, "register: %v", err)
	}
	tags := []string{}
	if effTags != "" {
		tags = strings.Split(effTags, ",")
	}
	// On first join, grant access to the users configured in the
	// "new-node default access" policy (admins always see every node).
	if isNew {
		if csv := n.hub.store.GetSetting("default_node_users", ""); csv != "" {
			n.hub.store.GrantNodeToUsers(id, splitScope(csv))
		}
	}

	n.mu.Lock()
	n.nodeID = id
	n.label = effLabel
	n.tags = tags
	n.host = hello.Host
	n.version = hello.AgentVersion
	n.lastSeen = time.Now()
	n.mu.Unlock()

	if old := n.hub.reg.addNode(n); old != nil && old != n {
		old.peer.Close() // displace a stale duplicate
	}

	n.hub.log.Info("node online", "nodeId", id, "label", effLabel, "addr", n.conn.RemoteAddr())
	n.hub.reg.broadcast("node", "event.node", map[string]any{"event": "online", "node": n.view()})
	n.hub.auditNode(id, "node.online", effLabel, n.conn.RemoteAddr())

	return wire.Welcome{NodeID: id, Heartbeat: 15}, nil
}

// handleNotify processes agent notifications (heartbeat + streaming emit).
func (n *nodeSession) handleNotify(method string, params json.RawMessage) {
	switch method {
	case wire.MethodPing:
		n.mu.Lock()
		n.lastSeen = time.Now()
		id := n.nodeID
		n.mu.Unlock()
		if id != "" {
			n.hub.store.SetNodeState(id, "online")
		}
	case wire.MethodEmit:
		var e wire.EmitParams
		if err := json.Unmarshal(params, &e); err != nil {
			return
		}
		n.mu.Lock()
		id := n.nodeID
		n.mu.Unlock()
		if e.Chan == "metrics" {
			var m wire.Metrics
			if json.Unmarshal(e.Data, &m) == nil {
				n.hub.store.InsertSample(id, m)
				n.hub.reg.broadcast("metrics", "event.metrics", map[string]any{"node": id, "metrics": m})
			}
		}
		if e.Chan == "shell" {
			var d map[string]any
			if json.Unmarshal(e.Data, &d) == nil {
				d["node"] = id
				n.hub.reg.broadcast("shell", "event.shell", d)
			}
		}
		if e.Chan == "task" {
			var d map[string]any
			if json.Unmarshal(e.Data, &d) == nil {
				d["node"] = id
				n.hub.reg.broadcast("task", "event.task", d)
			}
		}
	}
}

// runAgentGateways starts the TCP accept loop (WS agents arrive via the http mux).
func (h *Hub) runAgentTCP() error {
	ln, err := transport.ListenTCP(h.cfg.AgentTCP)
	if err != nil {
		return err
	}
	h.log.Info("agent TCP gateway listening", "addr", h.cfg.AgentTCP)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go h.serveAgent(conn)
	}
}
