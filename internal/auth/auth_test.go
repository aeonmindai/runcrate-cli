package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/runcrate/cli/internal/config"
)

func setupTestHome(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	return func() { os.Setenv("HOME", origHome) }
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"rc_live_abcdef123456789", "rc_live_abcd...****"},
		{"rc_live_ab", "****"},
		{"short", "****"},
		{"", "****"},
	}

	for _, tt := range tests {
		got := MaskKey(tt.input)
		if got != tt.want {
			t.Errorf("MaskKey(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLoginWithKey_Success(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"balance": 50}`))
	}))
	defer server.Close()

	err := LoginWithKey("rc_live_testkey", server.URL)
	if err != nil {
		t.Fatalf("LoginWithKey() error: %v", err)
	}

	// Verify config was saved
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error: %v", err)
	}
	if cfg.APIKey != "rc_live_testkey" {
		t.Errorf("saved APIKey = %q, want %q", cfg.APIKey, "rc_live_testkey")
	}
	if cfg.APIURL != server.URL {
		t.Errorf("saved APIURL = %q, want %q", cfg.APIURL, server.URL)
	}
}

func TestLoginWithKey_InvalidKey(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]map[string]string{
			"error": {"message": "bad key"},
		})
	}))
	defer server.Close()

	err := LoginWithKey("rc_live_badkey", server.URL)
	if err == nil {
		t.Fatal("LoginWithKey() expected error for invalid key, got nil")
	}

	// Config should NOT be saved
	cfg, _ := config.Load()
	if cfg.APIKey != "" {
		t.Errorf("APIKey should be empty after failed login, got %q", cfg.APIKey)
	}
}

func TestLogout(t *testing.T) {
	cleanup := setupTestHome(t)
	defer cleanup()

	// Save config first
	cfg := &config.Config{APIKey: "rc_live_logout_test", APIURL: "https://runcrate.ai"}
	config.Save(cfg)

	if err := Logout(); err != nil {
		t.Fatalf("Logout() error: %v", err)
	}

	// Config should be cleared
	loaded, _ := config.Load()
	if loaded.APIKey != "" {
		t.Errorf("APIKey after logout = %q, want empty", loaded.APIKey)
	}
}

func TestCallbackServer_ReceivesCode(t *testing.T) {
	port, resultCh, shutdown := StartCallbackServer(5 * time.Second)
	defer shutdown()

	if port == 0 {
		t.Fatal("StartCallbackServer returned port 0")
	}

	// Simulate browser callback
	go func() {
		resp, err := http.Get("http://127.0.0.1:" + fmt.Sprintf("%d", port) + "/callback?code=test_auth_code")
		if err != nil {
			t.Errorf("callback request error: %v", err)
			return
		}
		resp.Body.Close()
	}()

	result := <-resultCh
	if result.Error != nil {
		t.Fatalf("callback error: %v", result.Error)
	}
	if result.Code != "test_auth_code" {
		t.Errorf("code = %q, want %q", result.Code, "test_auth_code")
	}
}

func TestCallbackServer_MissingCode(t *testing.T) {
	port, resultCh, shutdown := StartCallbackServer(5 * time.Second)
	defer shutdown()

	go func() {
		resp, err := http.Get("http://127.0.0.1:" + fmt.Sprintf("%d", port) + "/callback")
		if err != nil {
			t.Errorf("callback request error: %v", err)
			return
		}
		resp.Body.Close()
	}()

	result := <-resultCh
	if result.Error == nil {
		t.Fatal("expected error for missing code, got nil")
	}
}

func TestCallbackServer_ErrorParam(t *testing.T) {
	port, resultCh, shutdown := StartCallbackServer(5 * time.Second)
	defer shutdown()

	go func() {
		resp, err := http.Get("http://127.0.0.1:" + fmt.Sprintf("%d", port) + "/callback?error=access_denied")
		if err != nil {
			t.Errorf("callback request error: %v", err)
			return
		}
		resp.Body.Close()
	}()

	result := <-resultCh
	if result.Error == nil {
		t.Fatal("expected error for error param, got nil")
	}
}

func TestCallbackServer_Timeout(t *testing.T) {
	_, resultCh, shutdown := StartCallbackServer(1 * time.Second)
	defer shutdown()

	result := <-resultCh
	if result.Error == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

// TestCallbackServer_PreflightOptions verifies the server answers the
// CORS / Private Network Access preflight that Chrome sends before the real
// fetch from the HTTPS /auth/authorized page to http://127.0.0.1:<port>.
// Without these headers Chrome blocks the request and the CLI hangs at
// "Waiting for browser authentication..." even though the user authorized.
//
// The preflight MUST NOT consume the result channel — the actual GET with
// ?code=... arrives right after.
func TestCallbackServer_PreflightOptions(t *testing.T) {
	port, resultCh, shutdown := StartCallbackServer(2 * time.Second)
	defer shutdown()

	req, err := http.NewRequest(http.MethodOptions,
		fmt.Sprintf("http://127.0.0.1:%d/callback", port), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "https://runcrate-kiy21rpwb-aeonmind.vercel.app")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if got := resp.Header.Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("Access-Control-Allow-Private-Network = %q, want %q", got, "true")
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "" {
		t.Errorf("Access-Control-Allow-Origin missing")
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got == "" {
		t.Errorf("Access-Control-Allow-Methods missing")
	}

	// The preflight must NOT have delivered a result.
	select {
	case r := <-resultCh:
		t.Fatalf("preflight unexpectedly produced a result: code=%q err=%v", r.Code, r.Error)
	default:
	}

	// And the real GET right after must still succeed.
	resp2, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=pna_ok", port))
	if err != nil {
		t.Fatalf("real callback: %v", err)
	}
	resp2.Body.Close()

	result := <-resultCh
	if result.Error != nil {
		t.Fatalf("real callback error after preflight: %v", result.Error)
	}
	if result.Code != "pna_ok" {
		t.Errorf("code = %q, want %q", result.Code, "pna_ok")
	}
}

// TestCallbackServer_CorsHeadersOnGet verifies the same PNA + CORS headers
// are emitted on the actual GET response too. Chrome enforces PNA on the
// iframe-loaded delivery path as well as the fetch path.
func TestCallbackServer_CorsHeadersOnGet(t *testing.T) {
	port, resultCh, shutdown := StartCallbackServer(2 * time.Second)
	defer shutdown()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=hdr_check", port))
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("GET Access-Control-Allow-Private-Network = %q, want %q", got, "true")
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got == "" {
		t.Errorf("GET Access-Control-Allow-Origin missing")
	}

	<-resultCh // drain
}

