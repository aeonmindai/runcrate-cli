package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestMCPEnv is an install environment in a temp home with nothing on PATH,
// so every client goes through its config file.
func newTestMCPEnv(t *testing.T) (*mcpEnv, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	now := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	return &mcpEnv{
		home:     t.TempDir(),
		goos:     "linux",
		getenv:   func(string) string { return "" },
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
		run: func(bool, string, ...string) (string, error) {
			t.Fatal("no command should run when nothing is on PATH")
			return "", nil
		},
		now: func() time.Time { now = now.Add(time.Second); return now },
		out: out,
	}, out
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // past the umask
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func backups(t *testing.T, path string) []string {
	t.Helper()
	m, err := filepath.Glob(path + ".bak-*")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func cursorServers(t *testing.T, path string) map[string]any {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(readTestFile(t, path)), &root); err != nil {
		t.Fatalf("written file is not JSON: %v", err)
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing: %v", root)
	}
	return servers
}

const cursorOriginal = `{
  "mcpServers": {
    "github": {"command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"]}
  },
  "big": 12345678901234567890
}
`

func TestMCPInstallMergesKeepsOtherServersAndBacksUp(t *testing.T) {
	e, _ := newTestMCPEnv(t)
	path := filepath.Join(e.home, ".cursor", "mcp.json")
	writeTestFile(t, path, cursorOriginal, 0o644)

	if err := runMCPInstall(e, []string{"cursor"}); err != nil {
		t.Fatal(err)
	}

	servers := cursorServers(t, path)
	if _, ok := servers["github"]; !ok {
		t.Errorf("other server was dropped: %v", servers)
	}
	rc, _ := servers["runcrate"].(map[string]any)
	if rc["url"] != mcpServerURL || len(rc) != 1 {
		t.Errorf("runcrate entry = %v, want {url: %s}", servers["runcrate"], mcpServerURL)
	}
	got := readTestFile(t, path)
	if !strings.Contains(got, "12345678901234567890") {
		t.Errorf("large number was not kept exactly:\n%s", got)
	}
	if !strings.HasSuffix(got, "}\n") || !strings.Contains(got, "\n  \"mcpServers\": {") {
		t.Errorf("want 2-space indent and a trailing newline:\n%s", got)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644 kept", fi.Mode().Perm())
	}

	b := backups(t, path)
	if len(b) != 1 {
		t.Fatalf("backups = %v, want one", b)
	}
	if readTestFile(t, b[0]) != cursorOriginal {
		t.Errorf("backup does not hold the original")
	}
}

func TestMCPInstallNewFileIsPrivate(t *testing.T) {
	e, _ := newTestMCPEnv(t)
	path := filepath.Join(e.home, ".codeium", "windsurf", "mcp_config.json")

	if err := runMCPInstall(e, []string{"windsurf"}); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"mcpServers\": {\n    \"runcrate\": {\n      \"serverUrl\": \"" + mcpServerURL + "\"\n    }\n  }\n}\n"
	if got := readTestFile(t, path); got != want {
		t.Errorf("file =\n%s\nwant\n%s", got, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if len(backups(t, path)) != 0 {
		t.Error("a new file should not get a backup")
	}
}

func TestMCPInstallSecondRunWritesNothing(t *testing.T) {
	e, _ := newTestMCPEnv(t)
	path := filepath.Join(e.home, ".cursor", "mcp.json")
	writeTestFile(t, path, cursorOriginal, 0o644)

	if err := runMCPInstall(e, []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	after := readTestFile(t, path)
	fi1, _ := os.Stat(path)

	if err := runMCPInstall(e, []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, path) != after {
		t.Error("second run changed the file")
	}
	if fi2, _ := os.Stat(path); !os.SameFile(fi1, fi2) {
		t.Error("second run replaced the file")
	}
	if b := backups(t, path); len(b) != 1 {
		t.Errorf("backups = %v, want only the first run's", b)
	}
}

func TestMCPInstallDifferingEntryNeedsForce(t *testing.T) {
	e, out := newTestMCPEnv(t)
	path := filepath.Join(e.home, ".cursor", "mcp.json")
	original := `{"mcpServers": {"runcrate": {"url": "https://example.com/mcp"}, "other": {"url": "https://other.example/mcp"}}}`
	writeTestFile(t, path, original, 0o600)

	if err := runMCPInstall(e, []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, path) != original {
		t.Fatal("differing entry was changed without --force")
	}
	if !strings.Contains(out.String(), "--force") {
		t.Errorf("output does not mention --force:\n%s", out)
	}

	e.force = true
	if err := runMCPInstall(e, []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	servers := cursorServers(t, path)
	if rc, _ := servers["runcrate"].(map[string]any); rc["url"] != mcpServerURL {
		t.Errorf("--force did not replace the entry: %v", servers["runcrate"])
	}
	if _, ok := servers["other"]; !ok {
		t.Error("--force dropped another server")
	}
	if b := backups(t, path); len(b) != 1 || readTestFile(t, b[0]) != original {
		t.Errorf("want one backup of the original, got %v", b)
	}
}

func TestMCPInstallLeavesInvalidJSONByteIdentical(t *testing.T) {
	e, out := newTestMCPEnv(t)
	path := filepath.Join(e.home, ".cursor", "mcp.json")
	jsonc := "{\n  // my servers\n  \"mcpServers\": {},\n}\n"
	writeTestFile(t, path, jsonc, 0o644)

	if err := runMCPInstall(e, []string{"cursor"}); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, path) != jsonc {
		t.Error("invalid JSON file was modified")
	}
	if len(backups(t, path)) != 0 {
		t.Error("invalid JSON file got a backup, so something was written")
	}
	if s := out.String(); !strings.Contains(s, mcpManual) || !strings.Contains(s, `"url": "`+mcpServerURL+`"`) {
		t.Errorf("want a manual line with the snippet:\n%s", s)
	}
}

func TestMCPInstallCodexAppendsOnce(t *testing.T) {
	e, out := newTestMCPEnv(t)
	path := filepath.Join(e.home, ".codex", "config.toml")
	original := "model = \"gpt-5\"\n\n[mcp_servers.other]\ncommand = \"npx\""
	writeTestFile(t, path, original, 0o600)

	for range 2 {
		if err := runMCPInstall(e, []string{"codex"}); err != nil {
			t.Fatal(err)
		}
	}
	want := original + "\n\n[mcp_servers.runcrate]\nurl = \"" + mcpServerURL + "\"\n"
	if got := readTestFile(t, path); got != want {
		t.Errorf("config.toml =\n%s\nwant\n%s", got, want)
	}
	if b := backups(t, path); len(b) != 1 {
		t.Errorf("backups = %v, want one", b)
	}
	if !strings.Contains(out.String(), "codex mcp login runcrate") {
		t.Errorf("output does not tell the user to log in:\n%s", out)
	}
}

func TestMCPInstallCodexKeepsDifferentTableEvenWithForce(t *testing.T) {
	e, _ := newTestMCPEnv(t)
	e.force = true
	path := filepath.Join(e.home, ".codex", "config.toml")
	original := "[mcp_servers.runcrate]\nurl = \"https://example.com/mcp\"\n"
	writeTestFile(t, path, original, 0o600)

	if err := runMCPInstall(e, []string{"codex"}); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, path) != original {
		t.Error("TOML was rewritten")
	}
}

func TestMCPInstallAllOnlyTouchesDetectedClients(t *testing.T) {
	e, out := newTestMCPEnv(t)
	if err := runMCPInstall(e, []string{"all"}); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, mcpServerURL) || !strings.Contains(s, mcpDocsURL) {
		t.Errorf("with nothing detected, want the URL and docs link:\n%s", s)
	}

	if err := os.MkdirAll(filepath.Join(e.home, ".codeium", "windsurf"), 0o700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runMCPInstall(e, []string{"all"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".codeium", "windsurf", "mcp_config.json")); err != nil {
		t.Errorf("detected client was not set up: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.home, ".cursor")); !os.IsNotExist(err) {
		t.Error("an undetected client got a config")
	}
	if !strings.Contains(out.String(), "Next: open Windsurf;") {
		t.Errorf("want the next step for Windsurf:\n%s", out)
	}
}

func TestMCPInstallDryRunWritesNothing(t *testing.T) {
	e, out := newTestMCPEnv(t)
	e.dryRun = true
	if err := runMCPInstall(e, []string{"cursor", "codex", "vscode"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(e.home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dry run created %v", entries)
	}
	if strings.Count(out.String(), mcpWould) != 3 {
		t.Errorf("want three 'would connect' lines:\n%s", out)
	}
}

func TestMCPInstallWindsurfWritesEveryDetectedDir(t *testing.T) {
	e, _ := newTestMCPEnv(t)
	windsurf := filepath.Join(e.home, ".codeium", "windsurf")
	devin := filepath.Join(e.home, ".config", "devin")
	if err := os.MkdirAll(devin, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := runMCPInstall(e, []string{"windsurf"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(devin, "mcp_config.json")); err != nil {
		t.Errorf("Devin Desktop config not written: %v", err)
	}
	if dirExists(windsurf) {
		t.Error("Windsurf config written although only Devin Desktop is installed")
	}

	if err := os.MkdirAll(windsurf, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runMCPInstall(e, []string{"all"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(windsurf, "mcp_config.json")); err != nil {
		t.Errorf("Windsurf config not written: %v", err)
	}
}
