package commands

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/runcrate/cli/internal/api"
	"github.com/spf13/cobra"
)

const (
	defaultMaxRuntimeMinutes = 60
	maxMaxRuntimeMinutes     = 24 * 60
)

// parseMaxRuntime reads --max-runtime: a Go duration (90m, 2h, 1h30m) or bare
// minutes (45). Rounds up to whole minutes; allows 1 minute to 24 hours.
func parseMaxRuntime(s string) (int, error) {
	s = strings.TrimSpace(s)
	var minutes int
	if n, err := strconv.Atoi(s); err == nil {
		minutes = n
	} else {
		d, err := time.ParseDuration(s)
		if err != nil {
			return 0, fmt.Errorf("invalid --max-runtime %q: use a duration like 90m, 2h or 1h30m, or a number of minutes", s)
		}
		if d <= 0 {
			minutes = 0
		} else {
			minutes = int(math.Ceil(d.Minutes()))
		}
	}
	if minutes < 1 || minutes > maxMaxRuntimeMinutes {
		return 0, fmt.Errorf("--max-runtime must be between 1m and 24h (got %s)", s)
	}
	return minutes, nil
}

// formatMinutes renders whole minutes as 45m, 1h0m, 1h30m.
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
	gpuType, gpuCount, err := parseGPUFlag(gpuRaw, gpuCountRaw, cmd.Flags().Changed("gpu-count"))
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

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run --gpu <type> [flags] -- <command...>",
		Short: "Run a command on a fresh GPU machine and stream its output",
		Long: `Run a command on its own GPU machine. Runcrate launches the machine, runs the
command with bash in /workspace (the volume's files when --volume is set),
streams its output here, and stops the machine when the command ends.

The exit code matches the job: the command's own code when it fails, 124 when it
hits --max-runtime, 130 when it is cancelled. Ctrl-C detaches and leaves the job
running.`,
		Example: `  runcrate run --gpu A4000 -- nvidia-smi
  runcrate run --gpu H100 --volume <volume-id> --max-runtime 2h -- python train.py --epochs 3
  runcrate run --gpu A4000 --env HF_TOKEN -- 'pip install -r requirements.txt && python eval.py'`,
		RunE: runJob,
	}
	// Flags end at the first argument, so the command's own flags pass through
	// even without --.
	cmd.Flags().SetInterspersed(false)
	cmd.Flags().String("gpu", "", "GPU type, e.g. A4000 or H100:2 (required)")
	cmd.Flags().Int("gpu-count", 1, "Number of GPUs (alternative to colon syntax)")
	cmd.Flags().String("volume", "", "Volume ID to mount as /workspace")
	cmd.Flags().String("max-runtime", formatMinutes(defaultMaxRuntimeMinutes), "Stop the job after this long: 90m, 2h, or minutes (1m to 24h)")
	cmd.Flags().String("template", "", "OS template (default ubuntu-devbox)")
	addJobOnlyFlags(cmd)
	cmd.Flags().String("name", "", "Job name (default job-<id>)")
	cmd.Flags().String("region", "", "Preferred region")
	cmd.Flags().BoolP("detach", "d", false, "Print the job ID and return without following its output")
	return cmd
}

var runCmd = newRunCmd()

func runJob(cmd *cobra.Command, args []string) error {
	req, err := buildJobRequest(cmd, args, os.LookupEnv)
	if err != nil {
		return err
	}
	cmd.SilenceUsage = true

	client, err := loadClient()
	if err != nil {
		return err
	}

	job, err := client.CreateJob(req)
	if err != nil {
		var se *api.APIError
		if errors.As(err, &se) {
			if id, ok := se.Details["job_id"].(string); ok && id != "" {
				return fmt.Errorf("creating job: %w\n  Job %s was created but its machine could not be launched; see: runcrate jobs status %s", err, id, id)
			}
		}
		return fmt.Errorf("creating job: %w", err)
	}

	detach, _ := cmd.Flags().GetBool("detach")
	if detach {
		fmt.Println(job.ID)
		fmt.Fprintf(os.Stderr, "Job %s is %s. Follow it with: runcrate jobs logs -f %s\n", job.Name, job.Status, job.ID)
		return nil
	}

	gpu := fmt.Sprintf("%dx %s", job.GPUCount, job.GPUType)
	if job.CostPerHour > 0 {
		gpu += fmt.Sprintf(" at $%.2f/hr", job.CostPerHour)
	}
	maxRuntime := formatMinutes(job.MaxRuntimeMinutes)
	if !cmd.Flags().Changed("max-runtime") {
		maxRuntime += " (set --max-runtime to change)"
	}
	fmt.Fprintf(os.Stderr, "Job %s (%s) is %s.\n", job.Name, job.ID, job.Status)
	fmt.Fprintf(os.Stderr, "  GPU:          %s\n", gpu)
	fmt.Fprintf(os.Stderr, "  Max runtime:  %s\n", maxRuntime)
	fmt.Fprintln(os.Stderr, "Launching a machine. Output streams below and the machine stops when the command ends. Ctrl-C detaches.")

	return followAndExit(cmd, job.ID)
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
