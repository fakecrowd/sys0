package main

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/fakecrowd/sys0/internal/wire"
)

func TestOwnershipMigrationReopenAndReconnect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := s.CreateUser("root", "password", "admin", nil)
	alice, _ := s.CreateUser("alice", "password", "member", nil)
	bob, _ := s.CreateUser("bob", "password", "member", nil)
	if err := s.db.Create(&Node{ID: "nabcdef", Fingerprint: "abcdef123456", Label: "old-label"}).Error; err != nil {
		t.Fatal(err)
	}
	// Simulate the exact old CSV schema content and absent migration marker.
	s.db.Model(&User{}).Where("id = ?", alice.ID).Update("node_scope", "nabcdef,nabcdef,missing")
	s.db.Model(&User{}).Where("id = ?", bob.ID).Update("node_scope", "nabcdef")
	s.db.Where("key = ?", "node_acl_v1").Delete(&Setting{})
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ := s.GetUser("alice")
	if len(u.NodeScope) != 2 {
		t.Fatalf("legacy grants not deduped/preserved=%v", u.NodeScope)
	}
	if err := s.ClaimNode("nabcdef", alice.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceNodeAccess("nabcdef", alice.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	u, _ = s.GetUser("bob")
	if len(u.NodeScope) != 0 {
		t.Fatalf("reopen resurrected revoked legacy grant=%v", u.NodeScope)
	}
	if _, _, _, _, err := s.UpsertNode("abcdef123456", "new-agent-label", "", wire.HostSummary{}, "v2"); err != nil {
		t.Fatal(err)
	}
	var n Node
	s.db.First(&n, "id = ?", "nabcdef")
	if n.OwnerID != alice.ID || n.Label != "old-label" {
		t.Fatalf("reconnect clobbered ownership=%+v", n)
	}
	if _, _, _, _, err := s.UpsertNode("abcdefDIFFERENT", "attacker", "", wire.HostSummary{}, "v3"); err == nil {
		t.Fatal("fingerprint collision overwrote node")
	}
	s.db.First(&n, "id = ?", "nabcdef")
	if n.OwnerID != alice.ID || n.Fingerprint != "abcdef123456" {
		t.Fatalf("collision clobbered node=%+v", n)
	}
	if err := s.DeleteUserAs(alice.ID, root.ID); err != nil {
		t.Fatal(err)
	}
	s.db.First(&n, "id = ?", "nabcdef")
	if n.OwnerID != root.ID {
		t.Fatalf("deleted owner's nodes must transfer=%+v", n)
	}
	recreated, err := s.CreateUser("alice", "password", "member", nil)
	if err != nil {
		t.Fatal(err)
	}
	if recreated.ID == alice.ID || len(recreated.NodeScope) != 0 {
		t.Fatalf("recreated user inherited grants=%+v", recreated)
	}
}
func TestOwnershipLastAdminAndSetupAtomic(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateUser("a", "password", "admin", nil)
	b, _ := s.CreateUser("b", "password", "admin", nil)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []uint{a.ID, b.ID} {
		wg.Add(1)
		go func(id uint) { defer wg.Done(); errs <- s.UpdateUserRole(id, "member") }(id)
	}
	wg.Wait()
	close(errs)
	fail := 0
	for err := range errs {
		if err != nil {
			fail++
		}
	}
	if fail != 1 || s.CountAdmins() != 1 {
		t.Fatalf("last admin race fail=%d admins=%d", fail, s.CountAdmins())
	}
	users, _ := s.ListUsers()
	for _, u := range users {
		if u.Role == "admin" && s.DeleteUser(u.ID) == nil {
			t.Fatal("deleted last admin")
		}
	}
	fresh := newTestStore(t)
	errs = make(chan error, 2)
	for _, name := range []string{"a", "b"} {
		wg.Add(1)
		go func(name string) { defer wg.Done(); errs <- fresh.SetupAdmin(name, "password") }(name)
	}
	wg.Wait()
	close(errs)
	fail = 0
	for err := range errs {
		if err != nil {
			fail++
		}
	}
	if fail != 1 || fresh.CountUsers() != 1 {
		t.Fatalf("setup race fail=%d users=%d", fail, fresh.CountUsers())
	}
}
func TestOwnershipReconnectConcurrentClaim(t *testing.T) {
	s := newTestStore(t)
	u, _ := s.CreateUser("u", "password", "member", nil)
	id, _, _, _, err := s.UpsertNode("123456abcdef", "host", "", wire.HostSummary{}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 21)
	wg.Add(1)
	go func() { defer wg.Done(); errs <- s.ClaimNode(id, u.ID) }()
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, _, e := s.UpsertNode("123456abcdef", "host", "", wire.HostSummary{}, "v2")
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n Node
	s.db.First(&n, "id = ?", id)
	if n.OwnerID != u.ID {
		t.Fatalf("claim lost by reconnect=%+v", n)
	}
}
