package auth

import (
	"fmt"
	"strings"

	"github.com/runcrate/cli/internal/api"
	"github.com/runcrate/cli/internal/config"
)

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
	return config.Save(cfg)
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
