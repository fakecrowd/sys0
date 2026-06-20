package main

// cache_api.go — console-facing visibility + control over the hub's release
// binary cache. Operators can SEE which agent/rescue release the hub currently
// has cached (and how fresh each asset is) and FORCE a refresh (re-pull the
// latest release from GitHub) without waiting for the 5-min/30-min cache TTLs.
//
// This pairs with the background binary warmer (startBinaryWarmer in
// accounts.go): the warmer keeps things hot automatically, while these
// endpoints let a human confirm the cached version and push a new release out
// immediately after a build.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// cachedAssetView is one binary's cache state for the console.
type cachedAssetView struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"` // agent | rescue
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	Cached bool   `json:"cached"`           // is it currently in binCache & fresh?
	Size   int    `json:"size,omitempty"`   // cached byte length (0 if not cached)
	AgeSec int64  `json:"ageSec,omitempty"` // seconds since this asset was pulled
}

// cacheStatusView is the whole release-cache picture for the console.
type cacheStatusView struct {
	OK            bool              `json:"ok"`
	Tag           string            `json:"tag"`           // current release tag (version)
	ReleaseName   string            `json:"releaseName"`   // release title
	ReleaseURL    string            `json:"releaseUrl"`    // GitHub release page
	PublishedAt   string            `json:"publishedAt"`   // upstream publish time
	ReleaseAgeSec int64             `json:"releaseAgeSec"` // seconds since the hub last fetched the release list
	HubVersion    string            `json:"hubVersion"`    // the running hub's own build version
	Assets        []cachedAssetView `json:"assets"`
	CachedCount   int               `json:"cachedCount"`
	TotalCount    int               `json:"totalCount"`
}

// apiCacheStatus reports what the hub currently has cached. Auth (any logged-in
// user) — read-only.
func (h *Hub) apiCacheStatus(c *gin.Context) {
	c.JSON(http.StatusOK, buildCacheStatus())
}

// apiCacheRefresh forces a re-fetch of the latest release list AND re-pulls
// every agent/rescue binary into the cache, so a freshly-built release is served
// immediately. Admin-only (it triggers outbound GitHub fetches). Returns the
// post-refresh cache status.
func (h *Hub) apiCacheRefresh(c *gin.Context) {
	// Invalidate the memoized release payload so cachedReleasePayload re-fetches.
	dlCache.mu.Lock()
	dlCache.fetched = time.Time{}
	dlCache.payload = nil
	dlCache.mu.Unlock()

	payload, err := cachedReleasePayload()
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
		return
	}
	// Force-pull every asset URL (bypassing the freshness check) so the cache
	// holds the newest bytes right now.
	urls := assetBinaryURLs(payload)
	refreshed := 0
	var failed []string
	for _, u := range urls {
		if _, _, perr := pullBinary(u); perr != nil {
			failed = append(failed, u)
			continue
		}
		refreshed++
	}
	status := buildCacheStatus()
	c.JSON(http.StatusOK, gin.H{
		"ok":        len(failed) == 0,
		"refreshed": refreshed,
		"failed":    failed,
		"status":    status,
	})
}

// buildCacheStatus assembles the current release + per-asset cache view.
func buildCacheStatus() cacheStatusView {
	out := cacheStatusView{OK: true, HubVersion: version, Assets: []cachedAssetView{}}

	payload, err := cachedReleasePayload()
	if err != nil {
		out.OK = false
		return out
	}

	// release age = how long since dlCache was last refreshed.
	dlCache.mu.Lock()
	if !dlCache.fetched.IsZero() {
		out.ReleaseAgeSec = int64(time.Since(dlCache.fetched).Seconds())
	}
	dlCache.mu.Unlock()

	var parsed struct {
		OK          bool   `json:"ok"`
		Tag         string `json:"tag"`
		Name        string `json:"name"`
		ReleaseURL  string `json:"releaseUrl"`
		PublishedAt string `json:"publishedAt"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
			OS   string `json:"os"`
			Arch string `json:"arch"`
			Kind string `json:"kind"`
		} `json:"assets"`
	}
	if jerr := json.Unmarshal(payload, &parsed); jerr != nil || !parsed.OK {
		out.OK = false
		return out
	}
	out.Tag = parsed.Tag
	out.ReleaseName = parsed.Name
	out.ReleaseURL = parsed.ReleaseURL
	out.PublishedAt = parsed.PublishedAt

	for _, a := range parsed.Assets {
		if a.Kind != "agent" && a.Kind != "rescue" {
			continue
		}
		av := cachedAssetView{Name: a.Name, Kind: a.Kind, OS: a.OS, Arch: a.Arch, URL: a.URL}
		binCacheMu.Lock()
		if e, ok := binCache[a.URL]; ok && time.Since(e.fetched) < binCacheTTL {
			av.Cached = true
			av.Size = len(e.body)
			av.AgeSec = int64(time.Since(e.fetched).Seconds())
		}
		binCacheMu.Unlock()
		out.TotalCount++
		if av.Cached {
			out.CachedCount++
		}
		out.Assets = append(out.Assets, av)
	}
	return out
}
