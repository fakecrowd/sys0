package main

import (
	"encoding/json"
	"strings"
)

// buildReleasePayload reshapes GitHub's "latest release" JSON into a compact,
// agent-only download list for the /dl page. On any parse error it returns a
// well-formed payload with an empty asset list and the error surfaced.
func buildReleasePayload(raw []byte, hubVersion string) []byte {
	type ghAsset struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
		DownloadCount      int    `json:"download_count"`
	}
	type ghRelease struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt string    `json:"published_at"`
		Message     string    `json:"message"` // present on error responses
		Assets      []ghAsset `json:"assets"`
	}

	var rel ghRelease
	out := map[string]any{
		"ok":         true,
		"hubVersion": hubVersion,
		"assets":     []any{},
	}
	if err := json.Unmarshal(raw, &rel); err != nil {
		out["ok"] = false
		out["error"] = "parse release: " + err.Error()
		b, _ := json.Marshal(out)
		return b
	}
	if rel.Message != "" && len(rel.Assets) == 0 {
		out["ok"] = false
		out["error"] = rel.Message
		b, _ := json.Marshal(out)
		return b
	}

	out["tag"] = rel.TagName
	out["name"] = rel.Name
	out["releaseUrl"] = rel.HTMLURL
	out["publishedAt"] = rel.PublishedAt

	assets := make([]any, 0, len(rel.Assets))
	for _, a := range rel.Assets {
		// only surface the agent binaries (skip the hub, checksums, etc.)
		if !strings.Contains(a.Name, "agent") {
			continue
		}
		osName, arch := parseTargetFromName(a.Name)
		assets = append(assets, map[string]any{
			"name":          a.Name,
			"url":           a.BrowserDownloadURL,
			"size":          a.Size,
			"downloadCount": a.DownloadCount,
			"os":            osName,
			"arch":          arch,
		})
	}
	out["assets"] = assets
	b, _ := json.Marshal(out)
	return b
}

// parseTargetFromName extracts os/arch from an asset filename like
// "sys0_202606181025_linux_amd64.tar.gz" (best-effort).
func parseTargetFromName(name string) (osName, arch string) {
	for _, o := range []string{"linux", "darwin", "windows"} {
		if strings.Contains(name, o) {
			osName = o
			break
		}
	}
	for _, a := range []string{"amd64", "arm64"} {
		if strings.Contains(name, a) {
			arch = a
			break
		}
	}
	return osName, arch
}
