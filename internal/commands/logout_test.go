package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/runcrate/cli/internal/config"
)

const logoutAgentToken = "rca_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

// logoutWith runs `runcrate logout` with an agent token stored, and returns how
// many sign-outs reached the server.
func logoutWith(t *testing.T, envToken string) int {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RUNCRATE_ACCESS_TOKEN", envToken)
	revoked := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/oauth/revoke" {
			revoked++
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
	if got := logoutWith(t, ""); got != 1 {
		t.Errorf("sign-outs sent = %d, want 1", got)
	}
}

func TestLogoutLeavesATokenFromTheEnvironmentAlone(t *testing.T) {
	if got := logoutWith(t, "rca_CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"); got != 0 {
		t.Errorf("sign-outs sent = %d, want 0", got)
	}
}
