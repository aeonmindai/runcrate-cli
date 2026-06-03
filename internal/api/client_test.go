package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateKey_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/billing/balance" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer rc_live_testkey" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		if r.Method != "GET" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"balance": 100}`))
	}))
	defer server.Close()

	client := NewClient("rc_live_testkey", server.URL)
	if err := client.ValidateKey(); err != nil {
		t.Errorf("ValidateKey() error: %v", err)
	}
}

func TestValidateKey_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]map[string]string{
			"error": {"message": "invalid key"},
		})
	}))
	defer server.Close()

	client := NewClient("rc_live_badkey", server.URL)
	err := client.ValidateKey()
	if err == nil {
		t.Fatal("ValidateKey() expected error for 401, got nil")
	}
}

func TestValidateKey_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]map[string]string{
			"error": {"message": "rate limited"},
		})
	}))
	defer server.Close()

	client := NewClient("rc_live_key", server.URL)
	err := client.ValidateKey()
	if err == nil {
		t.Fatal("ValidateKey() expected error for 429, got nil")
	}
}

func TestValidateKey_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]map[string]string{
			"error": {"message": "internal error"},
		})
	}))
	defer server.Close()

	client := NewClient("rc_live_key", server.URL)
	err := client.ValidateKey()
	if err == nil {
		t.Fatal("ValidateKey() expected error for 500, got nil")
	}
}

func TestValidateKey_UserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		if ua == "" {
			t.Error("User-Agent header is empty")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient("rc_live_key", server.URL)
	client.ValidateKey()
}

func TestExchangeCode_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/cli/token" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		var body struct {
			Code string `json:"code"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Code != "test_code_123" {
			t.Errorf("unexpected code: %s", body.Code)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(TokenResponse{
			APIKey:    "rc_live_newkey_from_exchange",
			KeyPrefix: "rc_live_newkey_fro",
		})
	}))
	defer server.Close()

	token, err := ExchangeCode(server.URL, "test_code_123")
	if err != nil {
		t.Fatalf("ExchangeCode() error: %v", err)
	}
	if token.APIKey != "rc_live_newkey_from_exchange" {
		t.Errorf("APIKey = %q, want %q", token.APIKey, "rc_live_newkey_from_exchange")
	}
	if token.KeyPrefix != "rc_live_newkey_fro" {
		t.Errorf("KeyPrefix = %q, want %q", token.KeyPrefix, "rc_live_newkey_fro")
	}
}

func TestExchangeCode_InvalidCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "Invalid or expired code"})
	}))
	defer server.Close()

	_, err := ExchangeCode(server.URL, "bad_code")
	if err == nil {
		t.Fatal("ExchangeCode() expected error for invalid code, got nil")
	}
}

func TestExchangeCode_EmptyAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(TokenResponse{APIKey: "", KeyPrefix: ""})
	}))
	defer server.Close()

	_, err := ExchangeCode(server.URL, "some_code")
	if err == nil {
		t.Fatal("ExchangeCode() expected error for empty API key, got nil")
	}
}

func TestExchangeCode_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := ExchangeCode(server.URL, "some_code")
	if err == nil {
		t.Fatal("ExchangeCode() expected error for 500, got nil")
	}
}
