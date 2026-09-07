package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fakecrowd/sys0/internal/wire"
)

func ownershipHub(t *testing.T) (*Hub, http.Handler, map[string]string) {
	t.Helper()
	s := newTestStore(t)
	h := &Hub{store: s, reg: NewRegistry(), cfg: HubConfig{JWTSecret: "ownership-tests"}}
	tokens := map[string]string{}
	for _, name := range []string{"root", "alice", "bob", "eve"} {
		role := "member"
		if name == "root" {
			role = "admin"
		}
		if _, err := s.CreateUser(name, "password", role, nil); err != nil {
			t.Fatal(err)
		}
		tokens[name] = h.signToken(name, role, time.Hour)
	}
	for _, n := range []Node{{ID: "n1", Fingerprint: "111111abcdef", Label: "private-label", IP: "10.9.8.7", Tags: "secret-tag"}, {ID: "n2", Fingerprint: "222222abcdef", Label: "another-private-label"}} {
		if err := s.db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	return h, h.Router(), tokens
}
func ownershipRequest(t *testing.T, r http.Handler, method, path, token string, body any, status int) map[string]any {
	t.Helper()
	w := keyRequest(t, r, method, path, token, body)
	if w.Code != status {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, status, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %s", w.Body.String())
	}
	return out
}
func claimNode(t *testing.T, r http.Handler, token, id string) map[string]any {
	t.Helper()
	return ownershipRequest(t, r, "POST", "/api/v1/nodes/"+id+"/claim", token, map[string]any{}, 200)["node"].(map[string]any)
}
func TestOwnershipClaimContract(t *testing.T) {
	_, r, tokens := ownershipHub(t)
	ownershipRequest(t, r, "POST", "/api/v1/nodes/n1/claim", "", map[string]any{}, 401)
	ownershipRequest(t, r, "POST", "/api/v1/nodes/missing/claim", tokens["alice"], map[string]any{}, 404)
	node := claimNode(t, r, tokens["alice"], "n1")
	if node["owner"] != "alice" || node["canClaim"] != false || node["canManageAccess"] != true || node["canAccess"] != true {
		t.Fatalf("claim view=%v", node)
	}
	claimNode(t, r, tokens["alice"], "n1")
	ownershipRequest(t, r, "POST", "/api/v1/nodes/n1/claim", tokens["bob"], map[string]any{}, 409)
}
func TestOwnershipUnownedDiscoveryIsMinimal(t *testing.T) {
	_, r, tokens := ownershipHub(t)
	out := ownershipRequest(t, r, "GET", "/api/v1/nodes", tokens["alice"], nil, 200)
	nodes := out["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("unowned discovery=%v", out)
	}
	n := nodes[0].(map[string]any)
	raw, _ := json.Marshal(out)
	if n["canClaim"] != true || n["canAccess"] != false || n["canManageAccess"] != false || n["owner"] != "" || n["label"] != "private-label" || strings.Contains(string(raw), "10.9.8.7") || strings.Contains(string(raw), "secret-tag") {
		t.Fatalf("unsafe unowned summary=%s", raw)
	}
	claimNode(t, r, tokens["bob"], "n1")
	out = ownershipRequest(t, r, "GET", "/api/v1/nodes", tokens["alice"], nil, 200)
	if len(out["nodes"].([]any)) != 1 {
		t.Fatalf("claimed node leaked=%v", out)
	}
}
func TestOwnershipAccessManagement(t *testing.T) {
	h, r, tokens := ownershipHub(t)
	claimNode(t, r, tokens["alice"], "n1")
	ownershipRequest(t, r, "GET", "/api/v1/nodes/n1/access", tokens["bob"], nil, 403)
	ownershipRequest(t, r, "POST", "/api/v1/nodes/n1/access", tokens["alice"], map[string]any{"users": []string{"bob", "bob"}}, 200)
	out := ownershipRequest(t, r, "GET", "/api/v1/nodes/n1/access", tokens["alice"], nil, 200)
	if out["owner"] != "alice" || len(out["users"].([]any)) != 4 {
		t.Fatalf("access contract=%v", out)
	}
	for _, v := range out["users"].([]any) {
		u := v.(map[string]any)
		if len(u) != 4 || u["role"] == nil || u["id"] == nil || u["allowed"] != (u["username"] != "eve") {
			t.Fatalf("candidate=%v", u)
		}
	}
	ownershipRequest(t, r, "POST", "/api/v1/nodes/n1/access", tokens["alice"], map[string]any{"users": []string{"missing"}}, 400)
	ownershipRequest(t, r, "GET", "/api/v1/nodes/n1", tokens["bob"], nil, 200)
	ownershipRequest(t, r, "POST", "/api/v1/nodes/n1/access", tokens["bob"], map[string]any{"users": []string{"eve"}}, 403)
	ownershipRequest(t, r, "DELETE", "/api/v1/nodes/n1", tokens["bob"], nil, 403)
	ownershipRequest(t, r, "POST", "/api/v1/nodes/n1/label", tokens["bob"], map[string]any{"label": "delegated"}, 200)
	w := ownershipRequest(t, r, "POST", "/api/v1/dispatch", tokens["bob"], map[string]any{"select": map[string]any{"nodes": []string{"n1"}}, "call": map[string]any{"method": "shell.run", "params": map[string]any{"cmd": "unused"}}, "dryRun": true}, 200)
	if w["ok"] != true {
		t.Fatalf("delegated dangerous operations denied=%v", w)
	}
	ownershipRequest(t, r, "POST", "/api/v1/nodes/n1/access", tokens["root"], map[string]any{"users": []string{}}, 200)
	ownershipRequest(t, r, "GET", "/api/v1/nodes/n1", tokens["bob"], nil, 403)
	ownershipRequest(t, r, "GET", "/api/v1/nodes/n1/access", tokens["alice"], nil, 200)
	rows, err := h.store.ListAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row["method"] == "node.access" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing ACL audit")
	}
}
func TestOwnershipConcurrentClaim(t *testing.T) {
	_, r, tokens := ownershipHub(t)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, name := range []string{"alice", "bob"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			codes <- keyRequest(t, r, "POST", "/api/v1/nodes/n1/claim", tokens[name], map[string]any{}).Code
		}(name)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("claim must have exactly one winner=%v", counts)
	}
}
func TestOwnershipKeyCannotManageAndPrivateREST(t *testing.T) {
	h, r, tokens := ownershipHub(t)
	claimNode(t, r, tokens["alice"], "n1")
	secret, _, err := h.store.CreateAccountKey("root", "scoped", []string{"host.info"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		method, path string
		body         any
	}{{"POST", "/api/v1/nodes/n2/claim", map[string]any{}}, {"GET", "/api/v1/nodes/n1/access", nil}, {"POST", "/api/v1/nodes/n1/access", map[string]any{"users": []string{"eve"}}}, {"GET", "/api/v1/users", nil}, {"POST", "/api/v1/users", map[string]any{"username": "injected", "password": "password"}}, {"POST", "/api/v1/nodes/n1/rescue-command", map[string]any{"kind": "restart-agent"}}, {"POST", "/api/v1/nodes/n1/detach", map[string]any{}}, {"POST", "/api/v1/nodes/n1/label", map[string]any{"label": "bad"}}} {
		ownershipRequest(t, r, p.method, p.path, secret, p.body, 403)
	}
	h.store.InsertSample("n1", wire.Metrics{TS: 123, CPUPct: 77})
	ownershipRequest(t, r, "GET", "/api/v1/metrics?node=n1", tokens["bob"], nil, 403)
	ownershipRequest(t, r, "GET", "/api/v1/metrics?node=n1", secret, nil, 403)
	ownershipRequest(t, r, "GET", "/api/v1/audit", tokens["bob"], nil, 403)
	ownershipRequest(t, r, "GET", "/api/v1/cache", tokens["bob"], nil, 403)
	ownershipRequest(t, r, "GET", "/api/v1/metrics?node=n1", tokens["alice"], nil, 200)
}
