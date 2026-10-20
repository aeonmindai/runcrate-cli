package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// ── follow ──────────────────────────────────────────────────────────────────

// errDetached reports that the follow context was cancelled (Ctrl-C).
var errDetached = errors.New("detached")

// jobFollower streams a job's output by long-polling its logs with the cursor.
type jobFollower struct {
	// client is called before every request, so a long follow picks up
	// refreshed sign-in tokens.
	client     func() (*api.Client, error)
	stdout     io.Writer
	stderr     io.Writer
	retryDelay func(attempt int) time.Duration
	// idleDelay spaces out polls that return no output, in case the server
	// answers at once instead of holding the request.
	idleDelay time.Duration

	atLineStart bool
	announced   map[string]bool
}

func newJobFollower() *jobFollower {
	return &jobFollower{
		client:     loadClient,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		retryDelay: backoff,
		idleDelay:  time.Second,
	}
}

// backoff waits 1s, 2s, 4s ... up to 15s between retries.
func backoff(attempt int) time.Duration {
	d := time.Second << min(attempt-1, 4)
	return min(d, 15*time.Second)
}

// retryable reports whether a failed request is worth repeating: network
// errors, rate limits and server errors. 401 is retried a few times because
// the client is rebuilt (and its token refreshed) before each attempt.
func retryable(err error, authFailures int) bool {
	var se *api.StatusError
	if !errors.As(err, &se) {
		return true
	}
	switch {
	case se.StatusCode == http.StatusUnauthorized:
		return authFailures <= 3
	case se.StatusCode == http.StatusTooManyRequests, se.StatusCode >= 500:
		return true
	}
	return false
}

// call runs fn with a fresh client, retrying transient failures until it
// succeeds, fails for good, or ctx is cancelled (errDetached).
func (f *jobFollower) call(ctx context.Context, fn func(*api.Client) error) error {
	failures, authFailures := 0, 0
	for {
		if ctx.Err() != nil {
			return errDetached
		}
		client, err := f.client()
		if err != nil {
			return err
		}
		err = fn(client)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return errDetached
		}
		var se *api.StatusError
		if errors.As(err, &se) && se.StatusCode == http.StatusUnauthorized {
			authFailures++
		}
		if !retryable(err, authFailures) {
			return err
		}
		failures++
		if failures == 1 {
			f.lineBreak()
			fmt.Fprintf(f.stderr, "Lost the connection to Runcrate (%v). Retrying; the job keeps running.\n", err)
		}
		if !sleepCtx(ctx, f.retryDelay(failures)) {
			return errDetached
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// lineBreak ends a partial output line on stderr so status lines start clean.
// stdout stays byte-for-byte the job's output.
func (f *jobFollower) lineBreak() {
	if !f.atLineStart {
		fmt.Fprintln(f.stderr)
		f.atLineStart = true
	}
}

func (f *jobFollower) announce(status string) {
	var msg string
	switch status {
	case "starting":
		msg = "Machine is starting and running setup."
	case "running":
		msg = "Command started."
	default:
		return
	}
	if f.announced[status] {
		return
	}
	f.announced[status] = true
	f.lineBreak()
	fmt.Fprintln(f.stderr, msg)
}

// follow writes the job's output from seq cursor `after` until the job ends,
// then returns the final job. On ctx cancel it returns errDetached.
func (f *jobFollower) follow(ctx context.Context, id string, after int64) (*api.Job, error) {
	f.atLineStart = true
	f.announced = map[string]bool{}
	for {
		var logs *api.JobLogs
		err := f.call(ctx, func(c *api.Client) error {
			var err error
			logs, err = c.GetJobLogs(ctx, id, after, true, 0)
			return err
		})
		if err != nil {
			return nil, err
		}

		f.announce(logs.Status)
		for _, ch := range logs.Chunks {
			if ch.Text == "" {
				continue
			}
			if _, err := io.WriteString(f.stdout, ch.Text); err != nil {
				return nil, err
			}
			f.atLineStart = strings.HasSuffix(ch.Text, "\n")
		}
		if logs.Cursor > after {
			after = logs.Cursor
		}
		if logs.Done {
			break
		}
		if len(logs.Chunks) == 0 && !sleepCtx(ctx, f.idleDelay) {
			return nil, errDetached
		}
	}

	var job *api.Job
	err := f.call(ctx, func(c *api.Client) error {
		var err error
		job, err = c.GetJob(id)
		return err
	})
	if err != nil {
		return nil, err
	}
	f.lineBreak()
	return job, nil
}

// streamJob follows a job to its end and prints the summary to stderr. It
// returns the exit code for the CLI; on detach it prints how to re-attach or
// cancel and returns 0. It never cancels the job.
func streamJob(ctx context.Context, f *jobFollower, id string) (int, error) {
	job, err := f.follow(ctx, id, 0)
	if errors.Is(err, errDetached) {
		f.lineBreak()
		fmt.Fprintln(f.stderr, "Detached. The job keeps running.")
		fmt.Fprintf(f.stderr, "  Follow:  runcrate jobs logs -f %s\n", id)
		fmt.Fprintf(f.stderr, "  Cancel:  runcrate jobs cancel %s\n", id)
		return 0, nil
	}
	if err != nil {
		f.lineBreak()
		return 0, fmt.Errorf("following job: %w\n  The job keeps running. Re-attach with: runcrate jobs logs -f %s", err, id)
	}
	fmt.Fprintln(f.stderr, jobSummary(job))
	return jobExitCode(job), nil
}

// jobExitCode maps a finished job to the CLI's exit code.
func jobExitCode(j *api.Job) int {
	switch j.Status {
	case "succeeded":
		return 0
	case "failed":
		if j.ExitCode != nil && *j.ExitCode > 0 && *j.ExitCode < 256 {
			return *j.ExitCode
		}
		return 1
	case "timed_out":
		return 124
	case "cancelled":
		return 130
	}
	return 1
}

// jobSummary is the one-line final status: outcome, exit code, runtime, cost.
func jobSummary(j *api.Job) string {
	var head string
	switch j.Status {
	case "succeeded":
		head = "Job succeeded"
	case "failed":
		head = "Job failed"
		if j.ExitCode != nil {
			head += fmt.Sprintf(" (exit code %d)", *j.ExitCode)
		}
	case "timed_out":
		head = fmt.Sprintf("Job timed out after its max runtime of %s", formatMinutes(j.MaxRuntimeMinutes))
	case "cancelled":
		head = "Job cancelled"
	default:
		head = "Job " + j.Status
	}
	if j.Error != "" {
		head += ": " + strings.TrimSuffix(j.Error, ".")
	}
	head += "."

	var details []string
	if d, ok := jobRuntime(j, time.Now()); ok {
		details = append(details, "ran "+formatDuration(d))
	}
	if j.CostUSD != nil {
		details = append(details, "cost "+formatCost(*j.CostUSD))
	}
	if len(details) == 0 {
		return head
	}
	s := strings.Join(details, ", ")
	return head + " " + strings.ToUpper(s[:1]) + s[1:] + "."
}

// jobRuntime is how long the command has run: started_at to ended_at, or to
// now while it runs. ok is false before it starts.
func jobRuntime(j *api.Job, now time.Time) (time.Duration, bool) {
	start, err := time.Parse(time.RFC3339, j.StartedAt)
	if err != nil {
		return 0, false
	}
	end := now
	if j.EndedAt != "" {
		if e, err := time.Parse(time.RFC3339, j.EndedAt); err == nil {
			end = e
		}
	}
	if end.Before(start) {
		return 0, true
	}
	return end.Sub(start), true
}

// formatDuration renders 45s, 12m5s, 2h3m.
func formatDuration(d time.Duration) string {
	if d >= time.Hour {
		d = d.Round(time.Minute)
		return strings.TrimSuffix(d.String(), "0s")
	}
	return d.Round(time.Second).String()
}

// formatShortDuration renders a coarse age: 45s, 12m, 3h, 2d.
func formatShortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		h := int(d.Hours())
		if m := int(d.Minutes()) % 60; m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func formatCost(c float64) string {
	if c > 0 && c < 0.01 {
		return fmt.Sprintf("$%.4f", c)
	}
	return fmt.Sprintf("$%.2f", c)
}

// ── commands ────────────────────────────────────────────────────────────────

var jobsCmd = &cobra.Command{
	Use:     "jobs",
	Aliases: []string{"job"},
	Short:   "List, follow and cancel jobs started with runcrate run",
}

var jobsListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List jobs, newest first",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		status, _ := cmd.Flags().GetString("status")
		limit, _ := cmd.Flags().GetInt("limit")
		list, err := client.ListJobs(strings.ReplaceAll(status, " ", ""), limit, "")
		if err != nil {
			return fmt.Errorf("fetching jobs: %w", err)
		}

		if jsonOutput {
			return printJSON(list.Jobs)
		}

		quiet, _ := cmd.Flags().GetBool("quiet")
		if quiet {
			for _, j := range list.Jobs {
				fmt.Println(j.ID)
			}
			return nil
		}

		if len(list.Jobs) == 0 {
			fmt.Println("No jobs found. Start one with: runcrate run --gpu A4000 -- <command>")
			return nil
		}

		now := time.Now()
		w := newTable()
		fmt.Fprintf(w, "ID\tNAME\tSTATUS\tGPU\tEXIT\tRUNTIME\tAGE\tCOST\n")
		for _, j := range list.Jobs {
			gpu := j.GPUType
			if j.GPUCount > 1 {
				gpu = fmt.Sprintf("%dx %s", j.GPUCount, j.GPUType)
			}
			exit := "-"
			if j.ExitCode != nil {
				exit = fmt.Sprintf("%d", *j.ExitCode)
			}
			runtime := "-"
			if d, ok := jobRuntime(&j, now); ok {
				runtime = formatShortDuration(d)
			}
			age := "-"
			if c, err := time.Parse(time.RFC3339, j.CreatedAt); err == nil {
				age = formatShortDuration(now.Sub(c)) + " ago"
			}
			cost := "-"
			if j.CostUSD != nil {
				cost = formatCost(*j.CostUSD)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				j.ID, truncate(j.Name, 24), statusColor(j.Status), gpu, exit, runtime, age, cost)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if list.HasMore {
			fmt.Fprintf(os.Stderr, "Showing the newest %d jobs. See more with --limit (up to 100).\n", len(list.Jobs))
		}
		return nil
	},
}

var jobsStatusCmd = &cobra.Command{
	Use:     "status <id>",
	Aliases: []string{"info"},
	Short:   "Show a job's details",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		j, err := client.GetJob(args[0])
		if err != nil {
			return fmt.Errorf("fetching job: %w", err)
		}

		if jsonOutput {
			return printJSON(j)
		}

		fmt.Println()
		fmt.Printf("  Name:         %s\n", j.Name)
		fmt.Printf("  ID:           %s\n", j.ID)
		fmt.Printf("  Status:       %s\n", statusColor(j.Status))
		if j.ExitCode != nil {
			fmt.Printf("  Exit code:    %d\n", *j.ExitCode)
		}
		if j.Error != "" {
			fmt.Printf("  Error:        %s\n", j.Error)
		}
		fmt.Printf("  Command:      %s\n", j.Command)
		fmt.Printf("  GPU:          %dx %s\n", j.GPUCount, j.GPUType)
		if j.Image != "" {
			fmt.Printf("  Image:        %s\n", j.Image)
		} else if j.Template != "" {
			fmt.Printf("  Template:     %s\n", j.Template)
		}
		if j.StorageID != "" {
			fmt.Printf("  Volume:       %s\n", j.StorageID)
		}
		if len(j.EnvKeys) > 0 {
			fmt.Printf("  Env:          %s\n", strings.Join(j.EnvKeys, ", "))
		}
		fmt.Printf("  Max runtime:  %s\n", formatMinutes(j.MaxRuntimeMinutes))
		if d, ok := jobRuntime(j, time.Now()); ok {
			fmt.Printf("  Runtime:      %s\n", formatDuration(d))
		}
		if j.CostUSD != nil {
			cost := formatCost(*j.CostUSD)
			if j.EndedAt == "" {
				cost += " so far"
			}
			if j.CostPerHour > 0 {
				cost += fmt.Sprintf(" ($%.2f/hr)", j.CostPerHour)
			}
			fmt.Printf("  Cost:         %s\n", cost)
		} else if j.CostPerHour > 0 {
			fmt.Printf("  Cost:         $%.2f/hr\n", j.CostPerHour)
		}
		if j.InstanceID != "" {
			fmt.Printf("  Instance:     %s\n", j.InstanceID)
		}
		fmt.Printf("  Created:      %s\n", j.CreatedAt)
		if j.StartedAt != "" {
			fmt.Printf("  Started:      %s\n", j.StartedAt)
		}
		if j.EndedAt != "" {
			fmt.Printf("  Ended:        %s\n", j.EndedAt)
		}
		if j.LogsTruncated {
			fmt.Printf("  Logs:         %.1f MB, middle dropped (kept the first 5 MB and the newest output)\n", float64(j.LogBytes)/(1<<20))
		}
		fmt.Println()
		return nil
	},
}

var jobsLogsCmd = &cobra.Command{
	Use:   "logs <id>",
	Short: "Print a job's output (-f to follow until it ends)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		follow, _ := cmd.Flags().GetBool("follow")
		if follow {
			return followAndExit(cmd, args[0])
		}

		client, err := loadClient()
		if err != nil {
			return err
		}
		return printAllLogs(cmd.Context(), client, args[0], os.Stdout)
	},
}

// printAllLogs pages through every chunk from the start until the cursor
// stops advancing.
func printAllLogs(ctx context.Context, client *api.Client, id string, out io.Writer) error {
	var after int64
	for {
		logs, err := client.GetJobLogs(ctx, id, after, false, 1000)
		if err != nil {
			return fmt.Errorf("fetching logs: %w", err)
		}
		for _, ch := range logs.Chunks {
			if _, err := io.WriteString(out, ch.Text); err != nil {
				return err
			}
		}
		if logs.Done || logs.Cursor <= after {
			return nil
		}
		after = logs.Cursor
	}
}

var jobsCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a job and stop its machine",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}

		j, err := client.CancelJob(args[0])
		if err != nil {
			return fmt.Errorf("cancelling job: %w", err)
		}

		if jsonOutput {
			return printJSON(j)
		}
		fmt.Printf("Job %s is %s. Its machine is stopping.\n", j.Name, j.Status)
		return nil
	},
}

func init() {
	jobsCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")

	jobsListCmd.Flags().String("status", "", "Only these statuses, comma separated (e.g. running,queued)")
	jobsListCmd.Flags().Int("limit", 20, "How many jobs to show (max 100)")
	jobsListCmd.Flags().BoolP("quiet", "q", false, "Only show job IDs")

	jobsLogsCmd.Flags().BoolP("follow", "f", false, "Follow the output until the job ends; exit with its exit code")

	jobsCmd.AddCommand(jobsListCmd)
	jobsCmd.AddCommand(jobsStatusCmd)
	jobsCmd.AddCommand(jobsLogsCmd)
	jobsCmd.AddCommand(jobsCancelCmd)
	rootCmd.AddCommand(jobsCmd)
}
