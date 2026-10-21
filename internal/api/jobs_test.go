package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateJobSendsContractBody(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/jobs" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data":{"id":"job-1","name":"job-job1","status":"queued","gpu_type":"A4000","gpu_count":1,"max_runtime_minutes":90,"exit_code":null,"cost_usd":null}}`))
	}))
	defer server.Close()

	sub, err := NewClient("rc_live_test", server.URL).CreateJob(CreateJobRequest{
		Command:           "python train.py",
		GPUType:           "A4000",
		MaxRuntimeMinutes: 90,
		Env:               map[string]string{"HF_TOKEN": "secret"},
		StorageID:         "vol-1",
	}, "")
	if err != nil {
		t.Fatalf("CreateJob() error: %v", err)
	}
	job := sub.Job
	if job.ID != "job-1" || job.Status != "queued" || job.ExitCode != nil || job.CostUSD != nil {
		t.Errorf("job = %+v", job)
	}

	if body["gpu_type"] != "A4000" || body["command"] != "python train.py" || body["storage_id"] != "vol-1" {
		t.Errorf("body = %v", body)
	}
	if body["max_runtime_minutes"] != float64(90) {
		t.Errorf("max_runtime_minutes = %v, want 90", body["max_runtime_minutes"])
	}
	env, ok := body["env"].(map[string]any)
	if !ok || env["HF_TOKEN"] != "secret" {
		t.Errorf("env = %#v, want an object", body["env"])
	}
	for _, unset := range []string{"template", "image", "name", "region", "count", "params", "tags", "code",
		"max_retries", "capacity_wait_minutes", "webhook_url", "image_registry_auth", "allow_cpu_container"} {
		if _, ok := body[unset]; ok {
			t.Errorf("unset %q was sent: %v", unset, body[unset])
		}
	}
}

func TestCreateJobErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Case") {
		case "budget":
			w.WriteHeader(http.StatusPaymentRequired)
			w.Write([]byte(`{"error":{"code":"BUDGET_REACHED","message":"Monthly budget reached"}}`))
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"error":{"code":"service_unavailable","message":"No machine available","details":{"job_id":"job-9"}}}`))
		}
	}))
	defer server.Close()

	do := func(c string) error {
		client := NewClient("rc_live_test", server.URL)
		client.httpClient.Transport = headerTransport{"X-Case", c}
		_, err := client.CreateJob(CreateJobRequest{Command: "true", GPUType: "A4000", MaxRuntimeMinutes: 1}, "")
		return err
	}

	err := do("budget")
	if err == nil || !strings.Contains(err.Error(), "monthly budget") || !strings.Contains(err.Error(), "owner or admin") {
		t.Errorf("budget error = %v", err)
	}

	err = do("launch")
	var se *APIError
	if !errors.As(err, &se) || se.Status != 503 || se.Details["job_id"] != "job-9" {
		t.Errorf("launch failure error = %#v", err)
	}
	if err.Error() != "No machine available (HTTP 503)" {
		t.Errorf("message = %q", err.Error())
	}
}

type headerTransport struct{ key, value string }

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set(h.key, h.value)
	return http.DefaultTransport.RoundTrip(r)
}

func TestGetJobLogsQueryAndMarker(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`{"data":{"job_id":"job-1","status":"running","chunks":[{"seq":7,"ts":"2026-10-20T10:01:00.000Z","text":"a"},{"seq":null,"ts":null,"text":"\n[runcrate] output dropped\n"}],"cursor":8,"truncated":true,"done":false}}`))
	}))
	defer server.Close()

	logs, err := NewClient("rc_live_test", server.URL).GetJobLogs(context.Background(), "job-1", 6, true, 0)
	if err != nil {
		t.Fatalf("GetJobLogs() error: %v", err)
	}
	if query != "after=6&follow=true" {
		t.Errorf("query = %q, want after=6&follow=true", query)
	}
	if logs.Cursor != 8 || !logs.Truncated || len(logs.Chunks) != 2 {
		t.Fatalf("logs = %+v", logs)
	}
	if logs.Chunks[0].Seq == nil || *logs.Chunks[0].Seq != 7 {
		t.Errorf("first chunk seq = %v, want 7", logs.Chunks[0].Seq)
	}
	if logs.Chunks[1].Seq != nil || logs.Chunks[1].TS != nil {
		t.Errorf("marker chunk should have null seq and ts: %+v", logs.Chunks[1])
	}
}

func TestListJobsPassesFiltersAndReadsMeta(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.Write([]byte(`{"data":[{"id":"job-2","status":"running"},{"id":"job-1","status":"queued"}],"meta":{"cursor":"abc","hasMore":true}}`))
	}))
	defer server.Close()

	list, err := NewClient("rc_live_test", server.URL).ListJobs(JobFilters{
		Status: "running,queued", Tags: map[string]string{"team": "ml"}, GroupID: "g1",
		Query: "train", GPUType: "H100", CreatedAfter: "2026-10-20T00:00:00Z",
	}, 2, "prev")
	if err != nil {
		t.Fatalf("ListJobs() error: %v", err)
	}
	if query != "created_after=2026-10-20T00%3A00%3A00Z&cursor=prev&gpu_type=H100&group_id=g1&limit=2&q=train&status=running%2Cqueued&tag=team%3Aml" {
		t.Errorf("query = %q", query)
	}
	if len(list.Jobs) != 2 || list.Cursor != "abc" || !list.HasMore {
		t.Errorf("list = %+v", list)
	}
}

func TestSubmitSendsAnIdempotencyKeyAndReadsAGroup(t *testing.T) {
	var keys []string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		json.NewDecoder(r.Body).Decode(&body)
		if r.URL.Path == "/api/v1/jobs/job-1/rerun" {
			w.Header().Set("Idempotent-Replayed", "true")
			w.Write([]byte(`{"data":{"id":"job-2","status":"queued","rerun_of":"job-1"}}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"data":{"id":"g1","count":2,"max_parallel":1,"status_counts":{"queued":2},"jobs":[{"id":"a"},{"id":"b"}]}}`))
	}))
	defer server.Close()
	client := NewClient("rc_live_test", server.URL)

	sub, err := client.CreateJob(CreateJobRequest{
		Command: "x", GPUType: []string{"H100", "A100-80GB:2"}, MaxRuntimeMinutes: 5,
		Params: []map[string]string{{"LR": "0.1"}, {"LR": "0.2"}}, MaxParallel: 1,
		Code: &JobCode{Git: &JobGit{URL: "https://github.com/a/b", TokenEnv: "GIT_TOKEN"}},
	}, "")
	if err != nil {
		t.Fatalf("CreateJob() error: %v", err)
	}
	if sub.Job != nil || sub.Group == nil || sub.Group.ID != "g1" || len(sub.Group.Jobs) != 2 {
		t.Fatalf("submission = %+v", sub)
	}
	if gpus, _ := body["gpu_type"].([]any); len(gpus) != 2 || gpus[1] != "A100-80GB:2" {
		t.Errorf("gpu_type = %#v, want the list", body["gpu_type"])
	}
	if code, _ := body["code"].(map[string]any); code["git"].(map[string]any)["token_env"] != "GIT_TOKEN" || code["upload_id"] != nil {
		t.Errorf("code = %#v", body["code"])
	}

	sub, err = client.RerunJob("job-1", map[string]any{"gpu_count": 2}, "my-key")
	if err != nil {
		t.Fatalf("RerunJob() error: %v", err)
	}
	if sub.Job == nil || sub.Job.RerunOf != "job-1" || !sub.Replayed || body["gpu_count"] != float64(2) {
		t.Errorf("rerun = %+v, body %v", sub, body)
	}
	if len(keys[0]) != 36 || keys[1] != "my-key" {
		t.Errorf("Idempotency-Keys = %q, want a fresh UUID then the given key", keys)
	}
}

func TestArtifactURLReadsTheRedirectWithoutFollowingIt(t *testing.T) {
	var signedHits int
	var signedAuth string
	signed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signedHits++
		signedAuth = r.Header.Get("Authorization")
		w.Write([]byte("bytes"))
	}))
	defer signed.Close()
	var path string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		if strings.HasSuffix(path, "/missing") {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"code":"not_found","message":"The job has no such output"}}`))
			return
		}
		http.Redirect(w, r, signed.URL+"/o?sig=1", http.StatusFound)
	}))
	defer api.Close()
	client := NewClient("rc_live_test", api.URL)

	url, err := client.ArtifactURL("job-1", "runs/a b.txt")
	if err != nil {
		t.Fatalf("ArtifactURL() error: %v", err)
	}
	if url != signed.URL+"/o?sig=1" || signedHits != 0 {
		t.Errorf("url = %q after %d hits on the signed URL, want it unfollowed", url, signedHits)
	}
	if path != "/api/v1/jobs/job-1/artifacts/runs/a%20b.txt" {
		t.Errorf("path = %q", path)
	}
	var out strings.Builder
	if err := DownloadSigned(context.Background(), url, &out); err != nil || out.String() != "bytes" || signedAuth != "" {
		t.Errorf("download = %q, %v, Authorization %q", out.String(), err, signedAuth)
	}

	_, err = client.ArtifactURL("job-1", "missing")
	var se *APIError
	if !errors.As(err, &se) || se.Status != 404 {
		t.Errorf("missing output error = %v", err)
	}
}
