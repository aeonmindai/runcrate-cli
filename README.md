# Runcrate CLI

The official command-line interface for [Runcrate](https://runcrate.ai) — deploy
and manage GPU instances, run remote commands over keyless SSH, copy files, and
manage workspaces, storage, templates, and billing from your terminal.

## Install

**macOS / Linux:**

```bash
curl -sSL https://runcrate.ai/install.sh | sh
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

Point the CLI at a different API origin with `runcrate config set url <url>` or the
`RUNCRATE_API_URL` env var (defaults to `https://runcrate.ai`).

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
read these releases, so no separate publish step is needed.

```bash
git tag v0.1.0
git push origin v0.1.0
```

> This repo is consumed by the Runcrate monorepo as a git submodule
> (`packages/cli`). Pushing to `main` notifies the monorepo to bump the
> submodule pointer.

## License

MIT
