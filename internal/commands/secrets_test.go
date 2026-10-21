package commands

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runcrate/cli/internal/api"
)

// The CLI reads the secrets of the environment it is switched to (X-Environment)
// and prints names and flags only, whatever else the answer holds.
func TestSecretsListReadsTheActiveEnvironment(t *testing.T) {
	var gotPath, gotEnv, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotEnv = r.Method, r.URL.Path, r.Header.Get("X-Environment")
		w.Write([]byte(`{"data":[
			{"key":"DB_URL","sensitive":true,"updated_at":"2026-10-20T11:00:00Z","value":"postgres://leak"},
			{"key":"HF_TOKEN","sensitive":false,"updated_at":"2026-10-21T09:30:00Z"}
		]}`))
	}))
	defer server.Close()

	list, err := api.NewOAuthClient(server.URL, "tok", "ws-1", "staging").ListSecrets()
	if err != nil {
		t.Fatalf("ListSecrets() error: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/api/v1/secrets" || gotEnv != "staging" {
		t.Fatalf("request = %s %s (X-Environment %q), want GET /api/v1/secrets in staging", gotMethod, gotPath, gotEnv)
	}

	var out bytes.Buffer
	if err := renderSecrets(&out, list, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "DB_URL", "yes", "2026-10-20", "HF_TOKEN", "no", "2026-10-21"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "postgres") {
		t.Errorf("table shows a value:\n%s", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "DB_URL ") || !strings.Contains(lines[1], " yes ") {
		t.Errorf("want one row per secret, DB_URL marked sensitive:\n%s", got)
	}
}

func TestRenderSecretsQuietAndEmpty(t *testing.T) {
	var quiet bytes.Buffer
	renderSecrets(&quiet, []api.Secret{{Key: "A"}, {Key: "B", Sensitive: true}}, true)
	if quiet.String() != "A\nB\n" {
		t.Errorf("quiet = %q, want one name per line", quiet.String())
	}

	var empty bytes.Buffer
	renderSecrets(&empty, nil, false)
	if !strings.Contains(empty.String(), secretsDashboardURL) {
		t.Errorf("empty list should say where to add secrets: %q", empty.String())
	}

	var none bytes.Buffer
	renderSecrets(&none, nil, true)
	if none.Len() != 0 {
		t.Errorf("quiet empty list printed %q, want nothing (so scripts see no names)", none.String())
	}
}

func TestSecretsListRefusalIsAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"code":"UNAUTHORIZED","message":"Invalid API key"}}`))
	}))
	defer server.Close()

	if _, err := api.NewClient("rc_live_bad", server.URL).ListSecrets(); err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("err = %v, want the API's message", err)
	}
}
