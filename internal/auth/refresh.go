package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/config"
)

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
}

func RefreshTokens(cfg *config.Config) error {
	if cfg.SupabaseURL == "" || cfg.RefreshToken == "" {
		return fmt.Errorf("missing Supabase config — please re-login")
	}

	url := strings.TrimRight(cfg.SupabaseURL, "/") + "/auth/v1/token?grant_type=refresh_token"
	payload := fmt.Sprintf(`{"refresh_token":"%s"}`, cfg.RefreshToken)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", cfg.SupabaseAnon)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading refresh response: %w", err)
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result refreshResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parsing refresh response: %w", err)
	}

	cfg.AccessToken = result.AccessToken
	cfg.RefreshToken = result.RefreshToken
	if result.ExpiresAt > 0 {
		cfg.ExpiresAt = result.ExpiresAt
	} else if result.ExpiresIn > 0 {
		cfg.ExpiresAt = time.Now().Unix() + int64(result.ExpiresIn)
	}

	return config.Save(cfg)
}

func NeedsRefresh(cfg *config.Config) bool {
	if cfg.ExpiresAt == 0 {
		return false
	}
	return time.Now().Unix() > cfg.ExpiresAt-60
}
