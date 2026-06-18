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
	if u, ok := s.AuthUser("admin", "secret"); !ok || u.Role != "admin" {
		t.Fatalf("auth ok=%v role=%q", ok, u.Role)
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

func TestMultiUserScopeAndGrant(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CreateUser("alice", "secret1", "admin", nil); err != nil {
		t.Fatal(err)
	}
	bob, err := s.CreateUser("bob", "secret2", "member", []string{"n1"})
	if err != nil {
		t.Fatal(err)
	}
	if bob.Role != "member" || len(bob.NodeScope) != 1 || bob.NodeScope[0] != "n1" {
		t.Fatalf("bob = %+v", bob)
	}
	// CountUsers / CountAdmins
	if s.CountUsers() != 2 {
		t.Fatalf("users = %d", s.CountUsers())
	}
	if s.CountAdmins() != 1 {
		t.Fatalf("admins = %d", s.CountAdmins())
	}
	// update scope
	if err := s.UpdateUserScope(bob.ID, []string{"n1", "n2"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUser("bob")
	if len(got.NodeScope) != 2 {
		t.Fatalf("scope = %+v", got.NodeScope)
	}
	// GrantNodeToUsers: appends to member, skips admin, dedupes
	s.GrantNodeToUsers("n3", []string{"bob", "alice"})
	got, _ = s.GetUser("bob")
	if len(got.NodeScope) != 3 {
		t.Fatalf("after grant scope = %+v", got.NodeScope)
	}
	s.GrantNodeToUsers("n3", []string{"bob"}) // dedupe
	got, _ = s.GetUser("bob")
	if len(got.NodeScope) != 3 {
		t.Fatalf("dedupe failed scope = %+v", got.NodeScope)
	}
	alice, _ := s.GetUser("alice")
	if len(alice.NodeScope) != 0 {
		t.Fatalf("admin should not be granted scope: %+v", alice.NodeScope)
	}
}

func TestSettingsKV(t *testing.T) {
	s := newTestStore(t)
	if v := s.GetSetting("default_node_users", "fallback"); v != "fallback" {
		t.Fatalf("expected fallback, got %q", v)
	}
	if err := s.SetSetting("default_node_users", "bob,carol"); err != nil {
		t.Fatal(err)
	}
	if v := s.GetSetting("default_node_users", ""); v != "bob,carol" {
		t.Fatalf("setting = %q", v)
	}
	// upsert overwrites
	s.SetSetting("default_node_users", "dave")
	if v := s.GetSetting("default_node_users", ""); v != "dave" {
		t.Fatalf("upsert = %q", v)
	}
}

func TestUpsertNodePreservesOperatorLabelTags(t *testing.T) {
	s := newTestStore(t)
	fp := "abcdef0123456789"
	host := wire.HostSummary{OS: "linux", Arch: "amd64", Kernel: "6.1", IP: "10.0.0.2"}

	// First connect: agent self-reports its hostname as the label.
	id, isNew, label, tags, err := s.UpsertNode(fp, "raw-hostname", "1.2.3.4:5", host, "v1")
	if err != nil || !isNew {
		t.Fatalf("first upsert: id=%q isNew=%v err=%v", id, isNew, err)
	}
	if label != "raw-hostname" || tags != "" {
		t.Fatalf("new node should seed agent label: label=%q tags=%q", label, tags)
	}

	// Operator renames the node and adds tags from the console.
	if err := s.SetNodeLabelTags(id, "prod-db-01", "db,critical"); err != nil {
		t.Fatal(err)
	}

	// Agent reconnects, still self-reporting the raw hostname. The operator's
	// label/tags MUST survive; only host facts/version refresh.
	_, isNew2, label2, tags2, err := s.UpsertNode(fp, "raw-hostname", "1.2.3.4:6",
		wire.HostSummary{OS: "linux", Arch: "amd64", Kernel: "6.5", IP: "10.0.0.9"}, "v2")
	if err != nil || isNew2 {
		t.Fatalf("reconnect upsert: isNew=%v err=%v", isNew2, err)
	}
	if label2 != "prod-db-01" {
		t.Fatalf("operator label clobbered on reconnect: got %q", label2)
	}
	if tags2 != "db,critical" {
		t.Fatalf("operator tags clobbered on reconnect: got %q", tags2)
	}

	// Confirm the persisted record reflects preserved label/tags + refreshed host.
	recs, err := s.ListNodeRecords()
	if err != nil || len(recs) != 1 {
		t.Fatalf("records=%d err=%v", len(recs), err)
	}
	r := recs[0]
	if r.Label != "prod-db-01" || r.Tags != "db,critical" {
		t.Fatalf("persisted label/tags = %q / %q", r.Label, r.Tags)
	}
	if r.Kernel != "6.5" || r.IP != "10.0.0.9" || r.AgentVersion != "v2" {
		t.Fatalf("host facts not refreshed: %+v", r)
	}
}

func TestUpsertNodeBackfillsEmptyLabel(t *testing.T) {
	s := newTestStore(t)
	fp := "fedcba9876543210"
	// New node connects with an empty label (agent couldn't resolve hostname).
	id, _, label, _, err := s.UpsertNode(fp, "", "a:1", wire.HostSummary{}, "v1")
	if err != nil || label != "" {
		t.Fatalf("first: label=%q err=%v", label, err)
	}
	// Later it reports a real label; with no operator label set, backfill it.
	_, _, label2, _, err := s.UpsertNode(fp, "node-7", "a:2", wire.HostSummary{}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if label2 != "node-7" {
		t.Fatalf("empty label should backfill from agent: got %q", label2)
	}
	_ = id
}
