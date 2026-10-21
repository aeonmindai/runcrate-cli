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
