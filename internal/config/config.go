package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	configDirName  = ".runcrate"
	configFileName = "config.yaml"
	defaultAPIURL  = "https://runcrate.ai"
)

type Config struct {
	APIURL string `yaml:"api_url,omitempty"`

	// OAuth auth (primary)
	AccessToken  string `yaml:"access_token,omitempty"`
	RefreshToken string `yaml:"refresh_token,omitempty"`
	ExpiresAt    int64  `yaml:"expires_at,omitempty"`
	ProjectID    string `yaml:"project_id,omitempty"`
	ProjectName  string `yaml:"project_name,omitempty"`
	UserEmail    string `yaml:"user_email,omitempty"`
	Role         string `yaml:"role,omitempty"`
	Environment  string `yaml:"environment,omitempty"`
	SupabaseURL  string `yaml:"supabase_url,omitempty"`
	SupabaseAnon string `yaml:"supabase_anon_key,omitempty"`

	// Legacy API key auth (backward compat)
	APIKey string `yaml:"api_key,omitempty"`
}

func (c *Config) IsOAuth() bool {
	return c.AccessToken != ""
}

func (c *Config) IsAuthenticated() bool {
	return c.AccessToken != "" || c.APIKey != ""
}

// NormalizeURL cleans up a user-supplied app/API base URL: it trims surrounding
// whitespace, drops any trailing slash, and assumes https:// when no scheme is
// given. This lets users pass a bare host (e.g. a Vercel preview domain) without
// the URL-building in login/api breaking on a missing scheme or double slash.
// An empty/blank input returns "" so callers can fall back to their default.
func NormalizeURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", configDirName)
	}
	return filepath.Join(home, configDirName)
}

func ConfigPath() string {
	return filepath.Join(DefaultConfigDir(), configFileName)
}

func Load() (*Config, error) {
	cfg := &Config{
		APIURL: defaultAPIURL,
	}

	data, err := os.ReadFile(ConfigPath())
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
	}

	// Env vars override file values
	if key := os.Getenv("RUNCRATE_API_KEY"); key != "" {
		cfg.APIKey = key
	}
	if url := os.Getenv("RUNCRATE_API_URL"); url != "" {
		cfg.APIURL = url
	}
	if token := os.Getenv("RUNCRATE_ACCESS_TOKEN"); token != "" {
		cfg.AccessToken = token
	}
	if pid := os.Getenv("RUNCRATE_PROJECT_ID"); pid != "" {
		cfg.ProjectID = pid
	}
	if env := os.Getenv("RUNCRATE_ENVIRONMENT"); env != "" {
		cfg.Environment = env
	}

	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}

	return cfg, nil
}

func Save(cfg *Config) error {
	dir := DefaultConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(ConfigPath(), data, 0600); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}

func Clear() error {
	err := os.Remove(ConfigPath())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
