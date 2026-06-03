package update

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const checkInterval = 24 * time.Hour

type checkCache struct {
	LastChecked   time.Time `json:"last_checked"`
	LatestVersion string    `json:"latest_version"`
}

var currentVersion string

func SetCurrentVersion(v string) {
	currentVersion = v
}

func cachePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".runcrate", "update-check.json")
}

func readCache() *checkCache {
	p := cachePath()
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var c checkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil
	}
	return &c
}

func writeCache(c *checkCache) {
	p := cachePath()
	if p == "" {
		return
	}
	dir := filepath.Dir(p)
	os.MkdirAll(dir, 0700)
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	os.WriteFile(p, data, 0600)
}

// NotifyFromCache prints a one-line update notice to w if the cached latest
// version is newer than current. Uses only local state — no network.
func NotifyFromCache(w io.Writer, current string) {
	if current == "dev" || current == "" {
		return
	}
	if os.Getenv("RUNCRATE_NO_UPDATE_CHECK") == "1" {
		return
	}

	c := readCache()
	if c == nil || c.LatestVersion == "" {
		return
	}

	if IsNewer(current, c.LatestVersion) {
		if IsHomebrew() {
			fmt.Fprintf(w, "\nUpdate available: %s → %s (run: brew upgrade runcrate)\n", current, c.LatestVersion)
		} else {
			fmt.Fprintf(w, "\nUpdate available: %s → %s (run: runcrate update)\n", current, c.LatestVersion)
		}
	}
}

// RefreshCache fetches the latest version from GitHub and updates the local
// cache. Intended to be called in a goroutine — errors are silently ignored.
func RefreshCache() {
	if os.Getenv("RUNCRATE_NO_UPDATE_CHECK") == "1" {
		return
	}

	c := readCache()
	if c != nil && time.Since(c.LastChecked) < checkInterval {
		return
	}

	info, err := CheckLatestVersion()
	if err != nil {
		return
	}

	writeCache(&checkCache{
		LastChecked:   time.Now(),
		LatestVersion: info.Version,
	})
}
