# jetscale

The JetScale CLI — an operator tool for managing cloud cost optimization from
the terminal.

## Status

**Alpha.** The CLI authenticates, targets multiple environments, manages cloud
accounts, and has a fully generated typed client covering all v2 API endpoints.
Core operator commands (recommendations, analyze, plan) are next. See
[ROADMAP.md](ROADMAP.md) for the build plan.

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

## Quick Start

### Build from Source

```bash
go install github.com/magefile/mage@latest
mage build        # → bin/jetscale
```

### Authenticate

```bash
jetscale auth login
# Email + password prompt, stores token in ~/.config/jetscale/tokens.yaml
```

### Use

```bash
jetscale auth whoami                         # check who you are
jetscale accounts list                       # discover cloud accounts
jetscale accounts use prod-us-east-1         # set active account
jetscale system info -o json                 # backend version info

# Script-friendly
jetscale auth whoami -o json | jq -r '.email'
JETSCALE_TOKEN=eyJ... jetscale auth status   # CI/headless auth
```

### Local Development

```bash
cd ../stack && tilt up                       # start backend
cd ../cli
mage syncSpec                                # fetch OpenAPI spec from localhost
mage generate                                # regenerate typed Go client
mage build                                   # compile
./bin/jetscale --local auth login            # authenticate to local backend
./bin/jetscale --local system info -o json   # test
```

See [docs/local-dev.md](docs/local-dev.md) for the full developer workflow.

## Architecture

```text
┌─────────────────────────────────────────────────────────┐
│  jetscale CLI (this repo)                               │
│                                                         │
│  cmd/jetscale/          ← entrypoint                    │
│  internal/cmd/          ← Cobra command tree             │
│  internal/api/generated/← oapi-codegen typed client      │
│  internal/api/          ← service layer (account tree)   │
│  internal/auth/         ← service layer (token refresh)  │
│  internal/config/       ← ~/.config/jetscale/ handling   │
│  internal/output/       ← table / json / yaml formatters │
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
flows through a **generated Go client** (`internal/api/generated/client.gen.go`,
~14k lines, ~264 types) derived from the Backend's OpenAPI spec.

- **Local dev:** `mage syncSpec` fetches from the running Tilt backend
- **CI:** Backend sends `repository_dispatch`, CLI regenerates and opens a PR

### Commands

```text
jetscale auth login|logout|whoami|status     # authentication
jetscale accounts list|use|current           # cloud account selection
jetscale config show|set|get|instances       # CLI configuration
jetscale system info|diagnostics             # backend health
jetscale version                             # build metadata
```

### Global Flags

```text
-i, --instance <name>   target a named instance (local, staging, production)
    --local              shorthand for -i local
    --api-url <url>      override API URL directly
    --account <name>     cloud account override
-o, --output <format>   table, json, yaml (default: table)
```

### Environment Variables

```text
JETSCALE_TOKEN       bearer token (overrides stored auth)
JETSCALE_API_URL     API URL (overrides instance resolution)
JETSCALE_INSTANCE    instance name (overrides config default)
JETSCALE_ACCOUNT     cloud account (overrides stored selection)
```

## Project Layout

```text
cli/
├── AGENTS.md                  # Repo constitution
├── README.md                  # You are here
├── ROADMAP.md                 # Phased build plan
├── go.mod / go.sum            # Go module
├── magefile.go                # Build system (mage)
├── .goreleaser.yml            # Multi-platform release builds
│
├── cmd/jetscale/
│   └── main.go                # Entrypoint
│
├── internal/
│   ├── cmd/                   # Cobra subcommands
│   │   ├── root.go            # root + global flags
│   │   ├── auth.go            # auth login|logout|whoami|status
│   │   ├── accounts.go        # accounts list|use|current
│   │   ├── config.go          # config show|set|get|instances
│   │   ├── system.go          # system info|diagnostics
│   │   └── version.go         # version
│   ├── api/
│   │   ├── generated/         # oapi-codegen output (do not hand-edit)
│   │   └── client.go          # account tree service layer
│   ├── auth/
│   │   ├── client.go          # auth service layer (sign-in, refresh, whoami)
│   │   └── token.go           # per-instance token storage
│   ├── config/                # config file + instance resolution
│   └── output/                # table / json / yaml formatters
│
├── openapi/
│   └── spec.json              # v2-only OpenAPI spec (synced from backend)
│
├── docs/
│   ├── local-dev.md           # Developer workflow
│   └── adr-001-account-selection.md
│
└── .github/workflows/
    ├── ci.yml                 # Lint → Test → Build
    └── sync-openapi.yml       # Reconcile spec on repository_dispatch
```

## Build Targets

```bash
mage build          # compile bin/jetscale
mage test           # go test -race ./...
mage lint           # golangci-lint
mage syncSpec       # fetch OpenAPI from running backend
mage generate       # oapi-codegen → generated client
mage codegen        # syncSpec + generate
mage smoke          # build + quick sanity checks
mage crossBuild     # all OS/arch pairs
mage clean          # remove bin/
mage all            # lint + test + build
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
