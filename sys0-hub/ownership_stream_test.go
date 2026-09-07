package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fakecrowd/sys0/internal/rpc"
	"github.com/gorilla/websocket"
)

func ownershipStream(t *testing.T, h *Hub, r http.Handler, token string) (<-chan string, func()) {
	t.Helper()
	server := httptest.NewServer(r)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/events?topics=node,metrics,shell,task", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		server.Close()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("SSE status=%d", resp.StatusCode)
	}
	lines := make(chan string, 100)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "data: ") {
				lines <- scanner.Text()
			}
		}
	}()
	return lines, func() { cancel(); resp.Body.Close(); server.Close() }
}
func streamLine(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case line := <-ch:
		return line
	case <-time.After(3 * time.Second):
		t.Fatal("SSE timed out")
		return ""
	}
}
func TestOwnershipSSEFiltersNodesAndRevocation(t *testing.T) {
	h, r, tokens := ownershipHub(t)
	alice, _ := h.store.GetUser("alice")
	bob, _ := h.store.GetUser("bob")
	h.store.ClaimNode("n1", alice.ID)
	h.store.ClaimNode("n2", bob.ID)
	h.store.ReplaceNodeAccess("n1", alice.ID, []string{"bob"})
	ch, closeStream := ownershipStream(t, h, r, tokens["bob"])
	defer closeStream()
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n1", "marker": "before-revoke"})
	if !strings.Contains(streamLine(t, ch), "before-revoke") {
		t.Fatal("allowed event lost")
	}
	h.store.ReplaceNodeAccess("n1", alice.ID, nil)
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n1", "marker": "private-revoked"})
	h.reg.broadcast("shell", "event.shell", map[string]any{"node": "n1", "marker": "private-shell"})
	h.reg.broadcast("node", "event.node", map[string]any{"node": map[string]any{"id": "n1", "label": "private-full-view"}})
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n2", "marker": "sentinel"})
	if got := streamLine(t, ch); !strings.Contains(got, "sentinel") {
		t.Fatalf("SSE leaked revoked node=%s", got)
	}
}
func TestOwnershipSSEKeyMethodScopeAndRevocation(t *testing.T) {
	h, r, _ := ownershipHub(t)
	root, _ := h.store.GetUser("root")
	h.store.ClaimNode("n1", root.ID)
	secret, key, err := h.store.CreateAccountKey("root", "metrics", []string{"host.watch"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ch, closeStream := ownershipStream(t, h, r, secret)
	defer closeStream()
	h.reg.broadcast("shell", "event.shell", map[string]any{"node": "n1", "marker": "private-shell"})
	h.reg.broadcast("task", "event.task", map[string]any{"node": "n1", "marker": "private-task"})
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n1", "marker": "sentinel"})
	if got := streamLine(t, ch); !strings.Contains(got, "sentinel") {
		t.Fatalf("methodScope bypass=%s", got)
	}
	h.store.RevokeKey(key.ID)
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n1", "marker": "revoked-key"})
	if got := streamLine(t, ch); got != "" {
		t.Fatalf("revoked key SSE=%s", got)
	}
}
func ownershipSocket(t *testing.T, r http.Handler, token string) (*websocket.Conn, func()) {
	t.Helper()
	server := httptest.NewServer(r)
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+token)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws", headers)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return ws, func() { ws.Close(); server.Close() }
}
func socketRead(t *testing.T, ws *websocket.Conn) rpc.Message {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m rpc.Message
	if err := ws.ReadJSON(&m); err != nil {
		t.Fatal(err)
	}
	return m
}
func socketCall(t *testing.T, ws *websocket.Conn, method string, params any) rpc.Message {
	t.Helper()
	if err := ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": "req", "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
	return socketRead(t, ws)
}
func TestOwnershipWebSocketEventsAndLiveRequests(t *testing.T) {
	h, r, tokens := ownershipHub(t)
	alice, _ := h.store.GetUser("alice")
	bob, _ := h.store.GetUser("bob")
	h.store.ClaimNode("n1", alice.ID)
	h.store.ClaimNode("n2", bob.ID)
	h.store.ReplaceNodeAccess("n1", alice.ID, []string{"bob"})
	ws, closeSocket := ownershipSocket(t, r, tokens["bob"])
	defer closeSocket()
	socketCall(t, ws, "hub.subscribe", map[string]any{"topics": []string{"metrics", "node", "shell"}})
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n1", "marker": "before"})
	if got := socketRead(t, ws); !strings.Contains(string(got.Params), "before") {
		t.Fatal("allowed WS event lost")
	}
	h.store.ReplaceNodeAccess("n1", alice.ID, nil)
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n1", "marker": "private"})
	h.reg.broadcast("metrics", "event.metrics", map[string]any{"node": "n2", "marker": "sentinel"})
	if got := socketRead(t, ws); !strings.Contains(string(got.Params), "sentinel") {
		t.Fatalf("WS leaked=%+v", got)
	}
	m := socketCall(t, ws, "hub.node", map[string]any{"node": "n1"})
	if m.Error == nil || m.Error.Code != rpc.CodeForbidden {
		t.Fatalf("stale WS ACL=%+v", m)
	}
	root, _ := h.store.GetUser("root")
	h.store.DeleteUserAs(bob.ID, root.ID)
	m = socketCall(t, ws, "hub.nodes", map[string]any{})
	if m.Error == nil {
		t.Fatalf("deleted user WS request=%+v", m)
	}
}
func TestOwnershipWebSocketKeyAndDemotion(t *testing.T) {
	h, r, _ := ownershipHub(t)
	root, _ := h.store.GetUser("root")
	alice, _ := h.store.GetUser("alice")
	h.store.ClaimNode("n1", alice.ID)
	secret, key, _ := h.store.CreateAccountKey("root", "readonly", []string{"host.info"}, 0)
	ws, closeSocket := ownershipSocket(t, r, secret)
	defer closeSocket()
	m := socketCall(t, ws, "hub.label", map[string]any{"node": "n1", "label": "bad"})
	if m.Error == nil || m.Error.Code != rpc.CodeForbidden {
		t.Fatalf("WS key mutation bypass=%+v", m)
	}
	h.store.UpdateUserRole(alice.ID, "admin")
	h.store.UpdateUserRole(root.ID, "member")
	m = socketCall(t, ws, "hub.node", map[string]any{"node": "n1"})
	if m.Error == nil || m.Error.Code != rpc.CodeForbidden {
		t.Fatalf("WS stale role=%+v", m)
	}
	h.store.RevokeKey(key.ID)
	m = socketCall(t, ws, "hub.nodes", map[string]any{})
	if m.Error == nil {
		t.Fatalf("revoked WS key=%+v", m)
	}
}
func TestOwnershipWebSocketUnrestrictedKeyCannotManageHub(t *testing.T) {
	for _, method := range []string{"hub.label", "hub.detach"} {
		t.Run(method, func(t *testing.T) {
			h, r, _ := ownershipHub(t)
			root, ok := h.store.GetUser("root")
			if !ok {
				t.Fatal("root user missing")
			}
			if err := h.store.ClaimNode("n1", root.ID); err != nil {
				t.Fatal(err)
			}
			secret, _, err := h.store.CreateAccountKey("root", "unrestricted", nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ws, closeSocket := ownershipSocket(t, r, secret)
			defer closeSocket()
			m := socketCall(t, ws, method, map[string]any{"node": "n1", "label": "bad"})
			if m.Error == nil || m.Error.Code != rpc.CodeForbidden {
				t.Fatalf("unrestricted key must be forbidden before offline lookup: %+v", m)
			}
		})
	}
}

func TestOwnershipDispatchExplicitTargetsDoNotRevealPresence(t *testing.T) {
	h, r, tokens := ownershipHub(t)
	alice, _ := h.store.GetUser("alice")
	h.store.ClaimNode("n1", alice.ID)
	h.reg.nodes["n1"] = &nodeGroup{nodeID: "n1", conns: map[string]*nodeSession{}}
	w := keyRequest(t, r, "POST", "/api/v1/dispatch", tokens["bob"], map[string]any{"select": map[string]any{"nodes": []string{"n1", "missing"}}, "call": map[string]any{"method": "host.info"}, "dryRun": true})
	var out struct {
		Items []struct {
			Error *rpc.Error `json:"error"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 2 || out.Items[0].Error == nil || out.Items[1].Error == nil {
		t.Fatalf("explicit target leak=%s", w.Body.String())
	}
	if out.Items[0].Error.Code != out.Items[1].Error.Code || out.Items[0].Error.Message != out.Items[1].Error.Message {
		t.Fatalf("presence observable=%s", w.Body.String())
	}
}

func TestOwnershipMethodDiscoveryScoped(t *testing.T) {
	h, r, _ := ownershipHub(t)
	secret, _, err := h.store.CreateAccountKey("root", "one", []string{"host.info"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := ownershipRequest(t, r, "GET", "/api/v1/methods", secret, nil, 200)
	methods := out["methods"].([]any)
	if len(methods) != 1 || methods[0].(map[string]any)["name"] != "host.info" {
		t.Fatalf("unfiltered methods=%v", methods)
	}
}
func TestOwnershipEventDiscoveryAndOutputScopes(t *testing.T) {
	h, _, tokens := ownershipHub(t)
	req, _ := http.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["bob"])
	a, _ := h.actorFromRequest(req)
	data, ok := h.filterEvent(a, "event.node", json.RawMessage(`{"node":{"id":"n1","label":"visible label","state":"online","agentCwd":"private-cwd","tags":["private-tag"],"host":{"ip":"private-ip"}},"extra":"private-extra"}`))
	if !ok || !strings.Contains(string(data), "visible label") || strings.Contains(string(data), "private-") {
		t.Fatalf("unowned event=%s visible=%v", data, ok)
	}
	root, _ := h.store.GetUser("root")
	h.store.ClaimNode("n1", root.ID)
	for _, scope := range []string{"task.output", "shell.output"} {
		secret, _, err := h.store.CreateAccountKey("root", scope, []string{scope}, 0)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+secret)
		actor, _ := h.actorFromRequest(req)
		event := "event." + strings.Split(scope, ".")[0]
		if _, ok := h.filterEvent(actor, event, json.RawMessage(`{"node":"n1","data":"output"}`)); !ok {
			t.Errorf("output reader denied %s", scope)
		}
	}
}
