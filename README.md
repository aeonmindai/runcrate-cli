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
