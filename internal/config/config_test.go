package config

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestDir(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()

	// Override config dir for tests
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)

	return dir, func() {
		os.Setenv("HOME", origHome)
	}
}

func TestDefaultConfigDir(t *testing.T) {
	dir, cleanup := setupTestDir(t)
	defer cleanup()

	got := DefaultConfigDir()
	want := filepath.Join(dir, ".runcrate")
	if got != want {
		t.Errorf("DefaultConfigDir() = %q, want %q", got, want)
	}
}

func TestConfigPath(t *testing.T) {
	dir, cleanup := setupTestDir(t)
	defer cleanup()

	got := ConfigPath()
	want := filepath.Join(dir, ".runcrate", "config.yaml")
	if got != want {
		t.Errorf("ConfigPath() = %q, want %q", got, want)
	}
}

func TestSaveAndLoad(t *testing.T) {
	_, cleanup := setupTestDir(t)
	defer cleanup()

	cfg := &Config{
		APIKey: "rc_live_testkey123",
		APIURL: "https://staging.runcrate.ai",
	}

	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.APIKey != cfg.APIKey {
		t.Errorf("APIKey = %q, want %q", loaded.APIKey, cfg.APIKey)
	}
	if loaded.APIURL != cfg.APIURL {
		t.Errorf("APIURL = %q, want %q", loaded.APIURL, cfg.APIURL)
	}
}

func TestLoadDefaults(t *testing.T) {
	_, cleanup := setupTestDir(t)
	defer cleanup()

	// No config file exists — should return defaults
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", cfg.APIKey)
	}
	if cfg.APIURL != defaultAPIURL {
		t.Errorf("APIURL = %q, want %q", cfg.APIURL, defaultAPIURL)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	_, cleanup := setupTestDir(t)
	defer cleanup()

	// Save a config first
	cfg := &Config{
		APIKey: "rc_live_filekey",
		APIURL: "https://file.runcrate.ai",
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Set env vars — should override file values
	os.Setenv("RUNCRATE_API_KEY", "rc_live_envkey")
	os.Setenv("RUNCRATE_API_URL", "https://env.runcrate.ai")
	defer os.Unsetenv("RUNCRATE_API_KEY")
	defer os.Unsetenv("RUNCRATE_API_URL")

	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if loaded.APIKey != "rc_live_envkey" {
		t.Errorf("APIKey = %q, want %q (env override)", loaded.APIKey, "rc_live_envkey")
	}
	if loaded.APIURL != "https://env.runcrate.ai" {
		t.Errorf("APIURL = %q, want %q (env override)", loaded.APIURL, "https://env.runcrate.ai")
	}
}

func TestClear(t *testing.T) {
	_, cleanup := setupTestDir(t)
	defer cleanup()

	// Save then clear
	cfg := &Config{APIKey: "rc_live_clearme", APIURL: "https://runcrate.ai"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if err := Clear(); err != nil {
		t.Fatalf("Clear() error: %v", err)
	}

	// File should be gone — Load returns defaults
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after Clear() error: %v", err)
	}
	if loaded.APIKey != "" {
		t.Errorf("APIKey after Clear() = %q, want empty", loaded.APIKey)
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"runcrate.ai", "https://runcrate.ai"},
		{"https://runcrate.ai", "https://runcrate.ai"},
		{"https://runcrate.ai/", "https://runcrate.ai"},
		{"http://localhost:3000", "http://localhost:3000"},
		{"http://localhost:3000/", "http://localhost:3000"},
		{"my-preview.vercel.app", "https://my-preview.vercel.app"},
		{"my-preview.vercel.app/", "https://my-preview.vercel.app"},
		{"  https://staging.runcrate.ai/  ", "https://staging.runcrate.ai"},
		{"", ""},
		{"   ", ""},
	}

	for _, tt := range tests {
		if got := NormalizeURL(tt.input); got != tt.want {
			t.Errorf("NormalizeURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestClearNoFile(t *testing.T) {
	_, cleanup := setupTestDir(t)
	defer cleanup()

	// Clear when no file exists — should not error
	if err := Clear(); err != nil {
		t.Errorf("Clear() with no file: %v", err)
	}
}

func TestSaveFilePermissions(t *testing.T) {
	_, cleanup := setupTestDir(t)
	defer cleanup()

	cfg := &Config{APIKey: "rc_live_perms", APIURL: "https://runcrate.ai"}
	if err := Save(cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Check file permissions (0600)
	info, err := os.Stat(ConfigPath())
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}

	// Check directory permissions (0700)
	dirInfo, err := os.Stat(DefaultConfigDir())
	if err != nil {
		t.Fatalf("Stat() dir error: %v", err)
	}
	dirPerm := dirInfo.Mode().Perm()
	if dirPerm != 0700 {
		t.Errorf("dir permissions = %o, want 0700", dirPerm)
	}
}
