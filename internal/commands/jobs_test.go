package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runcrate/cli/internal/api"
)

func TestParseMaxRuntime(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "90m", want: 90},
		{in: "2h", want: 120},
		{in: "1h30m", want: 90},
		{in: "45", want: 45},
		{in: "90s", want: 2},
		{in: "1s", want: 1},
		{in: "24h", want: 1440},
		{in: "1440", want: 1440},
		{in: "0", wantErr: true},
		{in: "0s", wantErr: true},
		{in: "-5m", wantErr: true},
		{in: "25h", wantErr: true},
		{in: "1441", wantErr: true},
		{in: "two hours", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseMaxRuntime(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseMaxRuntime(%q) = %d, want error", tt.in, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseMaxRuntime(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestJoinCommand(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"pip install -r req.txt && python train.py"}, "pip install -r req.txt && python train.py"},
		{[]string{"python", "-c", "print(1)"}, "python -c 'print(1)'"},
		{[]string{"ls", "-la", "/workspace/data_v2"}, "ls -la /workspace/data_v2"},
		{[]string{"echo", "it's here"}, `echo 'it'"'"'s here'`},
		{[]string{"echo", "$HOME", ""}, `echo '$HOME' ''`},
	}
	for _, tt := range tests {
		if got := joinCommand(tt.args); got != tt.want {
			t.Errorf("joinCommand(%q) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestBuildJobRequestBody(t *testing.T) {
	lookup := func(k string) (string, bool) {
		if k == "HF_TOKEN" {
			return "from-shell", true
		}
		return "", false
	}
	parse := func(argv ...string) (api.CreateJobRequest, error) {
		cmd := newRunCmd()
		if err := cmd.ParseFlags(argv); err != nil {
			t.Fatalf("ParseFlags(%q): %v", argv, err)
		}
		return buildJobRequest(cmd, cmd.Flags().Args(), lookup)
	}

	req, err := parse("--gpu", "A4000", "--max-runtime", "90s", "--volume", "vol-1",
		"--env", "A=1,2", "--env", "HF_TOKEN", "--", "python", "-c", "print(1)")
	if err != nil {
		t.Fatalf("buildJobRequest: %v", err)
	}
	raw, _ := json.Marshal(req)
	var body map[string]any
	json.Unmarshal(raw, &body)
	want := map[string]any{
		"command":             "python -c 'print(1)'",
		"gpu_type":            "A4000",
		"gpu_count":           float64(1),
		"max_runtime_minutes": float64(2),
		"storage_id":          "vol-1",
		"env":                 map[string]any{"A": "1,2", "HF_TOKEN": "from-shell"},
	}
	if !jsonEqual(body, want) {
		t.Errorf("body = %s\nwant   %v", raw, want)
	}

	// Defaults to 60 minutes; flags stop at the command, so its own flags pass through without --.
	req, err = parse("--gpu", "H100:2", "nvidia-smi", "-L")
	if err != nil {
		t.Fatalf("buildJobRequest without --: %v", err)
	}
	if req.MaxRuntimeMinutes != 60 || req.Command != "nvidia-smi -L" || req.GPUType != "H100" || req.GPUCount != 2 {
		t.Errorf("req = %+v", req)
	}

	for name, argv := range map[string][]string{
		"no gpu":           {"--", "true"},
		"no command":       {"--gpu", "A4000"},
		"bad env key":      {"--gpu", "A4000", "--env", "1A=x", "--", "true"},
		"unset env":        {"--gpu", "A4000", "--env", "MISSING", "--", "true"},
		"runtime too long": {"--gpu", "A4000", "--max-runtime", "25h", "--", "true"},
	} {
		if _, err := parse(argv...); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// ── follow loop ─────────────────────────────────────────────────────────────

type logStep struct {
	wantAfter int64
	status    int // HTTP status to answer instead of the page, when set
	resp      api.JobLogs
}

type fakeJobsServer struct {
	t        *testing.T
	mu       sync.Mutex
	steps    []logStep
	n        int
	job      api.Job
	follows  []string
	cancels  int
	finalGet int
	held     int // long polls held open until the client went away
}

func (f *fakeJobsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/v1/jobs/job-1/logs":
		if f.n >= len(f.steps) {
			f.held++
			f.mu.Unlock()
			// Hold the long poll like the server does until the client goes away.
			<-r.Context().Done()
			return
		}
		step := f.steps[f.n]
		f.n++
		f.follows = append(f.follows, r.URL.Query().Get("follow"))
		f.mu.Unlock()
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if after != step.wantAfter {
			f.t.Errorf("logs request %d: after = %d, want %d", f.n, after, step.wantAfter)
		}
		if step.status != 0 {
			w.WriteHeader(step.status)
			w.Write([]byte(`{"error":{"code":"x","message":"try later"}}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": step.resp})
		return
	case r.Method == "GET" && r.URL.Path == "/api/v1/jobs/job-1":
		f.finalGet++
		json.NewEncoder(w).Encode(map[string]any{"data": f.job})
	case r.Method == "POST" && r.URL.Path == "/api/v1/jobs/job-1/cancel":
		f.cancels++
		w.WriteHeader(http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
	f.mu.Unlock()
}

func seq(n int64) *int64 { return &n }

func chunk(n int64, text string) api.LogChunk { return api.LogChunk{Seq: seq(n), Text: text} }

func newTestFollower(url string, stdout, stderr *bytes.Buffer) *jobFollower {
	return &jobFollower{
		client:     func() (*api.Client, error) { return api.NewClient("rc_live_test", url), nil },
		stdout:     stdout,
		stderr:     stderr,
		retryDelay: func(int) time.Duration { return 0 },
	}
}

func intp(n int) *int { return &n }

func TestStreamJobPrintsChunksInOrderAndExitsWithJobCode(t *testing.T) {
	cost := 0.04
	fake := &fakeJobsServer{t: t, steps: []logStep{
		{wantAfter: 0, resp: api.JobLogs{Status: "starting", Chunks: []api.LogChunk{chunk(1, "hello "), chunk(2, "wor")}, Cursor: 2}},
		{wantAfter: 2, resp: api.JobLogs{Status: "running", Cursor: 2}},
		{wantAfter: 2, status: http.StatusServiceUnavailable},
		{wantAfter: 2, resp: api.JobLogs{Status: "running", Chunks: []api.LogChunk{
			chunk(3, "ld\n"), {Text: "\n[runcrate] output dropped\n"}, chunk(9, "tail"),
		}, Cursor: 9, Truncated: true}},
		{wantAfter: 9, resp: api.JobLogs{Status: "failed", Cursor: 9, Done: true}},
	}, job: api.Job{
		ID: "job-1", Status: "failed", ExitCode: intp(3), CostUSD: &cost,
		StartedAt: "2026-10-20T10:00:00.000Z", EndedAt: "2026-10-20T10:03:12.000Z",
	}}
	server := httptest.NewServer(fake)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	code, err := streamJob(context.Background(), newTestFollower(server.URL, &stdout, &stderr), "job-1")
	if err != nil {
		t.Fatalf("streamJob: %v", err)
	}
	if code != 3 {
		t.Errorf("exit code = %d, want 3", code)
	}
	if got, want := stdout.String(), "hello world\n\n[runcrate] output dropped\ntail"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	for _, f := range fake.follows {
		if f != "true" {
			t.Errorf("follow = %q, want true on every request", f)
		}
	}
	errOut := stderr.String()
	for _, want := range []string{"Machine is starting", "Command started.", "Lost the connection", "\nJob failed (exit code 3). Ran 3m12s, cost $0.04.\n"} {
		if strings.Count(errOut, want) != 1 {
			t.Errorf("stderr should contain %q once:\n%s", want, errOut)
		}
	}
}

func TestStreamJobExitCodes(t *testing.T) {
	tests := []struct {
		status string
		exit   *int
		want   int
	}{
		{"succeeded", intp(0), 0},
		{"failed", intp(2), 2},
		{"failed", nil, 1},
		{"timed_out", nil, 124},
		{"cancelled", nil, 130},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			fake := &fakeJobsServer{t: t, steps: []logStep{
				{wantAfter: 0, resp: api.JobLogs{Status: tt.status, Chunks: []api.LogChunk{chunk(1, "out\n")}, Cursor: 1, Done: true}},
			}, job: api.Job{ID: "job-1", Status: tt.status, ExitCode: tt.exit, MaxRuntimeMinutes: 60}}
			server := httptest.NewServer(fake)
			defer server.Close()

			var stdout, stderr bytes.Buffer
			code, err := streamJob(context.Background(), newTestFollower(server.URL, &stdout, &stderr), "job-1")
			if err != nil {
				t.Fatalf("streamJob: %v", err)
			}
			if code != tt.want {
				t.Errorf("exit code = %d, want %d", code, tt.want)
			}
			if fake.n != 1 || fake.finalGet != 1 {
				t.Errorf("logs requests = %d, job reads = %d; want 1 and 1 after done", fake.n, fake.finalGet)
			}
		})
	}
}

func TestStreamJobStopsOnPermanentError(t *testing.T) {
	fake := &fakeJobsServer{t: t, steps: []logStep{{wantAfter: 0, status: http.StatusNotFound}}}
	server := httptest.NewServer(fake)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	_, err := streamJob(context.Background(), newTestFollower(server.URL, &stdout, &stderr), "job-1")
	if err == nil || !strings.Contains(err.Error(), "runcrate jobs logs -f job-1") {
		t.Fatalf("err = %v, want a not-found error with the re-attach hint", err)
	}
	if fake.n != 1 {
		t.Errorf("a 404 was retried: %d requests", fake.n)
	}
}

// cancelOnWrite cancels the follow context shortly after output arrives, once
// the next long poll is open, standing in for Ctrl-C.
type cancelOnWrite struct {
	buf    bytes.Buffer
	cancel context.CancelFunc
}

func (c *cancelOnWrite) Write(p []byte) (int, error) {
	time.AfterFunc(100*time.Millisecond, c.cancel)
	return c.buf.Write(p)
}

func TestStreamJobDetachDoesNotCancel(t *testing.T) {
	fake := &fakeJobsServer{t: t, steps: []logStep{
		{wantAfter: 0, resp: api.JobLogs{Status: "running", Chunks: []api.LogChunk{chunk(1, "step 1\n")}, Cursor: 1}},
	}}
	server := httptest.NewServer(fake)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stderr bytes.Buffer
	stdout := &cancelOnWrite{cancel: cancel}
	f := newTestFollower(server.URL, nil, &stderr)
	f.stdout = stdout

	done := make(chan struct{})
	var code int
	var err error
	go func() {
		code, err = streamJob(ctx, f, "job-1")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamJob did not return after the context was cancelled")
	}

	if err != nil || code != 0 {
		t.Fatalf("streamJob = %d, %v; want 0, nil", code, err)
	}
	if stdout.buf.String() != "step 1\n" {
		t.Errorf("stdout = %q", stdout.buf.String())
	}
	for _, want := range []string{"Detached. The job keeps running.", "runcrate jobs logs -f job-1", "runcrate jobs cancel job-1"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.cancels != 0 {
		t.Errorf("detach called /cancel %d times", fake.cancels)
	}
	if fake.held != 1 {
		t.Errorf("held long polls = %d, want 1 (Ctrl-C should abort the open poll)", fake.held)
	}
}

func TestPrintAllLogsPagesUntilCursorStops(t *testing.T) {
	fake := &fakeJobsServer{t: t, steps: []logStep{
		{wantAfter: 0, resp: api.JobLogs{Status: "running", Chunks: []api.LogChunk{chunk(1, "a"), chunk(2, "b")}, Cursor: 2}},
		{wantAfter: 2, resp: api.JobLogs{Status: "running", Chunks: []api.LogChunk{chunk(3, "c")}, Cursor: 3}},
		{wantAfter: 3, resp: api.JobLogs{Status: "running", Cursor: 3}},
	}}
	server := httptest.NewServer(fake)
	defer server.Close()

	var out bytes.Buffer
	if err := printAllLogs(context.Background(), api.NewClient("rc_live_test", server.URL), "job-1", &out); err != nil {
		t.Fatalf("printAllLogs: %v", err)
	}
	if out.String() != "abc" {
		t.Errorf("out = %q, want abc", out.String())
	}
	for _, f := range fake.follows {
		if f != "" {
			t.Errorf("printAllLogs sent follow=%q", f)
		}
	}
}
