---
kind: agents
---

# CLI Repository Operations

Last reviewed: 2026-09-14 against `internal/auth/` and
`.github/workflows/ci.yml`; documentation changes remain subject to human
review.

**Status:** Operational Details

**Scope:** `Jetscale-ai/cli` only

**Authority:** [Repository Constitution](../AGENTS.md) →
[Supreme Constitution](https://github.com/Jetscale-ai/Governance/blob/main/AGENTS.md)

---

## Managed Workspace

This file is CLI-owned operational law and remains a real local file when
`../governance/` re-materializes the workspace. `.agents/AGENTS.md` is a
compatibility symlink to this file, not a second source of instructions.

Read the [workspace guide](README.md) and [import manifest](imports.json) for
bundle selection and refresh commands. The `baseline`, `go-service`, and
`terminal` bundles plus `gha-workflow-naming` provide shared capabilities.
Generated canonical and Cursor/Claude projections must not be hand-edited;
change CLI's manifest or propose changes in the owning Governance source. Do not
edit vendor submodules. Published Governance `main` is authoritative; local
drafts and materialized skills cannot grant additional permissions.

Use universal `save` guidance without a CLI-specific save wrapper. CLI's ban on
agent commits, pushes, and tags remains binding even when an imported skill
supports those actions elsewhere. Management does not authorize application
login, schema fetching, API writes, publication, or deployments. Changes to this
operational law require human review and commit (SOC 2 CC8.1).

## Commit Workflow

Agents prepare changes. Humans commit.

1. Agent makes file changes using Write/StrReplace/Delete tools.
2. Agent suggests a commit message following
   [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`,
   `fix:`, `chore:`, `docs:`, `refactor:`, `test:`, `ci:`).
3. Agent **STOPS** and waits for human to review and commit.

Material changes must include an `audit_log:` trailer:

```text
feat: add recommendations list command

audit_log:
  invariants: [Traceability, Contract-Driven]
  reason: First operator command backed by generated OpenAPI client
  issue: https://github.com/Jetscale-ai/cli/issues/NNN
```

## Verification Oracle

```bash
mage test && mage lint
```

Run this before suggesting any commit. If it fails, fix the issue first.

## Code Organization Rules

### Generated Code

- `internal/api/generated/client.gen.go` is machine-generated. Never hand-edit
  it. `mage generateClient` overwrites only that file, not its directory.
- Use pinned `mage generateClient` after updating `openapi/spec.json` when
  regeneration is authorized. `mage generate` also runs `go mod tidy`; do not
  use it during a dependency-frozen review. Reproduction proves consistency, not
  Backend producer identity; retain exact raw schema/manifest/run evidence.
- If regeneration breaks the build, fix in the spec or in wrapper code, not in
  the generated output.

### Command Structure

Each command lives in its own file under `internal/cmd/`:

```text
internal/cmd/
├── root.go            # Root command + global flags
├── version.go         # jetscale version
├── auth.go            # jetscale auth login|whoami|status|logout
├── accounts.go        # jetscale accounts list|use|current
├── config.go          # jetscale config show|set|get|instances
└── system.go          # jetscale system info|diagnostics
```

### Adding a New Command

1. Create `internal/cmd/<name>.go` with a `newXxxCmd()` function.
2. Register it in `root.go` via `root.AddCommand(newXxxCmd())`.
3. Add a test in `internal/cmd/<name>_test.go`.
4. Verify: `mage test && mage lint`.

## Authentication Compatibility

Commands use `auth.Session`: cookie login is verified through `/api/v2/auth/me`
and stored sessions are bound to API origin and compiled channel. The removed
`auth login --token` flag and unbound legacy credentials require migration to a
new interactive cookie login, or local-only ephemeral `--credentials-file` use.
Explicit channel token variables remain bearer-only inputs without cookie
fallback; they do not authenticate cookie-only endpoints.

Authenticated transport requires HTTPS except loopback HTTP, ignores ambient
proxies, rejects redirects including same-origin redirects, and accepts only an
origin URL, not a path-prefixed API base. See
[auth migration and transport compatibility](../docs/local-dev.md#auth-migration-and-transport-compatibility)
for destination, CA, session-store, and cleanup constraints.

Stored credentials are plaintext protected by Unix `0600` modes or Windows
handle-validated private ACLs, not an OS keychain or DPAPI. Windows storage
trusts the current user, SYSTEM, and Administrators; rejects permissive existing
files and reparse config paths; and leaves existing directory ACLs unchanged.
Entry read-modify-write holds the `.tokens.lock` OS interprocess lock with a
five-second wait limit before atomic replacement. Same-session updates remain
last-writer-wins. Never remove the sidecar as a stale-lock fix; handle close or
process exit releases the lock. Permission/platform errors fail closed; see
[storage security and recovery](../docs/local-dev.md#session-store-security-and-concurrent-commands).

OS keychain storage and independent server sessions remain outstanding. Native
Windows CI found held-reader replacement and test-executable naming failures;
repairs await native CI confirmation. Documentation or offline tests grant no
application-call authority.

### Proposed Stack stdin-auth path (inactive)

Working-tree `--login EMAIL --password-stdin` uses a temporary in-memory session
with detached signout; it never saves credentials. It rejects credential-file
and channel-token combinations. The generic reader's ability to accept a file is
not permission for unstable automation to do so.

Only the qualified Stack wrapper's internal anonymous pipe is proposed for
`https://unstable.jetscale.ai`, `devops+unstable@jetscale.ai` and the approved
1Password reference. Require the publication, actual-capture synthetic
qualification and runtime evidence gates in the
[proposed constitution scope](../AGENTS.md#proposed-stack-mediated-temporary-cli-authentication).
Basics and flags need separate authorization; neither chains provider setup,
FOCUS, BU creation or external provisioning. Login/signout can invalidate all
operator sessions. Failed or unverified cleanup blocks further writes; never
retry login/apply to discover whether an earlier operation succeeded. Dirty
unstable-channel builds are inputs, not provenance, authority or deployment
proof. Existing browser-only registration and CLI's secret and delivery bans
remain unchanged until human-reviewed publication.

## Branch Strategy

- `main` is the release branch. Protected.
- Feature branches: `feat/<description>` or `fix/<description>`.
- Proposed branch-matched artifact builds use exactly CLI `main` for Backend
  `main` and CLI `unstable` for Backend `unstable`. Both CLI branches must be
  reviewed trusted source because the host verifier runs with a Backend
  Actions-read token. Branch names do not grant agent write authority.
- Retire `sync-openapi.yml` as a fail-closed manual tombstone with no writes or
  legacy `repository_dispatch` acceptance; no automatic OpenAPI sync branch is
  created by the new receiver.

## Dependencies

- Use `go mod tidy` after adding/removing imports.
- Pin major versions. Avoid `@latest` in production code.
- `go.mod` and `go.sum` are authoritative lockfiles and must be committed.

## Release

Releases are automated on merge to `main`:

1. Ordinary `ci.yml` runs lint, Python contract tests, and race-enabled Go tests
   for PRs and pushes on `main`/`unstable`, with docs-only push exclusions. PRs
   and unstable pushes cross-build Linux/macOS/Windows amd64/arm64 without
   publishing CLI binaries; that matrix is not Windows auth-runtime validation.
   The new `test-windows-auth` job on `windows-2025` runs
   `go test -count=1 -timeout=5m ./internal/auth ./internal/cmd` with a
   10-minute job limit, `contents: read`, no persisted checkout credentials, and
   Go setup caching disabled. Both `build` and `release` depend on it. The first
   native run failed; repair confirmation requires a passing run after human
   landing.
2. Only eligible main pushes enter the release job. Checkout pins `github.sha`
   and fails if HEAD differs from the tested event SHA, not moving `main`.
   `go-semantic-release` calculates a version, then a separate step creates the
   tag and runs GoReleaser.
3. GoReleaser builds multi-platform binaries and creates a GitHub Release.
4. Homebrew tap (`jetscale-ai/homebrew-tap`) is updated automatically.

Do not manually create GitHub Releases or tags.

### Proposed branch-matched artifact builds

This remains an authority proposal pending human review/commit and activation,
not new agent commit/push/tag permission. The single receiver
`.github/workflows/build-cli.yml` accepts `workflow_dispatch` only on the exact
matching `main` or `unstable` branch. `scripts/unstable_build.py` and
`scripts/package_unstable.py` retain their names but support both lanes:

| Backend branch | CLI ref    | Channel    | Binary              | Artifact version             |
| -------------- | ---------- | ---------- | ------------------- | ---------------------------- |
| `main`         | `main`     | `live`     | `jetscale`          | `0.0.0-main.RUN.ATTEMPT`     |
| `unstable`     | `unstable` | `unstable` | `jetscale-unstable` | `0.0.0-unstable.RUN.ATTEMPT` |

`RUN` / `ATTEMPT` are the CLI consumer run ID and attempt. Both lanes publish
only immutable Linux amd64 Actions artifacts named
`<binary>-linux-amd64-<cli_run_id>-<attempt>`, retained for 14 days with
overwrite disabled. No Backend dispatch creates a stable tag, GitHub Release,
Homebrew update, branch write, deployment, or application API write. The
live-channel main artifact is not a stable release and grants no production
access.

The input contract has exactly ten fields: `upstream_repo`, `upstream_workflow`,
`upstream_run_id`, `upstream_run_url`, `upstream_sha`, `backend_run_attempt`,
`backend_branch`, `spec_sha`, `spec_version`, and `artifact_name`. Retain
independent producer/run/attempt/branch/hash checks, trusted prefetch,
network-disabled Docker generation/testing/packaging, bounded resources/timeouts
and cleanup, host package validation, and pre-upload producer recheck. Never
expose the host verification token to the sandbox. Full bounds remain in the
[proposed publication contract](../AGENTS.md#proposed-branch-matched-artifact-only-publication).

Human landing order is CLI `main` first (register the receiver and retire old
sync), then CLI `unstable` with the same complete sources, tools, and workflow.
Backend `pipeline.yml` / `build-release.yml` producers on both branches must
export the same manifest contract with the actual branch. The generalized
Backend dispatcher is required work, not implemented: it must live on Backend
`main`, handle completed `workflow_run` events for `main` and `unstable`, and
dispatch only successful push runs. Removal of inline main legacy sync dispatch
is also required. Follow the
[exact dispatch/landing handoff](../docs/backend-cli-builds.md) and verify
scoped tokens and downstream consumers before activation.

**Stable integration gap:** `.github/workflows/ci.yml` main-push stable releases
remain separate and consume the committed schema/generated client.
Workspace-only artifact builds do not refresh those inputs or prove stable
release parity. Durable schema integration needs a separate reviewed change.

The [local validation record](../docs/local-dev.md#credential-free-validation)
records local checks, hook results, and the failing native Windows CI run.
Native confirmation of the repairs remains pending. No remote new two-lane CI,
new two-lane Docker rehearsal, or unstable/production runtime auth is proved.
The
[previous Docker rehearsal](../docs/local-dev.md#unstable-receiver-implementation-and-validation-limits)
is historical old-receiver evidence only, not validation of the new path.
