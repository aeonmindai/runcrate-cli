package api

import (
	"encoding/json"
	"testing"
)

// These guard the v1 response field mappings that previously drifted from the
// server and broke ssh-keys (empty CreatedAt -> panic) and billing balance (0).

func TestSSHKeyDecodesServerShape(t *testing.T) {
	// Mirrors src/lib/services/ssh-keys.ts transformKey() output (camelCase,
	// fingerprint not public_key).
	body := `{"data":{"id":"k1","name":"laptop","fingerprint":"AAAAC3Nza","type":"ed25519","createdAt":"2026-06-01T12:00:00Z","lastUsed":null,"projectId":"p1"}}`
	var env apiEnvelope[SSHKey]
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.CreatedAt != "2026-06-01T12:00:00Z" {
		t.Errorf("CreatedAt = %q; empty value previously caused a [:10] slice panic", env.Data.CreatedAt)
	}
	if env.Data.Fingerprint != "AAAAC3Nza" {
		t.Errorf("Fingerprint = %q", env.Data.Fingerprint)
	}
	if env.Data.Type != "ed25519" {
		t.Errorf("Type = %q", env.Data.Type)
	}
}

func TestBalanceDecodesCreditsBalance(t *testing.T) {
	// Mirrors getProjectBalance(): { creditsBalance, activeUsageCost }.
	body := `{"data":{"creditsBalance":42.5,"activeUsageCost":1.25}}`
	var env apiEnvelope[Balance]
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.Credits != 42.5 {
		t.Errorf("Credits = %v; reading the wrong field previously reported 0", env.Data.Credits)
	}
	if env.Data.ActiveUsageCost != 1.25 {
		t.Errorf("ActiveUsageCost = %v", env.Data.ActiveUsageCost)
	}
}

func TestUsageDecodesServerShape(t *testing.T) {
	// Mirrors get_usage_summary RPC (snake_case).
	body := `{"data":{"total_requests":10,"total_prompt_tokens":100,"total_completion_tokens":50,"total_tokens":150,"total_cost":0.0123}}`
	var env apiEnvelope[UsageSummary]
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.TotalRequests != 10 || env.Data.TotalTokens != 150 || env.Data.TotalCost != 0.0123 {
		t.Errorf("usage mismatch: %+v", env.Data)
	}
}
