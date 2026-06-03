package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/runcrate/cli/internal/config"
)

// TestFullLoginLogoutFlow simulates the complete OAuth flow:
// 1. Start callback server
// 2. Simulate browser redirect with code
// 3. Exchange code for API key (mock server)
// 4. Save credentials via LoginWithKey
// 5. Verify config persisted
// 6. Logout
// 7. Verify config cleared
func TestFullLoginLogoutFlow(t *testing.T) {
	// Setup isolated home dir
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	// Mock API server that handles both validation and token exchange
	mockAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/cli/token":
			// Token exchange
			var body struct {
				Code string `json:"code"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.Code != "integration_test_code" {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "bad code"})
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(api.TokenResponse{
				APIKey:    "rc_live_integration_test_key_abc123",
				KeyPrefix: "rc_live_integration",
			})

		case "/api/v1/billing/balance":
			// Key validation
			if r.Header.Get("Authorization") != "Bearer rc_live_integration_test_key_abc123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"balance": 100}`))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockAPI.Close()

	// Step 1: Start callback server
	port, resultCh, shutdown := StartCallbackServer(5 * time.Second)
	defer shutdown()

	if port == 0 {
		t.Fatal("callback server failed to start")
	}

	// Step 2: Simulate browser redirect with auth code
	go func() {
		url := fmt.Sprintf("http://127.0.0.1:%d/callback?code=integration_test_code", port)
		resp, err := http.Get(url)
		if err != nil {
			t.Errorf("browser callback error: %v", err)
			return
		}
		resp.Body.Close()
	}()

	// Step 3: Receive the code
	result := <-resultCh
	if result.Error != nil {
		t.Fatalf("callback error: %v", result.Error)
	}
	if result.Code != "integration_test_code" {
		t.Fatalf("received code = %q, want %q", result.Code, "integration_test_code")
	}

	// Step 4: Exchange code for API key
	token, err := api.ExchangeCode(mockAPI.URL, result.Code)
	if err != nil {
		t.Fatalf("ExchangeCode() error: %v", err)
	}
	if token.APIKey != "rc_live_integration_test_key_abc123" {
		t.Fatalf("API key = %q, want %q", token.APIKey, "rc_live_integration_test_key_abc123")
	}

	// Step 5: Login (validate + save)
	err = LoginWithKey(token.APIKey, mockAPI.URL)
	if err != nil {
		t.Fatalf("LoginWithKey() error: %v", err)
	}

	// Step 6: Verify config persisted
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	if cfg.APIKey != "rc_live_integration_test_key_abc123" {
		t.Errorf("persisted APIKey = %q, want %q", cfg.APIKey, "rc_live_integration_test_key_abc123")
	}
	if cfg.APIURL != mockAPI.URL {
		t.Errorf("persisted APIURL = %q, want %q", cfg.APIURL, mockAPI.URL)
	}

	// Step 7: Logout
	err = Logout()
	if err != nil {
		t.Fatalf("Logout() error: %v", err)
	}

	// Step 8: Verify config cleared
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("config.Load() after logout error: %v", err)
	}
	if cfg.APIKey != "" {
		t.Errorf("APIKey after logout = %q, want empty", cfg.APIKey)
	}
}
