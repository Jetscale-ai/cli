# JetScale CLI — Roadmap

**Last updated:** 2026-03-11 **Status:** Active **Tracking:**
[GitHub Projects Board](https://github.com/orgs/Jetscale-ai/projects/) (TBD)

---

## Context

The JetScale CLI exists to give operators and CI/CD pipelines a first-class
terminal interface to the JetScale cost optimization platform. It consumes the
Backend's OpenAPI spec as its contract and releases independently.

This roadmap is structured in phases. Each phase is shippable: it delivers a
usable increment, not just scaffolding.

### Decision Records

- [ADR-001: OpenAPI-to-CLI Generation Strategy](https://github.com/Jetscale-ai/Backend/pull/244)
  (Backend PR #244) — Hybrid: OpenAPI-generated Go client + hand-crafted Cobra
  command tree for UX.
- [ADR-001: Account Selection](docs/adr-001-account-selection.md) — Cloud
  account resolution order (flag → env → config → auto-select).

---

## Phase 0 — Repo Inflation ✓

**Goal:** Establish the repo as a real project with governance, structure, and
CI.

| Deliverable                         | Status |
| ----------------------------------- | ------ |
| `AGENTS.md` (constitution)          | Done   |
| `README.md`                         | Done   |
| `ROADMAP.md` (this file)            | Done   |
| `.agents/AGENTS.md` (ops overlay)   | Done   |
| Go module (`go.mod`)                | Done   |
| Entrypoint (`cmd/jetscale/main.go`) | Done   |
| `magefile.go`                       | Done   |
| CI workflow (`.github/workflows/`)  | Done   |
| `.goreleaser.yml`                   | Done   |
| Pre-commit hooks                    | Done   |
| Conventional Commits config         | Done   |

---

## Phase 1 — OpenAPI Contract + Generated Client ✓

**Goal:** Establish the Backend → CLI contract pipeline.

| Deliverable                                                    | Status |
| -------------------------------------------------------------- | ------ |
| `mage syncSpec` (fetch from running backend)                   | Done   |
| OpenAPI 3.1 → 3.0 downgrade for oapi-codegen                   | Done   |
| v2-only path filtering + orphaned schema pruning               | Done   |
| Duplicate operationId dedup                                    | Done   |
| `mage generate` (oapi-codegen → typed Go client)               | Done   |
| `mage codegen` (syncSpec + generate pipeline)                  | Done   |
| `internal/api/generated/client.gen.go` (14k lines, ~264 types) | Done   |
| `sync-openapi.yml` workflow (repository_dispatch)              | Done   |
| Backend `TODO_OAPI_CODEGEN.md` with local dev path             | Done   |

**What works now:**

```bash
mage syncSpec       # auto-detects localhost:8000/:8010, fetches, filters, patches
mage generate       # oapi-codegen → internal/api/generated/client.gen.go
mage codegen        # both in one shot
```

### Key Decisions

- **Generator:** [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen)
  v2.6.0 (Go-native, no JRE, idiomatic Go with interfaces)
- **Spec version:** v2-only (`/api/v2` paths). v1 endpoints are legacy and not
  exposed.
- **Local dev:** `mage syncSpec` fetches from the live Tilt backend — no CI
  artifacts needed for development.
- **CI dispatch:** Backend sends `repository_dispatch` with
  `event_type: openapi-spec-updated`. CLI workflow fetches, regenerates, diffs,
  and opens a PR.

---

## Phase 2 — Auth + Config + Instance Management ✓

**Goal:** A CLI that can authenticate, target multiple environments, and manage
cloud accounts.

| Deliverable                                                     | Status |
| --------------------------------------------------------------- | ------ |
| `jetscale auth login` (email+password, `--token`)               | Done   |
| `jetscale auth whoami`                                          | Done   |
| `jetscale auth status` (script-friendly, exit code)             | Done   |
| `jetscale auth logout`                                          | Done   |
| Per-instance token storage (`~/.config/jetscale/tokens.yaml`)   | Done   |
| Transparent token auto-refresh                                  | Done   |
| `JETSCALE_TOKEN` env var override                               | Done   |
| `jetscale config show` / `set` / `get` / `instances`            | Done   |
| `~/.config/jetscale/config.yaml` management                     | Done   |
| Built-in instances (production, staging, local)                 | Done   |
| `--local` / `-i` / `--api-url` / `JETSCALE_INSTANCE` resolution | Done   |
| `jetscale accounts list` (hierarchical discovery)               | Done   |
| `jetscale accounts use <name>` (sticky active account)          | Done   |
| `jetscale accounts current` (with auto-select)                  | Done   |
| `--account` / `JETSCALE_ACCOUNT` override                       | Done   |
| `jetscale version`                                              | Done   |
| `jetscale system info` / `diagnostics`                          | Done   |
| Output formatters: `-o table\|json\|yaml`                       | Done   |

**What works now:**

```bash
jetscale --local auth login                 # interactive email+password
jetscale auth login --token eyJ...          # headless/CI
jetscale --local auth whoami -o json        # structured output for scripts
jetscale accounts list -o table             # tabular cloud account listing
jetscale accounts use prod-us-east-1        # sticky selection
jetscale --local system info -o json | jq '.data.build.version'
JETSCALE_TOKEN=eyJ... jetscale auth status  # env var auth for CI
```

### Architecture Notes

- **Zero hand-written HTTP.** All API calls flow through the generated client
  (`internal/api/generated/`). The `internal/auth/` and `internal/api/` packages
  are thin service layers for business logic (token refresh, account tree
  chaining) that delegate HTTP to the generated code.
- **Layered resolution.** Both instance and account selection follow the same
  pattern: flag → env var → config file → hardcoded default. Documented in
  `docs/local-dev.md`.

---

## Phase 3 — Core Operator Commands

**Goal:** Cover the primary operator workflows.

| Deliverable                                        | Status  |
| -------------------------------------------------- | ------- |
| `jetscale recommendations list`                    | Pending |
| `jetscale recommendations show <id>`               | Pending |
| `jetscale recommendations generate`                | Pending |
| `jetscale analyze <query>` (natural language)      | Pending |
| `jetscale plan list`                               | Pending |
| `jetscale plan show <id>`                          | Pending |
| `jetscale plan show <id> --terraform` (HCL output) | Pending |
| Progress indicators for long-running operations    | Pending |
| Pagination support (`--limit`, `--offset`)         | Pending |

**Exit criteria:** The CLI covers the same workflows as the Frontend dashboard
for the "power user" persona (Cloud Platform Engineer).

### Dependencies

- Backend v2 recommendations endpoints must be stable.
- The generated client already covers all v2 paths — no new codegen work needed.
  Commands wire into existing generated methods.

---

## Phase 4 — Release Pipeline + Distribution

**Goal:** Real users can install and update the CLI.

| Deliverable                                 | Status                |
| ------------------------------------------- | --------------------- |
| GoReleaser multi-platform builds            | Pending               |
| GitHub Releases with checksums + signatures | Pending               |
| Homebrew tap (`jetscale-ai/homebrew-tap`)   | Pending               |
| Shell completion (bash, zsh, fish)          | Done (Cobra built-in) |
| `jetscale update` self-update command       | Pending               |
| Install docs in README                      | Pending               |
| CHANGELOG.md (auto-generated from commits)  | Pending               |

**Exit criteria:** `brew install jetscale-ai/tap/jetscale` works. Binary is
signed. Shell completions install correctly.

---

## Phase 5 — CI/CD Integration + Scriptability

**Goal:** The CLI is a building block for automation.

| Deliverable                                         | Status  |
| --------------------------------------------------- | ------- |
| `JETSCALE_TOKEN` env var for non-interactive auth   | Done    |
| `JETSCALE_API_URL` env var for custom endpoints     | Done    |
| `JETSCALE_INSTANCE` env var for instance selection  | Done    |
| `JETSCALE_ACCOUNT` env var for account selection    | Done    |
| `-o json` structured output on all commands         | Done    |
| Exit codes: 0 success, 1 error                      | Done    |
| `jetscale wait <plan-id>` (block until complete)    | Pending |
| GitHub Action: `jetscale-ai/setup-jetscale-cli`     | Pending |
| Example: GHA workflow using CLI for drift detection | Pending |

**Exit criteria:** A GitHub Actions workflow can `jetscale analyze` a cloud
account and open a PR with recommendations, fully non-interactive.

---

## Future / Out of Scope (for now)

These are recorded for context but not planned for the near term:

| Idea                                  | Notes                                      |
| ------------------------------------- | ------------------------------------------ |
| `terraform-provider-jetscale`         | Separate repo, consumes same OpenAPI spec  |
| `pulumi-jetscale` provider            | Separate repo, likely Pulumi bridge        |
| Plugin system (user-defined commands) | Only if CLI surface becomes unwieldy       |
| Browser-based OAuth login flow        | Replace email+password with device auth    |
| WebSocket streaming for agent updates | Real-time plan execution monitoring        |
| Offline mode / spec caching           | Cache spec locally for `--help` generation |
| Multi-format table (markdown, CSV)    | `-o csv` for spreadsheet export            |

---

## Principles

1. **Contract-driven, not hard-coded.** Every API call flows through generated
   client code. If it is not in the OpenAPI spec, it does not exist in the CLI.

2. **Reconciliation, not mirroring.** Backend triggers CLI regeneration, but the
   CLI repo decides whether to merge. Diffs are reviewed, not blindly applied.

3. **Operator-first UX.** The CLI is not a 1:1 mapping of REST endpoints.
   Commands are organized by _workflow_ (`analyze`, `recommendations`, `plan`),
   not by HTTP method.

4. **Independent release cadence.** CLI versions track Backend API
   compatibility, not Backend deploy frequency. A Backend deploy that does not
   change the OpenAPI spec produces no CLI change.

5. **Scriptable by default.** Every command supports `-o json`, deterministic
   exit codes, and env-var auth. The CLI is a CI/CD primitive, not just a human
   tool.

6. **No hand-written HTTP.** Service layers chain calls and handle business
   logic; the generated client handles all HTTP. Manual API code is a smell.
