package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runcrate/cli/internal/api"
)

func parseRun(t *testing.T, readFile func(string) ([]byte, error), argv ...string) (api.CreateJobRequest, error) {
	t.Helper()
	cmd := newRunCmd()
	if err := cmd.ParseFlags(argv); err != nil {
		t.Fatalf("ParseFlags(%q): %v", argv, err)
	}
	req, err := buildJobRequest(cmd, cmd.Flags().Args(), func(string) (string, bool) { return "", false })
	if err != nil {
		return req, err
	}
	return req, applyJobRunFlags(cmd, &req, readFile)
}

func TestRunSendsEveryJobField(t *testing.T) {
	params := func(path string) ([]byte, error) {
		if path != "sweep.json" {
			t.Fatalf("read %q", path)
		}
		return []byte(`[{"LR":"0.1"},{"LR":"0.2"}]`), nil
	}
	req, err := parseRun(t, params,
		"--gpu", "H100, A100-80GB:2", "--gpu-count", "4", "--max-runtime", "168h", "--capacity-wait", "0",
		"--retries", "2", "--retry-on", "nonzero_exit", "--retry-exit-codes", "137,143",
		"--params", "sweep.json", "--max-parallel", "1", "--tag", "team=ml", "--tag", "run.id=a=b",
		"--webhook-url", "https://example.com/hook", "--webhook-events", "started,finished",
		"--git", "https://github.com/a/b", "--git-ref", "main", "--git-token-env", "GIT_TOKEN",
		"--image", "ghcr.io/a/b:1", "--registry-username-env", "REG_USER", "--registry-password-env", "REG_PASS",
		"--allow-cpu-container", "--", "python", "train.py")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, _ := json.Marshal(req)
	var body map[string]any
	json.Unmarshal(raw, &body)
	want := map[string]any{
		"command":               "python train.py",
		"gpu_type":              []any{"H100", "A100-80GB:2"},
		"gpu_count":             float64(4),
		"max_runtime_minutes":   float64(10080),
		"image":                 "ghcr.io/a/b:1",
		"capacity_wait_minutes": float64(0),
		"max_retries":           float64(2),
		"retry_on":              []any{"nonzero_exit"},
		"retry_exit_codes":      []any{float64(137), float64(143)},
		"params":                []any{map[string]any{"LR": "0.1"}, map[string]any{"LR": "0.2"}},
		"max_parallel":          float64(1),
		"tags":                  map[string]any{"team": "ml", "run.id": "a=b"},
		"webhook_url":           "https://example.com/hook",
		"webhook_events":        []any{"started", "finished"},
		"code":                  map[string]any{"git": map[string]any{"url": "https://github.com/a/b", "ref": "main", "token_env": "GIT_TOKEN"}},
		"image_registry_auth":   map[string]any{"username_env": "REG_USER", "password_env": "REG_PASS"},
		"allow_cpu_container":   true,
	}
	if !jsonEqual(body, want) {
		t.Errorf("body = %s\nwant   %v", raw, want)
	}

	// Without the run-only flags, nothing extra is sent (a schedule spec stays the same).
	req, err = parseRun(t, nil, "--gpu", "A4000", "--", "true")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	raw, _ = json.Marshal(req)
	if string(raw) != `{"command":"true","gpu_type":"A4000","max_runtime_minutes":60,"gpu_count":1}` {
		t.Errorf("plain body = %s", raw)
	}
}

func TestRunRefusesInconsistentJobFlags(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte(`{"not":"a list"}`), nil }
	for name, argv := range map[string][]string{
		"retry-on without retries":     {"--retry-on", "nonzero_exit"},
		"unknown retry-on":             {"--retries", "1", "--retry-on", "always"},
		"exit code out of range":       {"--retries", "1", "--retry-exit-codes", "256"},
		"max-parallel without a group": {"--max-parallel", "2"},
		"params not a list":            {"--params", "p.json"},
		"bad tag":                      {"--tag", "Team=ml"},
		"tag without value":            {"--tag", "team"},
		"events without url":           {"--webhook-events", "finished"},
		"unknown event":                {"--webhook-url", "https://e.com", "--webhook-events", "done"},
		"code and git":                 {"--code", ".", "--git", "https://github.com/a/b"},
		"git ref without git":          {"--git-ref", "main"},
		"half a registry login":        {"--registry-username-env", "U"},
		"capacity wait above a day":    {"--capacity-wait", "25h"},
	} {
		argv = append([]string{"--gpu", "A4000"}, append(argv, "--", "true")...)
		if _, err := parseRun(t, read, argv...); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestRerunSendsOnlyTheChangedFlags(t *testing.T) {
	parse := func(argv ...string) map[string]any {
		t.Helper()
		cmd := newJobsRerunCmd()
		if err := cmd.ParseFlags(argv); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		o, err := rerunOverrides(cmd, func(k string) (string, bool) { return "v-" + k, true })
		if err != nil {
			t.Fatalf("rerunOverrides: %v", err)
		}
		return o
	}
	if o := parse(); len(o) != 0 {
		t.Errorf("no flags sent %v, want nothing (the job's own settings)", o)
	}
	if o := parse("--gpu-count", "2"); !jsonEqual(o, map[string]any{"gpu_count": 2}) {
		t.Errorf("count only = %v, want gpu_count alone (applies to the job's GPUs)", o)
	}
	o := parse("--gpu", "H100:2", "--max-runtime", "2h", "--env", "TOKEN", "--volume", "vol-2", "--tag", "a=b")
	want := map[string]any{
		"gpu_type": "H100", "gpu_count": 2, "max_runtime_minutes": 120,
		"env": map[string]string{"TOKEN": "v-TOKEN"}, "storage_id": "vol-2", "tags": map[string]string{"a": "b"},
	}
	if !jsonEqual(o, want) {
		t.Errorf("overrides = %v\nwant %v", o, want)
	}
}

// ── wait ────────────────────────────────────────────────────────────────────

type statusServer struct {
	mu       sync.Mutex
	statuses []string // one per GET; "503" answers a server error
	n        int
}

func (s *statusServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	status := s.statuses[min(s.n, len(s.statuses)-1)]
	s.n++
	s.mu.Unlock()
	if status == "503" {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"code":"service_unavailable","message":"busy"}}`))
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "job-1", "status": status, "exit_code": 3}})
}

func testWaiter(url string, clock *time.Time, slept *[]time.Duration) *jobWaiter {
	return &jobWaiter{
		client: func() (*api.Client, error) { return api.NewClient("rc_live_test", url), nil },
		sleep: func(_ context.Context, d time.Duration) bool {
			*slept = append(*slept, d)
			*clock = clock.Add(d)
			return true
		},
		now:   func() time.Time { return *clock },
		first: 2 * time.Second,
		max:   5 * time.Second,
	}
}

func TestJobsWaitBacksOffUntilTheJobEnds(t *testing.T) {
	srv := httptest.NewServer(&statusServer{statuses: []string{"queued", "starting", "503", "running", "running", "failed"}})
	defer srv.Close()
	clock, slept := time.Unix(0, 0), []time.Duration{}
	job, err := testWaiter(srv.URL, &clock, &slept).wait(context.Background(), "job-1", 0)
	if err != nil || job.Status != "failed" || jobExitCode(job) != 3 {
		t.Fatalf("wait = %+v, %v", job, err)
	}
	// 2s, 4s, then a retry after the 503 (1s), then 5s (capped) twice.
	want := []time.Duration{2 * time.Second, 4 * time.Second, time.Second, 5 * time.Second, 5 * time.Second}
	if !jsonEqual(slept, want) {
		t.Errorf("slept %v, want %v", slept, want)
	}
}

func TestJobsWaitTimesOutWithTheJobAsLastSeen(t *testing.T) {
	srv := httptest.NewServer(&statusServer{statuses: []string{"running"}})
	defer srv.Close()
	clock, slept := time.Unix(0, 0), []time.Duration{}
	job, err := testWaiter(srv.URL, &clock, &slept).wait(context.Background(), "job-1", 7*time.Second)
	if !errors.Is(err, errWaitTimeout) || job == nil || job.Status != "running" {
		t.Fatalf("wait = %+v, %v; want the running job and errWaitTimeout", job, err)
	}
	if !jsonEqual(slept, []time.Duration{2 * time.Second, 4 * time.Second, time.Second}) {
		t.Errorf("slept %v: the last sleep must stop at the timeout", slept)
	}
}

func TestJobsWaitStopsOnANotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"not_found","message":"Job not found"}}`))
	}))
	defer srv.Close()
	clock, slept := time.Unix(0, 0), []time.Duration{}
	_, err := testWaiter(srv.URL, &clock, &slept).wait(context.Background(), "job-1", 0)
	var se *api.APIError
	if !errors.As(err, &se) || se.Status != 404 || len(slept) != 0 {
		t.Errorf("wait = %v after %d sleeps, want the 404 at once", err, len(slept))
	}
}

// ── download ────────────────────────────────────────────────────────────────

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// artifactServer redirects each artifact to /signed/<path>, which answers files[path].
func artifactServer(files map[string]string) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := strings.CutPrefix(r.URL.Path, "/api/v1/jobs/job-1/artifacts/"); ok {
			http.Redirect(w, r, srv.URL+"/signed/"+p, http.StatusFound)
			return
		}
		if p, ok := strings.CutPrefix(r.URL.Path, "/signed/"); ok {
			io.WriteString(w, files[p])
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	return srv
}

func TestDownloadSavesOutputsAtTheirPathsAndChecksThem(t *testing.T) {
	srv := artifactServer(map[string]string{"model.pt": "weights", "runs/a/log.txt": "hello", "bad.bin": "tampered"})
	defer srv.Close()
	dir := t.TempDir()
	var stderr bytes.Buffer
	d := &artifactDownloader{client: api.NewClient("rc_live_test", srv.URL), fetch: api.DownloadSigned, stderr: &stderr}

	good := []api.Artifact{{Path: "model.pt", Bytes: 7, SHA256: sum("weights")}, {Path: "runs/a/log.txt", Bytes: 5, SHA256: sum("hello")}}
	if err := d.download(context.Background(), "job-1", good, dir, false); err != nil {
		t.Fatalf("download: %v", err)
	}
	for path, want := range map[string]string{"model.pt": "weights", "runs/a/log.txt": "hello"} {
		if got, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path))); string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	if err := d.download(context.Background(), "job-1", good[:1], dir, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("existing file error = %v, want a --force hint", err)
	}
	if err := d.download(context.Background(), "job-1", good[:1], dir, true); err != nil {
		t.Errorf("--force: %v", err)
	}

	bad := []api.Artifact{{Path: "bad.bin", Bytes: 8, SHA256: sum("original")}}
	if err := d.download(context.Background(), "job-1", bad, dir, false); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("mismatch error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.bin")); !os.IsNotExist(err) {
		t.Error("a file that failed its check was kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.bin.part")); !os.IsNotExist(err) {
		t.Error("the partial file was kept")
	}

	escape := []api.Artifact{{Path: "../outside.txt", Bytes: 1, SHA256: sum("x")}}
	if err := d.download(context.Background(), "job-1", escape, dir, false); err == nil || !strings.Contains(err.Error(), "leaves the download directory") {
		t.Errorf("escaping path error = %v, want it refused before any download", err)
	}
}

func TestSelectArtifacts(t *testing.T) {
	all := []api.Artifact{{Path: "model.pt"}, {Path: "runs/a.txt"}, {Path: "runs/b.txt"}, {Path: "runs2/c.txt"}}
	got, err := selectArtifacts(all, []string{"runs", "model.pt", "runs/a.txt"})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	var paths []string
	for _, a := range got {
		paths = append(paths, a.Path)
	}
	if strings.Join(paths, " ") != "runs/a.txt runs/b.txt model.pt" {
		t.Errorf("picked %v: a directory takes what is under it (not runs2/), once each", paths)
	}
	if _, err := selectArtifacts(all, []string{"nope"}); err == nil {
		t.Error("an unknown path was not refused")
	}
}

func TestCodeArchiveLeavesOutGitAndCaches(t *testing.T) {
	dir := t.TempDir()
	for path, body := range map[string]string{"train.py": "print(1)", "pkg/util.py": "x = 1", ".git/HEAD": "ref", "pkg/__pycache__/u.pyc": "c"} {
		full := filepath.Join(dir, filepath.FromSlash(path))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	archive, err := codeArchive(dir)
	if err != nil {
		t.Fatalf("codeArchive: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("not gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	if strings.Join(names, " ") != "pkg/util.py train.py" {
		t.Errorf("archive holds %v", names)
	}
	if _, err := codeArchive(filepath.Join(dir, "train.py")); err == nil {
		t.Error("a file that is not a .tar.gz was accepted")
	}
}
