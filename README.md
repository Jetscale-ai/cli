# jetscale

The JetScale CLI — an operator tool for managing cloud cost optimization from
the terminal.

```text
jetscale recommendations list --account prod-us-east-1 --format table
jetscale analyze "Rightsize our RDS fleet in eu-west-1"
jetscale plan show plan-8f3a --terraform
```

## Status

**Pre-alpha.** This repo has been inflated but does not yet produce a working
binary. See [ROADMAP.md](ROADMAP.md) for the build plan.

## What This Is

`jetscale` is a standalone Go binary that provides authenticated, typed access
to the [JetScale Backend API](https://github.com/Jetscale-ai/Backend). It is
designed for:

- **Operators** who prefer terminals over dashboards
- **CI/CD pipelines** that need to trigger or query JetScale programmatically
- **IaC workflows** where JetScale recommendations feed into Terraform/Pulumi
  plans

The CLI is a **first-class product surface**, not a thin wrapper. It has its own
release cadence, install path, and compatibility contract against the Backend
API.

## Architecture

```text
┌─────────────────────────────────────────────────────────┐
│  jetscale CLI (this repo)                               │
│                                                         │
│  cmd/jetscale/          ← entrypoint + root command     │
│  internal/cmd/          ← subcommand implementations    │
│  internal/api/generated/← OpenAPI-generated Go client   │
│  internal/api/client.go ← auth, retries, base URL       │
│  internal/config/       ← ~/.config/jetscale/ handling  │
│  internal/output/       ← table / json / yaml formatters│
└────────────────┬────────────────────────────────────────┘
                 │  HTTPS + JWT
                 ▼
┌─────────────────────────────────────────────────────────┐
│  JetScale Backend (Jetscale-ai/Backend)                 │
│  FastAPI · OpenAPI 3.0 · /api/v2/*                      │
└─────────────────────────────────────────────────────────┘
```

### Contract Boundary

The CLI never hard-codes endpoint paths or request shapes. All API interaction
flows through a **generated Go client** derived from the Backend's OpenAPI spec.
When the Backend API changes:

1. Backend CI exports a pinned OpenAPI spec artifact
2. Backend sends `repository_dispatch` to this repo
3. CLI CI regenerates `internal/api/generated/`, runs tests, opens a PR
4. If only generated code changed and checks pass, the PR auto-merges

This gives auditable diffs, independent releases, and no hidden cross-repo
mutation.

## Project Layout

```text
cli/
├── AGENTS.md                  # Repo constitution (federated from Governance)
├── README.md                  # You are here
├── ROADMAP.md                 # Phased build plan
├── go.mod / go.sum            # Go module
├── Makefile                   # Dev shortcuts (build, test, lint, generate)
├── .goreleaser.yml            # Multi-platform release builds
│
├── cmd/jetscale/
│   └── main.go                # Entrypoint
│
├── internal/
│   ├── cmd/                   # Subcommands (auth, analyze, recommendations, …)
│   ├── api/
│   │   ├── client.go          # Authenticated HTTP client
│   │   └── generated/         # OpenAPI-generated code (do not hand-edit)
│   ├── config/                # Config file + keychain helpers
│   └── output/                # Formatters (table, json, yaml)
│
├── openapi/
│   └── spec.json              # Pinned OpenAPI spec (fetched from Backend)
│
├── .agents/
│   └── AGENTS.md              # Operational overlay (local agent rules)
│
└── .github/
    └── workflows/
        ├── ci.yml             # Lint → Test → Build on push/PR
        └── sync-openapi.yml   # Reconcile spec on repository_dispatch
```

## Quick Start (once binary exists)

### Install

```bash
# Homebrew (macOS / Linux)
brew install jetscale-ai/tap/jetscale

# Go install
go install github.com/Jetscale-ai/cli/cmd/jetscale@latest

# Binary download
curl -sSfL https://github.com/Jetscale-ai/cli/releases/latest/download/jetscale_$(uname -s)_$(uname -m).tar.gz | tar xz
```

### Authenticate

```bash
jetscale auth login
# Opens browser for OAuth flow, stores token in OS keychain
```

### Use

```bash
jetscale recommendations list
jetscale analyze "What EC2 instances are oversized?"
jetscale plan show plan-abc123
jetscale config set default-account prod-us-east-1
```

## Development

### Prerequisites

- Go 1.22+
- [golangci-lint](https://golangci-lint.run/)
- [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) (for client
  regeneration)

### Build & Test

```bash
make build       # → bin/jetscale
make test        # go test ./...
make lint        # golangci-lint run
make generate    # regenerate OpenAPI client from openapi/spec.json
```

## Governance

This repo is governed by the
[JetScale Governance Constitution](https://github.com/Jetscale-ai/Governance/blob/main/AGENTS.md).
Local rules are in [AGENTS.md](AGENTS.md) and
[.agents/AGENTS.md](.agents/AGENTS.md).

Stack: **Go + Modules** per
[`go-mod.md`](https://github.com/Jetscale-ai/Governance/blob/main/.agents/codex/stacks/go-mod.md).

## License

Proprietary. Copyright © 2026 JetScale AI Inc.
