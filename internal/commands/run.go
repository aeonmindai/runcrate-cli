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
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

const (
	defaultMaxRuntimeMinutes = 60
	// maxMaxRuntimeMinutes is the API's limit: 7 days per attempt.
	maxMaxRuntimeMinutes = 7 * 24 * 60
	// maxCapacityWaitMinutes is how long a job may wait for a GPU at most.
	maxCapacityWaitMinutes = 24 * 60
	// maxJobGPUs is how many GPUs --gpu may list, and the most per machine.
	maxJobGPUs = 8
)

// parseFlagMinutes reads a duration flag: a Go duration (90m, 2h, 1h30m) or
// bare minutes (45), rounded up to whole minutes, within [lo, hi].
func parseFlagMinutes(flag, s string, lo, hi int) (int, error) {
	s = strings.TrimSpace(s)
	var minutes int
	if n, err := strconv.Atoi(s); err == nil {
		minutes = n
	} else {
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid --%s %q: use a duration like 90m, 2h or 1h30m, or a number of minutes", flag, s)
		}
		if d <= 0 {
			minutes = 0
		} else {
			minutes = int(math.Ceil(d.Minutes()))
		}
	}
	if minutes < lo || minutes > hi {
		return 0, fmt.Errorf("--%s must be between %s and %s (got %s)", flag, formatMinutes(lo), formatMinutes(hi), s)
	}
	return minutes, nil
}

// parseMaxRuntime reads --max-runtime: 1 minute to 168h (7 days).
func parseMaxRuntime(s string) (int, error) {
	return parseFlagMinutes("max-runtime", s, 1, maxMaxRuntimeMinutes)
}

// parseJobGPU reads a job's --gpu: one GPU (A4000, H100:2) or GPUs to try in
// order, comma separated (H100,A100-80GB:2), where --gpu-count applies to the
// entries without a count. The result is the gpu_type value (a string or a
// list) and gpu_count (0 to leave it out).
func parseJobGPU(raw string, count int, countChanged bool) (any, int, error) {
	if !strings.Contains(raw, ",") {
		gpuType, gpuCount, err := parseGPUFlag(strings.TrimSpace(raw), count, countChanged)
		return gpuType, gpuCount, err
	}
	var entries []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		name, n, hasCount := strings.Cut(entry, ":")
		if name == "" {
			return nil, 0, fmt.Errorf("invalid --gpu %q: list GPUs to try in order, e.g. H100,A100-80GB:2", raw)
		}
		if hasCount {
			if v, err := strconv.Atoi(n); err != nil || v < 1 || v > maxJobGPUs {
				return nil, 0, fmt.Errorf("invalid GPU count in %q: expected 1 to %d after the colon", entry, maxJobGPUs)
			}
		}
		entries = append(entries, entry)
	}
	if len(entries) > maxJobGPUs {
		return nil, 0, fmt.Errorf("--gpu lists at most %d GPUs", maxJobGPUs)
	}
	if !countChanged {
		count = 0
	}
	return entries, count, nil
}

// shellSafe matches arguments that mean the same thing quoted or not.
var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes an argument for bash when it needs it.
func shellQuote(arg string) string {
	if arg == "" {
		return "''"
	}
	if shellSafe.MatchString(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'"'"'`) + "'"
}

// joinCommand turns the arguments after -- into one bash command. A single
// argument is used as is, so `-- 'make && ./train'` keeps its shell syntax;
// several are quoted where needed so `-- python -c 'print(1)'` runs as typed.
func joinCommand(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

var envKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseEnvFlags reads repeated --env KEY=VALUE. A bare KEY takes its value
// from the local environment, so secrets stay out of shell history.
func parseEnvFlags(vals []string, lookup func(string) (string, bool)) (map[string]string, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	env := make(map[string]string, len(vals))
	for _, kv := range vals {
		key, value, hasValue := strings.Cut(kv, "=")
		if !envKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid --env %q: the name must be letters, digits and underscores, not starting with a digit", kv)
		}
		if !hasValue {
			v, ok := lookup(key)
			if !ok {
				return nil, fmt.Errorf("--env %s: %s is not set in your environment (use --env %s=value)", key, key, key)
			}
			value = v
		}
		env[key] = value
	}
	return env, nil
}

// addJobOnlyFlags registers the flags that only a job takes, shared by run and
// schedule create.
func addJobOnlyFlags(cmd *cobra.Command) {
	cmd.Flags().String("image", "", "Container image to run the command in")
	cmd.Flags().StringArray("env", nil, "Environment variable KEY=VALUE, or KEY to copy it from your shell (repeatable)")
}

// buildJobRequest turns the job flags (gpu, gpu-count, max-runtime, volume,
// image, env, template, name, region) and the command into the create body.
func buildJobRequest(cmd *cobra.Command, args []string, lookupEnv func(string) (string, bool)) (api.CreateJobRequest, error) {
	var req api.CreateJobRequest

	if len(args) == 0 {
		return req, fmt.Errorf("give the command to run after --, e.g. runcrate run --gpu A4000 -- python train.py")
	}

	gpuRaw, _ := cmd.Flags().GetString("gpu")
	gpuCountRaw, _ := cmd.Flags().GetInt("gpu-count")
	gpuType, gpuCount, err := parseJobGPU(gpuRaw, gpuCountRaw, cmd.Flags().Changed("gpu-count"))
	if err != nil {
		return req, err
	}
	if gpuType == "" {
		return req, fmt.Errorf("--gpu is required (see GPUs with: runcrate instances types)")
	}

	minutes := defaultMaxRuntimeMinutes
	if cmd.Flags().Changed("max-runtime") {
		raw, _ := cmd.Flags().GetString("max-runtime")
		if minutes, err = parseMaxRuntime(raw); err != nil {
			return req, err
		}
	}

	envVals, _ := cmd.Flags().GetStringArray("env")
	env, err := parseEnvFlags(envVals, lookupEnv)
	if err != nil {
		return req, err
	}

	req.Command = joinCommand(args)
	req.GPUType = gpuType
	req.GPUCount = gpuCount
	req.MaxRuntimeMinutes = minutes
	req.Env = env
	req.StorageID, _ = cmd.Flags().GetString("volume")
	req.Template, _ = cmd.Flags().GetString("template")
	req.Image, _ = cmd.Flags().GetString("image")
	req.Name, _ = cmd.Flags().GetString("name")
	req.Region, _ = cmd.Flags().GetString("region")
	return req, nil
}

var (
	tagKeyPattern = regexp.MustCompile(`^[a-z0-9_.:-]{1,40}$`)
	retryOnValues = map[string]bool{"machine_failure": true, "nonzero_exit": true}
	webhookEvents = map[string]bool{"started": true, "finished": true}
)

// parseTagFlags reads repeated --tag key=value.
func parseTagFlags(vals []string) (map[string]string, error) {
	if len(vals) == 0 {
		return nil, nil
	}
	tags := make(map[string]string, len(vals))
	for _, kv := range vals {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !tagKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("invalid --tag %q: use key=value, the key 1 to 40 lower case letters, digits or _ . : -", kv)
		}
		tags[key] = value
	}
	return tags, nil
}

// splitList reads a comma separated flag value, dropping empty items.
func splitList(raw string) []string {
	var items []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// addJobRunFlags registers the job fields only runcrate run sets (a schedule's
// job spec does not take them).
func addJobRunFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("capacity-wait", "", "How long to wait for a free GPU before failing: 0 to 24h (default 30m)")
	f.Int("retries", 0, "Retry a failed attempt up to this many times on a new machine (0 to 10); machine failures are retried")
	f.String("retry-on", "", "Also retry when the command fails: nonzero_exit (comma separated with machine_failure)")
	f.String("retry-exit-codes", "", "With --retry-on nonzero_exit, only these exit codes, e.g. 137,143")
	f.Int("count", 0, "Run this many copies of the job as a group (1 to 100); each gets RUNCRATE_JOB_INDEX")
	f.String("params", "", "JSON file (or - for stdin) with a list of env objects: one job per entry, as a group")
	f.Int("max-parallel", 0, "With --count or --params, run at most this many at once")
	f.StringArray("tag", nil, "Tag key=value, to find jobs with jobs list --tag (repeatable)")
	f.String("webhook-url", "", "https URL Runcrate POSTs to (signed) when the job finishes")
	f.String("webhook-events", "", "When to call --webhook-url: finished (default), started, or both comma separated")
	f.String("code", "", "Upload this directory (or .tar.gz); the command runs in it, at /workspace/code")
	f.String("git", "", "Clone this https git repository; the command runs in it, at /workspace/code")
	f.String("git-ref", "", "Branch, tag or commit of --git (default: its default branch)")
	f.String("git-token-env", "", "Env variable or secret holding a token for a private --git repository")
	f.String("registry-username-env", "", "Env variable or secret holding the registry username for a private --image")
	f.String("registry-password-env", "", "Env variable or secret holding the registry password or token for a private --image")
	f.Bool("allow-cpu-container", false, "Run --image even on a machine without GPU access for containers")
	f.String("idempotency-key", "", "Submit at most once per key for 24 hours (default: a new key each run)")
}

// applyJobRunFlags sets the run-only fields on req. --code is uploaded later,
// once there is a client. readFile reads --params (- is stdin).
func applyJobRunFlags(cmd *cobra.Command, req *api.CreateJobRequest, readFile func(string) ([]byte, error)) error {
	f := cmd.Flags()
	if f.Changed("capacity-wait") {
		raw, _ := f.GetString("capacity-wait")
		minutes, err := parseFlagMinutes("capacity-wait", raw, 0, maxCapacityWaitMinutes)
		if err != nil {
			return err
		}
		req.CapacityWaitMinutes = &minutes
	}

	req.MaxRetries, _ = f.GetInt("retries")
	retryOn, _ := f.GetString("retry-on")
	for _, v := range splitList(retryOn) {
		if !retryOnValues[v] {
			return fmt.Errorf("invalid --retry-on %q: use machine_failure, nonzero_exit or both", v)
		}
		req.RetryOn = append(req.RetryOn, v)
	}
	codes, _ := f.GetString("retry-exit-codes")
	for _, v := range splitList(codes) {
		code, err := strconv.Atoi(v)
		if err != nil || code < 1 || code > 255 {
			return fmt.Errorf("invalid --retry-exit-codes %q: exit codes are 1 to 255", v)
		}
		req.RetryExitCodes = append(req.RetryExitCodes, code)
	}
	if req.MaxRetries == 0 && (len(req.RetryOn) > 0 || len(req.RetryExitCodes) > 0) {
		return errors.New("--retry-on and --retry-exit-codes need --retries 1 or more")
	}

	req.Count, _ = f.GetInt("count")
	if path, _ := f.GetString("params"); path != "" {
		raw, err := readFile(path)
		if err != nil {
			return fmt.Errorf("reading --params: %w", err)
		}
		if err := json.Unmarshal(raw, &req.Params); err != nil {
			return fmt.Errorf("--params must be a JSON list of objects of strings, e.g. [{\"LR\": \"0.1\"}]: %w", err)
		}
		if len(req.Params) == 0 {
			return errors.New("--params lists no jobs")
		}
	}
	req.MaxParallel, _ = f.GetInt("max-parallel")
	if req.MaxParallel > 0 && req.Count == 0 && req.Params == nil {
		return errors.New("--max-parallel needs --count or --params")
	}

	tagVals, _ := f.GetStringArray("tag")
	tags, err := parseTagFlags(tagVals)
	if err != nil {
		return err
	}
	req.Tags = tags

	req.WebhookURL, _ = f.GetString("webhook-url")
	events, _ := f.GetString("webhook-events")
	for _, v := range splitList(events) {
		if !webhookEvents[v] {
			return fmt.Errorf("invalid --webhook-events %q: use started, finished or both", v)
		}
		req.WebhookEvents = append(req.WebhookEvents, v)
	}
	if len(req.WebhookEvents) > 0 && req.WebhookURL == "" {
		return errors.New("--webhook-events needs --webhook-url")
	}

	codeDir, _ := f.GetString("code")
	gitURL, _ := f.GetString("git")
	gitRef, _ := f.GetString("git-ref")
	gitTokenEnv, _ := f.GetString("git-token-env")
	switch {
	case codeDir != "" && gitURL != "":
		return errors.New("use --code or --git, not both")
	case gitURL != "":
		req.Code = &api.JobCode{Git: &api.JobGit{URL: gitURL, Ref: gitRef, TokenEnv: gitTokenEnv}}
	case gitRef != "" || gitTokenEnv != "":
		return errors.New("--git-ref and --git-token-env need --git")
	}

	user, _ := f.GetString("registry-username-env")
	pass, _ := f.GetString("registry-password-env")
	if user != "" || pass != "" {
		if user == "" || pass == "" {
			return errors.New("set both --registry-username-env and --registry-password-env")
		}
		req.ImageRegistryAuth = &api.ImageRegistryAuth{UsernameEnv: user, PasswordEnv: pass}
	}
	req.AllowCPUContainer, _ = f.GetBool("allow-cpu-container")
	return nil
}

// skippedCodeDirs are left out of an uploaded directory.
var skippedCodeDirs = map[string]bool{".git": true, "__pycache__": true, "node_modules": true}

// codeArchive is the tar.gz to upload for --code: a .tar.gz or .tgz file as
// it is, or a directory packed here (regular files only, without .git,
// __pycache__ and node_modules), byte for byte the same for the same files.
func codeArchive(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if !strings.HasSuffix(path, ".tar.gz") && !strings.HasSuffix(path, ".tgz") {
			return nil, fmt.Errorf("--code %s: give a directory or a .tar.gz file", path)
		}
		return os.ReadFile(path)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != path && skippedCodeDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(path, p)
		if err != nil {
			return err
		}
		// Only the name, mode and contents go in, so the same files make the
		// same bytes (and the same sha256, which the upload dedupes on).
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     filepath.ToSlash(rel),
			Mode:     int64(info.Mode().Perm()),
			Size:     info.Size(),
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		file, err := os.Open(p)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("packing --code %s: %w", path, err)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// uploadCode sends --code and returns the job's code field. The same archive
// again answers the same upload, so nothing is sent twice.
func uploadCode(client *api.Client, path string, stderr io.Writer) (*api.JobCode, error) {
	archive, err := codeArchive(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(archive)
	upload, err := client.CreateUpload(int64(len(archive)), hex.EncodeToString(sum[:]))
	if err != nil {
		return nil, fmt.Errorf("preparing the code upload: %w", err)
	}
	if upload.URL != "" {
		fmt.Fprintf(stderr, "Uploading %s (%s)...\n", path, formatBytes(int64(len(archive))))
		if err := api.PutUpload(context.Background(), upload.URL, archive); err != nil {
			return nil, fmt.Errorf("uploading the code: %w", err)
		}
	}
	return &api.JobCode{UploadID: upload.UploadID}, nil
}

// formatBytes renders a size as 512 B, 3.4 KB, 12.0 MB, 1.20 GB.
func formatBytes(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run --gpu <type> [flags] -- <command...>",
		Short: "Run a command on a fresh GPU machine and stream its output",
		Long: `Run a command on its own GPU machine. Runcrate launches the machine, runs the
command with bash in /workspace (the volume's files when --volume is set),
streams its output here, and stops the machine when the command ends.

The exit code matches the job: the command's own code when it fails, 124 when it
hits --max-runtime, 130 when it is cancelled. Ctrl-C detaches and leaves the job
running. With --count or --params it starts a group of jobs, prints their IDs
and returns.`,
		Example: `  runcrate run --gpu A4000 -- nvidia-smi
  runcrate run --gpu H100 --volume <volume-id> --max-runtime 2h -- python train.py --epochs 3
  runcrate run --gpu A4000 --env HF_TOKEN -- 'pip install -r requirements.txt && python eval.py'
  runcrate run --gpu H100,A100-80GB --code . --retries 2 --tag project=vision -- python train.py
  runcrate run --gpu L40S --params sweep.json --max-parallel 4 -- 'python train.py --lr $LR'`,
		RunE: runJob,
	}
	// Flags end at the first argument, so the command's own flags pass through
	// even without --.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().String("gpu", "", "GPU type, e.g. A4000 or H100:2, or GPUs to try in order: H100,A100-80GB:2 (required)")
	cmd.Flags().Int("gpu-count", 1, "Number of GPUs (alternative to colon syntax)")
	cmd.Flags().String("volume", "", "Volume ID to mount as /workspace")
	cmd.Flags().String("max-runtime", formatMinutes(defaultMaxRuntimeMinutes), "Stop each attempt after this long: 90m, 2h, or minutes (1m to 168h)")
	cmd.Flags().String("template", "", "OS template (default ubuntu-devbox)")
	addJobOnlyFlags(cmd)
	cmd.Flags().String("name", "", "Job name (default job-<id>)")
	cmd.Flags().String("region", "", "Preferred region")
	addJobRunFlags(cmd)
	cmd.Flags().BoolP("detach", "d", false, "Print the job ID and return without following its output")
	return cmd
}

var runCmd = newRunCmd()

func runJob(cmd *cobra.Command, args []string) error {
	req, err := buildJobRequest(cmd, args, os.LookupEnv)
	if err != nil {
		return err
	}
	if err := applyJobRunFlags(cmd, &req, readFileOrStdin); err != nil {
		return err
	}
	cmd.SilenceUsage = true

	client, err := loadClient()
	if err != nil {
		return err
	}
	if dir, _ := cmd.Flags().GetString("code"); dir != "" {
		if req.Code, err = uploadCode(client, dir, os.Stderr); err != nil {
			return err
		}
	}

	key, _ := cmd.Flags().GetString("idempotency-key")
	sub, err := client.CreateJob(req, key)
	if err != nil {
		return submitError("creating job", err)
	}
	detach, _ := cmd.Flags().GetBool("detach")
	return startedJobs(cmd, sub, detach, !cmd.Flags().Changed("max-runtime"))
}

// readFileOrStdin reads a file, or stdin for "-".
func readFileOrStdin(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

// submitError names the job that was created when its machine could not be
// launched, and, when no answer came, the key that finds the job if it was made.
func submitError(doing string, err error) error {
	var ne *api.SubmitNetworkError
	if errors.As(err, &ne) {
		return fmt.Errorf("%s: %w\n  The job may have been created. Re-run the same command with --idempotency-key %s: "+
			"it answers that job instead of starting another (for 24 hours)", doing, err, ne.Key)
	}
	var se *api.APIError
	if errors.As(err, &se) {
		if id, ok := se.Details["job_id"].(string); ok && id != "" {
			return fmt.Errorf("%s: %w\n  Job %s was created but its machine could not be launched; see: runcrate jobs status %s", doing, err, id, id)
		}
	}
	return fmt.Errorf("%s: %w", doing, err)
}

// startedJobs reports what a submit or rerun started: a group's job IDs, or
// one job, followed until it ends unless detach.
func startedJobs(cmd *cobra.Command, sub *api.JobSubmission, detach, defaultRuntime bool) error {
	if g := sub.Group; g != nil {
		if jsonOutput {
			return printJSON(g)
		}
		for _, j := range g.Jobs {
			fmt.Println(j.ID)
		}
		fmt.Fprintf(os.Stderr, "Started %d jobs in group %s, at most %d at once.\n", g.Count, g.ID, g.MaxParallel)
		fmt.Fprintf(os.Stderr, "  Progress:  runcrate jobs group status %s\n", g.ID)
		fmt.Fprintf(os.Stderr, "  Jobs:      runcrate jobs list --group %s\n", g.ID)
		fmt.Fprintf(os.Stderr, "  Cancel:    runcrate jobs group cancel %s\n", g.ID)
		printWebhookSecret(g.WebhookSecret)
		return nil
	}

	job := sub.Job
	printWebhookSecret(job.WebhookSecret)
	if detach {
		if jsonOutput {
			return printJSON(job)
		}
		fmt.Println(job.ID)
		fmt.Fprintf(os.Stderr, "Job %s is %s. Follow it with: runcrate jobs logs -f %s\n", job.Name, job.Status, job.ID)
		return nil
	}

	gpu := fmt.Sprintf("%dx %s", job.GPUCount, job.GPUType)
	if job.CostPerHour > 0 {
		gpu += fmt.Sprintf(" at $%.2f/hr", job.CostPerHour)
	}
	maxRuntime := formatMinutes(job.MaxRuntimeMinutes)
	if defaultRuntime {
		maxRuntime += " (set --max-runtime to change)"
	}
	fmt.Fprintf(os.Stderr, "Job %s (%s) is %s.\n", job.Name, job.ID, job.Status)
	fmt.Fprintf(os.Stderr, "  GPU:          %s\n", gpu)
	fmt.Fprintf(os.Stderr, "  Max runtime:  %s\n", maxRuntime)
	fmt.Fprintln(os.Stderr, "Launching a machine. Output streams below and the machine stops when the command ends. Ctrl-C detaches.")

	return followAndExit(cmd, job.ID)
}

// printWebhookSecret shows the callback signing secret, which is only ever in
// the answer that created the job.
func printWebhookSecret(secret string) {
	if secret != "" {
		fmt.Fprintf(os.Stderr, "Webhook signing secret (shown once): %s\n", secret)
	}
}

// followAndExit streams a job until it ends or the user presses Ctrl-C, then
// exits with the job's exit code.
func followAndExit(cmd *cobra.Command, id string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	code, err := streamJob(ctx, newJobFollower(), id)
	if err != nil {
		return err
	}
	if code != 0 {
		stop()
		os.Exit(code)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(runCmd)
}
