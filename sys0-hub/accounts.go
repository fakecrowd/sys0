package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ---- first-run setup ----

// apiSetupStatus reports whether the hub still needs first-run setup (no users).
func (h *Hub) apiSetupStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true, "needsSetup": h.store.CountUsers() == 0})
}

// apiSetup creates the first administrator. Only callable while no user exists.
func (h *Hub) apiSetup(c *gin.Context) {
	if h.store.CountUsers() > 0 {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "already initialized"})
		return
	}
	var body struct{ Username, Password string }
	if c.BindJSON(&body) != nil {
		return
	}
	if strings.TrimSpace(body.Username) == "" || len(body.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "username required and password must be at least 6 characters"})
		return
	}
	if _, err := h.store.CreateUser(body.Username, body.Password, "admin", nil); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	tok := h.signToken(body.Username, "admin", 12*time.Hour)
	c.JSON(http.StatusOK, gin.H{"ok": true, "token": tok, "role": "admin", "username": body.Username})
}

// ---- current user ----

func (h *Hub) apiMe(c *gin.Context) {
	a := actorOf(c)
	u, ok := h.store.GetUser(a.ID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "user": u})
}

func (h *Hub) apiChangeOwnPassword(c *gin.Context) {
	var body struct{ OldPassword, NewPassword string }
	if c.BindJSON(&body) != nil {
		return
	}
	a := actorOf(c)
	if _, ok := h.store.AuthUser(a.ID, body.OldPassword); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "current password incorrect"})
		return
	}
	if len(body.NewPassword) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "new password must be at least 6 characters"})
		return
	}
	u, _ := h.store.GetUser(a.ID)
	if err := h.store.SetUserPassword(u.ID, body.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- user management (admin) ----

func (h *Hub) apiListUsers(c *gin.Context) {
	users, err := h.store.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "users": users})
}

func (h *Hub) apiCreateUser(c *gin.Context) {
	var body struct {
		Username  string   `json:"username"`
		Password  string   `json:"password"`
		Role      string   `json:"role"`
		NodeScope []string `json:"nodeScope"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	if strings.TrimSpace(body.Username) == "" || len(body.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "username required and password must be at least 6 characters"})
		return
	}
	rec, err := h.store.CreateUser(body.Username, body.Password, body.Role, body.NodeScope)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "user": rec})
}

func paramUint(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "bad id"})
		return 0, false
	}
	return uint(id), true
}

func (h *Hub) apiUserScope(c *gin.Context) {
	id, ok := paramUint(c)
	if !ok {
		return
	}
	var body struct {
		NodeScope []string `json:"nodeScope"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	if err := h.store.UpdateUserScope(id, body.NodeScope); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Hub) apiUserRole(c *gin.Context) {
	id, ok := paramUint(c)
	if !ok {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	// guard: don't demote the last admin
	if body.Role != "admin" {
		if u, found := h.store.GetUserByID(id); found && u.Role == "admin" && h.store.CountAdmins() <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "cannot demote the last admin"})
			return
		}
	}
	if err := h.store.UpdateUserRole(id, body.Role); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Hub) apiUserPassword(c *gin.Context) {
	id, ok := paramUint(c)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	if len(body.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "password must be at least 6 characters"})
		return
	}
	if err := h.store.SetUserPassword(id, body.Password); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Hub) apiDeleteUser(c *gin.Context) {
	id, ok := paramUint(c)
	if !ok {
		return
	}
	// guard: don't delete the last admin, and don't delete yourself
	if u, found := h.store.GetUserByID(id); found {
		if u.Username == actorOf(c).ID {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "cannot delete your own account"})
			return
		}
		if u.Role == "admin" && h.store.CountAdmins() <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "cannot delete the last admin"})
			return
		}
	}
	if err := h.store.DeleteUser(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- default new-node access policy (admin) ----

func (h *Hub) apiGetDefaultAccess(c *gin.Context) {
	csv := h.store.GetSetting("default_node_users", "")
	c.JSON(http.StatusOK, gin.H{"ok": true, "users": splitScope(csv)})
}

func (h *Hub) apiSetDefaultAccess(c *gin.Context) {
	var body struct {
		Users []string `json:"users"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	if err := h.store.SetSetting("default_node_users", strings.Join(body.Users, ",")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- agent downloads (/dl) ----

// releaseCache memoizes the GitHub releases lookup for a short window so the
// /dl page doesn't hammer the API.
type releaseCache struct {
	mu      sync.Mutex
	fetched time.Time
	payload []byte
	status  int
}

var dlCache releaseCache

const releaseSource = "https://api.github.com/repos/fakecrowd/sys0/releases/latest"

// cachedReleasePayload returns the compact, agent-only release payload (the same
// JSON served at /api/v1/releases), memoized for 5 minutes. Shared by the /dl
// list handler and the /agent redirect handler.
func cachedReleasePayload() ([]byte, error) {
	dlCache.mu.Lock()
	defer dlCache.mu.Unlock()
	if time.Since(dlCache.fetched) < 5*time.Minute && dlCache.payload != nil {
		return dlCache.payload, nil
	}
	req, _ := http.NewRequest("GET", releaseSource, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sys0-hub")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch releases: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	payload := buildReleasePayload(raw, version)
	dlCache.fetched = time.Now()
	dlCache.payload = payload
	dlCache.status = http.StatusOK
	return payload, nil
}

// apiReleases proxies the latest GitHub release's agent assets for the /dl page.
// Public (no auth) so operators can grab the agent binary easily.
func (h *Hub) apiReleases(c *gin.Context) {
	payload, err := cachedReleasePayload()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", payload)
}

// apiAgentRedirect streams the latest agent binary matching ?os=&arch= DIRECTLY
// from the hub (HTTP 200 + body), proxying it from the GitHub release. It does
// NOT 302 to github.com — many restricted networks can reach the hub but not
// GitHub/its CDN, so a redirect would leave them unable to download. A minimal
// client (e.g. sys0-rescue) fetches the right agent with a single GET, no JSON
// parsing required. Caller must pass os/arch (linux|darwin|windows,
// amd64|arm64). Public, no auth.
func (h *Hub) apiAgentRedirect(c *gin.Context) {
	h.serveBinary(c, "agent")
}

// apiRescueRedirect streams the latest sys0-rescue binary matching ?os=&arch=
// directly from the hub. Same direct-proxy contract as apiAgentRedirect (no
// github.com redirect). Public, no auth.
func (h *Hub) apiRescueRedirect(c *gin.Context) {
	h.serveBinary(c, "rescue")
}

// serveBinary resolves the release asset of the given kind/os/arch and streams
// its bytes to the client as 200 OK, proxying from GitHub through the hub. This
// is the key to working on networks that can reach the hub but not GitHub: the
// node never talks to github.com — only to the hub it already trusts.
func (h *Hub) serveBinary(c *gin.Context, kind string) {
	wantOS := strings.ToLower(c.Query("os"))
	wantArch := strings.ToLower(c.Query("arch"))
	if wantOS == "" || wantArch == "" {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "os and arch query params required"})
		return
	}
	payload, err := cachedReleasePayload()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	url, name, ok := assetFor(payload, kind, wantOS, wantArch)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no " + kind + " asset for " + wantOS + "/" + wantArch})
		return
	}
	body, ctype, err := fetchBinary(url)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	if name != "" {
		c.Header("Content-Disposition", "attachment; filename=\""+name+"\"")
	}
	c.Header("Content-Length", strconv.Itoa(len(body)))
	c.Data(http.StatusOK, ctype, body)
}

// ---- release binary proxy cache ----

// binCacheEntry is a cached release binary (bytes + content-type) keyed by its
// download URL. The asset set is tiny (~12 files) and changes only on a new
// release, so a short-TTL in-memory cache keeps the hub from re-pulling from
// GitHub on every node download while staying bounded.
type binCacheEntry struct {
	fetched time.Time
	body    []byte
	ctype   string
}

var (
	binCacheMu sync.Mutex
	binCache   = map[string]binCacheEntry{}
)

const binCacheTTL = 30 * time.Minute
const binMaxSize = 64 << 20 // 64 MiB per asset hard cap

// fetchBinary returns the asset bytes for url, served from binCache when fresh
// (within binCacheTTL) and otherwise pulled from GitHub via pullBinary. This is
// the node-facing hot path (serveBinary); the cache warmer keeps entries fresh
// so this almost always hits memory instead of a slow hub->GitHub fetch.
func fetchBinary(url string) ([]byte, string, error) {
	binCacheMu.Lock()
	if e, ok := binCache[url]; ok && time.Since(e.fetched) < binCacheTTL {
		body, ctype := e.body, e.ctype
		binCacheMu.Unlock()
		return body, ctype, nil
	}
	binCacheMu.Unlock()
	return pullBinary(url)
}

// pullBinary unconditionally downloads a release asset URL server-side,
// following redirects (github.com -> objects.githubusercontent.com), stores the
// bytes in binCache, and returns (body, contentType, error). Used by
// fetchBinary on a cache miss and by the cache warmer to refresh entries.
func pullBinary(url string) ([]byte, string, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "sys0-hub")
	resp, err := http.DefaultClient.Do(req) // default client follows up to 10 redirects
	if err != nil {
		return nil, "", fmt.Errorf("fetch binary: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, binMaxSize))
	if err != nil {
		return nil, "", fmt.Errorf("read binary: %w", err)
	}
	ctype := resp.Header.Get("Content-Type")
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	binCacheMu.Lock()
	binCache[url] = binCacheEntry{fetched: time.Now(), body: body, ctype: ctype}
	binCacheMu.Unlock()
	return body, ctype, nil
}

// ---- release binary cache warmer ----

const (
	binWarmInterval = 5 * time.Minute  // how often the warmer checks freshness
	binWarmMaxAge   = 20 * time.Minute // re-pull assets older than this (< binCacheTTL so a cached asset never lapses while nodes are pulling)
)

// assetBinaryURLs returns the download URLs of all agent+rescue assets in the
// compact release payload (used by the cache warmer to pre-pull them).
func assetBinaryURLs(payload []byte) []string {
	var parsed struct {
		OK     bool `json:"ok"`
		Assets []struct {
			URL  string `json:"url"`
			Kind string `json:"kind"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil || !parsed.OK {
		return nil
	}
	urls := make([]string, 0, len(parsed.Assets))
	for _, a := range parsed.Assets {
		if (a.Kind == "agent" || a.Kind == "rescue") && a.URL != "" {
			urls = append(urls, a.URL)
		}
	}
	return urls
}

// startBinaryWarmer keeps the release binaries hot in binCache so node
// downloads (sys0-rescue pulling the ~6.7MB agent, operators using /dl) are
// always served from the hub's memory instead of triggering a slow hub->GitHub
// fetch mid-stream (the #1 cause of multi-minute downloads). It pre-warms on
// startup and refreshes every binWarmInterval, re-pulling any asset older than
// binWarmMaxAge. When a new release lands the asset URLs change, so stale
// entries age out at binCacheTTL and the new ones get warmed automatically.
// Blocking; run in its own goroutine.
func startBinaryWarmer(log *slog.Logger) {
	warm := func() {
		payload, err := cachedReleasePayload()
		if err != nil {
			log.Warn("binary warmer: release fetch failed", "err", err)
			return
		}
		urls := assetBinaryURLs(payload)
		refreshed := 0
		for _, u := range urls {
			binCacheMu.Lock()
			e, ok := binCache[u]
			fresh := ok && time.Since(e.fetched) < binWarmMaxAge
			binCacheMu.Unlock()
			if fresh {
				continue
			}
			if _, _, err := pullBinary(u); err != nil {
				log.Warn("binary warmer: pull failed", "url", u, "err", err)
				continue
			}
			refreshed++
		}
		if refreshed > 0 {
			log.Info("binary warmer: cache refreshed", "refreshed", refreshed, "assets", len(urls))
		}
	}
	warm()
	t := time.NewTicker(binWarmInterval)
	defer t.Stop()
	for range t.C {
		warm()
	}
}

// assetFor finds the download URL AND filename of the asset with the given
// kind/os/arch in the compact release payload.
func assetFor(payload []byte, kind, wantOS, wantArch string) (url, name string, ok bool) {
	var parsed struct {
		OK     bool `json:"ok"`
		Assets []struct {
			URL  string `json:"url"`
			Name string `json:"name"`
			OS   string `json:"os"`
			Arch string `json:"arch"`
			Kind string `json:"kind"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil || !parsed.OK {
		return "", "", false
	}
	for _, a := range parsed.Assets {
		if a.Kind == kind && a.OS == wantOS && a.Arch == wantArch && a.URL != "" {
			return a.URL, a.Name, true
		}
	}
	return "", "", false
}

// ---- rescue liveness (sys0-rescue -> hub binding) ----

// rescueInfo records the last report from a node's supervising rescue process.
// Beyond liveness it carries the rescue's self-reported phase/detail so the
// console can show a live status view (downloading, supervising, restarting…).
type rescueInfo struct {
	version      string
	status       string // phase: starting|downloading|starting-agent|supervising|restarting|error
	detail       string // free-text detail (last log-worthy event)
	restarts     int    // how many times the rescue has (re)started the agent
	lastExit     int    // last agent exit code (-1 = none yet)
	lastUptimeMs int64  // how long the agent ran last time
	cwd          string // rescue work dir (download/stage/decoy location)
	agentPid     int    // pid of the agent the rescue currently supervises (-1 = none)
	trace        []traceEvent // recent rescue activity (agent startup sequence, etc.)
	firstSeen    time.Time
	lastSeen     time.Time
}

// traceEvent is one timestamped line from the rescue's activity log.
type traceEvent struct {
	T int64  `json:"t"` // unix seconds
	M string `json:"m"` // message
}

// rescueReports tracks, per node id, the most recent rescue report. A node is
// considered "rescue-supervised" if its last report is within rescueTTL.
var (
	rescueMu      sync.Mutex
	rescueReports = map[string]rescueInfo{}
	// rescueDismissed holds node ids an operator has explicitly dismissed,
	// mapped to the time the dismissal expires. A runaway rescue elsewhere can
	// keep POSTing /rescue/report forever; without this, the synthesized
	// bootstrapping node is impossible to clear (deleting the map entry just
	// gets recreated on the next 30s report). While dismissed, incoming reports
	// for that id are dropped and the node is hidden.
	rescueDismissed = map[string]time.Time{}
)

const rescueTTL = 90 * time.Second

// rescueDismissWindow is how long a dismissal suppresses a node id. Long enough
// to outlast a runaway reporter the operator is going to hunt down/kill.
const rescueDismissWindow = 30 * time.Minute

// rescueIsDismissed reports whether a node id is currently suppressed.
// Caller must hold rescueMu.
func rescueIsDismissed(nodeID string) bool {
	until, ok := rescueDismissed[nodeID]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(rescueDismissed, nodeID)
		return false
	}
	return true
}

// dismissRescue suppresses a rescue-only node id and forgets its last report.
func dismissRescue(nodeID string) {
	rescueMu.Lock()
	defer rescueMu.Unlock()
	rescueDismissed[nodeID] = time.Now().Add(rescueDismissWindow)
	delete(rescueReports, nodeID)
}

// rescueView is the live rescue status surfaced on a NodeView.
type rescueView struct {
	Live         bool            `json:"live"`
	Version      string          `json:"version"`
	Status       string          `json:"status"`
	Detail       string          `json:"detail"`
	Restarts     int             `json:"restarts"`
	LastExit     int             `json:"lastExit"`
	LastUptimeMs int64           `json:"lastUptimeMs"`
	SinceSec     int64           `json:"sinceSec"`           // seconds the rescue has been continuously reporting
	AgeSec       int64           `json:"ageSec"`             // seconds since the last report
	Cwd          string          `json:"cwd,omitempty"`      // rescue work dir
	AgentPid     int             `json:"agentPid,omitempty"` // supervised agent pid
	Trace        []traceEvent    `json:"trace,omitempty"`    // recent rescue activity (startup sequence)
	Commands     []rescueCommand `json:"commands,omitempty"` // recent operator commands + their status
}

// rescueStatus returns the live rescue status for a node (Live=false when no
// fresh report exists within rescueTTL).
func rescueStatus(nodeID string) rescueView {
	rescueMu.Lock()
	defer rescueMu.Unlock()
	if rescueIsDismissed(nodeID) {
		return rescueView{}
	}
	info, ok := rescueReports[nodeID]
	if !ok || time.Since(info.lastSeen) > rescueTTL {
		return rescueView{}
	}
	return rescueView{
		Live:         true,
		Version:      info.version,
		Status:       info.status,
		Detail:       info.detail,
		Restarts:     info.restarts,
		LastExit:     info.lastExit,
		LastUptimeMs: info.lastUptimeMs,
		Cwd:          info.cwd,
		AgentPid:     info.agentPid,
		Trace:        info.trace,
		SinceSec:     int64(time.Since(info.firstSeen).Seconds()),
		AgeSec:       int64(time.Since(info.lastSeen).Seconds()),
		Commands:     rescueCommandHistory(nodeID),
	}
}

// liveRescueNodes returns the node ids that currently have a fresh rescue report
// (within rescueTTL), each with its live status. Used to surface rescue-only
// nodes — ones where the rescue is bootstrapping but the agent hasn't connected
// yet — so operators can watch the download/start before the agent appears.
func liveRescueNodes() map[string]rescueView {
	rescueMu.Lock()
	defer rescueMu.Unlock()
	out := map[string]rescueView{}
	for id, info := range rescueReports {
		if time.Since(info.lastSeen) > rescueTTL {
			continue
		}
		if rescueIsDismissed(id) {
			continue
		}
		out[id] = rescueView{
			Live:         true,
			Version:      info.version,
			Status:       info.status,
			Detail:       info.detail,
			Restarts:     info.restarts,
			LastExit:     info.lastExit,
			LastUptimeMs: info.lastUptimeMs,
			Cwd:          info.cwd,
			AgentPid:     info.agentPid,
			Trace:        info.trace,
			SinceSec:     int64(time.Since(info.firstSeen).Seconds()),
			AgeSec:       int64(time.Since(info.lastSeen).Seconds()),
			Commands:     rescueCommandHistory(id),
		}
	}
	return out
}

// apiRescueReport accepts a small JSON report from a sys0-rescue process. It is
// authenticated by the same pre-shared agent key. The node id is derived from
// the agent fingerprint exactly as UpsertNode does ("n" + fingerprint[:6]), so
// the rescue binds to the same node the agent registers — no DB write needed.
// The rescue reports from cold start (before the agent is even downloaded) and
// continuously through every phase, so this can arrive with no live agent yet.
func (h *Hub) apiRescueReport(c *gin.Context) {
	var body struct {
		Key          string `json:"key"`
		Fingerprint  string `json:"fingerprint"`
		Version      string `json:"version"`
		OS           string `json:"os"`
		Arch         string `json:"arch"`
		Status       string `json:"status"`
		Detail       string `json:"detail"`
		Cwd          string `json:"cwd"`
		AgentPid     int    `json:"agentPid"`
		Restarts     int    `json:"restarts"`
		LastExit     int    `json:"lastExit"`
		LastUptimeMs int64  `json:"lastUptimeMs"`
		Trace        []traceEvent `json:"trace"`
		Results      []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"results"`
	}
	if c.BindJSON(&body) != nil {
		return
	}
	if body.Key != h.cfg.AccessKey {
		c.JSON(http.StatusForbidden, gin.H{"ok": false, "error": "invalid access key"})
		return
	}
	if len(body.Fingerprint) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"ok": false, "error": "invalid fingerprint"})
		return
	}
	// Clamp free-text to keep the in-memory map bounded.
	if len(body.Detail) > 240 {
		body.Detail = body.Detail[:240]
	}
	if body.Status == "" {
		body.Status = "supervising"
	}
	if len(body.Cwd) > 512 {
		body.Cwd = body.Cwd[:512]
	}
	// Clamp trace to keep the in-memory map bounded (rescue caps at trace_cap=32).
	if len(body.Trace) > 32 {
		body.Trace = body.Trace[len(body.Trace)-32:]
	}
	for i := range body.Trace {
		if len(body.Trace[i].M) > 160 {
			body.Trace[i].M = body.Trace[i].M[:160]
		}
	}
	if body.AgentPid == 0 {
		body.AgentPid = -1 // rescue sends -1 for "no agent"; normalize a missing field too
	}
	nodeID := "n" + body.Fingerprint[:6]
	now := time.Now()
	rescueMu.Lock()
	if rescueIsDismissed(nodeID) {
		// Operator dismissed this (likely a runaway/zombie rescue). Ack so the
		// reporter doesn't error-loop, but don't resurrect the node.
		rescueMu.Unlock()
		c.JSON(http.StatusOK, gin.H{"ok": true, "dismissed": true})
		return
	}
	prev, existed := rescueReports[nodeID]
	first := now
	if existed && time.Since(prev.lastSeen) <= rescueTTL && !prev.firstSeen.IsZero() {
		first = prev.firstSeen // preserve continuous-uptime origin
	}
	rescueReports[nodeID] = rescueInfo{
		version:      body.Version,
		status:       body.Status,
		detail:       body.Detail,
		restarts:     body.Restarts,
		lastExit:     body.LastExit,
		lastUptimeMs: body.LastUptimeMs,
		cwd:          body.Cwd,
		agentPid:     body.AgentPid,
		trace:        body.Trace,
		firstSeen:    first,
		lastSeen:     now,
	}
	rescueMu.Unlock()
	// Record any command results the rescue is reporting back, then hand it the
	// commands still pending so it can execute them (HTTPS long-poll style).
	applyRescueResults(nodeID, body.Results)
	pending := pendingRescueCommands(nodeID)
	// Is the agent for this node currently connected to the hub? The rescue uses
	// this to decide whether to (re)launch the agent — the two are decoupled
	// processes that only learn each other's liveness via the hub, so a rescue
	// no longer supervises the agent as a child (which restart-spammed whenever a
	// separately-started agent already held the single-instance lock).
	agentOnline := h.reg.get(nodeID) != nil
	// nudge consoles so the rescue badge/detail updates without a poll
	h.reg.broadcast("node", "event.node", gin.H{
		"event": "rescue", "id": nodeID,
		"rescueVersion": body.Version, "status": body.Status, "detail": body.Detail,
	})
	c.JSON(http.StatusOK, gin.H{"ok": true, "node": nodeID, "commands": pending, "agentOnline": agentOnline})
}
