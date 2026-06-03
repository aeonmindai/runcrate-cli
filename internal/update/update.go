package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	releasesURL = "https://api.github.com/repos/aeonmindai/runcrate-cli/releases?per_page=20"
	httpTimeout = 10 * time.Second
)

type ReleaseInfo struct {
	Version     string
	DownloadURL string
	ChecksumURL string
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func CheckLatestVersion() (*ReleaseInfo, error) {
	client := &http.Client{Timeout: httpTimeout}
	req, err := http.NewRequest("GET", releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching releases: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("parsing releases: %w", err)
	}

	binaryName := binaryNameForPlatform()

	for _, r := range releases {
		// Standalone runcrate-cli repo tags releases as plain `vX.Y.Z`.
		if !strings.HasPrefix(r.TagName, "v") {
			continue
		}
		version := r.TagName

		info := &ReleaseInfo{Version: version}
		for _, a := range r.Assets {
			if a.Name == binaryName {
				info.DownloadURL = a.BrowserDownloadURL
			}
			if a.Name == "checksums.txt" {
				info.ChecksumURL = a.BrowserDownloadURL
			}
		}

		if info.DownloadURL != "" {
			return info, nil
		}
	}

	return nil, fmt.Errorf("no CLI release found for %s/%s", runtime.GOOS, runtime.GOARCH)
}

func IsNewer(current, latest string) bool {
	if current == "dev" || current == "" {
		return false
	}
	c := ensureVPrefix(current)
	l := ensureVPrefix(latest)
	if !semver.IsValid(c) || !semver.IsValid(l) {
		return false
	}
	return semver.Compare(l, c) > 0
}

func DownloadAndReplace(info *ReleaseInfo) error {
	client := &http.Client{Timeout: 120 * time.Second}

	// Download the binary
	resp, err := client.Get(info.DownloadURL)
	if err != nil {
		return fmt.Errorf("downloading binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	binaryData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading binary: %w", err)
	}

	// Verify checksum if available
	if info.ChecksumURL != "" {
		if err := verifyChecksum(client, info.ChecksumURL, binaryNameForPlatform(), binaryData); err != nil {
			return fmt.Errorf("checksum verification failed: %w", err)
		}
	}

	// Find current binary path
	currentBin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	currentBin, err = filepath.EvalSymlinks(currentBin)
	if err != nil {
		return fmt.Errorf("resolving binary path: %w", err)
	}

	dir := filepath.Dir(currentBin)
	base := filepath.Base(currentBin)

	if runtime.GOOS == "windows" {
		// Windows locks running executables — use rename dance
		newPath := filepath.Join(dir, base+".new")
		oldPath := filepath.Join(dir, base+".old")

		if err := os.WriteFile(newPath, binaryData, 0755); err != nil {
			return fmt.Errorf("writing new binary: %w", err)
		}
		os.Remove(oldPath)
		if err := os.Rename(currentBin, oldPath); err != nil {
			os.Remove(newPath)
			return fmt.Errorf("renaming current binary: %w", err)
		}
		if err := os.Rename(newPath, currentBin); err != nil {
			os.Rename(oldPath, currentBin)
			return fmt.Errorf("replacing binary: %w", err)
		}
		os.Remove(oldPath)
	} else {
		// Unix: atomic rename
		tmpPath := filepath.Join(dir, ".runcrate-update-"+fmt.Sprintf("%d", time.Now().UnixNano()))
		if err := os.WriteFile(tmpPath, binaryData, 0755); err != nil {
			return fmt.Errorf("writing new binary: %w", err)
		}
		if err := os.Rename(tmpPath, currentBin); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("replacing binary: %w", err)
		}
	}

	return nil
}

func IsHomebrew() bool {
	bin, err := os.Executable()
	if err != nil {
		return false
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return false
	}
	return strings.Contains(bin, "/Cellar/") || strings.Contains(bin, "/homebrew/")
}

func binaryNameForPlatform() string {
	name := fmt.Sprintf("runcrate-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func verifyChecksum(client *http.Client, checksumURL, binaryName string, binaryData []byte) error {
	resp, err := client.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("downloading checksums: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	actual := sha256.Sum256(binaryData)
	actualHex := hex.EncodeToString(actual[:])

	for _, line := range strings.Split(string(body), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == binaryName {
			if parts[0] == actualHex {
				return nil
			}
			return fmt.Errorf("expected %s, got %s", parts[0], actualHex)
		}
	}

	return fmt.Errorf("no checksum found for %s", binaryName)
}

func ensureVPrefix(v string) string {
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}
