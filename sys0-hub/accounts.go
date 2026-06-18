package main

import (
	"encoding/json"
	"fmt"
	"io"
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

// apiAgentRedirect 302-redirects to the download URL of the latest agent binary
// matching ?os=&arch=. This lets a minimal client (e.g. sys0-rescue) fetch the
// right agent with a single GET + redirect-follow, no JSON parsing required.
// os/arch default to the requester is irrelevant server-side; caller must pass
// them (linux|darwin|windows, amd64|arm64). Public, no auth.
func (h *Hub) apiAgentRedirect(c *gin.Context) {
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
	var parsed struct {
		OK     bool `json:"ok"`
		Assets []struct {
			URL  string `json:"url"`
			OS   string `json:"os"`
			Arch string `json:"arch"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil || !parsed.OK {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": "release list unavailable"})
		return
	}
	for _, a := range parsed.Assets {
		if a.OS == wantOS && a.Arch == wantArch && a.URL != "" {
			c.Redirect(http.StatusFound, a.URL)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"ok": false, "error": "no agent asset for " + wantOS + "/" + wantArch})
}
