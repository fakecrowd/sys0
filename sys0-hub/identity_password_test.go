package main

import (
	"net/http"
	"testing"
)

func TestIdentityPasswordRequiresAccountLogin(t *testing.T) {
	h, r, _ := ownershipHub(t)
	secret, _, err := h.store.CreateAccountKey("alice", "machine", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	w := keyRequest(t, r, "POST", "/api/v1/me/password", secret, map[string]any{"OldPassword": "password", "NewPassword": "changed-password"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("machine credential changed password: %d %s", w.Code, w.Body.String())
	}
}
func TestIdentityWrongPasswordRemainsBadRequest(t *testing.T) {
	_, r, tokens := ownershipHub(t)
	w := keyRequest(t, r, "POST", "/api/v1/me/password", tokens["alice"], map[string]any{"OldPassword": "incorrect", "NewPassword": "changed-password"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("wrong password status=%d", w.Code)
	}
}
