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

	job, err := NewClient("rc_live_test", server.URL).CreateJob(CreateJobRequest{
		Command:           "python train.py",
		GPUType:           "A4000",
		MaxRuntimeMinutes: 90,
		Env:               map[string]string{"HF_TOKEN": "secret"},
		StorageID:         "vol-1",
	})
	if err != nil {
		t.Fatalf("CreateJob() error: %v", err)
	}
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
	for _, unset := range []string{"template", "image", "name", "region"} {
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
		_, err := client.CreateJob(CreateJobRequest{Command: "true", GPUType: "A4000", MaxRuntimeMinutes: 1})
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

	list, err := NewClient("rc_live_test", server.URL).ListJobs("running,queued", 2, "prev")
	if err != nil {
		t.Fatalf("ListJobs() error: %v", err)
	}
	if query != "cursor=prev&limit=2&status=running%2Cqueued" {
		t.Errorf("query = %q", query)
	}
	if len(list.Jobs) != 2 || list.Cursor != "abc" || !list.HasMore {
		t.Errorf("list = %+v", list)
	}
}
