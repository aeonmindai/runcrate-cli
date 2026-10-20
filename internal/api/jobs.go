package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Job is one shell command run on its own machine (RUN-486).
type Job struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Status            string   `json:"status"`
	Command           string   `json:"command"`
	GPUType           string   `json:"gpu_type"`
	GPUCount          int      `json:"gpu_count"`
	Template          string   `json:"template"`
	Image             string   `json:"image"`
	EnvKeys           []string `json:"env_keys"`
	StorageID         string   `json:"storage_id"`
	MaxRuntimeMinutes int      `json:"max_runtime_minutes"`
	ExitCode          *int     `json:"exit_code"`
	Error             string   `json:"error"`
	InstanceID        string   `json:"instance_id"`
	EnvironmentID     string   `json:"environment_id"`
	CreatedAt         string   `json:"created_at"`
	StartedAt         string   `json:"started_at"`
	EndedAt           string   `json:"ended_at"`
	CostPerHour       float64  `json:"cost_per_hour"`
	CostUSD           *float64 `json:"cost_usd"`
	LogBytes          int64    `json:"log_bytes"`
	LogsTruncated     bool     `json:"logs_truncated"`
}

// LogChunk is a raw fragment of a job's combined stdout and stderr. Seq and
// TS are null on the marker chunk that stands where truncated output was dropped.
type LogChunk struct {
	Seq  *int64  `json:"seq"`
	TS   *string `json:"ts"`
	Text string  `json:"text"`
}

// JobLogs is one page of a job's output. Pass Cursor back as `after`; Done means
// the job has ended and every chunk was read.
type JobLogs struct {
	JobID     string     `json:"job_id"`
	Status    string     `json:"status"`
	Chunks    []LogChunk `json:"chunks"`
	Cursor    int64      `json:"cursor"`
	Truncated bool       `json:"truncated"`
	Done      bool       `json:"done"`
}

// JobList is one page of jobs, newest first.
type JobList struct {
	Jobs    []Job
	Cursor  string
	HasMore bool
}

// CreateJobRequest is the body of POST /api/v1/jobs.
type CreateJobRequest struct {
	Command           string            `json:"command"`
	GPUType           string            `json:"gpu_type"`
	MaxRuntimeMinutes int               `json:"max_runtime_minutes"`
	GPUCount          int               `json:"gpu_count,omitempty"`
	Template          string            `json:"template,omitempty"`
	Image             string            `json:"image,omitempty"`
	Env               map[string]string `json:"env,omitempty"`
	StorageID         string            `json:"storage_id,omitempty"`
	Name              string            `json:"name,omitempty"`
	Region            string            `json:"region,omitempty"`
}

// LongPollTimeout bounds a follow=true logs request. The server holds it for
// up to 25 s, so this leaves room for the response without hanging on a dead
// connection for the client's default timeout.
const LongPollTimeout = 40 * time.Second

// CreateJob submits a job. When the job was created but its machine could not
// be launched, the *StatusError carries Details["job_id"].
func (c *Client) CreateJob(req CreateJobRequest) (*Job, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encoding request: %w", err)
	}

	resp, err := c.do("POST", "/api/v1/jobs", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Job]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// ListJobs returns one page of jobs. status is a comma list; empty means all.
// limit <= 0 uses the server default; cursor is meta.cursor from a previous page.
func (c *Client) ListJobs(status string, limit int, cursor string) (*JobList, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	path := "/api/v1/jobs"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}

	resp, err := c.do("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result struct {
		Data []Job `json:"data"`
		Meta struct {
			Cursor  any  `json:"cursor"`
			HasMore bool `json:"hasMore"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	list := &JobList{Jobs: result.Data, HasMore: result.Meta.HasMore}
	// The cursor is opaque: keep it as the string to send back.
	switch v := result.Meta.Cursor.(type) {
	case string:
		list.Cursor = v
	case float64:
		list.Cursor = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return list, nil
}

// GetJob returns one job.
func (c *Client) GetJob(id string) (*Job, error) {
	resp, err := c.do("GET", "/api/v1/jobs/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Job]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// GetJobLogs returns chunks after the seq cursor `after`. With follow, the
// server waits up to 25 s for new output or the end, and the request is bounded
// by LongPollTimeout. limit <= 0 uses the server default. Cancelling ctx aborts
// the request.
func (c *Client) GetJobLogs(ctx context.Context, id string, after int64, follow bool, limit int) (*JobLogs, error) {
	q := url.Values{}
	q.Set("after", strconv.FormatInt(after, 10))
	if follow {
		q.Set("follow", "true")
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, LongPollTimeout)
		defer cancel()
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}

	resp, err := c.doCtx(ctx, "GET", "/api/v1/jobs/"+url.PathEscape(id)+"/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[JobLogs]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}

// CancelJob stops the job's machine; the job becomes cancelled.
func (c *Client) CancelJob(id string) (*Job, error) {
	resp, err := c.do("POST", "/api/v1/jobs/"+url.PathEscape(id)+"/cancel", nil)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if err := checkHTTPStatus(resp); err != nil {
		return nil, err
	}

	var result apiEnvelope[Job]
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	return &result.Data, nil
}
