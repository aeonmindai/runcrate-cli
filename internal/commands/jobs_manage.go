package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

// exitWaitTimeout is the exit code of jobs wait when --timeout passes first
// (EX_TEMPFAIL): the job keeps running.
const exitWaitTimeout = 75

// ── rerun ───────────────────────────────────────────────────────────────────

// rerunOverrides turns the changed rerun flags into the fields to change. A
// job's env values are kept; --env adds or replaces some.
func rerunOverrides(cmd *cobra.Command, lookupEnv func(string) (string, bool)) (map[string]any, error) {
	f := cmd.Flags()
	o := map[string]any{}
	if f.Changed("gpu") {
		raw, _ := f.GetString("gpu")
		count, _ := f.GetInt("gpu-count")
		gpuType, gpuCount, err := parseJobGPU(raw, count, f.Changed("gpu-count"))
		if err != nil {
			return nil, err
		}
		o["gpu_type"] = gpuType
		if gpuCount > 0 {
			o["gpu_count"] = gpuCount
		}
	} else if f.Changed("gpu-count") {
		// A new count alone applies to the job's own GPUs.
		o["gpu_count"], _ = f.GetInt("gpu-count")
	}
	if f.Changed("max-runtime") {
		raw, _ := f.GetString("max-runtime")
		minutes, err := parseMaxRuntime(raw)
		if err != nil {
			return nil, err
		}
		o["max_runtime_minutes"] = minutes
	}
	if f.Changed("capacity-wait") {
		raw, _ := f.GetString("capacity-wait")
		minutes, err := parseFlagMinutes("capacity-wait", raw, 0, maxCapacityWaitMinutes)
		if err != nil {
			return nil, err
		}
		o["capacity_wait_minutes"] = minutes
	}
	if f.Changed("retries") {
		o["max_retries"], _ = f.GetInt("retries")
	}
	if f.Changed("env") {
		vals, _ := f.GetStringArray("env")
		env, err := parseEnvFlags(vals, lookupEnv)
		if err != nil {
			return nil, err
		}
		o["env"] = env
	}
	if f.Changed("tag") {
		vals, _ := f.GetStringArray("tag")
		tags, err := parseTagFlags(vals)
		if err != nil {
			return nil, err
		}
		o["tags"] = tags
	}
	for flag, field := range map[string]string{
		"name": "name", "region": "region", "image": "image", "template": "template", "volume": "storage_id",
	} {
		if f.Changed(flag) {
			o[field], _ = f.GetString(flag)
		}
	}
	return o, nil
}

func newJobsRerunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rerun <id>",
		Short: "Run a job again as a new job, with its env values and any flag changed",
		Long: `Run a job's command and settings again as a new job, then follow it like
runcrate run. Its env values come along (only whoever submitted the job can reuse
them); --env adds or replaces some, and any other flag replaces that setting.`,
		Example: `  runcrate jobs rerun <job-id>
  runcrate jobs rerun <job-id> --gpu H100 --max-runtime 4h --env EPOCHS=10`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			overrides, err := rerunOverrides(cmd, os.LookupEnv)
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			client, err := loadClient()
			if err != nil {
				return err
			}
			key, _ := cmd.Flags().GetString("idempotency-key")
			sub, err := client.RerunJob(args[0], overrides, key)
			if err != nil {
				return submitError("rerunning job", err)
			}
			detach, _ := cmd.Flags().GetBool("detach")
			return startedJobs(cmd, sub, detach || jsonOutput, false)
		},
	}
	f := cmd.Flags()
	f.String("gpu", "", "GPU type, or GPUs to try in order (H100,A100-80GB:2)")
	f.Int("gpu-count", 1, "Number of GPUs")
	f.String("max-runtime", "", "Stop each attempt after this long: 90m, 2h, or minutes (1m to 168h)")
	f.String("capacity-wait", "", "How long to wait for a free GPU: 0 to 24h")
	f.Int("retries", 0, "Retry a failed attempt up to this many times (0 to 10)")
	f.StringArray("env", nil, "Add or replace an env variable KEY=VALUE, or KEY to copy it from your shell (repeatable)")
	f.StringArray("tag", nil, "Replace the tags with these key=value (repeatable)")
	f.String("name", "", "Job name")
	f.String("region", "", "Preferred region")
	f.String("image", "", "Container image to run the command in")
	f.String("template", "", "OS template")
	f.String("volume", "", "Volume ID to mount as /workspace")
	f.String("idempotency-key", "", "Rerun at most once per key for 24 hours")
	f.BoolP("detach", "d", false, "Print the new job's ID and return without following its output")
	return cmd
}

var jobsRerunCmd = newJobsRerunCmd()

// ── wait ────────────────────────────────────────────────────────────────────

// errWaitTimeout reports that jobs wait gave up before the job ended.
var errWaitTimeout = errors.New("wait timed out")

// jobWaiter polls a job until it ends.
type jobWaiter struct {
	client func() (*api.Client, error)
	sleep  func(ctx context.Context, d time.Duration) bool
	now    func() time.Time
	// first and max bound the gap between polls, which doubles.
	first, max time.Duration
}

func newJobWaiter() *jobWaiter {
	return &jobWaiter{client: loadClient, sleep: sleepCtx, now: time.Now, first: 2 * time.Second, max: 30 * time.Second}
}

// wait returns the job once it has ended. Past timeout (0 for none) it returns
// the job as last seen with errWaitTimeout; on ctx cancel, errDetached. Network
// errors, rate limits and server errors are retried.
func (w *jobWaiter) wait(ctx context.Context, id string, timeout time.Duration) (*api.Job, error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = w.now().Add(timeout)
	}
	gap, failures, authFailures := w.first, 0, 0
	var last *api.Job
	for {
		client, err := w.client()
		if err != nil {
			return nil, err
		}
		job, err := client.GetJob(id)
		switch {
		case err == nil:
			failures, authFailures = 0, 0
			last = job
			if isJobFinished(job.Status) {
				return job, nil
			}
		default:
			var se *api.APIError
			if errors.As(err, &se) && se.Status == http.StatusUnauthorized {
				authFailures++
			}
			if !retryable(err, authFailures) {
				return nil, err
			}
			failures++
		}

		next := gap
		if failures > 0 {
			next = backoff(failures)
		}
		if !deadline.IsZero() {
			left := deadline.Sub(w.now())
			if left <= 0 {
				return last, errWaitTimeout
			}
			next = min(next, left)
		}
		if !w.sleep(ctx, next) {
			return last, errDetached
		}
		if failures == 0 {
			gap = min(gap*2, w.max)
		}
	}
}

// isJobFinished reports whether a status is final.
func isJobFinished(status string) bool {
	switch status {
	case "queued", "starting", "running":
		return false
	}
	return true
}

var jobsWaitCmd = &cobra.Command{
	Use:   "wait <id>",
	Short: "Wait for a job to end and exit with its exit code",
	Long: `Wait until the job ends, then print its summary and exit like runcrate run:
0 when it succeeded, the command's exit code when it failed, 124 when it timed
out, 130 when it was cancelled. With --timeout, exit 75 if it is still running
then (the job keeps running).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		timeoutRaw, _ := cmd.Flags().GetString("timeout")
		var timeout time.Duration
		if timeoutRaw != "" {
			d, err := time.ParseDuration(timeoutRaw)
			if err != nil || d <= 0 {
				return fmt.Errorf("invalid --timeout %q: use a duration like 30m or 2h", timeoutRaw)
			}
			timeout = d
		}
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
		defer stop()

		job, err := newJobWaiter().wait(ctx, args[0], timeout)
		switch {
		case errors.Is(err, errDetached):
			fmt.Fprintln(os.Stderr, "Stopped waiting. The job keeps running.")
			return nil
		case errors.Is(err, errWaitTimeout):
			if jsonOutput && job != nil {
				_ = printJSON(job)
			}
			status := "live"
			if job != nil {
				status = job.Status
			}
			fmt.Fprintf(os.Stderr, "Job %s is still %s after %s. It keeps running; wait again or cancel it with: runcrate jobs cancel %s\n",
				args[0], status, timeoutRaw, args[0])
			stop()
			os.Exit(exitWaitTimeout)
		case err != nil:
			return fmt.Errorf("waiting for job: %w", err)
		}

		if jsonOutput {
			if err := printJSON(job); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(os.Stderr, jobSummary(job))
		}
		if code := jobExitCode(job); code != 0 {
			stop()
			os.Exit(code)
		}
		return nil
	},
}

// ── artifacts and download ──────────────────────────────────────────────────

var jobsArtifactsCmd = &cobra.Command{
	Use:     "artifacts <id>",
	Aliases: []string{"outputs"},
	Short:   "List the files a job wrote to $RUNCRATE_OUTPUT_DIR",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		artifacts, err := client.ListArtifacts(args[0])
		if err != nil {
			return fmt.Errorf("fetching outputs: %w", err)
		}
		return renderArtifacts(os.Stdout, args[0], artifacts, jsonOutput)
	},
}

func renderArtifacts(w io.Writer, id string, artifacts []api.Artifact, asJSON bool) error {
	if asJSON {
		if artifacts == nil {
			artifacts = []api.Artifact{}
		}
		return printJSONTo(w, artifacts)
	}
	if len(artifacts) == 0 {
		fmt.Fprintln(w, "No outputs. A job keeps the files its command writes to $RUNCRATE_OUTPUT_DIR for 30 days.")
		return nil
	}
	t := newTableTo(w)
	fmt.Fprintf(t, "PATH\tSIZE\tSHA256\n")
	for _, a := range artifacts {
		fmt.Fprintf(t, "%s\t%s\t%s\n", a.Path, formatBytes(a.Bytes), a.SHA256[:min(len(a.SHA256), 12)])
	}
	if err := t.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "Download them with: runcrate jobs download %s\n", id)
	return nil
}

// artifactDownloader saves a job's outputs under a directory.
type artifactDownloader struct {
	client *api.Client
	// fetch writes a signed URL's body to w.
	fetch  func(ctx context.Context, url string, w io.Writer) error
	stderr io.Writer
}

// selectArtifacts picks the outputs named by paths: a file's path, or a
// directory's ("runs" or "runs/") for everything under it. No paths is all.
func selectArtifacts(all []api.Artifact, paths []string) ([]api.Artifact, error) {
	if len(paths) == 0 {
		return all, nil
	}
	var picked []api.Artifact
	seen := map[string]bool{}
	for _, p := range paths {
		dir := strings.TrimSuffix(p, "/") + "/"
		found := false
		for _, a := range all {
			if a.Path == p || strings.HasPrefix(a.Path, dir) {
				found = true
				if !seen[a.Path] {
					seen[a.Path] = true
					picked = append(picked, a)
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("the job has no output %q (see: runcrate jobs artifacts <id>)", p)
		}
	}
	return picked, nil
}

// download saves each artifact under dir at its path, checking its SHA-256.
// An existing file is only replaced with force.
func (d *artifactDownloader) download(ctx context.Context, id string, artifacts []api.Artifact, dir string, force bool) error {
	var total int64
	for _, a := range artifacts {
		rel := filepath.FromSlash(a.Path)
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("refusing output path %q: it leaves the download directory", a.Path)
		}
		dest := filepath.Join(dir, rel)
		if _, err := os.Stat(dest); err == nil && !force {
			return fmt.Errorf("%s already exists; pass --force to replace it", dest)
		}
		if err := d.save(ctx, id, a, dest); err != nil {
			return err
		}
		total += a.Bytes
	}
	fmt.Fprintf(d.stderr, "Downloaded %d file(s), %s, to %s\n", len(artifacts), formatBytes(total), dir)
	return nil
}

func (d *artifactDownloader) save(ctx context.Context, id string, a api.Artifact, dest string) error {
	signed, err := d.client.ArtifactURL(id, a.Path)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", a.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	part := dest + ".part"
	file, err := os.Create(part)
	if err != nil {
		return err
	}
	hash := sha256.New()
	err = d.fetch(ctx, signed, io.MultiWriter(file, hash))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil && hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		err = errors.New("its SHA-256 does not match the job's record; try again")
	}
	if err != nil {
		os.Remove(part)
		return fmt.Errorf("downloading %s: %w", a.Path, err)
	}
	return os.Rename(part, dest)
}

var jobsDownloadCmd = &cobra.Command{
	Use:   "download <id> [path...]",
	Short: "Download a job's outputs (all, or the given files or directories)",
	Example: `  runcrate jobs download <job-id>
  runcrate jobs download <job-id> model.pt runs/ -o ./results`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		client, err := loadClient()
		if err != nil {
			return err
		}
		all, err := client.ListArtifacts(args[0])
		if err != nil {
			return fmt.Errorf("fetching outputs: %w", err)
		}
		picked, err := selectArtifacts(all, args[1:])
		if err != nil {
			return err
		}
		if len(picked) == 0 {
			fmt.Fprintln(os.Stderr, "The job has no outputs to download.")
			return nil
		}
		dir, _ := cmd.Flags().GetString("output")
		force, _ := cmd.Flags().GetBool("force")
		d := &artifactDownloader{client: client, fetch: api.DownloadSigned, stderr: os.Stderr}
		return d.download(cmd.Context(), args[0], picked, dir, force)
	},
}

// ── delete ──────────────────────────────────────────────────────────────────

var jobsDeleteCmd = &cobra.Command{
	Use:     "delete <id>",
	Aliases: []string{"rm"},
	Short:   "Delete a job that ended, with its logs and outputs",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			j, err := client.GetJob(args[0])
			if err != nil {
				return fmt.Errorf("fetching job: %w", err)
			}
			ok, err := confirm(os.Stdin, os.Stderr,
				fmt.Sprintf("Delete job %s (%s) with its logs and outputs? This cannot be undone. [y/N] ", j.Name, j.ID))
			if err != nil {
				return errors.New("no answer to the prompt; pass --yes to delete without asking")
			}
			if !ok {
				fmt.Println("Kept the job.")
				return nil
			}
		}
		if err := client.DeleteJob(args[0]); err != nil {
			var se *api.APIError
			if errors.As(err, &se) && se.Status == http.StatusConflict {
				return fmt.Errorf("deleting job: %w\n  Cancel it first: runcrate jobs cancel %s", err, args[0])
			}
			return fmt.Errorf("deleting job: %w", err)
		}
		fmt.Println("Job deleted.")
		return nil
	},
}

// ── groups ──────────────────────────────────────────────────────────────────

var jobsGroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Show or cancel a group of jobs (from run --count or --params)",
}

func renderJobGroup(w io.Writer, g *api.JobGroup) {
	fmt.Fprintf(w, "Group %s: %d jobs, at most %d at once.\n", g.ID, g.Count, g.MaxParallel)
	for _, status := range []string{"queued", "starting", "running", "succeeded", "failed", "timed_out", "cancelled"} {
		if n := g.StatusCounts[status]; n > 0 {
			fmt.Fprintf(w, "  %-11s %d\n", status, n)
		}
	}
	if g.CostUSD != nil {
		fmt.Fprintf(w, "  Cost:       %s\n", formatCost(*g.CostUSD))
	}
}

var jobsGroupStatusCmd = &cobra.Command{
	Use:   "status <group-id>",
	Short: "Show how many of a group's jobs are in each status, and its cost",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		g, err := client.GetJobGroup(args[0])
		if err != nil {
			return fmt.Errorf("fetching group: %w", err)
		}
		if jsonOutput {
			return printJSON(g)
		}
		renderJobGroup(os.Stdout, g)
		return nil
	},
}

var jobsGroupCancelCmd = &cobra.Command{
	Use:   "cancel <group-id>",
	Short: "Cancel every job of a group that has not ended",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := loadClient()
		if err != nil {
			return err
		}
		g, err := client.CancelJobGroup(args[0])
		if err != nil {
			return fmt.Errorf("cancelling group: %w", err)
		}
		if jsonOutput {
			return printJSON(g)
		}
		renderJobGroup(os.Stdout, g)
		return nil
	},
}

func init() {
	jobsWaitCmd.Flags().String("timeout", "", "Stop waiting after this long (e.g. 30m) and exit 75; the job keeps running")
	jobsDownloadCmd.Flags().StringP("output", "o", ".", "Directory to save the outputs in, at their paths")
	jobsDownloadCmd.Flags().Bool("force", false, "Replace files that already exist")
	jobsDeleteCmd.Flags().BoolP("yes", "y", false, "Delete without asking")

	jobsGroupCmd.AddCommand(jobsGroupStatusCmd)
	jobsGroupCmd.AddCommand(jobsGroupCancelCmd)
	for _, c := range []*cobra.Command{jobsRerunCmd, jobsWaitCmd, jobsArtifactsCmd, jobsDownloadCmd, jobsDeleteCmd, jobsGroupCmd} {
		jobsCmd.AddCommand(c)
	}
}
