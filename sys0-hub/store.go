package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/fakecrowd/sys0/internal/wire"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// ---- gorm models ----

// Node is a persisted agent record (survives disconnects).
type Node struct {
	ID           string `gorm:"primaryKey"`
	Label        string
	Fingerprint  string `gorm:"uniqueIndex"`
	Tags         string
	LastAddr     string
	OS           string
	Arch         string
	Kernel       string
	IP           string
	AgentVersion string
	State        string
	FirstSeen    int64
	LastSeen     int64
}

type User struct {
	ID         uint   `gorm:"primaryKey"`
	Username   string `gorm:"uniqueIndex"`
	SecretHash string
	Role       string
	CreatedAt  int64
}

type APIKey struct {
	ID             string `gorm:"primaryKey"`
	Name           string
	SecretHash     string
	Role           string
	NodeScope      string
	MethodScope    string
	AllowDangerous bool
	RateLimit      int
	CreatedAt      int64
	RevokedAt      int64
}

type Audit struct {
	ID           uint `gorm:"primaryKey"`
	ActorKind    string
	ActorID      string
	Method       string
	SelectJSON   string
	ParamsDigest string
	TargetCount  int
	DryRun       bool
	Outcome      string
	StartedAt    int64
	FinishedAt   int64
}

type Sample struct {
	ID       uint   `gorm:"primaryKey"`
	NodeID   string `gorm:"index:idx_samples_node_ts,priority:1"`
	TS       int64  `gorm:"index:idx_samples_node_ts,priority:2"`
	CPUPct   float64
	MemUsed  uint64
	MemTotal uint64
	Load1    float64
	NetRx    uint64
	NetTx    uint64
}

// Store is the gorm-backed persistence layer.
type Store struct{ db *gorm.DB }

// OpenStore opens (and migrates) the database.
func OpenStore(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1) // serialize writes for sqlite
	}
	if err := db.AutoMigrate(&Node{}, &User{}, &APIKey{}, &Audit{}, &Sample{}); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if sqlDB, err := s.db.DB(); err == nil {
		return sqlDB.Close()
	}
	return nil
}

// ---- hashing ----

func hashSecret(secret string) string {
	salt := randHexS(8)
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

func randHexS(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- nodes ----

// UpsertNode registers or updates a node by fingerprint and returns its id.
func (s *Store) UpsertNode(fp, label, addr string, host wire.HostSummary, version string) (string, error) {
	now := time.Now().Unix()
	var n Node
	err := s.db.Where("fingerprint = ?", fp).First(&n).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		n = Node{ID: "n" + fp[:6], Fingerprint: fp, FirstSeen: now}
	} else if err != nil {
		return "", err
	}
	n.Label = label
	n.LastAddr = addr
	n.OS = host.OS
	n.Arch = host.Arch
	n.Kernel = host.Kernel
	n.IP = host.IP
	n.AgentVersion = version
	n.State = "online"
	n.LastSeen = now
	if err := s.db.Clauses(clause.OnConflict{UpdateAll: true}).Save(&n).Error; err != nil {
		return "", err
	}
	return n.ID, nil
}

func (s *Store) SetNodeState(id, state string) error {
	return s.db.Model(&Node{}).Where("id = ?", id).
		Updates(map[string]any{"state": state, "last_seen": time.Now().Unix()}).Error
}

func (s *Store) SetNodeLabelTags(id, label, tags string) error {
	return s.db.Model(&Node{}).Where("id = ?", id).
		Updates(map[string]any{"label": label, "tags": tags}).Error
}

// ListNodeRecords returns all persisted nodes (online + offline).
func (s *Store) ListNodeRecords() ([]Node, error) {
	var nodes []Node
	err := s.db.Order("id").Find(&nodes).Error
	return nodes, err
}

// DeleteNode removes a persisted node record (used to forget an offline node).
func (s *Store) DeleteNode(id string) error {
	return s.db.Where("id = ?", id).Delete(&Node{}).Error
}

// ---- users ----

func (s *Store) EnsureUser(username, secret, role string) error {
	var count int64
	s.db.Model(&User{}).Where("username = ?", username).Count(&count)
	if count > 0 {
		return nil
	}
	return s.db.Create(&User{Username: username, SecretHash: hashSecret(secret), Role: role, CreatedAt: time.Now().Unix()}).Error
}

func (s *Store) AuthUser(username, secret string) (role string, ok bool) {
	var u User
	if err := s.db.Where("username = ?", username).First(&u).Error; err != nil {
		return "", false
	}
	return u.Role, verifySecret(secret, u.SecretHash)
}

// ---- api keys ----

// KeyRecord is the API-facing view of an api key (no secret).
type KeyRecord struct {
	ID, Name, Role         string
	NodeScope, MethodScope string
	AllowDangerous         bool
	RateLimit              int
	CreatedAt, RevokedAt   int64
}

func keyView(k APIKey) KeyRecord {
	return KeyRecord{
		ID: k.ID, Name: k.Name, Role: k.Role,
		NodeScope: k.NodeScope, MethodScope: k.MethodScope,
		AllowDangerous: k.AllowDangerous, RateLimit: k.RateLimit,
		CreatedAt: k.CreatedAt, RevokedAt: k.RevokedAt,
	}
}

func (s *Store) CreateKey(name, role string, nodeScope, methodScope []string, allowDangerous bool, rate int) (string, KeyRecord, error) {
	secret := "sk_" + randHexS(20)
	k := APIKey{
		ID: "k" + randHexS(4), Name: name, SecretHash: hashSecret(secret), Role: role,
		NodeScope: strings.Join(nodeScope, ","), MethodScope: strings.Join(methodScope, ","),
		AllowDangerous: allowDangerous, RateLimit: rate, CreatedAt: time.Now().Unix(),
	}
	if err := s.db.Create(&k).Error; err != nil {
		return "", KeyRecord{}, err
	}
	return secret, keyView(k), nil
}

func (s *Store) AuthKey(secret string) (KeyRecord, bool) {
	var keys []APIKey
	if err := s.db.Where("revoked_at = 0").Find(&keys).Error; err != nil {
		return KeyRecord{}, false
	}
	for _, k := range keys {
		if verifySecret(secret, k.SecretHash) {
			return keyView(k), true
		}
	}
	return KeyRecord{}, false
}

func (s *Store) ListKeys() ([]KeyRecord, error) {
	var keys []APIKey
	if err := s.db.Where("revoked_at = 0").Order("created_at desc").Find(&keys).Error; err != nil {
		return nil, err
	}
	out := make([]KeyRecord, 0, len(keys))
	for _, k := range keys {
		out = append(out, keyView(k))
	}
	return out, nil
}

func (s *Store) RevokeKey(id string) error {
	return s.db.Model(&APIKey{}).Where("id = ?", id).Update("revoked_at", time.Now().Unix()).Error
}

// ---- audit ----

func (s *Store) InsertAudit(actorKind, actorID, method, selectJSON, digest string, targets int, dryRun bool, outcome string, started, finished int64) {
	s.db.Create(&Audit{
		ActorKind: actorKind, ActorID: actorID, Method: method, SelectJSON: selectJSON,
		ParamsDigest: digest, TargetCount: targets, DryRun: dryRun, Outcome: outcome,
		StartedAt: started, FinishedAt: finished,
	})
}

func (s *Store) ListAudit(limit int) ([]map[string]any, error) {
	var rows []Audit
	if err := s.db.Order("id desc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, map[string]any{
			"id": r.ID, "actorKind": r.ActorKind, "actorId": r.ActorID, "method": r.Method,
			"select": r.SelectJSON, "targetCount": r.TargetCount, "dryRun": r.DryRun,
			"outcome": r.Outcome, "startedAt": r.StartedAt, "finishedAt": r.FinishedAt,
		})
	}
	return out, nil
}

// ---- samples ----

func (s *Store) InsertSample(nodeID string, m wire.Metrics) {
	s.db.Create(&Sample{
		NodeID: nodeID, TS: m.TS, CPUPct: m.CPUPct, MemUsed: m.MemUsed,
		MemTotal: m.MemTotal, Load1: m.Load1, NetRx: m.NetRx, NetTx: m.NetTx,
	})
}

func (s *Store) QuerySamples(nodeID string, from, to int64) ([]wire.Metrics, error) {
	if to == 0 {
		to = time.Now().Unix() + 1
	}
	var rows []Sample
	err := s.db.Where("node_id = ? AND ts BETWEEN ? AND ?", nodeID, from, to).Order("ts").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]wire.Metrics, 0, len(rows))
	for _, r := range rows {
		out = append(out, wire.Metrics{
			TS: r.TS, CPUPct: r.CPUPct, MemUsed: r.MemUsed, MemTotal: r.MemTotal,
			Load1: r.Load1, NetRx: r.NetRx, NetTx: r.NetTx,
		})
	}
	return out, nil
}
