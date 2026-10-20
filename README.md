# Runcrate CLI

The official command-line interface for [Runcrate](https://runcrate.ai) — deploy
and manage GPU instances, run remote commands over keyless SSH, copy files, and
manage workspaces, storage, templates, and billing from your terminal.

## Install

**macOS / Linux:**

```bash
curl -sSL https://runcrate.ai/install.sh | sh
```

**Homebrew (macOS / Linux):**

```bash
brew install aeonmindai/tap/runcrate
```

**Windows (PowerShell):**

```powershell
irm https://runcrate.ai/install.ps1 | iex
```

Binaries are published to this repo's [GitHub Releases](https://github.com/aeonmindai/runcrate-cli/releases)
for linux/macOS (amd64 + arm64) and windows (amd64). `rc` is installed as a
shortcut alias for `runcrate`.

## Getting started

```bash
runcrate login                 # browser OAuth
runcrate instances             # list instances
runcrate ssh my-gpu            # interactive shell (ephemeral SSH cert)
runcrate ssh my-gpu -- nvidia-smi
runcrate cp ./train.py my-gpu:/root/workspace/
```

For unattended or agent-run work, give the instance an auto stop so it does not keep
billing after the job ends. Durations are whole minutes written like `30m`, `4h` or
`1h30m`. `--max-runtime` accepts 5m to 168h, `--idle-stop` accepts 5m to 24h (idle
means GPU use under 5% and nobody connected over SSH, the web terminal or the editor; GPU instances only). Auto stop terminates the instance.

```bash
runcrate instances create --name train --gpu H100 --max-runtime 4h --idle-stop 30m
runcrate instances info train   # Max runtime, Idle stop, Auto stop at, Stop reason

# Extend it before a long job (total time from start), or turn a limit off
runcrate instances auto-stop train --max-runtime 8h --idle-stop off
```

Point the CLI at a different API origin with `runcrate config set url <url>` or the
`RUNCRATE_API_URL` env var (defaults to `https://runcrate.ai`).

## Jobs

A job runs one command on its own GPU machine, streams its output, and stops the
machine (and its billing) when the command ends.

```bash
runcrate run --gpu A4000 -- nvidia-smi
runcrate run --gpu H100 --volume <volume-id> --max-runtime 2h -- python train.py --epochs 3
runcrate run --gpu A4000 --env HF_TOKEN -d -- 'pip install -r requirements.txt && python eval.py'
runcrate jobs list [--status running,queued] [--limit 20] [-q] [--json]
runcrate jobs status <id>      # exit code, error, cost, instance, env keys
runcrate jobs logs <id> [-f]   # print output; -f follows until the job ends
runcrate jobs cancel <id>
```

- The command runs with bash in `/workspace`, which holds the volume's files when
  `--volume` is set; files written there are copied back when the machine stops.
- `--max-runtime` takes `90m`, `2h`, `1h30m` or minutes, from 1m to 24h (default 1h).
- `--env KEY=VALUE` is repeatable; `--env KEY` copies the value from your shell.
  Values are never stored or shown.
- `runcrate run` and `runcrate jobs logs -f` exit with the job's code: the command's
  own code, 124 on timeout, 130 when cancelled. Ctrl-C detaches and leaves the job
  running; `-d` / `--detach` prints the job ID and returns.

## Schedules

A schedule starts an instance or a job later: once with `--at`, or on a recurring
5-field `--cron`. A command after `--` makes it a job (same flags as `runcrate run`);
without one it starts an instance (same flags as `runcrate instances create`, so
`--name` is required). `--max-runtime` is required for both, so nothing a schedule
starts keeps running unattended.

```bash
runcrate schedule create --cron '0 2 * * *' --tz Europe/London --name nightly-train \
  --gpu H100 --volume <volume-id> --max-runtime 2h -- python train.py
runcrate schedule create --at '2026-10-21 09:00' --name morning-box \
  --gpu A4000 --max-runtime 8h --idle-stop 30m
runcrate schedule list [-q] [--json]   # when, next run (your local time), last result
runcrate schedule get <id>             # details, last run, last error
runcrate schedule pause <id>           # resume <id> turns it back on
runcrate schedule run-now <id>         # start it now; the next run stays as it is
runcrate schedule delete <id> [--yes]  # what it already started keeps running
```

- `--at '2026-10-21 02:00'` is the wall time in `--tz`; a time with an offset
  (`2026-10-21T02:00:00+01:00`) is used as is. `--tz` takes an IANA name and
  defaults to this machine's timezone, for `--cron` too.
- Across daylight saving: a time the clocks skip moves forward by the gap (02:30
  becomes 03:30), and a time that happens twice runs the first time.
- Recurring runs are at least 15 minutes apart. `--overlap skip` (the default)
  skips a run while the previous one is still going; `--overlap allow` starts it.
- Runs start within about 2 minutes of their time, as you, in the current
  environment. While the environment's monthly budget is reached, runs are skipped.
- A schedule created with an API key or an agent sign-in launches with it; when
  that key is revoked or the sign-in ends, the schedule pauses (last result
  `paused: key revoked`).

## Updating

```bash
runcrate update                # self-update to the latest release
# Homebrew installs: brew upgrade runcrate
```

## Development

```bash
make build                     # build ./bin/runcrate
make test                      # go test ./...
make build-all                 # cross-compile all platforms into ./bin
```

## Releasing

Releases are tag-driven. Pushing a `vX.Y.Z` tag runs `.github/workflows/release.yml`,
which tests, cross-compiles the five targets, generates `checksums.txt`, and
publishes a GitHub Release. The public installer scripts and `runcrate update`
read these releases, so no separate publish step is needed. The same workflow asks
[aeonmindai/homebrew-tap](https://github.com/aeonmindai/homebrew-tap) to regenerate its
formula from the new checksums (needs the `HOMEBREW_TAP_TOKEN` secret; without it the
tap's own six-hourly sync picks the release up).

```bash
git tag v0.1.0
git push origin v0.1.0
```

> This repo is consumed by the Runcrate monorepo as a git submodule
> (`packages/cli`). Pushing to `main` notifies the monorepo to bump the
> submodule pointer.

## License

MIT
