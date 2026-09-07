package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdentitySigningUsesAuthenticatedRecord(t *testing.T) {
	h, _, _ := ownershipHub(t)
	u, ok := h.store.AuthUser("alice", "password")
	if !ok {
		t.Fatal("auth")
	}
	replaceIdentity(t, h)
	tok := h.signUserToken(u, time.Hour)
	claims, ok := h.verifyToken(tok)
	if !ok || claims.UID != u.ID {
		t.Fatalf("signing rebound authenticated UID %d to %+v", u.ID, claims)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	if _, ok := h.actorFromRequest(req); ok {
		t.Fatal("deleted user token authenticated")
	}
}
