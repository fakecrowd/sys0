package main

import (
	"net/http/httptest"
	"testing"
)

func TestOwnershipCredentialNeverFollowsRecreatedUsername(t *testing.T) {
	h, r, tokens := ownershipHub(t)
	secret, _, err := h.store.CreateAccountKey("alice", "key", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	alice, _ := h.store.GetUser("alice")
	root, _ := h.store.GetUser("root")
	if err := h.store.DeleteUserAs(alice.ID, root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.CreateUser("alice", "newpassword", "admin", nil); err != nil {
		t.Fatal(err)
	}
	ownershipRequest(t, r, "GET", "/api/v1/nodes", tokens["alice"], nil, 401)
	ownershipRequest(t, r, "GET", "/api/v1/nodes", secret, nil, 401)
}
func TestOwnershipCapturedActorRevocation(t *testing.T) {
	h, _, tokens := ownershipHub(t)
	alice, _ := h.store.GetUser("alice")
	if err := h.store.UpdateUserScope(alice.ID, []string{"n1"}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["alice"])
	a, ok := h.actorFromRequest(req)
	if !ok || !a.nodeAllowed("n1") {
		t.Fatal("missing initial grant")
	}
	if err := h.store.UpdateUserScope(alice.ID, nil); err != nil {
		t.Fatal(err)
	}
	if a.nodeAllowed("n1") {
		t.Fatal("captured actor retained revoked ACL")
	}
}
func TestOwnershipPrivateMCPAudit(t *testing.T) {
	_, r, tokens := ownershipHub(t)
	w := keyRequest(t, r, "POST", "/mcp", tokens["alice"], map[string]any{"jsonrpc": "2.0", "id": "audit", "method": "resources/read", "params": map[string]any{"uri": "sys0://audit"}})
	if !containsJSONError(w.Body.Bytes()) {
		t.Fatalf("MCP audit leak=%s", w.Body.String())
	}
}
func containsJSONError(b []byte) bool { return bytesContains(b, []byte(`"error"`)) }
func bytesContains(b, sub []byte) bool {
	for i := 0; i+len(sub) <= len(b); i++ {
		if string(b[i:i+len(sub)]) == string(sub) {
			return true
		}
	}
	return false
}
