package main

import (
	"github.com/gin-gonic/gin"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type identityBarrierBody struct {
	io.Reader
	entered, release chan struct{}
	once             bool
}

func (b *identityBarrierBody) Read(p []byte) (int, error) {
	if !b.once {
		b.once = true
		close(b.entered)
		<-b.release
	}
	return b.Reader.Read(p)
}
func (b *identityBarrierBody) Close() error { return nil }

func replaceIdentity(t *testing.T, h *Hub) UserRecord {
	t.Helper()
	old, _ := h.store.GetUser("alice")
	root, _ := h.store.GetUser("root")
	if err := h.store.DeleteUserAs(old.ID, root.ID); err != nil {
		t.Fatal(err)
	}
	u, err := h.store.CreateUser("alice", "replacement-password", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
func TestIdentitySlowBodyCannotIssueReplacementKey(t *testing.T) {
	h, r, tokens := ownershipHub(t)
	b := &identityBarrierBody{Reader: strings.NewReader(`{"name":"stolen"}`), entered: make(chan struct{}), release: make(chan struct{})}
	req := httptest.NewRequest("POST", "/api/v1/me/keys", b)
	req.Header.Set("Authorization", "Bearer "+tokens["alice"])
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); r.ServeHTTP(w, req) }()
	select {
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("body barrier not reached")
	}
	replaceIdentity(t, h)
	close(b.release)
	<-done
	if w.Code < 400 {
		t.Fatalf("stale request minted replacement credential: %d %s", w.Code, w.Body.String())
	}
	keys, err := h.store.ListKeysForOwner("alice")
	if err != nil || len(keys) != 0 {
		t.Fatalf("replacement keys=%v err=%v", keys, err)
	}
}
func TestIdentityCapturedSelfOperations(t *testing.T) {
	for _, op := range []string{"me", "list", "revoke", "password"} {
		t.Run(op, func(t *testing.T) {
			h, _, tokens := ownershipHub(t)
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("Authorization", "Bearer "+tokens["alice"])
			a, ok := h.actorFromRequest(req)
			if !ok {
				t.Fatal("auth")
			}
			replacement := replaceIdentity(t, h)
			secret, key, err := h.store.CreateAccountKey("alice", "private", nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("actor", a)
			c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"OldPassword":"replacement-password","NewPassword":"attacker-password"}`))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Params = gin.Params{{Key: "id", Value: key.ID}}
			switch op {
			case "me":
				h.apiMe(c)
			case "list":
				h.apiListOwnKeys(c)
			case "revoke":
				h.apiRevokeOwnKey(c)
			case "password":
				h.apiChangeOwnPassword(c)
			}
			if op == "me" && w.Code == 200 {
				t.Fatalf("read replacement user %d: %s", replacement.ID, w.Body.String())
			}
			if op == "list" && strings.Contains(w.Body.String(), key.ID) {
				t.Fatalf("leaked replacement keys: %s", w.Body.String())
			}
			if _, ok := h.store.AuthKey(secret); !ok {
				t.Fatal("revoked replacement key")
			}
			if _, ok := h.store.AuthUser("alice", "replacement-password"); !ok {
				t.Fatal("changed replacement password")
			}
		})
	}
}
