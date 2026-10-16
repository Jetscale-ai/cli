# jetscale

The JetScale CLI — cloud cost optimization from the terminal.

Last reviewed: 2026-09-14 against the working-tree workflows, `scripts/`,
`magefile.go`, `openapi/spec.json`, and `internal/`.

## Governance and contributor setup

Agent guidance is managed by the sibling `../governance/` checkout, using
[`baseline`, `go-service`, and `terminal` imports](.agents/imports.json) plus
workflow-naming guidance. Read the [constitution](AGENTS.md),
[local operating rules](.agents/agents.md), and
[workspace refresh guide](.agents/README.md).

From the Governance checkout:

```sh
pnpm materialize:runtimes
pnpm govern --repo ../cli --dry-run
pnpm govern --repo ../cli
```

These commands refresh local agent projections, not CLI source, schemas,
binaries, credentials, or CI activation. Governance is not a Go build or binary
runtime dependency. Keep CLI-specific instructions local; never edit imported
symlink targets. Published Governance `main` remains authoritative, and law
changes require human review and commit.

## Install

The existing stable delivery path installs `jetscale`. Local and unstable
installation are separate, explicit human operations; neither replaces stable.

### Homebrew (macOS / Linux)

```bash
brew install jetscale-ai/tap/jetscale
```

### Download Binary

Pre-built binaries for macOS, Linux, and Windows (amd64 + arm64) are attached to
every [GitHub Release](https://github.com/Jetscale-ai/cli/releases/latest).

```bash
# Example: Linux amd64
curl -sL https://github.com/Jetscale-ai/cli/releases/latest/download/jetscale_$(curl -sL https://api.github.com/repos/Jetscale-ai/cli/releases/latest | grep tag_name | cut -d '"' -f4 | sed 's/^v//')_linux_amd64.tar.gz | tar xz
sudo mv jetscale /usr/local/bin/
```

### Build from Source

```bash
go install github.com/magefile/mage@latest
mage build        # → bin/jetscale
```

### Local workstation binary

With Go, Mage, and dependencies already cached, run `mage installLocal` yourself
to rebuild and atomically install `jetscale-local` in `~/.local/bin`. It does
not fetch schemas, generate code, or run the installed binary. See
[local development](docs/local-dev.md#optional-user-bin-install).

### Unstable CI artifact (intended UX; activation pending)

There is no unstable installer function. After human ratification and CI
activation, manually select a successful, reviewed **CLI unstable** run and its
exact attempt-specific artifact; do not select a latest-by-name result. Both
branch-matched build lanes publish only Linux amd64 Actions artifacts, with
14-day retention. The separate CLI `main` artifact contains live-channel
`jetscale`, version `0.0.0-main.RUN.ATTEMPT`; it is not a stable release or
Homebrew update. Installing it over an existing `jetscale` would replace that
binary and use the same live-channel configuration. See the
[build and landing handoff](docs/backend-cli-builds.md) before selecting a lane.

These are templates, not runnable values: replace `<cli_run_id>` and `<attempt>`
with the reviewed CLI run coordinates. Use a fresh download directory, not one
containing an older package.

```sh
gh run download <cli_run_id> --repo Jetscale-ai/cli \
  --name jetscale-unstable-linux-amd64-<cli_run_id>-<attempt> \
  --dir ./reviewed-unstable
cd ./reviewed-unstable
sha256sum -c SHA256SUMS
```

Stop on any checksum failure. Before enabling execution, review
`provenance.json` and `manifest.json`: compare CLI/Backend SHAs,
producer/consumer run IDs and attempts, and schema/binary hashes with the
reviewed Actions evidence. Match `backend_sha` to the **deployed Backend SHA**
of the intended target, not merely a branch tip or API version. Checksums prove
package consistency, not trust in an arbitrary run or runtime compatibility.

Only after those checks, explicitly install into your user-owned bin directory:

```sh
chmod +x jetscale-unstable
mkdir -p ~/.local/bin
install -m 0755 jetscale-unstable ~/.local/bin/jetscale-unstable
```

These steps neither execute the binary nor configure automatic updates. They
replace only an existing `jetscale-unstable` at that destination; no `latest`
alias, automatic run, or automatic install/update is provided. No activated
unstable run or downloadable artifact is claimed yet.

## Compiled Channels

One command surface, three compiled channels. Renaming a binary does not change
its channel; each channel still supports named instances and `--api-url`.

| Binary              | Compiled channel | Default target                 | Delivery                                                             |
| ------------------- | ---------------- | ------------------------------ | -------------------------------------------------------------------- |
| `jetscale`          | `live`           | `https://console.jetscale.ai`  | Stable release / Homebrew; separate main artifact pending activation |
| `jetscale-local`    | `local`          | `https://jetscale.internal`    | Offline build from local sources                                     |
| `jetscale-unstable` | `unstable`       | `https://unstable.jetscale.ai` | CI artifact implemented; activation pending                          |

Channel defaults, local build/install, and the branch-matched receiver are
source-implemented. The previous Docker rehearsal covered only the old
unstable-only receiver with synthetic GitHub provenance; it is not validation of
the new two-lane path. Human review/activation, real producer/receiver CI, and
unstable/production functionality remain unverified. Local credentials-file
account listing passed as described below. Authority remains proposed. Explicit
target overrides are not a security boundary: verify the destination before
using auth.

## Quick Start

The following commands exist in source; local runtime proof is limited to the
credentials-file account-listing flow below. Normal `auth login` requires a TTY
for its hidden password prompt, verifies the cookie session through
`/api/v2/auth/me`, then persists it in the channel's atomically replaced
`tokens.yaml` store. Credentials are plaintext, protected by `0600` on Unix or
handle-validated private ACLs on Windows, not an OS keychain or DPAPI.
`auth whoami`, `auth status`, and `auth logout` use that session.

Windows storage trusts the current user, SYSTEM, and Administrators; it rejects
permissive existing files and reparse config paths without changing existing
directory ACLs. A `.tokens.lock` OS interprocess lock covers entry
read-modify-write with a five-second wait limit; same-session updates remain
last-writer-wins. See
[storage security and platform errors](docs/local-dev.md#session-store-security-and-concurrent-commands)
for permission failures and lock-timeout recovery.

**Session impact:** Backend login closes prior sessions for the same user,
including browser sessions. This CLI-only change does not modify Backend or
guarantee an independent server session.

```bash
# Authenticate
jetscale auth login                         # email + password prompt

# Explore
jetscale auth whoami                        # check identity
jetscale accounts list                      # discover cloud accounts
jetscale accounts use prod-us-east-1        # set active account
jetscale system info -o json                # backend version info
jetscale recommendations list --limit 10    # latest recommendations
jetscale recommendations show <id>          # existing recommendation details

# Script-friendly
jetscale auth whoami -o json | jq -r '.email'
```

For local-only, per-command auth from the existing Stack credentials file, run
from `cli/` after `mage buildLocal`:

```sh
./bin/jetscale-local -i local --ca-file ../stack/certs/jetscale-dev-ca.pem --credentials-file ../stack/.env accounts list
```

`--credentials-file` reads only `JETSCALE_DEFAULT_ADMIN_EMAIL` and
`JETSCALE_DEFAULT_ADMIN_PASSWORD`, without shell evaluation. It requires a
compiled local-channel binary and HTTPS `jetscale.internal` or a loopback API.
The session stays in memory and is signed out on command success or error;
cleanup failure returns nonzero. No credentials are saved by this mode. Unset
`JETSCALE_LOCAL_TOKEN` before using it.

With the same flag, `auth login` only verifies login and signs out: pass the
flag again to `accounts list`. For a persistent human session, use normal
interactive login without the flag, then run accounts commands. See the
[local auth examples](docs/local-dev.md#local-cookie-authentication). **Local
validation passed (2026-09-14, operator-reported):** After `mage buildLocal`
rebuilt `bin/jetscale-local`, the exact
[local JSON account-listing invocation](docs/local-dev.md#local-validation-evidence)
exited 0, verified login through `/auth/me` within the CLI, returned 20 accounts
(2 AWS, 4 AZURE, 14 GCP), and confirmed temporary sign-out with no credentials
persisted. Focused race tests for cmd/auth/api passed. This is ephemeral local
execution evidence, not durable artifact/CI or unstable/production proof. Final
broader validation passed (operator-reported): `go test -race -count=1 ./...`,
`go test -count=1 -tags mage .`, `go vet ./...`,
`GOTOOLCHAIN=go1.25.8 mage lint` (0 issues), `mage buildLocal`, and
`git diff --check`. Plain `mage lint` initially panicked under host Go 1.26
because the installed linter was compiled with Go 1.25; matching `GOTOOLCHAIN`
resolved it without dependency changes. Persistent interactive login remains
outside this runtime proof.

## What This Is

`jetscale` is a standalone Go binary that provides authenticated, typed access
to the [JetScale Backend API](https://github.com/Jetscale-ai/Backend). It is
designed for:

- **Operators** who prefer terminals over dashboards
- **CI/CD pipelines** that need to query or trigger JetScale programmatically
- **IaC workflows** where JetScale recommendations feed into Terraform/Pulumi
  plans

The CLI is a **first-class product surface** with its own release cadence,
install path, and compatibility contract against the Backend API.

## Status

**Alpha.** The CLI versions independently of Backend. Source includes auth,
multi-instance targeting, cloud account commands, and AWS-sourced recommendation
`list` / `show` with pagination. The generated client covers the checked-in
schema, not necessarily every endpoint in the current Backend.

Known gaps:

- Cookie auth is source-implemented through origin- and channel-bound
  `auth.Session`, including verified login and local per-command file auth.
  Account listing no longer skips company-less superusers. Local file-mode
  account listing passed; real unstable/production tests, OS keychain storage,
  and independent server sessions remain pending. The local pass does not prove
  other authenticated workflows. Native Windows CI found held-reader replacement
  and test-executable naming failures; the repairs await native CI confirmation.
  See the [validation record](docs/local-dev.md#credential-free-validation) for
  local checks and the failing CI run.
- The refreshed schema and generated bindings include custom recommendation
  regeneration, `excluded_source_keys`, and operation inspection. Their Cobra
  commands remain pending, as do `analyze` and `plan`.
- Credential-free generation, contract, and serialization tests can be
  automated; they neither prove semantic E2E behavior nor authorize regeneration
  calls.

See [ROADMAP.md](ROADMAP.md) for work packages and exit criteria.

## Commands

```text
Core commands (noun-verb; get also answers to show and view)
  jetscale auth login|logout|whoami|status
  jetscale companies list|get|create
  jetscale business-units list|get|create          # alias: bu
  jetscale accounts list|get|link|status|wait|use|current
  jetscale recommendations list|get|sources|inspect|regenerate   # alias: recs
  jetscale cost-sources list|get|create|validate|enable
  jetscale feature-flags list|get|set              # --root | --company [--business-unit|--account]

Billing commands
  jetscale plans list|get
  jetscale licenses list|get

Additional commands
  jetscale api <path> [-X METHOD] [-f k=v] [-F k=v] [--input FILE] [--paginate]
  jetscale config show|set|get|instances           # config set company <slug>
  jetscale system info|diagnostics
  jetscale version [-o json|yaml]
```

Resource commands share one contract, described in
[Resource Commands](docs/resource-commands.md): `-o json` with curated fields or
`-q` for IDs, explicit empty lists, idempotent writes that report created,
already exists (unchanged) or drift and never change a drifted resource,
`--dry-run` on every write, prompts only on a terminal with `-y/--yes` to skip
them, secrets only through `--credential-file`, errors with a `Try:` line and a
`transient` flag, and exit codes 0 ok, 1 failure or uncertain write, 2 usage, 3
not found, 4 conflict, 5 forbidden or unconfirmed, 6 timeout, 7 drift.
`--company` defaults to `config set company`.

The older `prepare basics` and `flags show|plan|apply --profile` commands remain
for the published basics and flags lanes but are hidden from help; use
`companies`, `business-units` and `feature-flags` instead.

For the bounded basics API subset, implemented Stack wrapper with optional
non-secret manifest, and pending unstable API/auth registration, see
[Prepare Basics](docs/prepare-basics.md). Implementation support is not live
execution authority or full readiness.

For enable-only `flags show|plan|apply --profile FILE`, offline validation,
company cascades and application-wide root effects, see
[Feature Flags](docs/feature-flags.md).

Temporary stdin authentication is source-implemented, not an active unstable
automation grant. The proposed Stack wrapper path requires human-reviewed
publication and actual-capture synthetic qualification before real secrets;
basics and flags require separate authorization. A dirty unstable-channel build
is only input, not provenance or authority. Login/signout may invalidate all
operator sessions. See the
[proposal and unchanged exclusions](AGENTS.md#proposed-stack-mediated-temporary-cli-authentication).

## Global Flags

```text
-i, --instance <name>   target a named instance (local, staging, production)
    --local              shorthand for -i local
    --api-url <url>      override API URL directly
    --ca-file <path>     additional trusted CA PEM for authenticated requests
    --credentials-file <path> local-channel per-command auth from a dotenv file
    --login <email>      temporary per-command login; requires --password-stdin
    --password-stdin     bounded password line; temporary memory-only session
    --account <name>     cloud account: ID, provider account ID or name
-o, --output <format>   table, json, yaml (default: table)
```

## Environment Variables

```text
JETSCALE_TOKEN       bearer token (overrides stored auth)
JETSCALE_API_URL     API URL (overrides instance resolution)
JETSCALE_INSTANCE    instance name (overrides config default)
JETSCALE_ACCOUNT     cloud account (overrides stored selection)
```

For endpoint, instance, account, config-directory, and token overrides, non-live
builds use `JETSCALE_LOCAL_` or `JETSCALE_UNSTABLE_` instead of `JETSCALE_`
(e.g. `JETSCALE_LOCAL_API_URL`). Explicit bearer auth uses
`JETSCALE_LOCAL_TOKEN` or `JETSCALE_UNSTABLE_TOKEN` with **no generic
`JETSCALE_TOKEN` fallback**. Live retains `JETSCALE_TOKEN`. Use these variables
only for endpoints that actually support bearer auth; they do not make
cookie-only endpoints compatible. An invalid, empty, or rejected explicit token
does not fall back to stored cookies. Verify the destination before supplying a
token. The `auth login --token` flag has been removed to avoid secrets in argv.

Live keeps the legacy `~/.config/jetscale/` config/token location unchanged,
including when `XDG_CONFIG_HOME` is set. Non-live defaults are
`$XDG_CONFIG_HOME/jetscale/{local,unstable}/`, falling back to
`~/.config/jetscale/{local,unstable}/`. Channel-specific `CONFIG_DIR` overrides
are supported. Stored cookie sessions are additionally bound to the exact API
origin and compiled channel; changing the destination does not reuse another
origin's cookies. Unbound legacy credentials require a fresh login.

**Upgrade and transport compatibility:** Unset the channel token variable and
run a new interactive `auth login` for cookie-based commands; legacy unbound
credentials and the removed `--token` flag cannot be reused. Local file mode is
an alternative for one command only. Authenticated clients require an origin URL
(no `/api/v2` suffix), ignore ambient proxies, and reject every redirect,
including same-origin redirects. HTTPS is required except loopback HTTP, which
cannot carry Secure cookies. Use `--ca-file` for a private CA without disabling
TLS verification. Proxy-only, path-mounted, or redirect-dependent deployments
are not supported by this transport. Windows storage is implemented, but native
Windows auth-runtime validation remains outstanding. See
[auth migration and transport compatibility](docs/local-dev.md#auth-migration-and-transport-compatibility)
for exact binding, storage, and failure-recovery behavior.

## Architecture

```text
┌─────────────────────────────────────────────────────────┐
│  jetscale CLI (this repo)                               │
│                                                         │
│  cmd/jetscale/          ← entrypoint                    │
│  internal/cmd/          ← Cobra command tree             │
│  internal/api/generated/← oapi-codegen typed client      │
│  internal/api/          ← service layer (account tree)   │
│  internal/auth/         ← auth + token storage           │
│  internal/config/       ← channel-scoped configuration  │
│  internal/buildinfo/    ← compiled channel + provenance │
│  internal/output/       ← table / json / yaml formatters │
└────────────────┬────────────────────────────────────────┘
                 │  HTTPS + cookies (local accounts verified)
                 ▼
┌─────────────────────────────────────────────────────────┐
│  JetScale Backend (Jetscale-ai/Backend)                 │
│  FastAPI · OpenAPI 3.0 · /api/v2/*                      │
└─────────────────────────────────────────────────────────┘
```

Application API calls use the **generated Go client**
(`internal/api/generated/client.gen.go`) derived from the Backend OpenAPI
schema. `mage generateClient` overwrites only `client.gen.go`, not the entire
`generated/` directory. It pins `oapi-codegen` to **v2.6.0** without running
`go mod tidy`; `mage generate` calls it and then tidies modules. Schema
processing retains v2 paths, downgrades OpenAPI 3.1 to 3.0, prunes unused
schemas, applies the narrow optional-discriminator pointer workaround, and
deterministically deduplicates operation IDs. See
[generation details](docs/local-dev.md#explicit-schema-sync-and-pinned-generation).

Bindings regenerate automatically; the hand-crafted Cobra workflow UX is
intentionally maintained. Schema changes can require command/service changes,
with compilation and contract tests gating publication rather than promising
maintenance-free compatibility.

### Branch-Matched Artifact Pipeline (Activation Pending)

The single receiver is `.github/workflows/build-cli.yml`, renamed from
`build-unstable.yml`. It accepts `workflow_dispatch` on exactly `main` or
`unstable`, matching the verified Backend producer branch:

| Backend branch | CLI ref    | Channel    | Binary              | Artifact version             |
| -------------- | ---------- | ---------- | ------------------- | ---------------------------- |
| `main`         | `main`     | `live`     | `jetscale`          | `0.0.0-main.RUN.ATTEMPT`     |
| `unstable`     | `unstable` | `unstable` | `jetscale-unstable` | `0.0.0-unstable.RUN.ATTEMPT` |

`RUN` and `ATTEMPT` are the CLI consumer run ID and attempt. The scripts retain
`scripts/unstable_build.py` and `scripts/package_unstable.py` as their names but
support both lanes; offline tests remain in `scripts/test_*.py`.

The Backend two-lane dispatcher is **required work, not implemented**. It must
live on Backend `main`, handle `workflow_run` completion for both branches, and
dispatch only after a successful, completed producer **push**. The exact ten
inputs are `upstream_repo`, `upstream_workflow`, `upstream_run_id`,
`upstream_run_url`, `upstream_sha`, `backend_run_attempt`, `backend_branch`,
`spec_sha`, `spec_version`, and `artifact_name`. The receiver independently
verifies repository/workflow, event, branch, conclusion, run/attempt, Backend
SHA, schema manifest, and raw schema hash, then downloads the exact artifact.
Payload assertions alone are insufficient. Both CLI branches must be reviewed
trusted source: the host verifier runs with a Backend Actions-read token before
the generation sandbox starts.

After trusted tool/dependency prefetch, a network-disabled Docker sandbox based
on `golang:1.25.8-bookworm` runs `processSpec`, `generateClient` (no tidy),
module-file checks, race-enabled Go tests, Mage tests, `go vet`, and packaging.
Only generation uses `GOPROXY=file:///modules/cache/download` for pinned
`go run` metadata lookup from the read-only prefetched cache, with no network
fallback; other phases use `GOPROXY=off`. Docker retains `--network=none`.
Packaging checks offline JSON version metadata and help. The untouched trusted
host validates package files/hashes and rechecks producer evidence before
uploading `<binary>-linux-amd64-<cli_run_id>-<attempt>` with 14-day retention
and overwrite disabled. The package contains only the lane's binary,
`SHA256SUMS`, `raw-schema.json`, `processed-schema.json`, `manifest.json`, and
`provenance.json`. The previous Docker rehearsal used a real raw Backend export
but synthetic GitHub provenance and the old unstable-only receiver. It is
historical evidence, not validation of this two-lane implementation, actual
upstream CI, or runtime E2E behavior. See the
[qualified local rehearsal record](docs/local-dev.md#unstable-receiver-implementation-and-validation-limits).

Both lanes publish **only immutable Linux amd64 Actions artifacts**: no tags,
GitHub Releases, Homebrew updates, branch writes, stable promotion, deployment,
or Backend API writes. A `live` compiled channel is not stable release parity or
permission to access production. More OS/architecture targets are follow-up
work, not promised coverage.

CLI release version stays independent. Backend `info.version` is descriptive,
not sufficient identity: provenance must bind Backend SHA, raw and processed
schema SHA-256, CLI SHA, and producer/consumer run attempts. Offline `version`
supports text, `-o json`, and `-o yaml`, exposing compiled channel, Backend SHA,
schema hash, API version, source hash, and dirty state alongside CLI metadata.
JSON/YAML keys include `channel`, `backend_sha`, `schema_sha256`, `api_version`,
`source_sha256`, and `source_dirty`. Unknown values are not compatibility
evidence; a local Backend SHA override is not CI proof.

**Landing order (human review and activation pending):** CLI `main` first to
register `build-cli.yml` and retire `sync-openapi.yml` as a fail-closed manual
tombstone with no writes or legacy `repository_dispatch` acceptance; then CLI
`unstable` with the same complete sources, tools, and workflow. Backend's
`pipeline.yml` / `build-release.yml` producers on `main` and `unstable` must
export the same manifest contract with the actual branch. Generalize the
completion dispatcher on Backend `main` to both branches and remove the inline
main legacy sync dispatch. The exact dispatch and landing handoff is in
[docs/backend-cli-builds.md](docs/backend-cli-builds.md).

Existing scoped token permissions and downstream consumers still need
verification. The authority proposal in [AGENTS.md](AGENTS.md) remains pending
human ratification; no real new CI run is proved. Schema exchange/publication
stays in GitHub Actions artifacts; no Unbrowse or third-party capture service is
used. No runtime credentials or billing records belong in schema/build
artifacts. GitHub verification credentials stay outside the generation sandbox.
See
[sandbox requirements](AGENTS.md#proposed-branch-matched-artifact-only-publication).

**Ordinary CI is separate:** `.github/workflows/ci.yml` handles PRs and pushes
for `main`/`unstable`, with docs-only push exclusions. It runs lint, Python
contract tests, and race-enabled Go tests. PRs and unstable pushes also
cross-build Linux/macOS/Windows amd64/arm64 without publishing CLI binaries;
this matrix is not Windows auth-runtime validation. The new `test-windows-auth`
job uses `windows-2025` to run
`go test -count=1 -timeout=5m ./internal/auth ./internal/cmd` within a 10-minute
job limit, with a read-only contents token, no persisted checkout credentials,
and Go setup caching disabled. Both build and release depend on it. The first
native run failed; repair confirmation requires a passing run after human
landing. `build-cli.yml` remains the separate workflow-dispatched, schema-bound
Linux amd64 artifact receiver.

**Stable integration gap:** Only eligible main pushes enter `ci.yml`'s separate
tag/Release/Homebrew path. Release checkout pins `github.sha` and fails on a
HEAD mismatch instead of following moving `main`. It consumes the committed
schema and generated client. Workspace-only artifact builds do not refresh those
committed inputs, trigger a stable release, or prove stable release parity.
Durable schema integration needs a separate reviewed change.

## Local Development

Run from this repository with Go, Mage, and module dependencies already cached:

```bash
mage buildLocal                 # offline → bin/jetscale-local
./bin/jetscale-local version     # offline metadata inspection
./bin/jetscale-local --help      # offline command discovery
mage installLocal               # human invocation: atomic ~/.local/bin install
```

`buildLocal` and `installLocal` neither fetch a schema nor run code generation.
They use the current sources, generated client, and `openapi/spec.json`, with
module downloads disabled. A missing local toolchain or dependency cache must be
resolved separately. `installLocal` rebuilds and atomically installs only
`jetscale-local`; `JETSCALE_LOCAL_BIN_DIR` selects an absolute user-owned
install directory. It does not replace stable `jetscale` or edit shell
configuration.

Refresh the schema deliberately, against an intended running Backend:

```bash
mage syncSpecFrom https://jetscale.internal  # explicit network/schema update
mage generate                                # pinned generator; may download tools
mage buildLocal
```

See [docs/local-dev.md](docs/local-dev.md) for channel-specific development,
generation, validation, and installation details. The
[credential-free validation record](docs/local-dev.md#credential-free-validation)
records the parent-observed final local checks and all hooks passing. Native
Windows runtime, remote two-lane CI, a new two-lane Docker rehearsal, and
unstable/production runtime auth remain unproved. These are not new runs
performed by this documentation update.

### Build Targets

```bash
mage build          # compile bin/jetscale (live channel)
mage buildLocal     # offline bin/jetscale-local; no schema fetch
mage installLocal   # explicit human-run atomic user install
mage test           # go test -race ./...
mage lint           # golangci-lint
mage syncSpec       # fetch OpenAPI from running backend
mage generateClient # pinned oapi-codegen v2.6.0; no tidy
mage generate       # generateClient + go mod tidy
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
