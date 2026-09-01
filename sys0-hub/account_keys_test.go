package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAccountKeyInheritsLiveOwnerPermissions(t *testing.T) {
	s := newTestStore(t)
	alice, err := s.CreateUser("alice", "secret1", "member", []string{"n1"})
	if err != nil {
		t.Fatal(err)
	}
	secret, rec, err := s.CreateAccountKey("alice", "mcp", []string{"host.info"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Owner != "alice" {
		t.Fatalf("owner=%q", rec.Owner)
	}

	h := &Hub{store: s}
	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	actor, ok := h.actorFromRequest(req)
	if !ok {
		t.Fatal("account key did not authenticate")
	}
	if actor.Role != "member" || actor.ScopeAll || !actor.nodeAllowed("n1") || actor.nodeAllowed("n2") || actor.AllowDangerous {
		t.Fatalf("member actor=%+v", actor)
	}
	if len(actor.MethodScope) != 1 || actor.MethodScope[0] != "host.info" {
		t.Fatalf("methods=%v", actor.MethodScope)
	}

	if err := s.UpdateUserRole(alice.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	actor, ok = h.actorFromRequest(req)
	if !ok || actor.Role != "admin" || !actor.ScopeAll || !actor.AllowDangerous {
		t.Fatalf("admin permissions were not inherited live: ok=%v actor=%+v", ok, actor)
	}

	if err := s.DeleteUser(alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.actorFromRequest(req); ok {
		t.Fatal("key must stop authenticating when owner is deleted")
	}
}

func TestAccountKeysAreOwnerIsolated(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateUser("alice", "secret1", "member", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("bob", "secret2", "member", nil); err != nil {
		t.Fatal(err)
	}
	_, alice, err := s.CreateAccountKey("alice", "a", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, bob, err := s.CreateAccountKey("bob", "b", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	keys, err := s.ListKeysForOwner("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].ID != alice.ID {
		t.Fatalf("alice keys=%+v", keys)
	}
	if changed, err := s.RevokeKeyForOwner(bob.ID, "alice"); err != nil || changed {
		t.Fatalf("alice revoked bob key: changed=%v err=%v", changed, err)
	}
	if changed, err := s.RevokeKeyForOwner(alice.ID, "alice"); err != nil || !changed {
		t.Fatalf("alice own revoke: changed=%v err=%v", changed, err)
	}
}

func TestLegacyKeysAreAssignedToFirstAdminWithoutPrivilegeExpansion(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateUser("member", "secret1", "member", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("admin", "secret2", "admin", nil); err != nil {
		t.Fatal(err)
	}
	legacySecret := "sk_" + "legacy"
	legacy := APIKey{
		ID: "legacy", Name: "old", SecretHash: hashSecret(legacySecret), CreatedAt: 1,
		Role: "operator", NodeScope: "n1", AllowDangerous: false,
	}
	if err := s.db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.backfillLegacyKeyOwners(); err != nil {
		t.Fatal(err)
	}
	var got APIKey
	if err := s.db.First(&got, "id = ?", "legacy").Error; err != nil {
		t.Fatal(err)
	}
	if got.Owner != "admin" {
		t.Fatalf("legacy owner=%q", got.Owner)
	}

	h := &Hub{store: s}
	req := httptest.NewRequest("GET", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+legacySecret)
	actor, ok := h.actorFromRequest(req)
	if !ok {
		t.Fatal("legacy key did not authenticate after migration")
	}
	if actor.ScopeAll || !actor.nodeAllowed("n1") || actor.nodeAllowed("n2") {
		t.Fatalf("legacy node narrowing was expanded: %+v", actor)
	}
	if actor.AllowDangerous {
		t.Fatalf("legacy dangerous=false was expanded: %+v", actor)
	}
}

func TestAccountKeyRoutesEnforceOwnershipAndNoCredentialChaining(t *testing.T) {
	s := newTestStore(t)
	for _, u := range []struct{ name, role string }{{"alice", "member"}, {"bob", "member"}, {"root", "admin"}} {
		if _, err := s.CreateUser(u.name, "secret-"+u.name, u.role, []string{"n1"}); err != nil {
			t.Fatal(err)
		}
	}
	h := &Hub{cfg: HubConfig{JWTSecret: "test-jwt"}, store: s, reg: NewRegistry()}
	r := h.Router()
	token := func(user, role string) string { return h.signToken(user, role, time.Hour) }

	w := keyRequest(t, r, http.MethodPost, "/api/v1/me/keys", token("alice", "member"), map[string]any{
		"name": "bad-scope", "methodScope": []string{"host.not-real"},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown method scope status=%d body=%s", w.Code, w.Body.String())
	}

	aliceSecret, aliceID := createKeyViaAPI(t, r, token("alice", "member"), "alice-key")
	_, bobID := createKeyViaAPI(t, r, token("bob", "member"), "bob-key")

	w = keyRequest(t, r, http.MethodPost, "/api/v1/me/keys", token("alice", "member"), map[string]any{
		"name": "mcp-scoped", "methodScope": []string{"host.info"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("scoped create status=%d body=%s", w.Code, w.Body.String())
	}
	var scoped struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &scoped); err != nil {
		t.Fatal(err)
	}
	w = keyRequest(t, r, http.MethodPost, "/mcp", scoped.Key, map[string]any{
		"jsonrpc": "2.0", "id": "tools", "method": "tools/list", "params": map[string]any{},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("MCP tools/list status=%d body=%s", w.Code, w.Body.String())
	}
	var tools struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tools); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Result.Tools {
		names[tool.Name] = true
	}
	if !names["sys0_list_nodes"] || !names["sys0_host_info"] || names["sys0_shell_run"] {
		t.Fatalf("MCP tools do not reflect key permissions: %v", names)
	}
	for _, n := range []Node{{ID: "n1", Fingerprint: "fingerprint-node-1", Label: "allowed"}, {ID: "n2", Fingerprint: "fingerprint-node-2", Label: "denied"}} {
		if err := s.db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	assertMCPNodeList(t, r, scoped.Key, "tools/call", map[string]any{"name": "sys0_list_nodes", "arguments": map[string]any{}}, "n1")
	assertMCPNodeList(t, r, scoped.Key, "resources/read", map[string]any{"uri": "sys0://nodes"}, "n1")

	w = keyRequest(t, r, http.MethodGet, "/api/v1/me/keys", token("alice", "member"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("alice list status=%d body=%s", w.Code, w.Body.String())
	}
	var listed struct {
		OK   bool        `json:"ok"`
		Keys []KeyRecord `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	foundAlice := false
	for _, key := range listed.Keys {
		if key.Owner != "alice" || key.ID == bobID {
			t.Fatalf("alice list leaked another account: %+v", listed.Keys)
		}
		foundAlice = foundAlice || key.ID == aliceID
	}
	if !foundAlice {
		t.Fatalf("alice key missing from own list: %+v", listed.Keys)
	}

	w = keyRequest(t, r, http.MethodDelete, "/api/v1/me/keys/"+bobID, token("alice", "member"), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-account revoke status=%d body=%s", w.Code, w.Body.String())
	}
	if _, ok := s.AuthKey(aliceSecret); !ok {
		t.Fatal("cross-account revoke affected alice key")
	}

	w = keyRequest(t, r, http.MethodPost, "/api/v1/me/keys", aliceSecret, map[string]any{"name": "child"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("key created another key: status=%d body=%s", w.Code, w.Body.String())
	}

	w = keyRequest(t, r, http.MethodGet, "/api/v1/keys", token("root", "admin"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin audit status=%d body=%s", w.Code, w.Body.String())
	}
	var audit struct {
		Keys []KeyRecord `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &audit); err != nil {
		t.Fatal(err)
	}
	owners := map[string]bool{}
	for _, k := range audit.Keys {
		owners[k.Owner] = true
	}
	if !owners["alice"] || !owners["bob"] {
		t.Fatalf("admin audit owners=%v", owners)
	}
}

func createKeyViaAPI(t *testing.T, r http.Handler, token, name string) (string, string) {
	t.Helper()
	w := keyRequest(t, r, http.MethodPost, "/api/v1/me/keys", token, map[string]any{"name": name})
	if w.Code != http.StatusOK {
		t.Fatalf("create key status=%d body=%s", w.Code, w.Body.String())
	}
	var out struct {
		Key string    `json:"key"`
		Rec KeyRecord `json:"record"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Key == "" || out.Rec.ID == "" {
		t.Fatalf("incomplete create response: %s", w.Body.String())
	}
	return out.Key, out.Rec.ID
}

func keyRequest(t *testing.T, r http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func assertMCPNodeList(t *testing.T, r http.Handler, credential, method string, params map[string]any, want string) {
	t.Helper()
	w := keyRequest(t, r, http.MethodPost, "/mcp", credential, map[string]any{
		"jsonrpc": "2.0", "id": method, "method": method, "params": params,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", method, w.Code, w.Body.String())
	}
	var msg struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	var text string
	if method == "tools/call" {
		var result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(msg.Result, &result); err != nil {
			t.Fatal(err)
		}
		text = result.Content[0].Text
	} else {
		var result struct {
			Contents []struct {
				Text string `json:"text"`
			} `json:"contents"`
		}
		if err := json.Unmarshal(msg.Result, &result); err != nil {
			t.Fatal(err)
		}
		text = result.Contents[0].Text
	}
	var nodes []NodeView
	if err := json.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("%s nodes: %v text=%s", method, err, text)
	}
	if len(nodes) != 1 || nodes[0].ID != want {
		t.Fatalf("%s leaked node scope: %+v", method, nodes)
	}
}
