package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/runcrate/cli/internal/api"
)

func mockServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/inst-1":
			json.NewEncoder(w).Encode(map[string]any{
				"data": api.Instance{ID: "inst-1", Name: "my-gpu", Status: "running", IP: "10.0.0.1"},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/inst-stopped":
			json.NewEncoder(w).Encode(map[string]any{
				"data": api.Instance{ID: "inst-stopped", Name: "stopped-gpu", Status: "terminated", IP: "10.0.0.2"},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/inst-noip":
			json.NewEncoder(w).Encode(map[string]any{
				"data": api.Instance{ID: "inst-noip", Name: "pending-gpu", Status: "deploying", IP: ""},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []api.Instance{
					{ID: "inst-1", Name: "my-gpu", Status: "running", IP: "10.0.0.1"},
					{ID: "inst-2", Name: "my-gpu", Status: "running", IP: "10.0.0.2"},
					{ID: "inst-3", Name: "unique-gpu", Status: "running", IP: "10.0.0.3"},
					{ID: "inst-stopped", Name: "stopped-gpu", Status: "terminated", IP: "10.0.0.4"},
				},
			})
		case r.Method == "GET" && r.URL.Path == "/api/v1/instances/not-found":
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "not found"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestResolveInstance_ByID(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	id, ip, err := resolveInstance(client, "inst-1")
	if err != nil {
		t.Fatalf("resolveInstance() error: %v", err)
	}
	if id != "inst-1" {
		t.Errorf("id = %q, want %q", id, "inst-1")
	}
	if ip != "10.0.0.1" {
		t.Errorf("ip = %q, want %q", ip, "10.0.0.1")
	}
}

func TestResolveInstance_ByName(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	id, ip, err := resolveInstance(client, "unique-gpu")
	if err != nil {
		t.Fatalf("resolveInstance() error: %v", err)
	}
	if id != "inst-3" {
		t.Errorf("id = %q, want %q", id, "inst-3")
	}
	if ip != "10.0.0.3" {
		t.Errorf("ip = %q, want %q", ip, "10.0.0.3")
	}
}

func TestResolveInstance_NotRunning(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	_, _, err := resolveInstance(client, "inst-stopped")
	if err == nil {
		t.Fatal("expected error for terminated instance, got nil")
	}
}

func TestResolveInstance_NoIP(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	_, _, err := resolveInstance(client, "inst-noip")
	if err == nil {
		t.Fatal("expected error for instance with no IP, got nil")
	}
}

func TestResolveInstance_NotFound(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	_, _, err := resolveInstance(client, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent instance, got nil")
	}
}

func TestResolveInstanceID_ByID(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	id, err := resolveInstanceID(client, "inst-1")
	if err != nil {
		t.Fatalf("resolveInstanceID() error: %v", err)
	}
	if id != "inst-1" {
		t.Errorf("id = %q, want %q", id, "inst-1")
	}
}

func TestResolveInstanceID_ByName(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	id, err := resolveInstanceID(client, "unique-gpu")
	if err != nil {
		t.Fatalf("resolveInstanceID() error: %v", err)
	}
	if id != "inst-3" {
		t.Errorf("id = %q, want %q", id, "inst-3")
	}
}

func TestResolveInstanceID_DuplicateName(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	_, err := resolveInstanceID(client, "my-gpu")
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestResolveInstanceID_NotFound(t *testing.T) {
	server := mockServer(t)
	defer server.Close()
	client := api.NewClient("rc_live_test", server.URL)

	_, err := resolveInstanceID(client, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent instance, got nil")
	}
}

func TestGenerateEphemeralKeyPair(t *testing.T) {
	pubKey, privKey, err := generateEphemeralKeyPair()
	if err != nil {
		t.Fatalf("generateEphemeralKeyPair() error: %v", err)
	}
	if pubKey == "" {
		t.Error("public key is empty")
	}
	if privKey == "" {
		t.Error("private key is empty")
	}
	if len(pubKey) < 20 {
		t.Errorf("public key too short: %q", pubKey)
	}
	if !contains(pubKey, "ssh-ed25519") {
		t.Errorf("public key doesn't start with ssh-ed25519: %q", pubKey)
	}
	if !contains(privKey, "OPENSSH PRIVATE KEY") {
		t.Errorf("private key doesn't contain PEM header: %q", privKey[:50])
	}

	// Generate a second pair — must be different
	pubKey2, _, err := generateEphemeralKeyPair()
	if err != nil {
		t.Fatalf("second generateEphemeralKeyPair() error: %v", err)
	}
	if pubKey == pubKey2 {
		t.Error("two generated key pairs are identical — not random")
	}
}

func TestWriteTempSSHFiles(t *testing.T) {
	tempDir, keyPath, certPath, err := writeTempSSHFiles(
		"-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n",
		"ssh-ed25519-cert-v01@openssh.com AAAA...",
	)
	if err != nil {
		t.Fatalf("writeTempSSHFiles() error: %v", err)
	}
	defer os.RemoveAll(tempDir)

	if keyPath == "" || certPath == "" {
		t.Fatal("paths are empty")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
