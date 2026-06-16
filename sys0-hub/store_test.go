package main

import (
	"path/filepath"
	"testing"

	"github.com/fakecrowd/sys0/internal/wire"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUserAuth(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureUser("admin", "secret", "admin"); err != nil {
		t.Fatal(err)
	}
	if role, ok := s.AuthUser("admin", "secret"); !ok || role != "admin" {
		t.Fatalf("auth ok=%v role=%q", ok, role)
	}
	if _, ok := s.AuthUser("admin", "wrong"); ok {
		t.Fatal("expected wrong password to fail")
	}
}

func TestAPIKeyScopes(t *testing.T) {
	s := newTestStore(t)
	secret, rec, err := s.CreateKey("bot", "operator", []string{"n1"}, []string{"host.info"}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := s.AuthKey(secret)
	if !ok {
		t.Fatal("auth key failed")
	}
	if got.ID != rec.ID || got.NodeScope != "n1" || got.MethodScope != "host.info" || got.AllowDangerous {
		t.Fatalf("unexpected key record: %+v", got)
	}
	if _, ok := s.AuthKey("sk_bogus"); ok {
		t.Fatal("bogus key should not auth")
	}
	if err := s.RevokeKey(rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.AuthKey(secret); ok {
		t.Fatal("revoked key should not auth")
	}
}

func TestSamplesAndAudit(t *testing.T) {
	s := newTestStore(t)
	s.InsertSample("n1", wire.Metrics{TS: 100, CPUPct: 12.5, MemUsed: 1, MemTotal: 2, Load1: 0.5})
	s.InsertSample("n1", wire.Metrics{TS: 200, CPUPct: 30})
	got, err := s.QuerySamples("n1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TS != 100 || got[1].CPUPct != 30 {
		t.Fatalf("samples = %+v", got)
	}
	s.InsertAudit("user", "admin", "shell.run", `{"all":true}`, "abc", 3, false, "ok", 1, 2)
	rows, err := s.ListAudit(10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("audit rows = %d err=%v", len(rows), err)
	}
	if rows[0]["method"] != "shell.run" || rows[0]["targetCount"].(int) != 3 {
		t.Fatalf("audit row = %+v", rows[0])
	}
}
