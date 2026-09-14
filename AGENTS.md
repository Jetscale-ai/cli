# JetScale CLI — Repository Constitution

Last reviewed: 2026-09-14 against the working-tree workflows, `scripts/`,
`magefile.go`, `openapi/spec.json`, and `internal/`; proposals remain
unratified.

- **Status:** Active; channel/publication amendments proposed, not ratified
- **Version:** 0.2.x
- **Authority:**
  [Supreme Constitution](https://github.com/Jetscale-AI/Governance/blob/main/AGENTS.md)
- **Management source:** `../governance/` (local capability projections)
- **Blueprint:** `library` (standalone binary, operator tool)
- **Stack:**
  [`go-mod`](https://github.com/Jetscale-ai/Governance/blob/main/.agents/codex/stacks/go-mod.md)
- **Owner:** Engineering Team
- **Risk Level:** Low (no customer data at rest; authenticated proxy to Backend
  API)

> **Proposed amendment (2026-09-14):** Channel strategy, compatibility metadata,
> verification scope, and the artifact-only publication contract below require
> human review and commit before becoming effective. This draft does not ratify
> itself, activate CI, or widen the agent commit/push/tag ban. Existing ratified
> law remains in force. Work packages and exit criteria are in
> [ROADMAP.md](ROADMAP.md).

## 0. Situational Awareness (Required Context)

This repository is managed from `../governance/` through
[`.agents/imports.json`](.agents/imports.json). Read the repo-local
[operating rules](.agents/agents.md) and [workspace guide](.agents/README.md)
before using imported capabilities.

### Authority and management

Published Governance `main` is authoritative, followed by its ratified Codex,
this repository constitution, and local operating rules. Imported skills are
capabilities, not execution grants. Local Governance drafts and generated
projections cannot ratify law or override CLI's narrower restrictions.

The manifest selects `baseline`, `go-service`, and `terminal`, plus the
Governance-owned `gha-workflow-naming` skill. `terminal-ux` covers the ordinary
Cobra command surface; `tui-design` is available only for future
persistent-screen interfaces. No custom save wrapper is introduced: use
universal `save` guidance subject to CLI's human-only commit/push/tag boundary.

The management integration is materialized locally; this documentation amendment
still requires human review and commit (SOC 2 CC8.1). It does not activate the
proposed artifact publisher or grant permission to use application credentials.
See the [refresh procedure](.agents/README.md#refresh-from-governance).

### Universal Red Lines (Excerpt)

- Agents must never execute commits, pushes, or tags.
- Agents must never output, log, or persist secrets.
- Agents must never impersonate humans.

### Default Failure Mode

If instructions conflict, evidence is ambiguous, or blast radius is unknown:
**STOP → AUDIT → ASK**.

### Eudaimonia is 12 Invariants (Not the `audit_log` Subset)

The Eudaimonia Framework is the Supreme set of **12 universal invariants**.
`audit_log:` entries reference only the invariants that applied for a given
change; they are not the full framework definition.

### Tooling Preflight (Mandatory)

```bash
require_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "Missing required tool: $1" >&2; exit 1; }; }

# Canonical-law bootstrap tooling (private Governance repo)
require_cmd gh
gh auth status -h github.com >/dev/null 2>&1 || { echo "gh not authenticated for github.com (run: gh auth login)" >&2; exit 1; }
require_cmd base64
require_cmd sed

# Repo toolchain
require_cmd go
require_cmd golangci-lint
```

### Load Canonical Law (How)

```bash
gh api repos/Jetscale-ai/Governance/contents/AGENTS.md --jq .content | base64 -d
gh api repos/Jetscale-ai/Governance/contents/.agents/codex/protocols/bootstrap.md --jq .content | base64 -d
gh api repos/Jetscale-ai/Governance/contents/.agents/codex/stacks/go-mod.md --jq .content | base64 -d
```

### Hard Stop on Missing Law

If canonical law cannot be retrieved (permission/404/network), the agent must
not invent governance. Treat the repository as **Advisory Mode** and ask a human
for clarification or access.

### Load Local Operations (How)

```bash
sed -n '1,220p' .agents/agents.md
```

---

## 1. Purpose

This repository owns the **JetScale CLI** (`jetscale`): a first-class operator
tool that provides authenticated, typed access to the JetScale Backend API from
the terminal. It is a standalone Go binary with its own release cadence,
changelog, and install path.

### Proposed Channel Strategy

The same custom command surface is compiled as `jetscale` (`live`, stable
release or separate main-branch Actions artifact), `jetscale-local` (`local`,
workstation build), and `jetscale-unstable` (`unstable`, Actions artifact).
Channel identity is compiled, not inferred from the executable filename. All
retain custom `--api-url` and named-instance support; an override does not grant
authority to access its destination.

Source implements channel defaults and offline `mage buildLocal` /
`mage installLocal`. Installation is an explicit human operation, atomically
replacing only the user-local binary. Neither target fetches schemas or
generates code. Preserve stable users' legacy `~/.config/jetscale/`; non-live
defaults use `XDG_CONFIG_HOME/jetscale/{channel}` with the home-directory
fallback, without changing live's legacy behavior when `XDG_CONFIG_HOME` is set.
Both token lookup paths use channel-specific `JETSCALE_LOCAL_TOKEN` /
`JETSCALE_UNSTABLE_TOKEN`, never falling back to generic `JETSCALE_TOKEN`; live
retains that legacy variable. `auth.Session` implements Backend cookie login
verified through `/api/v2/auth/me`, with storage bound to the normalized API
origin and compiled channel. Local `--credentials-file` authentication is
in-memory and signs out after each command. Unbound legacy credentials require a
new login; `auth login --token` is removed. OS keychain storage, independent
server sessions, and real unstable/production validation remain pending. See
[auth migration and transport compatibility](docs/local-dev.md#auth-migration-and-transport-compatibility).

Stable installation retains its existing release/Homebrew path. Intended
unstable installation is human-operated: download the exact reviewed CLI
run/attempt artifact, verify checksums and provenance against Actions evidence
and the deployed Backend SHA, then explicitly chmod/install only
`jetscale-unstable`. No unstable installer function, latest selection, automatic
execution, or automatic install/update exists. The
[installation templates](README.md#install) do not authorize activation. The
main-branch artifact also requires exact-run verification and explicit human
installation; it uses the `jetscale` name and live configuration, so installing
over stable replaces that binary. Its `live` channel does not make it a stable
release, grant production access, or prove runtime compatibility.

**Rationale:** predictable operator targeting without disrupting stable users
(Business Goal; SOC 2 CC6.1 and CC8.1). These source facts do not establish live
functionality or authorize publication.

### Why a Separate Repo

| Concern                | Decision                                                                       |
| ---------------------- | ------------------------------------------------------------------------------ |
| Release cadence        | CLI versions independently from Backend deploys                                |
| Install path           | `brew install jetscale-ai/tap/jetscale`, standalone binary                     |
| Compatibility contract | Schema-derived client; exact source/schema provenance, independent CLI version |
| Language               | Go (binary distribution) vs Backend (Python/FastAPI)                           |
| Audience               | Operators, CI/CD pipelines, IaC scripts                                        |

### What This Repo Does **Not** Own

- The Backend API itself (lives in `Jetscale-ai/Backend`)
- The OpenAPI spec (generated by FastAPI, exported by Backend CI)
- Terraform/Pulumi providers (future separate repos)

## 2. Codex Ratification

This constitution composes from
[`Jetscale-ai/Governance@main`](https://github.com/Jetscale-ai/Governance):

| Artifact                                       | Role                   |
| ---------------------------------------------- | ---------------------- |
| `.agents/codex/stacks/go-mod.md`               | Go tooling invariants  |
| `.agents/codex/blueprints/library.md`          | Library repo archetype |
| `.agents/codex/protocols/bootstrap.md`         | Session bootstrap      |
| `.agents/codex/protocols/ci-monitoring.md`     | CI health monitoring   |
| `.agents/codex/protocols/branch-protection.md` | Branch workflow        |
| `.agents/codex/protocols/audit-trail.md`       | Commit audit trail     |

## 3. Invariants (Local Narrowing)

In addition to the 12 Eudaimonia invariants and the `go-mod` stack invariants:

1. **Contract-Driven:** The CLI consumes a versioned OpenAPI spec artifact
   published by Backend CI. It must never hard-code endpoint paths or request
   shapes that are not derived from the spec. Bindings regenerate automatically;
   intentional Cobra UX and service adaptations remain hand-maintained, with
   compilation and contract tests gating changes.
2. **No Embedded Secrets:** Live auth tokens retain
   `~/.config/jetscale/tokens.yaml`; non-live storage follows its channel config
   directory. Contents are plaintext protected by Unix `0600` modes or Windows
   handle-validated private ACLs, not an OS keychain or DPAPI. Windows trusts
   the current user, SYSTEM, and Administrators, rejects permissive existing
   files and reparse config paths, and never changes existing directory ACLs.
   Entry read-modify-write uses a `.tokens.lock` OS interprocess lock with a
   five-second wait limit and atomic store replacement; same-session updates
   remain last-writer-wins. The binary must never bundle or log credentials.
   Stored sessions are keyed and checked by API origin and compiled channel,
   independently of directory separation. Login uses opaque session cookies, not
   bearer tokens from the response. Explicit channel token variables select
   bearer-only mode without cookie fallback; this does not make cookie-only
   endpoints bearer-compatible. Native Windows auth-runtime validation remains
   outstanding despite the implemented ACL/locking support and CI gate; Linux
   tests and Windows cross-compilation/vet do not establish it.
3. **Offline-Safe Help:** `jetscale --help` and all `--help` subcommands must
   work without network access.
4. **Deterministic Builds:** Stable release binaries use GoReleaser in CI. The
   proposed branch-matched lanes build only Linux amd64 Actions artifacts. Local
   and CI builds must identify their channel and inputs; different channels need
   not have identical defaults.
5. **Generated Code Isolation:** OpenAPI-generated client code is
   `internal/api/generated/client.gen.go`. Never hand-edit that file;
   `GenerateClient` overwrites only it, not the entire directory.
6. **Proposed Provenance Contract:** CLI version remains independent of Backend.
   Backend `info.version` is insufficient identity. Bind Backend SHA, raw and
   processed schema SHA-256, CLI SHA, producer run/attempt, and consumer
   run/attempt and matching branches in both lanes' provenance. Compiled
   `Channel`, `BackendSHA`, `SchemaSHA256`, `APIVersion`, `SourceSHA256`, and
   `SourceDirty` supplement CLI version/commit metadata; unknown fields or a
   supplied local Backend SHA do not attest compatibility. Offline `version`
   supports text, JSON, and YAML. Pin generation to `oapi-codegen` v2.6.0:
   `GenerateClient` must not tidy dependencies; `Generate` adds the explicit
   `go mod tidy` step. Processing includes the narrow optional-discriminator
   pointer transform and deterministic operation-ID deduplication.
   **Rationale:** reproducible inputs and attributable artifacts (SOC 2 CC8.1).

## 4. Verification

```bash
go test ./... && golangci-lint run
```

### Proposed Validation Boundary

Schema processing/generation and credential-free contract/serialization tests
may be automated before Go tests and build. They do not prove semantic E2E
behavior, deployed auth compatibility, or permission to call regeneration.
Current recommendation `list` / `show` covers AWS-sourced records only. The
regenerated schema and bindings include custom multi-cloud regeneration,
`excluded_source_keys`, and operation inspection, but their Cobra commands are
pending. Cookie auth is implemented, with operator-reported local file-mode
account-listing proof; it does not establish every authenticated workflow or
unstable/production compatibility. Schema descriptions mentioning Bearer auth do
not override cookie-only endpoint behavior. The
[validation record](docs/local-dev.md#credential-free-validation) records the
parent-observed local checks and hook results. Native Windows CI found
held-reader replacement and test-executable naming failures; repairs are
implemented but await native CI confirmation. None of these local checks prove
remote new two-lane CI, a new two-lane Docker rehearsal, or unstable/production
runtime auth. The previous Docker rehearsal covered only the old unstable-only
receiver with synthetic GitHub provenance.

The planned FOCUS API harness belongs to Stack, with a separate authorized
execution contract: at most six serial regenerations, no retries, and
operation-bound evidence capture. It does not replace UI coverage. CLI artifact
CI must not run it implicitly. **Rationale:** keep test evidence truthful and
application mutations scoped (SOC 2 CC6.1 and CC8.1).

## 5. Release Protocol

Ordinary `.github/workflows/ci.yml` handles pushes and PRs for `main` and
`unstable` (with docs-only push exclusions). It runs lint, Python contract
checks, and race-enabled Go tests. PRs and unstable pushes additionally
cross-build Linux/macOS/Windows on amd64/arm64 without publishing CLI binaries;
that matrix is not runtime or Windows auth validation. The new
`test-windows-auth` job on `windows-2025` runs
`go test -count=1 -timeout=5m ./internal/auth ./internal/cmd` with a 10-minute
job limit, `contents: read`, checkout credential persistence disabled, and Go
setup caching disabled. Both `build` and `release` depend on it. The first
native run failed; repair confirmation requires a passing run after human
landing. The separate `build-cli.yml` receiver is workflow-dispatched and builds
the schema-bound Linux amd64 package described below.

1. An eligible CLI `main` push enters `ci.yml`'s stable release job after lint
   and tests, including `test-windows-auth`. Release checkout pins `github.sha`
   and fails if HEAD differs from the tested event SHA, instead of following
   moving `main`.
2. `go-semantic-release` calculates the next version from Conventional Commits;
   the workflow's separate tag step creates and pushes `vX.Y.Z`.
3. GoReleaser builds multi-platform binaries → GitHub Release + Homebrew tap
   update (`jetscale-ai/homebrew-tap`).
4. This stable path consumes the committed schema/generated client.
   Branch-matched artifact builds regenerate only in disposable workspaces; they
   neither refresh committed inputs nor trigger stable tags, Releases, or
   Homebrew updates. Durable stable schema integration remains a separate
   reviewed gap; no stable release parity is claimed.

The proposed landing retires `.github/workflows/sync-openapi.yml` as a
fail-closed manual tombstone with no writes and removes legacy
`repository_dispatch` acceptance. The required Backend dispatcher must use only
the artifact receiver below, not the stable release path; that two-lane
dispatcher is not implemented yet.

### Proposed Branch-Matched Artifact-Only Publication

**Status:** Proposed only; human review, ratification, and activation pending.
The receiver/verifier/packager support both branches in source and have reported
local validation. The Backend two-lane dispatcher remains required work. Real
two-lane producer/receiver CI and a new Docker rehearsal remain unproved; the
previous rehearsal covers the old unstable-only receiver, not this path.
**Owner:** CLI Engineering, coordinated with Backend Engineering. **Rationale:**
distribute traceable branch-matched clients without stable release or runtime
side effects (Business Goal; SOC 2 CC8.1).

This proposal registers only GitHub Actions artifact builds in `Jetscale-ai/cli`
through the single renamed receiver `.github/workflows/build-cli.yml`. It grants
no new agent write permissions and is not a designated unstable Git-write
exception. Existing agent commit, push, and tag prohibitions remain unchanged;
only human review and commit can ratify this authority.

| Backend branch | CLI workflow ref | Channel    | Binary              | Artifact version             |
| -------------- | ---------------- | ---------- | ------------------- | ---------------------------- |
| `main`         | `main`           | `live`     | `jetscale`          | `0.0.0-main.RUN.ATTEMPT`     |
| `unstable`     | `unstable`       | `unstable` | `jetscale-unstable` | `0.0.0-unstable.RUN.ATTEMPT` |

`RUN` and `ATTEMPT` are the CLI consumer run ID and attempt. A main artifact's
live channel does not make it a stable release or authorize production access.

1. **Trigger and trust:** Accept `workflow_dispatch` on exactly CLI `main` or
   `unstable`, matching the reviewed Backend producer's successful, completed
   **push** branch. The required, not-yet-implemented Backend dispatcher must
   reside on `main` and handle `workflow_run` completion for both branches. The
   exact ten inputs are `upstream_repo`, `upstream_workflow`, `upstream_run_id`,
   `upstream_run_url`, `upstream_sha`, `backend_run_attempt`, `backend_branch`,
   `spec_sha`, `spec_version`, and `artifact_name`. Verify repository, workflow
   identity, event, branch, conclusion, run ID, attempt, source SHA, schema
   manifest, and raw schema hash against provider evidence. Dispatch payload
   assertions alone are insufficient. Download the exact verified artifact,
   never the latest matching name. Both CLI branches must be reviewed trusted
   source: their host verifier executes with a Backend Actions-read token before
   sandboxing. No legacy `repository_dispatch` acceptance is part of this
   contract.
2. **Build and evidence:** The implemented receiver uses
   `scripts/unstable_build.py`, `scripts/package_unstable.py`, and offline tests
   in `scripts/test_*.py`. The scripts retain their names but support both
   lanes. After trusted prefetch, run `processSpec`, pinned `generateClient` (no
   tidy), unchanged `go.mod`/`go.sum` checks, race-enabled Go tests, Mage tests,
   `go vet`, and packaging inside the network-disabled `golang:1.25.8-bookworm`
   sandbox. Packaging verifies offline JSON version metadata and help. The
   untouched trusted host must validate the exact bounded regular package files,
   reject links/unexpected members, verify checksums and provenance, and recheck
   producer evidence before upload; it must not execute sandbox scripts or
   binaries. Publish only the lane's Linux amd64 binary (`jetscale` or
   `jetscale-unstable`), `SHA256SUMS`, `raw-schema.json`,
   `processed-schema.json`, `manifest.json`, and `provenance.json`, bound to
   exact CLI/Backend SHAs, matching branches, raw/processed hashes, and
   producer/consumer attempts. The immutable artifact name is
   `<binary>-linux-amd64-<cli_run_id>-<attempt>`, with 14-day retention and
   overwrite disabled. Additional targets require reviewed validation.

   **Sandbox bounds:** Preserve the workflow's 40-minute job limit, 8-minute
   prefetch step, 22-minute sandbox step with a 20-minute process timeout,
   2-minute host-validation step, and cleanup on failure/cancellation. The
   sandbox runs as the host UID/GID with a read-only root, all capabilities
   dropped, no new privileges, no network, a 6 GiB memory limit with no extra
   swap, 2 CPUs, 512 PIDs, and separate 2 GiB `/tmp` and `/cache` tmpfs mounts.
   Mount only a disposable checkout copy and output as writable; evidence and
   private per-job module cache stay read-only. Do not mount the original
   checkout, Docker socket, credentials, event payload, or Actions command
   files. Allowlist environment variables, disable Go downloads/toolchain
   switching and shared caches, and suppress sandbox output from the Actions
   command interpreter. Generation alone must use
   `GOPROXY=file:///modules/cache/download` for pinned `go run` metadata lookup
   from the read-only prefetched cache, without network fallback. Other phases
   retain `GOPROXY=off`; Docker retains `--network=none`. Verifier calls have
   30-second limits within 300 seconds per invocation, with no retries;
   packaging has a 120-second build limit and 10-second limits for other child
   commands, also without retries. Retain bounded archive/schema inputs and fail
   closed on exhausted limits; missing cache entries do not authorize network
   fallback.

3. **Excluded effects:** No branch writes, tags, GitHub Releases, Homebrew
   updates, stable/`latest` promotion, deployment, production access, Backend
   application API writes, or recommendation regeneration. No runtime
   credentials, billing records, or raw customer data enter the build or
   artifacts. Schema exchange/publication remains in GitHub Actions artifacts,
   not Unbrowse or third-party capture services. GitHub verification credentials
   are restricted to host fetch/recheck steps and must never enter the sandbox.
4. **Activation:** Human review and commit of canonical/local authority remain
   mandatory. Land CLI `main` first to register `build-cli.yml` and retire
   `sync-openapi.yml` as a fail-closed manual tombstone with no writes or legacy
   dispatch acceptance. Then land the same complete sources, tools, and workflow
   on CLI `unstable`; both branches require trusted-source review. Backend's
   `pipeline.yml` / `build-release.yml` producers on `main` and `unstable` must
   export the same manifest contract with the actual branch. Generalize the
   completion dispatcher on Backend `main` to completed runs on both branches
   and remove the inline main legacy sync dispatch. Follow the
   [exact dispatch/landing handoff](docs/backend-cli-builds.md). Verify existing
   scoped cross-repository token permissions and downstream consumers; do not
   assume token availability or broaden credentials to make the workflow pass.
   Validate the new two-lane path, then verify real producer completion, exact
   artifact/provenance, receiver execution, pre-upload recheck, cleanup, and
   retention for both lanes through reviewed upstream CI. Preserve finite
   job/network limits and retry bounds. The
   [historical local result](docs/local-dev.md#unstable-receiver-implementation-and-validation-limits)
   covers only the old unstable-only receiver with synthetic provenance; it is
   ignored ephemeral evidence, not proof of the new path or durable upstream
   proof. Neither that pass, implemented source, nor this proposal ratifies
   authority, activates CI, proves a new CI run, or grants agent commit/push/tag
   authority.
5. **Fail closed and recover:** Failed checks, mismatched run/attempt/SHA/hash,
   unknown source/consumer, secret exposure, or exhausted bounds stop
   publication. Do not upload a runnable binary after validation failure.
   Recovery is a human-reviewed correction and newly attributable run, not
   relaxed checks, overwritten evidence, automatic authority renewal, or stable
   promotion.

## 6. Naming Convention Compliance

Per
[`docs/conventions.md`](https://github.com/Jetscale-ai/Governance/blob/main/docs/conventions.md):

- Repo name: `cli` (no domain prefix — application source code)
- Produces: standalone binaries sharing commands with compiled channel defaults
