package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/runcrate/cli/internal/config"
)

const logoutAgentToken = "rca_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

// logoutWith runs `runcrate logout` with an agent token stored, and returns the
// tokens whose sign-out reached the server.
func logoutWith(t *testing.T, envToken string) []string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RUNCRATE_ACCESS_TOKEN", envToken)
	var revoked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/oauth/revoke" {
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			revoked = append(revoked, body["token"])
		}
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	if err := config.Save(&config.Config{APIURL: server.URL, AccessToken: logoutAgentToken, ProjectID: "p1"}); err != nil {
		t.Fatal(err)
	}
	if err := logoutCmd.RunE(logoutCmd, nil); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := os.Stat(config.ConfigPath()); !os.IsNotExist(err) {
		t.Error("the stored credentials were not cleared")
	}
	return revoked
}

func TestLogoutSignsTheStoredAgentTokenOut(t *testing.T) {
	if got := logoutWith(t, ""); len(got) != 1 || got[0] != logoutAgentToken {
		t.Errorf("signed out %q, want the stored token", got)
	}
}

func TestLogoutSignsTheStoredTokenOutEvenWithATokenInTheEnvironment(t *testing.T) {
	envToken := "rca_CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	got := logoutWith(t, envToken)
	if len(got) != 1 || got[0] != logoutAgentToken {
		t.Errorf("signed out %q, want only the stored token", got)
	}
}
