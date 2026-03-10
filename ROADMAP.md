# JetScale CLI — Roadmap

**Last updated:** 2026-03-10  
**Status:** Draft  
**Tracking:**
[GitHub Projects Board](https://github.com/orgs/Jetscale-ai/projects/) (TBD)

---

## Context

The JetScale CLI exists to give operators and CI/CD pipelines a first-class
terminal interface to the JetScale cost optimization platform. It consumes the
Backend's OpenAPI spec as its contract and releases independently.

This roadmap is structured in phases. Each phase is shippable: it delivers a
usable increment, not just scaffolding.

### Decision Record

The architectural decision behind this repo is captured in
[ADR-001: OpenAPI-to-CLI Generation Strategy](https://github.com/Jetscale-ai/Backend/pull/244)
(Backend PR #244). The chosen approach is **Hybrid**: OpenAPI-generated Go
client + hand-crafted Cobra command tree for UX.

---

## Phase 0 — Repo Inflation (current)

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
| `Makefile`                          | Done   |
| CI workflow (`.github/workflows/`)  | Done   |
| `.goreleaser.yml`                   | Done   |

**Exit criteria:** `make build && make test && make lint` passes in CI.

---

## Phase 1 — OpenAPI Contract + Generated Client

**Goal:** Establish the Backend → CLI contract pipeline.

| Deliverable                                       | Status  |
| ------------------------------------------------- | ------- |
| Export pinned `openapi/spec.json` from Backend CI | Pending |
| `oapi-codegen` config for Go client generation    | Pending |
| `internal/api/generated/` client package          | Pending |
| `make generate` target                            | Pending |
| `sync-openapi.yml` workflow (repository_dispatch) | Pending |
| Backend CI: `repository_dispatch` on spec change  | Pending |

**Exit criteria:** Backend merge that changes API → CLI repo gets a PR with
regenerated client code. The generated client compiles and has a smoke test.

### Key Decisions

- **Generator:** [`oapi-codegen`](https://github.com/oapi-codegen/oapi-codegen)
  (Go-native, no JRE dependency, produces idiomatic Go with interfaces)
- **Spec version:** We pin to the Backend's `/api/v2` OpenAPI spec only. v1
  endpoints are not exposed through the CLI.
- **Dispatch mechanism:** Backend CI sends `repository_dispatch` with
  `event_type: openapi-spec-updated` and a payload containing the spec
  version/SHA. CLI workflow fetches the artifact, regenerates, diffs, and opens
  a PR if anything changed.

---

## Phase 2 — Auth + Config + First Commands

**Goal:** A CLI that can authenticate and do something useful.

| Deliverable                                     | Status  |
| ----------------------------------------------- | ------- |
| `jetscale auth login` (browser OAuth flow)      | Pending |
| `jetscale auth status`                          | Pending |
| Token storage (OS keychain / file fallback)     | Pending |
| `jetscale config set/get/list`                  | Pending |
| `~/.config/jetscale/config.yaml` management     | Pending |
| `jetscale version` (build metadata)             | Pending |
| Authenticated HTTP client (`internal/api/`)     | Pending |
| `jetscale recommendations list`                 | Pending |
| Output formatters: `--format table\|json\|yaml` | Pending |

**Exit criteria:** An operator can `jetscale auth login`, then
`jetscale recommendations list --format table` and see real data from a staging
Backend.

### UX Principles

- Every command works with `--help` offline
- Default output is human-readable tables; `--format json` for piping
- Errors include actionable next steps, not stack traces
- Auth failures suggest `jetscale auth login`

---

## Phase 3 — Core Operator Commands

**Goal:** Cover the primary operator workflows.

| Deliverable                                        | Status  |
| -------------------------------------------------- | ------- |
| `jetscale analyze <query>` (natural language)      | Pending |
| `jetscale recommendations generate`                | Pending |
| `jetscale recommendations show <id>`               | Pending |
| `jetscale plan list`                               | Pending |
| `jetscale plan show <id>`                          | Pending |
| `jetscale plan show <id> --terraform` (HCL output) | Pending |
| `jetscale accounts list`                           | Pending |
| `jetscale accounts set-default <id>`               | Pending |
| Progress indicators for long-running operations    | Pending |
| WebSocket streaming for agent updates              | Pending |

**Exit criteria:** The CLI covers the same workflows as the Frontend dashboard
for the "power user" persona (Cloud Platform Engineer from `personas-jtbd.md`).

---

## Phase 4 — Release Pipeline + Distribution

**Goal:** Real users can install and update the CLI.

| Deliverable                                 | Status  |
| ------------------------------------------- | ------- |
| GoReleaser multi-platform builds            | Pending |
| GitHub Releases with checksums + signatures | Pending |
| Homebrew tap (`jetscale-ai/homebrew-tap`)   | Pending |
| Shell completion (bash, zsh, fish)          | Pending |
| `jetscale update` self-update command       | Pending |
| Install docs in README                      | Pending |
| CHANGELOG.md (auto-generated from commits)  | Pending |

**Exit criteria:** `brew install jetscale-ai/tap/jetscale` works. Binary is
signed. Shell completions install correctly.

---

## Phase 5 — CI/CD Integration + Scriptability

**Goal:** The CLI is a building block for automation.

| Deliverable                                         | Status  |
| --------------------------------------------------- | ------- |
| `JETSCALE_TOKEN` env var for non-interactive auth   | Pending |
| `JETSCALE_API_URL` env var for custom endpoints     | Pending |
| `--output json` guaranteed stable schema            | Pending |
| Exit codes: 0 success, 1 error, 2 auth failure      | Pending |
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
| Multi-tenant org switching            | Depends on Backend RBAC maturity           |
| Offline mode / spec caching           | Cache spec locally for `--help` generation |

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

5. **Scriptable by default.** Every command supports `--format json`,
   deterministic exit codes, and env-var auth. The CLI is a CI/CD primitive, not
   just a human tool.
