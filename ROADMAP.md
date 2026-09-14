# JetScale CLI — Roadmap

**Last updated:** 2026-09-14 **Status:** Active; CI activation pending

Last reviewed: 2026-09-14 against the working-tree workflows, `scripts/`,
`magefile.go`, `openapi/spec.json`, and `internal/`.

CLI versions independently of Backend; source implementation is not deployment
or runtime-validation evidence.

---

## Context

The JetScale CLI exists to give operators and CI/CD pipelines a first-class
terminal interface to the JetScale cost optimization platform. It consumes the
Backend's OpenAPI spec as its contract and releases independently.

The work packages below separate source implementation, activation, and live
proof. Owners are CLI Engineering unless another repository is named. No work
package grants execution authority or ratifies the publication proposal.

### Governance management

**Status:** Local integration materialized; documentation amendments await human
review and commit. This is separate from CI publication activation.

- `../governance/` manages the explicit [import manifest](.agents/imports.json):
  `baseline`, `go-service`, `terminal`, and the workflow-naming skill.
- Repo-local [operating rules](.agents/agents.md) remain authoritative for CLI
  workflows; `.agents/AGENTS.md` is a compatibility symlink to that file.
- Canonical and Cursor/Claude projections are generated, not vendored source.
  Refresh and validation instructions live in the
  [workspace guide](.agents/README.md).
- No agent commit/push/tag authority, application access, schema refresh,
  publisher activation, or dependency on Governance at binary runtime is added.

**Exit criteria:** Re-governing preserves local instructions and the manifest,
managed links resolve with the sibling checkout present, and a second run leaves
identical projection contents. Humans review and commit the management docs;
WP4's publisher activation remains independently gated.

### Decision Records

- [ADR-001: OpenAPI-to-CLI Generation Strategy](https://github.com/Jetscale-ai/Backend/pull/244)
  (Backend PR #244) — Hybrid: OpenAPI-generated Go client + hand-crafted Cobra
  command tree for UX.
- [ADR-001: Account Selection](docs/adr-001-account-selection.md) — Cloud
  account resolution order (flag → env → config → auto-select).

---

## WP1 — Compiled Channels and Local Delivery

**Status:** Source-implemented; release/runtime proof separate.

| Binary              | Channel    | Default destination            |
| ------------------- | ---------- | ------------------------------ |
| `jetscale`          | `live`     | `https://console.jetscale.ai`  |
| `jetscale-local`    | `local`    | `https://jetscale.internal`    |
| `jetscale-unstable` | `unstable` | `https://unstable.jetscale.ai` |

All channels retain named instances and custom `--api-url`. The compiled
channel, not the filename, controls defaults. Live preserves
`~/.config/jetscale/`; non-live uses `XDG_CONFIG_HOME/jetscale/{channel}` or the
equivalent fallback under `~/.config`. These defaults are separate from the
origin- and channel-bound cookie sessions implemented in WP3.

`mage buildLocal` builds offline from existing source/schema/generated code.
Human-run `mage installLocal` rebuilds and atomically installs `jetscale-local`
into the user bin directory without replacing stable. Neither fetches schemas.
Build metadata includes channel, CLI SHA, Backend SHA, schema hash, API version,
source hash, and dirty state; unknown or operator-supplied values are not proof.
Offline `version -o json` and `version -o yaml` are implemented alongside text
output. Stable installation remains the existing release/Homebrew path. Both
branch-matched Actions artifacts require manual exact-run download and
verification (see WP4), not an installer function. The main artifact uses the
live-channel `jetscale` name but is not a stable release.

**Exit criteria:** Credential-free tests verify defaults, override behavior,
legacy live config preservation, non-live separation, offline help/version, and
atomic local install failure behavior. Missing cached dependencies fail without
network fallback. No install is performed merely to update documentation.

---

## WP2 — Reproducible Schema Contract

**Status:** Schema processing and pinned generation source-implemented; durable
stable schema integration remains a gap.

- `mage syncSpec` / `syncSpecFrom` explicitly fetch a schema; `processSpec`
  transforms an existing one. Processing filters v2 paths, downgrades OpenAPI
  3.1 to 3.0, prunes orphaned schemas, applies a narrow optional-discriminator
  pointer transform, and deterministically deduplicates operation IDs.
- `mage generateClient` pins `oapi-codegen` **v2.6.0** without tidying modules;
  `mage generate` calls it and then runs `go mod tidy`. Generation overwrites
  only `internal/api/generated/client.gen.go`, not its directory; never
  hand-edit that generated file.
- The schema and client have been regenerated from the current Backend export.
  They include custom regeneration, `excluded_source_keys`, and operation/source
  inspection APIs. Generated bindings do not automatically add Cobra commands.
- Compatibility identity is **Backend SHA + raw schema SHA-256 + processed
  schema SHA-256 + CLI SHA**, not generic Backend `info.version`. CLI version
  remains independent; API version is supplementary metadata.
- Retire `sync-openapi.yml` as a fail-closed manual tombstone with no writes or
  legacy `repository_dispatch` acceptance. Branch-matched builds regenerate only
  in disposable workspaces, not the repository. The separate `ci.yml` main-push
  stable release consumes the committed schema/generated client; automatic
  stable schema integration and stable release parity are not proved.

**Exit criteria:** Exact schema inputs and generator version reproduce the
client; credential-free contract/serialization tests cover the consumed request
and response shapes. Stable release integration needs a separate reviewed
committed-schema update and verified release evidence. Generation success is not
semantic E2E evidence.

---

## WP3 — Auth Compatibility and Credential Safety

**Status:** CLI-only cookie auth and local credentials-file mode
source-implemented; local file-mode account listing passed. Windows ACL storage,
interprocess locking, and a native Windows CI gate are implemented. The first
native run failed on held-reader replacement and test-executable naming; repairs
await native CI confirmation. OS keychain storage, independent server sessions,
and real unstable/production tests remain pending.

- `auth.Session` binds cookies to the exact API origin and compiled channel.
  Login verifies `/api/v2/auth/me`; normal interactive login uses a hidden TTY
  password prompt and persists the session through an atomically replaced
  `tokens.yaml` store: plaintext protected by Unix `0600` or Windows
  handle-validated private ACLs, not an OS keychain or DPAPI. Whoami, status,
  and logout use the cookie session. Unbound legacy credentials require a fresh
  login.
- Windows storage trusts the current user, SYSTEM, and Administrators; rejects
  permissive existing files and reparse config paths; and never changes existing
  directory ACLs. Entry read-modify-write holds a `.tokens.lock` OS interprocess
  lock with a five-second wait limit. Different entries are preserved;
  same-session updates remain last-writer-wins. `golang.org/x/sys` is now a
  direct dependency at unchanged `v0.41.0`. See
  [storage security and platform errors](docs/local-dev.md#session-store-security-and-concurrent-commands).
- The root `--credentials-file` flag reads only `JETSCALE_DEFAULT_ADMIN_EMAIL`
  and `JETSCALE_DEFAULT_ADMIN_PASSWORD` from `../stack/.env`, without shell
  evaluation. It is restricted to a local-channel build targeting HTTPS
  `jetscale.internal` or loopback. Each authenticated command signs in, keeps
  the session in memory, and signs out on success or error without saving
  credentials. Cleanup failure returns nonzero. File-mode `auth login` verifies
  then signs out; subsequent commands need the flag again.
- `--ca-file` extends trust for authenticated requests without disabling TLS
  verification. Normal login followed by accounts commands remains the
  persistent human-use path. Account listing no longer skips company-less
  superusers.
- The `auth login --token` flag is removed to avoid argv secrets. Explicit
  `JETSCALE_LOCAL_TOKEN` / `JETSCALE_UNSTABLE_TOKEN` remain bearer inputs only
  for endpoints that support them, without generic `JETSCALE_TOKEN` fallback;
  live retains its legacy variable. Invalid, empty, or rejected explicit tokens
  do not fall back to stored cookies. File mode rejects a set channel token.
- Authenticated transport accepts origin URLs only, ignores ambient proxies,
  rejects all redirects, and requires HTTPS except loopback HTTP. Secure cookies
  still require HTTPS. Migration requires a new login for unbound legacy
  credentials; bound alias migration is limited to the same origin/channel. See
  [migration and transport compatibility](docs/local-dev.md#auth-migration-and-transport-compatibility).
  Native Windows auth-runtime validation remains outstanding; implementation,
  cross-compilation, and vet do not establish Windows runtime support.

**Server limitation:** Backend login closes prior sessions for the same user,
including browser sessions. No Backend change or independent-session guarantee
is included. API generation is unaffected by this auth change.

**Local validation (2026-09-14, operator-reported):** `mage buildLocal` rebuilt
`bin/jetscale-local`. The exact
[credentials-file JSON invocation](docs/local-dev.md#local-validation-evidence)
against `https://jetscale.internal` exited 0, verified `/auth/me` within the
CLI, returned 20 accounts (2 AWS, 4 AZURE, 14 GCP), and confirmed temporary
sign-out with no credentials persisted. Focused cmd/auth/api race tests passed.
This is ephemeral evidence of that local execution, not durable artifact/CI or
unstable/production proof. No Backend changes were made. Final broader
validation passed (operator-reported): `go test -race -count=1 ./...`,
`go test -count=1 -tags mage .`, `go vet ./...`,
`GOTOOLCHAIN=go1.25.8 mage lint` (0 issues), `mage buildLocal`, and
`git diff --check`. Plain `mage lint` initially panicked under host Go 1.26
because the installed linter was compiled with Go 1.25; matching `GOTOOLCHAIN`
resolved it without dependency changes.

**Final local checks (2026-09-14, parent-observed): PASSED.** The
[canonical validation record](docs/local-dev.md#credential-free-validation)
lists local checks and the subsequent native CI failures. Held-reader
replacement and `.test.exe` naming repairs are implemented with regression
coverage; a passing native `test-windows-auth` run remains required. These
results do not establish remote new two-lane builds or unstable/production
runtime auth.

**Exit criteria:** Credential-free checks cover verified login, cookie rotation,
read calls, logout, origin/channel isolation, token precedence, and file-mode
cleanup on success/error. Retain the bounded local file-mode pass; native
Windows storage/locking/auth tests, persistent interactive runtime validation,
and separately authorized real unstable/production tests remain outstanding,
with no secrets in logs or evidence. Keychain storage and independent server
sessions require follow-up work. The local pass alone does not close this
package.

---

## WP4 — Verified Branch-Matched Artifact Delivery

**Status:** Two-lane receiver/verifier/packager source has reported local
validation. The Backend two-lane completion dispatcher is required work, not
implemented. Human ratification/activation, real producer/receiver CI, and a new
two-lane Docker rehearsal remain outstanding. The previous Docker rehearsal
covered only the old unstable-only receiver, not this path. **Owners:** Backend
producer/dispatcher; CLI receiver/build. **Dependency:** WP2.

The single receiver is `.github/workflows/build-cli.yml` (renamed from
`build-unstable.yml`). `scripts/unstable_build.py` and
`scripts/package_unstable.py` retain their names but support both lanes:

| Backend branch | CLI ref    | Channel    | Binary              | Artifact version             |
| -------------- | ---------- | ---------- | ------------------- | ---------------------------- |
| `main`         | `main`     | `live`     | `jetscale`          | `0.0.0-main.RUN.ATTEMPT`     |
| `unstable`     | `unstable` | `unstable` | `jetscale-unstable` | `0.0.0-unstable.RUN.ATTEMPT` |

`RUN` / `ATTEMPT` identify the CLI consumer run and attempt, not the producer.

1. The required Backend completion dispatcher must live on `main` and handle
   `workflow_run` for `main` and `unstable`; only successful completed **push**
   producers may dispatch CLI `workflow_dispatch` to the exact matching branch.
   This dispatcher remains unimplemented. CLI receiver source no longer accepts
   legacy `repository_dispatch` events; Backend's legacy dispatch paths still
   need replacement.
2. The ten inputs are `upstream_repo`, `upstream_workflow`, `upstream_run_id`,
   `upstream_run_url`, `upstream_sha`, `backend_run_attempt`, `backend_branch`,
   `spec_sha`, `spec_version`, and `artifact_name`. CLI verifies the producer
   repository/workflow, event, branch, conclusion, run/attempt, SHA, manifest,
   and raw hash against provider evidence before downloading the exact artifact.
   Both CLI branches must be reviewed trusted source because the host verifier
   runs with a Backend Actions-read token; sandboxing later work does not make
   arbitrary receiver source safe.
3. Prefetch pinned tools and locked dependencies on the trusted host, then run
   `processSpec`, `generateClient` (no tidy), unchanged-module checks,
   race-enabled Go tests, Mage tests, `go vet`, and packaging in a
   network-disabled `golang:1.25.8-bookworm` Docker sandbox. Packaging includes
   offline version/help checks, not runtime API calls. Generation alone uses
   `GOPROXY=file:///modules/cache/download` for pinned `go run` metadata lookup,
   with no network fallback; other phases use `GOPROXY=off`, and Docker retains
   `--network=none`.
4. Validate the package on the untouched trusted host without executing its
   binary, then recheck producer evidence for reruns or artifact replacement.
5. Publish only the lane's **Linux amd64** binary, `SHA256SUMS`, raw and
   processed schemas, manifest, and provenance linking CLI/Backend SHAs and run
   attempts. The immutable artifact is
   `<binary>-linux-amd64-<cli_run_id>-<attempt>`, retained for 14 days with
   overwrite disabled. Further targets require additional validation.

**Landing and activation gates (human-owned):**

1. Land CLI `main` first: register `build-cli.yml` and retire `sync-openapi.yml`
   as a fail-closed manual tombstone with no writes or legacy dispatch trigger.
2. Land the same complete sources, tools, and workflow on CLI `unstable`; review
   both branches as trusted host code.
3. Verify Backend `pipeline.yml` / `build-release.yml` producers on `main` and
   `unstable` export the same manifest contract with the actual branch.
   Generalize the dispatcher on Backend `main` to completed runs on both
   branches and remove the inline main legacy sync dispatch.
4. Ratify the proposed authority through human review/commit, verify scoped
   token permissions and consumers, and obtain real CI evidence for both lanes.

See [the exact dispatch/landing handoff](docs/backend-cli-builds.md). No new
real CI run is proved. Schema exchange stays in GitHub Actions artifacts,
without Unbrowse or third-party capture; runtime credentials and billing records
are excluded. This proposal grants no agent write permissions.

**Intended installation UX:** A human selects the exact reviewed CLI
run/artifact, checks `SHA256SUMS`, compares provenance with Actions evidence and
the deployed Backend SHA, then explicitly installs the selected binary. No
latest selection, automatic execution, or automatic install/update exists. The
main artifact is live-channel `jetscale`, not a stable release; installing over
stable replaces that binary and shares its live configuration. See the
[unstable download/install template](README.md#unstable-ci-artifact-intended-ux-activation-pending).

**Historical validation only:** The earlier full sandbox rehearsal used the old
unstable-only receiver and synthetic GitHub provenance. It does not validate the
new workflow, branch matching, or two-lane packaging. See the
[ephemeral evidence qualifier](docs/local-dev.md#unstable-receiver-implementation-and-validation-limits).
**Final local validation:** The
[validation record](docs/local-dev.md#credential-free-validation) records the
parent-observed final checks and all hooks passing. This is local test evidence,
not remote two-lane CI or a new Docker rehearsal.

**Exit criteria:** Negative tests reject mismatched branches and failed or
mismatched producer evidence. Validate the new two-lane implementation, then
obtain reviewed activated producer/receiver CI runs for both branches yielding
downloadable, checksum-verifiable artifacts with real provenance. No tags,
releases, Homebrew updates, branch writes, production access, Backend API
writes, or stable promotion from either dispatch lane. Failure publishes no
runnable artifact.

**Ordinary CI:** `ci.yml` runs lint, Python contract tests and race-enabled Go
tests for PRs and pushes on `main`/`unstable`, with docs-only push exclusions.
PRs and unstable pushes cross-build Linux/macOS/Windows amd64/arm64 without
publishing CLI binaries. The new `test-windows-auth` job runs
`go test -count=1 -timeout=5m ./internal/auth ./internal/cmd` on `windows-2025`
with a 10-minute job limit, `contents: read`, checkout credential persistence
disabled, and Go setup caching disabled. Both build and release depend on it;
native repair confirmation remains pending human landing. Cross-builds do not
prove Windows auth behavior, and ordinary CI does not prove the separate
`build-cli.yml` artifact receiver.

**Stable integration gap:** Only eligible main pushes enter `ci.yml`'s semantic
versioning, GoReleaser, GitHub Release, and Homebrew path. Release checkout is
pinned to `github.sha` and fails on a HEAD mismatch, not moving `main`. Releases
use the committed schema/generated client, which workspace-only artifact builds
do not refresh. Stable schema integration requires a separate reviewed change; a
main artifact neither triggers that release path nor proves stable release
parity.

---

## WP5 — Recommendation Workflows

**Status:** AWS-sourced reads implemented; custom multi-cloud workflow pending.
**Dependencies:** WP2 and WP3 before authenticated runtime proof.

| Deliverable                                        | Status                                     |
| -------------------------------------------------- | ------------------------------------------ |
| `jetscale recommendations list`                    | Source-implemented; AWS-sourced only       |
| `jetscale recommendations show <id>`               | Source-implemented; AWS-sourced only       |
| `jetscale recommendations generate`                | Pending                                    |
| `jetscale analyze <query>` (natural language)      | Pending                                    |
| `jetscale plan list`                               | Pending                                    |
| `jetscale plan show <id>`                          | Pending                                    |
| `jetscale plan show <id> --terraform` (HCL output) | Pending                                    |
| Progress indicators for long-running operations    | Pending                                    |
| Pagination support (`--limit`, `--offset`)         | Source-implemented for recommendation list |

The checked-in schema and regenerated bindings now include custom regeneration
(including streaming), `excluded_source_keys`, operation list/detail, and
operation-bound FOCUS source inspection/rows. Cobra wiring for those workflows
remains pending. Existing AWS-sourced `show` is not inspection of a multi-cloud
regeneration run; do not claim dashboard parity.

**Exit criteria:** Preserve tested AWS read behavior; wire the generated
multi-cloud regeneration/inspection contracts into explicitly authorized
commands with operation IDs, bounded waiting, and evidence retrieval. Validate
serialization without credentials before separately authorized semantic tests.

---

## WP6 — Bounded API Validation and Automation

**Status:** Planned. **Owner:** Stack for the FOCUS API harness; CLI for
commands. **Dependencies:** WP3 and WP5; WP4 when testing the distributed
unstable binary.

The planned FOCUS API harness runs at most **six serial regenerations, with no
retries**: AWS EC2, Azure Storage, and GKE, each without then with FOCUS.
Capture operation IDs, selected sources, completion/failure state, and
operation-bound evidence for quality comparison. Keep UI coverage separate; API
tests cannot prove debug-mode controls or rendering work.

**Exit criteria:** Stack owns an explicitly authorized, bounded execution
contract and sanitized evidence for each attempted case. Missing auth, schema,
source selection, or inspection support blocks execution. Generation/contract CI
never automatically invokes this semantic harness or grants regeneration
permission. No cloud remediation, ingestion, deployment, or unrelated data
mutation is implied.

Later automation candidates remain `jetscale wait <plan-id>`, a setup GitHub
Action, and a reviewed drift-detection example. Structured output and env-var
support are foundations, not proof of unattended end-to-end success.

---

## Future / Out of Scope (for now)

These are recorded for context but not planned for the near term:

| Idea                                     | Notes                                                    |
| ---------------------------------------- | -------------------------------------------------------- |
| `terraform-provider-jetscale`            | Separate repo, consumes same OpenAPI spec                |
| `pulumi-jetscale` provider               | Separate repo, likely Pulumi bridge                      |
| `jetscale update` self-update command    | Nice-to-have, not critical with Homebrew                 |
| Plugin system (user-defined commands)    | Only if CLI surface becomes unwieldy                     |
| Browser-based OAuth login flow           | Replace email+password with device auth                  |
| WebSocket streaming for agent updates    | Real-time plan execution monitoring                      |
| Broader offline workflows / spec caching | Help/version and local builds already have offline paths |
| Multi-format table (markdown, CSV)       | `-o csv` for spreadsheet export                          |
| CHANGELOG.md (auto-generated)            | Nice-to-have; GitHub Releases suffice now                |
| Release binary signing                   | Not urgent for alpha                                     |

---

## Principles

1. **Contract-driven, not hard-coded.** Every API call flows through generated
   client code. If it is not in the OpenAPI spec, it does not exist in the CLI.

2. **Reconciliation, not mirroring.** The intended Backend dispatch triggers CLI
   regeneration, but the CLI repo reviews durable source changes. Once
   activated, both branch-matched builds generate in isolated CI workspaces and
   publish artifacts, never branch writes.

3. **Operator-first UX.** The CLI is not a 1:1 mapping of REST endpoints.
   Commands are organized by _workflow_ (`analyze`, `recommendations`, `plan`),
   not by HTTP method.

4. **Independent release cadence.** CLI versions track Backend API
   compatibility, not Backend deploy frequency. Branch-matched artifacts use
   run/attempt versions to bind new Backend provenance independently of the
   stable CLI release version.

5. **Scriptable by design.** Structured output, deterministic exit codes, and
   scoped non-interactive auth must be tested per command. These capabilities do
   not substitute for runtime authorization or semantic evidence.

6. **No hand-written HTTP.** Service layers chain calls and handle business
   logic; the generated client handles all HTTP. Manual API code is a smell.
   Bindings regenerate automatically, but intentional command UX and service
   adaptations remain hand-maintained, with compile and contract-test gates.
