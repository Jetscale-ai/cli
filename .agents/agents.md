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

Use universal `save` guidance without a CLI-specific save wrapper. Commit and
push follow Authorized Ref Movement in the Commit Workflow below. Merging to
`main`, tags, and releases stay human-only, and that limit remains binding even
when an imported skill supports merges, tags, or releases elsewhere. Management
does not authorize application login, schema fetching, API writes, publication,
or deployments. Changes to this operational law require human review and commit
(SOC 2 CC8.1).

## Commit Workflow

Agents may commit and push to a non-main branch and open a pull request only
through an attended human request, a repository's designated unstable-autonomy
contract, or a human-approved standing automation (Governance AGENTS.md §4.A,
Authorized Ref Movement). Manual human execution remains the default outside
those paths. Branch state alone grants nothing. Humans merge to `main`. Tags and
releases stay human-only.

1. Agent makes file changes using Write/StrReplace/Delete tools.
2. Agent commits with a message following
   [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`,
   `fix:`, `chore:`, `docs:`, `refactor:`, `test:`, `ci:`).
3. Agent pushes the non-main branch and opens a pull request, then **STOPS**.
   Humans review and merge. Agents do not merge to `main`, tag, or release.

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

Run this before committing. If it fails, fix the issue first.

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

### Stack stdin-auth path

Working-tree `--login EMAIL --password-stdin` uses a temporary in-memory session
with detached signout; it never saves credentials. It rejects credential-file
and channel-token combinations. The generic reader's ability to accept a file is
not permission for unstable automation to do so.

Only the qualified Stack wrapper's internal anonymous pipe is permitted for
`https://unstable.jetscale.ai`, `devops+unstable@jetscale.ai` and the unchanged
approved 1Password reference. The
[accepted constitution scope](../AGENTS.md#proposed-stack-mediated-temporary-cli-authentication)
is ratified by human review and commit of that text and becomes effective
automatically upon matching human-reviewed publication on Governance, CLI and
Stack `main`. No later status edit, active marker or activation ceremony is
required. Acceptance alone supplies neither publication nor runtime authority.
The 2026-11-16 `sso_bootstrap` amendment is accepted upon human review and
commit, effective automatically with matching human-reviewed publication of all
revised Governance Constitution/secret-management, CLI and Stack seed
adoption/registration/procedure scope on their respective `main` branches. No
later status edit, active marker or ceremony is required. Preserve the
2026-11-14 acceptance and existing grants until new publication; this request
prepares law, not self-ratification or execution. Rationale: SOC 2 CC6.1/CC8.1
and web/MCP SSO readiness without manual provisioning intervention. Stack's
[seed registration](https://github.com/Jetscale-AI/Stack/blob/main/docs/operations/unstable-browser-contract.md#cli-seed-api-registration)
and
[seed procedure](https://github.com/Jetscale-AI/Stack/blob/main/docs/operations/unstable-cli-seed.md)
own execution scope, ordering, exact session budgets, bounds and recovery.

**Five-seat amendment (2026-11-17):** The seed profile below uses minimum
five-seat quota-only capacity, including Stack's default company/license setup,
`capacity` and `sso_bootstrap`. Stack owns desired state and ordering; the
generic CLI still requires explicit `--min-seats`; the five-seat Stack profile
passes `5`, with no tenant-specific default or commercial pricing-plan change.
This preparation is pending human ratification and matching publication of
Governance Constitution/secret-management, CLI law and Stack seed
adoption/registration/procedure on their respective `main` branches. Five-seat
scope becomes effective automatically upon that matching human-reviewed
publication, without a later status edit, marker or activation ceremony. Local
drafts grant no authority; human review and commit ratify the amendment.
Preserve the 2026-11-14 acceptance, 2026-11-16 amendment and existing published
grants; the published two-seat auth profile remains binding until matched
publication. Offline examples/tests do not authorize live five-seat execution or
delivery. Rationale: reproducible five-seat unstable setup without changing
billing terms (Business Goal; SOC 2 CC8.1). Agents cannot ratify or publish this
amendment. The exact minimum-seat line in the constitution is profile scope
evidence, not an activation marker or execution grant; require matching
published scope in both Governance seed-auth sections, CLI's auth section and
Stack's base seed and seed-auth registrations for five-seat selections. Explicit
legacy `auth.min_seats=2` remains supported within existing published stage
grants; missing five-seat scope cannot disable those grants or silently upgrade
them. Addatech remains unchanged. Default company/license setup retains its
existing absent-company atomic setup/reconciliation rules; an existing company
without a license stays blocked. Grow only eligible finite quotas below 5 to
exactly 5; quotas of 5 or more and null (unlimited) and repeat applies are
noops. Do not change catalog minimums, pricing terms or other quotas.

The opt-in auth profile selects only `Unstable Jetscale` / `unstable-jetscale`,
explicit pinned company UUID and `agent@jetscale.ai`. Require explicit human
authorization for platform-wide superadmin privilege, invitation/email, seat,
root-wide auth impact and full company cascades; company linkage does not
contain privilege. All nine default stage names and selection rules remain
unchanged; only the default/license and auth seat minimum changes under the
publication condition. Legacy auth selections cannot mix them. `sso_bootstrap`
is opt-in and mutually exclusive with `capacity`, `auth_prepare`, `sso_activate`
and those nine stages. The helper must fail closed for the new stage on old
three-stage law, without re-disabling existing grants. Existing cloud/FOCUS and
BU seed effects retain Stack's separate human-started/private-file and
credential-transfer constraints, not new authority from this auth amendment.

Once five-seat scope is effective, follow the stage effects in the constitution:
`capacity` fixes `auth.min_seats=5` with unlimited/larger capacity noop;
`auth_prepare` creates only missing non-secret auth rows DISABLED and uses named
user reconciliation, without mappings; `sso_activate` requires completed
identity/password-first proof, then effective MULTI, exact `OrganizationSSO`
provider, existing `[Console] Jetscale` organization mapping and SSO. Use only
the two named mapping keys; inspect required-SSO read-only, requiring deployed
false-default evidence if absent. Account for activation by mapping writes, not
just flag writes.

`sso_bootstrap` always ensures the same minimum five-seat quota-only capacity,
creates absent disabled non-secret MULTI/SSO root/company rows and ensures the
exact company-linked superadmin through existing typed primitives. Invite an
absent target once, reuse a matching `PENDING_ACTIVATION` identity and valid
unused invitation without resend, and preserve existing completed identity; only
a matching signup-complete `ACTIVE` ordinary user may be promoted. Conflicts
block. Optional SSO uses the same ordered MULTI/provider/existing
organization/SSO reconciliation and mapping-activation checks, without disabling
rows or changing domains, trust or required-SSO. Stack owns orchestration; no
new CLI command or seed engine is needed.

Before SSO configuration, require deployed shared web/MCP behavior proof that
genuine first SSO preserves company/superadmin, sets `ACTIVE` and
`signup_completed_at`, consumes invitations and leaves the password hash absent.
Require existing IdP eligibility, isolated `unstable`/`unstable_user` deployment
and deployed WorkOS client/organization proof for both exact
`https://unstable.jetscale.ai/auth/sso/callback` and
`https://unstable.jetscale.ai/oauth/sso/callback`. Local source/tests, including
an undelivered email-only fix, do not prove deployed behavior. No prior target
password signup/login or prior web login is required; MCP can be the first
authentic SSO login. Preserve optional-password access in all newly activated
companies, including root-affected other companies, with false required-SSO and
deployed absent-row default proof.

Stack must verify root impact and globally coordinate exclusive writers. Limit
other-company reads to bounded required-SSO policy/two-flag evidence and
exact-email invitation conflicts; no other-company writes. Generic
`feature-flags` does not establish isolation, effective policy, cascade
completion or readiness; legacy `flags` keeps its four-key list and separate
safeguards. Preserve the existing nonzero actor/explicit superuser guard and
operator identity; never self-provision privilege or substitute a login source.

Qualify binary and helper using synthetic secrets under actual capture and
transport settings before real credentials, then verify operation-relevant
runtime evidence. Keep password transfer in the internal anonymous pipe and
sessions in memory. Auth commands must pass explicit HTTP/time caps and
`--max-pages 1`, reserve one cleanup attempt, sign out and check the final
process exit; printed JSON is not cleanup proof. Stack owns per-command budgets
and the 900-second overall limit with 300-second cleanup reserve,
100-descendant/1,000 combined charged-request ceilings, including auth and
cleanup. Charge each enforced command HTTP ceiling before invocation, including
its signout slot, and reserve the worst-case remaining apply path before
mutation. Include phase rechecks, cascade and final readbacks; never refund
unused ceilings or reset accounting between phases. Lifecycle overhead is not
the full command cost. Larger topology may block; do not enlarge limits or retry
automatically.

Login/signout can invalidate operator sessions and requires authorization and
coordination. Reconcile uncertain writes by scoped reads, never login/write
replay; failed or unverified cleanup blocks further runtime work. Legacy auth
completion still requires fresh target company/superuser/ACTIVE/signup timestamp
evidence and separately authorized password-first verification; optional SSO
does not block password-only completion. `sso_bootstrap` reports verified
configuration as "config ready awaiting first SSO", not `ACTIVE`, completed
signup or login evidence. With SSO omitted, report provisioning only, not SSO
readiness. Genuine first SSO completion requires separate login authority; no
OAuth/MCP client setup or round-trip test authority is added.

No disabling, required-SSO/domain/trust changes, reassignment, resend, direct
user activation/forced signup completion, password setting/reset, signup-link
scraping, SAML/OAuth bypass, target credential source, OAuth grants, WorkOS/IdP
administration, external-agent provisioning or privilege self-provisioning is
authorized. Prohibited direct activation does not prohibit separately authorized
genuine first SSO completion; provisioning supplies no such login authority.
Preserve secret, filesystem, tenancy and delivery bans. Dirty builds and
source/test inspection are not qualification, provenance, deployed compatibility
or live success. Human ratification/publication and runtime execution gates
remain binding.

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
not permission to merge to `main`, tag, or release. The single receiver
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
