package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/runcrate/cli/internal/config"
)

// AgentTokenPrefix starts every Runcrate agent token: an opaque sign-in that
// acts in one environment and is never refreshed (it expires, or is signed out).
const AgentTokenPrefix = "rca_"

// IsAgentToken reports whether an access token is a Runcrate agent token, as
// opposed to a Supabase session from an older login.
func IsAgentToken(token string) bool {
	return strings.HasPrefix(token, AgentTokenPrefix)
}

func LoginWithOAuth(tokenResp *api.OAuthTokenResponse, ws *api.OAuthWorkspace, apiURL string) error {
	cfg := &config.Config{
		APIURL:       apiURL,
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    tokenResp.ExpiresAt,
		ProjectID:    ws.ID,
		ProjectName:  ws.Name,
		UserEmail:    tokenResp.UserEmail,
		Role:         ws.Role,
		SupabaseURL:  tokenResp.SupabaseURL,
		SupabaseAnon: tokenResp.SupabaseAnon,
	}
	// An agent token works in the one environment picked in the browser.
	if tokenResp.Environment != nil {
		cfg.Environment = tokenResp.Environment.Name
	}
	return config.Save(cfg)
}

// RevokeAgentToken signs an agent token out on Runcrate: the next request with
// it is refused. Holding the token is the authority (RFC 7009).
func RevokeAgentToken(apiURL, token string) error {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", strings.TrimRight(apiURL, "/")+"/api/auth/oauth/revoke", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "runcrate-cli/"+api.Version)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// LoginWithKey validates an API key and saves it to config (legacy).
func LoginWithKey(apiKey, apiURL string) error {
	client := api.NewClient(apiKey, apiURL)

	fmt.Print("Validating credentials... ")
	if err := client.ValidateKey(); err != nil {
		fmt.Println("failed")
		return err
	}
	fmt.Println("ok")

	cfg := &config.Config{
		APIKey: apiKey,
		APIURL: apiURL,
	}
	return config.Save(cfg)
}

func Logout() error {
	return config.Clear()
}

func MaskKey(key string) string {
	if len(key) <= 12 {
		return "****"
	}
	return key[:12] + "..." + strings.Repeat("*", 4)
}
