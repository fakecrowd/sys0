package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/wire"
	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed persistence layer.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS nodes(
  id TEXT PRIMARY KEY, label TEXT, fingerprint TEXT UNIQUE, tags TEXT,
  last_addr TEXT, os TEXT, arch TEXT, kernel TEXT, ip TEXT,
  agent_version TEXT, state TEXT, first_seen INTEGER, last_seen INTEGER);
CREATE TABLE IF NOT EXISTS users(
  id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT UNIQUE,
  secret_hash TEXT, role TEXT, created_at INTEGER);
CREATE TABLE IF NOT EXISTS api_keys(
  id TEXT PRIMARY KEY, name TEXT, secret_hash TEXT, role TEXT,
  node_scope TEXT, method_scope TEXT, allow_dangerous INTEGER,
  rate_limit INTEGER, created_at INTEGER, revoked_at INTEGER);
CREATE TABLE IF NOT EXISTS audit(
  id INTEGER PRIMARY KEY AUTOINCREMENT, actor_kind TEXT, actor_id TEXT,
  method TEXT, select_json TEXT, params_digest TEXT, target_count INTEGER,
  dry_run INTEGER, outcome TEXT, started_at INTEGER, finished_at INTEGER);
CREATE TABLE IF NOT EXISTS samples(
  node_id TEXT, ts INTEGER, cpu_pct REAL, mem_used INTEGER, mem_total INTEGER,
  load1 REAL, net_rx INTEGER, net_tx INTEGER);
CREATE INDEX IF NOT EXISTS idx_samples_node_ts ON samples(node_id, ts);
`

// OpenStore opens (and migrates) the database.
func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // modernc sqlite: serialize writes
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ---- hashing ----

func hashSecret(secret string) string {
	salt := randHex(8)
	sum := sha256.Sum256([]byte(salt + secret))
	return salt + "$" + hex.EncodeToString(sum[:])
}

func verifySecret(secret, stored string) bool {
	parts := strings.SplitN(stored, "$", 2)
	if len(parts) != 2 {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + secret))
	return hex.EncodeToString(sum[:]) == parts[1]
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- nodes ----

// NodeRecord mirrors a row in nodes.
type NodeRecord struct {
	ID, Label, Fingerprint, Tags   string
	LastAddr, OS, Arch, Kernel, IP string
	AgentVersion, State            string
	FirstSeen, LastSeen            int64
}

// UpsertNode registers or updates a node by fingerprint and returns its id.
func (s *Store) UpsertNode(fp, label, addr string, host wire.HostSummary, version string) (string, error) {
	id := "n" + fp[:6]
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO nodes(id,label,fingerprint,tags,last_addr,os,arch,kernel,ip,agent_version,state,first_seen,last_seen)
		VALUES(?,?,?,'',?,?,?,?,?,?, 'online',?,?)
		ON CONFLICT(fingerprint) DO UPDATE SET
		  label=excluded.label, last_addr=excluded.last_addr, os=excluded.os, arch=excluded.arch,
		  kernel=excluded.kernel, ip=excluded.ip, agent_version=excluded.agent_version,
		  state='online', last_seen=excluded.last_seen`,
		id, label, fp, addr, host.OS, host.Arch, host.Kernel, host.IP, version, now, now)
	if err != nil {
		return "", err
	}
	// read back canonical id (may differ if fingerprint already existed)
	var rid string
	if err := s.db.QueryRow(`SELECT id FROM nodes WHERE fingerprint=?`, fp).Scan(&rid); err == nil {
		id = rid
	}
	return id, nil
}

// SetNodeState updates the lifecycle state and last_seen.
func (s *Store) SetNodeState(id, state string) error {
	_, err := s.db.Exec(`UPDATE nodes SET state=?, last_seen=? WHERE id=?`, state, time.Now().Unix(), id)
	return err
}

// SetNodeLabelTags updates label/tags.
func (s *Store) SetNodeLabelTags(id, label, tags string) error {
	_, err := s.db.Exec(`UPDATE nodes SET label=?, tags=? WHERE id=?`, label, tags, id)
	return err
}

// ---- users ----

// EnsureUser creates a user if missing.
func (s *Store) EnsureUser(username, secret, role string) error {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username=?`, username).Scan(&n)
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO users(username,secret_hash,role,created_at) VALUES(?,?,?,?)`,
		username, hashSecret(secret), role, time.Now().Unix())
	return err
}

// AuthUser verifies credentials and returns the role.
func (s *Store) AuthUser(username, secret string) (role string, ok bool) {
	var hash string
	if err := s.db.QueryRow(`SELECT secret_hash, role FROM users WHERE username=?`, username).Scan(&hash, &role); err != nil {
		return "", false
	}
	return role, verifySecret(secret, hash)
}

// ---- api keys ----

// KeyRecord mirrors an api_keys row (without the secret).
type KeyRecord struct {
	ID, Name, Role         string
	NodeScope, MethodScope string
	AllowDangerous         bool
	RateLimit              int
	CreatedAt, RevokedAt   int64
}

// CreateKey returns the plaintext key (shown once) and its record.
func (s *Store) CreateKey(name, role string, nodeScope, methodScope []string, allowDangerous bool, rate int) (string, KeyRecord, error) {
	id := "k" + randHex(4)
	secret := "sk_" + randHex(20)
	rec := KeyRecord{
		ID: id, Name: name, Role: role,
		NodeScope: strings.Join(nodeScope, ","), MethodScope: strings.Join(methodScope, ","),
		AllowDangerous: allowDangerous, RateLimit: rate, CreatedAt: time.Now().Unix(),
	}
	_, err := s.db.Exec(`INSERT INTO api_keys(id,name,secret_hash,role,node_scope,method_scope,allow_dangerous,rate_limit,created_at,revoked_at)
		VALUES(?,?,?,?,?,?,?,?,?,0)`,
		id, name, hashSecret(secret), role, rec.NodeScope, rec.MethodScope, b2i(allowDangerous), rate, rec.CreatedAt)
	if err != nil {
		return "", KeyRecord{}, err
	}
	return secret, rec, nil
}

// AuthKey resolves a plaintext key to its record (active only).
func (s *Store) AuthKey(secret string) (KeyRecord, bool) {
	rows, err := s.db.Query(`SELECT id,name,secret_hash,role,node_scope,method_scope,allow_dangerous,rate_limit,created_at,revoked_at FROM api_keys WHERE revoked_at=0`)
	if err != nil {
		return KeyRecord{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var r KeyRecord
		var hash string
		var ad, revoked int64
		if err := rows.Scan(&r.ID, &r.Name, &hash, &r.Role, &r.NodeScope, &r.MethodScope, &ad, &r.RateLimit, &r.CreatedAt, &revoked); err != nil {
			continue
		}
		r.AllowDangerous = ad == 1
		if verifySecret(secret, hash) {
			return r, true
		}
	}
	return KeyRecord{}, false
}

// ListKeys returns all active keys.
func (s *Store) ListKeys() ([]KeyRecord, error) {
	rows, err := s.db.Query(`SELECT id,name,role,node_scope,method_scope,allow_dangerous,rate_limit,created_at,revoked_at FROM api_keys WHERE revoked_at=0 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyRecord
	for rows.Next() {
		var r KeyRecord
		var ad int64
		rows.Scan(&r.ID, &r.Name, &r.Role, &r.NodeScope, &r.MethodScope, &ad, &r.RateLimit, &r.CreatedAt, &r.RevokedAt)
		r.AllowDangerous = ad == 1
		out = append(out, r)
	}
	return out, nil
}

// RevokeKey marks a key revoked.
func (s *Store) RevokeKey(id string) error {
	_, err := s.db.Exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, time.Now().Unix(), id)
	return err
}

// ---- audit ----

// InsertAudit records a dispatch invocation.
func (s *Store) InsertAudit(actorKind, actorID, method, selectJSON, digest string, targets int, dryRun bool, outcome string, started, finished int64) {
	s.db.Exec(`INSERT INTO audit(actor_kind,actor_id,method,select_json,params_digest,target_count,dry_run,outcome,started_at,finished_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		actorKind, actorID, method, selectJSON, digest, targets, b2i(dryRun), outcome, started, finished)
}

// ListAudit returns recent audit rows.
func (s *Store) ListAudit(limit int) ([]map[string]any, error) {
	rows, err := s.db.Query(`SELECT id,actor_kind,actor_id,method,select_json,target_count,dry_run,outcome,started_at,finished_at
		FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var ak, ai, m, sj, oc string
		var tc, dr int
		var st, ft int64
		rows.Scan(&id, &ak, &ai, &m, &sj, &tc, &dr, &oc, &st, &ft)
		out = append(out, map[string]any{
			"id": id, "actorKind": ak, "actorId": ai, "method": m, "select": sj,
			"targetCount": tc, "dryRun": dr == 1, "outcome": oc, "startedAt": st, "finishedAt": ft,
		})
	}
	return out, nil
}

// ---- samples ----

// InsertSample stores one metrics sample.
func (s *Store) InsertSample(nodeID string, m wire.Metrics) {
	s.db.Exec(`INSERT INTO samples(node_id,ts,cpu_pct,mem_used,mem_total,load1,net_rx,net_tx) VALUES(?,?,?,?,?,?,?,?)`,
		nodeID, m.TS, m.CPUPct, m.MemUsed, m.MemTotal, m.Load1, m.NetRx, m.NetTx)
}

// QuerySamples returns samples for a node within [from,to].
func (s *Store) QuerySamples(nodeID string, from, to int64) ([]wire.Metrics, error) {
	if to == 0 {
		to = time.Now().Unix() + 1
	}
	rows, err := s.db.Query(`SELECT ts,cpu_pct,mem_used,mem_total,load1,net_rx,net_tx FROM samples
		WHERE node_id=? AND ts BETWEEN ? AND ? ORDER BY ts`, nodeID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []wire.Metrics{}
	for rows.Next() {
		var m wire.Metrics
		rows.Scan(&m.TS, &m.CPUPct, &m.MemUsed, &m.MemTotal, &m.Load1, &m.NetRx, &m.NetTx)
		out = append(out, m)
	}
	return out, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
