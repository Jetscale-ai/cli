# Local development

Last reviewed: 2026-09-14 against the working-tree `magefile.go`, `internal/`,
`openapi/spec.json`, and `scripts/`; branch-matched build documentation now
tracks `.github/workflows/build-cli.yml` and the auth/release gates in
`.github/workflows/ci.yml`. Local checks are reported below; native Windows
runtime, remote two-lane CI, and a new two-lane Docker rehearsal remain
unproved.

## Agent workspace setup

For agent-assisted development, keep a `governance` checkout beside `cli` and
follow the [managed workspace guide](../.agents/README.md). Run materialization
and `pnpm govern --repo ../cli` from Governance after changing imports or
updating that checkout. The repository-local operating source is
[`.agents/agents.md`](../.agents/agents.md); the uppercase path remains a
compatibility symlink.

This setup is independent of the Go toolchain and the fast build loop below.
Missing Governance links do not prevent offline CLI builds, but agents must not
invent missing law or edit through imported symlinks. No `.env`, cookie store,
API capture service, or live test is part of governance setup.

## Fast edit/build loop

Use Go 1.25 or newer (see `go.mod`), Git, and an existing Mage installation.
Dependencies must already be in the Go module cache. No Backend, cloud
credentials, login, or inference access is needed to build or run offline help.

```sh
mage buildLocal
./bin/jetscale-local version
./bin/jetscale-local version -o json
./bin/jetscale-local version -o yaml
./bin/jetscale-local --help
./bin/jetscale-local config show
```

`BuildLocal` compiles the current source and checked-in generated client into
`bin/jetscale-local`. It does not fetch a schema, regenerate code, load `.env`,
install tools, or contact a Backend. Go dependency fetching and automatic
Go-toolchain downloads are disabled; missing dependencies fail the build rather
than silently downloading them. Provision dependencies separately before using
this loop. `mage build` continues to produce the live `bin/jetscale` binary.

## Optional user-bin install

Run this yourself when you want the local binary on your PATH:

```sh
mage installLocal
```

This rebuilds first, then installs only `jetscale-local` to `~/.local/bin`.
Ensure that directory is already on your PATH; the target does not modify shell
startup files. `JETSCALE_LOCAL_BIN_DIR` may select another absolute, user-owned
bin directory. Do not run this with elevated privileges.

The install copies into a temporary regular file in the destination directory,
sets mode `0755`, syncs and closes it, then atomically renames it into place.
Existing regular files can be replaced, but source/target symlinks, non-regular
files, and symlink directory components are rejected. A hard link to `jetscale`
is replaced as a directory entry without modifying the live inode. This assumes
an operator-owned directory, not a directory concurrently controlled by an
untrusted writer. It does not execute the installed binary. There is no unstable
installer function or automatic installation/update.

## Stable and unstable installation

Stable `jetscale` keeps the existing Homebrew/GitHub Release installation path
in [README.md](../README.md#install). Local installation uses only the explicit
human-run `mage installLocal` above.

The intended unstable UX is an explicit human download from an exact reviewed
CLI run and attempt-specific GitHub Actions artifact, followed by checksum and
provenance checks, then chmod/install into a user-owned bin directory. The
[unstable download template](../README.md#unstable-ci-artifact-intended-ux-activation-pending)
uses clearly marked run/attempt placeholders. Match provenance to reviewed
Actions evidence and the deployed Backend SHA before enabling execution; do not
use a branch tip, API version, or latest-by-name lookup as a substitute. The
implemented artifacts are Linux amd64 only and retained for 14 days. No
automatic run/update or live activated artifact is claimed.

The main-branch Actions artifact is separate from stable delivery: Backend
`main` builds CLI `main` as live-channel `jetscale`, version
`0.0.0-main.RUN.ATTEMPT`. It requires the same exact-run checksum/provenance
review; installing it over stable replaces `jetscale` and uses the same live
configuration. Backend `unstable` builds CLI `unstable` as `jetscale-unstable`,
version `0.0.0-unstable.RUN.ATTEMPT`. Neither dispatch lane creates a stable
tag, Release, or Homebrew update. `RUN` / `ATTEMPT` refer to the CLI consumer.
See [the exact build/landing handoff](backend-cli-builds.md).

## Compiled channels

| Channel          | Default instance | Default endpoint               |
| ---------------- | ---------------- | ------------------------------ |
| `live` (default) | `production`     | `https://console.jetscale.ai`  |
| `local`          | `local`          | `https://jetscale.internal`    |
| `unstable`       | `unstable`       | `https://unstable.jetscale.ai` |

Channel is compiled in, not selected by renaming the binary or an environment
variable. Unknown or empty compiled channels fail validation before config or
API operations. A missing non-live instance fails instead of falling back to
live. Named/custom instances and explicit `--api-url` overrides remain
supported, including an intentional production selection; channels are safe
defaults, not an access-control boundary.

Resolution order remains:

1. `--api-url`.
2. The channel's `API_URL` environment variable.
3. `-i` / `--instance` or `--local` (shorthand for `-i local`).
4. The channel's `INSTANCE` environment variable.
5. Saved `default_instance`.
6. The compiled channel's default instance.

Live retains its legacy `local` instance with `api_url: auto` and loopback
probing. In local and unstable builds, the built-in `local` instance instead
uses `https://jetscale.internal`, with no probe or fallback. Explicitly saved
custom `api_url: auto` values still opt into the legacy loopback probe.

```sh
./bin/jetscale-local config set instance.devbox https://devbox.example.com
./bin/jetscale-local -i devbox config show
./bin/jetscale-local --api-url http://localhost:8010 config show
```

## Config isolation and migration decision

Live keeps **exactly** its legacy location, `~/.config/jetscale/config.yaml`, or
the explicit `JETSCALE_CONFIG_DIR` override. It does not newly adopt
`XDG_CONFIG_HOME`; moving live config now would break existing operator state.
There is no automatic cross-channel migration or copying of config, cookies, or
tokens. Matching origin/channel-bound legacy aliases have a limited in-store
migration path described below; unbound legacy credentials cannot be reused.

Local and unstable use, respectively:

- `XDG_CONFIG_HOME/jetscale/local` and `XDG_CONFIG_HOME/jetscale/unstable` when
  `XDG_CONFIG_HOME` is set to an absolute path;
- when unset, `~/.config/jetscale/local` and `~/.config/jetscale/unstable`.

A nonempty relative `XDG_CONFIG_HOME` is rejected, not silently ignored.

Each contains its own `config.yaml` and protected `tokens.yaml` session store.
Explicit directory overrides are exact paths; pointing two channels to the same
directory removes filesystem separation. Stored cookie sessions remain keyed and
checked by exact API origin and compiled channel, not just instance alias.

| Purpose          | Live                  | Local                       | Unstable                       |
| ---------------- | --------------------- | --------------------------- | ------------------------------ |
| Endpoint         | `JETSCALE_API_URL`    | `JETSCALE_LOCAL_API_URL`    | `JETSCALE_UNSTABLE_API_URL`    |
| Instance         | `JETSCALE_INSTANCE`   | `JETSCALE_LOCAL_INSTANCE`   | `JETSCALE_UNSTABLE_INSTANCE`   |
| Config directory | `JETSCALE_CONFIG_DIR` | `JETSCALE_LOCAL_CONFIG_DIR` | `JETSCALE_UNSTABLE_CONFIG_DIR` |
| Account override | `JETSCALE_ACCOUNT`    | `JETSCALE_LOCAL_ACCOUNT`    | `JETSCALE_UNSTABLE_ACCOUNT`    |
| Token            | `JETSCALE_TOKEN`      | `JETSCALE_LOCAL_TOKEN`      | `JETSCALE_UNSTABLE_TOKEN`      |

Generic endpoint, instance, config, account, and token variables are ignored by
non-live builds. `auth.Session` gives an explicitly set channel `TOKEN` variable
precedence over stored sessions; there is no `JETSCALE_TOKEN` fallback in local
or unstable builds. Explicit tokens use bearer auth only for endpoints that
actually support it. Invalid, empty, or rejected explicit tokens do not fall
back to stored cookies. A supplied token is an explicit choice for the resolved
destination; verify that destination before use. Unbound legacy stored
credentials require a fresh login.

## Auth migration and transport compatibility

These rules describe `internal/auth/session.go` and the command session
lifecycle, not a blanket claim of deployed or cross-platform compatibility.

- **Re-login after upgrade:** `auth login --token` is removed. Unset the
  channel's `TOKEN` variable (even an empty value blocks cookie login), verify
  the destination, and run interactive `auth login`. Unbound legacy alias
  entries are rejected; adding a destination override does not bind them.
  Existing bearer-only automation must use a channel token variable only where
  the endpoint supports bearer auth. Stored expired bearer tokens are rejected,
  not automatically refreshed or converted to cookies.
- **Binding and alias migration:** Sessions use `channel|origin` keys. Origin
  includes scheme, lowercased hostname, and port, with default ports normalized
  away. Different aliases for the same origin/channel share a session; a
  different origin or channel requires its own credentials. A legacy alias is
  eligible only if its saved origin/channel already match and no canonical entry
  exists. Saving migrates those matching aliases. An empty canonical entry after
  logout prevents fallback to stale legacy credentials.
- **API URL shape:** Authenticated clients accept an HTTP(S) origin, optionally
  with a trailing `/`, but no userinfo, path prefix, query, or fragment. Use
  `https://host`, not `https://host/api/v2`. Path-mounted API deployments are
  not supported by this client.
- **Direct transport only:** Authenticated requests ignore ambient proxy
  settings, including `HTTP_PROXY` and `HTTPS_PROXY`, and do not inherit
  `http.DefaultTransport`. Proxy-required networks need a separately reviewed
  compatibility change, not an assumed environment override. All 3xx responses
  fail, including same-origin redirects: use the final origin and ensure the
  generated endpoint routes do not redirect. Redirect-only SSO or ingress flows
  are not supported. Requests to another origin or with an overridden Host are
  blocked. The client timeout is 30 seconds per request.
- **TLS and loopback:** HTTPS is required except for `localhost` or literal
  loopback HTTP. Plaintext `localhost` dials `127.0.0.1` without DNS resolution;
  use `[::1]` explicitly for IPv6. Secure cookies are not sent over HTTP, so
  allowing loopback HTTP does not guarantee login compatibility. `--ca-file`
  extends system roots with a PEM bundle; TLS 1.2 minimum, certificate and
  hostname checks remain enabled. The Mage-only `JETSCALE_LOCAL_CA_FILE`
  variable does not configure authenticated clients.
- **Cookie lifecycle and storage:** Login response cookies remain opaque; bearer
  tokens in the login body are ignored. Persistent mode saves cookie rotations
  and deletions even when a command fails. Max-Age is stored as absolute expiry,
  not restarted on reload; session cookies without expiry survive CLI exits
  until deleted or signed out. Partitioned cookies are unsupported. The store is
  a private regular `tokens.yaml`, not a symlink. Contents are plaintext,
  protected by Unix modes or Windows ACLs, not an OS keychain or DPAPI. The
  platform-specific checks and concurrency limits are described below.
- **Failure recovery:** Sign-out requires a 2xx response; a 401 does not prove
  server invalidation. A failed sign-out retains credentials except for explicit
  server cookie deletions. Failed verification attempts sign-out; uncertain
  sign-in transport failures may leave a server session, with cleanup unverified
  and no automatic retry. A failed save of a fresh interactive login also
  attempts sign-out. Nonzero cleanup/save results remain failures even if the
  command printed successful output.

For local automation, `--credentials-file` replaces persistent login with a
single in-memory session, not a stored login for later commands. See the two
flows below. Neither channel binding nor the file-mode allowlist grants runtime
authority or prevents Backend login from closing other sessions for that user.

### Session-store security and concurrent commands

`internal/auth/token_storage_unix.go` retains `0600` file creation and rejects
files with group/other permissions on read. Store directories must not be
symlinks or group/other-writable; existing `0755` directories are accepted, and
new directories are created with `0700` permissions.

`internal/auth/token_storage_windows.go` validates ownership, type, and ACLs
through open handles. Trusted principals are the current user, SYSTEM, and
Administrators. New store files and directories receive private protected ACLs.
Existing permissive files are rejected rather than silently repaired, including
before replacement. Directory ACLs may allow others read/traverse access, but
not write access; existing directory ACLs are never changed. Junctions and other
reparse points anywhere in the config path are rejected, as are unsupported
access-control entries (ACEs). These checks do not encrypt the plaintext
credentials or protect them from the trusted principals.

Entry updates hold the `.tokens.lock` sidecar's OS interprocess lock across the
entire read-modify-write, then replace `tokens.yaml` atomically. Lock
acquisition waits at most five seconds. Concurrent updates to different entries
preserve one another; updates to the same origin/channel session remain
last-writer-wins, not a merge of concurrent cookie changes or an independent
server-session guarantee. The lock is released when its handle closes or its
process exits. The sidecar remains in place: do not delete or replace it as a
stale-lock fix. The existing `golang.org/x/sys` dependency is now direct at the
same version, `v0.41.0`, for platform ACL/locking support.

**Platform errors:** Unsafe owner/ACL, unsupported ACE, reparse-path, file-type,
or Unix permission errors stop storage access; there is no insecure fallback.
Have the owner review permissions or select a compliant, non-reparse config
location using the channel's config-directory override. Do not weaken checks,
copy credentials into logs, or expect the CLI to repair existing Windows
directories. A lock timeout reports
`token store is busy: lock wait timed out; retry the command`. Let the competing
command finish and inspect its outcome before retrying, especially if the failed
command may already have made a server request. Save/cleanup errors remain
nonzero failures.

**Evidence limit:** The Windows implementation and native CI gate exist in
source; native Windows runtime tests have not run. See
[credential-free validation](#credential-free-validation) for the
parent-observed final checks and all-hooks pass. Cross-builds and vet do not
prove Windows ACL or locking behavior at runtime.

## Local cookie authentication

Cookie auth is implemented in the CLI through origin- and channel-bound
`auth.Session`. Login verifies the session with `/api/v2/auth/me` before it can
be saved. `auth whoami`, `auth status`, `auth logout`, and authenticated API
commands use the session. Account listing no longer skips company-less
superusers. This auth change does not require API regeneration or Backend edits.

**Local file-mode account listing passed.** The
[local validation record](#local-validation-evidence) below describes the exact
operator-reported execution, not proof of every example or auth workflow.
Persistent interactive runtime validation, OS keychain storage, independent
server sessions, and real unstable/production tests remain pending.

**Browser impact:** Backend login closes prior sessions for the same user,
including browser sessions. Both login modes below invoke that existing
behavior; a temporary CLI session is not an independent server-session
guarantee.

### Persistent interactive session

From `cli/`, rebuild the local binary, then sign in without
`--credentials-file`:

```sh
mage buildLocal
./bin/jetscale-local -i local --ca-file ../stack/certs/jetscale-dev-ca.pem auth login
./bin/jetscale-local -i local --ca-file ../stack/certs/jetscale-dev-ca.pem auth whoami
./bin/jetscale-local -i local --ca-file ../stack/certs/jetscale-dev-ca.pem accounts list
./bin/jetscale-local -i local --ca-file ../stack/certs/jetscale-dev-ca.pem auth logout
```

Login requires a TTY, prompts for email and a hidden password, and persists the
verified cookie session in the atomically replaced `tokens.yaml` store in the
channel's config directory, protected by `0600` on Unix or private ACLs on
Windows. Passwords are not stored; saved credentials are plaintext, not OS
keychain- or DPAPI-encrypted. Subsequent commands reuse the stored session for
the same origin and channel. `auth status` checks that session against the
server; `auth logout` signs out and clears its stored credentials on success.

The `auth login --token` flag has been removed to avoid secrets in argv. Unset
`JETSCALE_LOCAL_TOKEN` when using cookies so an explicit bearer override does
not mask the stored session.

### Temporary credentials-file session

Use the existing Stack `.env` directly, without sourcing it or copying its
credentials into command arguments:

```sh
./bin/jetscale-local -i local --ca-file ../stack/certs/jetscale-dev-ca.pem --credentials-file ../stack/.env accounts list
```

The root `--credentials-file` flag:

- works only in a compiled local-channel build targeting HTTPS
  `jetscale.internal` or a loopback API (HTTP or HTTPS);
- reads only `JETSCALE_DEFAULT_ADMIN_EMAIL` and
  `JETSCALE_DEFAULT_ADMIN_PASSWORD`, without shell evaluation or variable
  expansion; other keys are ignored;
- requires the channel `TOKEN` variable to be unset, even if its value is empty;
- signs in for each authenticated command, keeps its session in memory, and
  signs out on command success or error without saving credentials;
- returns nonzero if cleanup fails, even if the requested read succeeded.

With `--credentials-file`, `auth login` only verifies login and then signs out.
It does not prepare a session for the next command: pass the flag again to
`accounts list`, or use the persistent interactive flow instead. Do not combine
file mode with `auth logout`; file mode already performs its own sign-out.

A failed or uncertain sign-in/sign-out can leave server state unverified. Treat
cleanup errors as failures, not proof that the server session was removed. Do
not include passwords, cookies, or `.env` contents in validation evidence.

### Local validation evidence

On 2026-09-14, the operator reported a successful rebuild and local runtime test
from `cli/`:

```sh
mage buildLocal
./bin/jetscale-local --api-url https://jetscale.internal --ca-file ../stack/certs/jetscale-dev-ca.pem --credentials-file ../stack/.env accounts list -o json
```

- `mage buildLocal` rebuilt `bin/jetscale-local`.
- The CLI invocation exited 0 and verified login through `/api/v2/auth/me`
  within the CLI.
- JSON output contained 20 accounts: 2 AWS, 4 AZURE, and 14 GCP. Account names,
  IDs, and raw output are intentionally omitted.
- Temporary sign-out was confirmed; no credentials were persisted.
- Focused race tests for `internal/cmd`, `internal/auth`, and `internal/api`
  passed. Final broader validation also passed (operator-reported):
  `go test -race -count=1 ./...`, `go test -count=1 -tags mage .`,
  `go vet ./...`, `GOTOOLCHAIN=go1.25.8 mage lint` (0 issues),
  `mage buildLocal`, and `git diff --check`. Plain `mage lint` initially
  panicked under host Go 1.26 because the installed linter was compiled with Go
  1.25; matching `GOTOOLCHAIN` resolved it without dependency changes.

This is ephemeral evidence of the exact local execution reported in the session,
not a durable build artifact or CI attestation. It is separate from the
synthetic GitHub-provenance sandbox rehearsal below and does not prove
unstable/production behavior, persistent interactive login, or other
authenticated workflows. No Backend changes were made. No credentials or raw
account data are included in this record.

## Explicit schema sync and pinned generation

Only sync when you intentionally want the running Backend's schema. This
contacts the named Backend and changes `openapi/spec.json`:

```sh
mage syncSpecFrom https://jetscale.internal
mage generate
mage buildLocal
```

TLS certificate and hostname verification remain enabled. Install your local CA
into system trust yourself, or set `JETSCALE_LOCAL_CA_FILE` to an existing
trusted PEM CA bundle for `SyncSpecFrom`. That optional bundle extends system
trust; missing/invalid bundles fail closed. Redirects are refused: pass the
final Backend URL explicitly. There is no insecure TLS switch. This Mage-only CA
setting does not configure CLI authentication/API clients: use the root
`--ca-file` flag to add a trusted CA PEM for authenticated requests. It extends
system trust while retaining certificate and hostname verification; an invalid
or missing file fails closed.

`GenerateClient` invokes
`go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.6.0` without
tidying dependencies. It overwrites only `internal/api/generated/client.gen.go`,
not the whole directory. It ignores any ambient `oapi-codegen` binary and does
not install a global tool. `Generate` calls `GenerateClient`, then runs
`go mod tidy`; review generated code and module changes. Explicit local
generation may download the pinned module and dependencies into Go's cache. The
branch-matched receiver instead prefetches on the trusted host, runs
`mage processSpec` followed by `mage generateClient` offline, and rejects any
`go.mod`/`go.sum` changes. Generation alone sets
`GOPROXY=file:///modules/cache/download`: pinned `go run` needs metadata lookup
from the read-only prefetched cache even with the module already cached. There
is no network fallback; other phases retain `GOPROXY=off`, and Docker retains
`--network=none`. `BuildLocal` never runs either operation implicitly.

`ProcessSpec` filters v2 paths, prunes orphaned schemas, and converts OpenAPI
3.1 nullable forms to 3.0.3. `patchOptionalDiscriminatorPointers` adds trusted
`x-go-type-skip-optional-pointer` metadata only for optional, non-nullable,
nonempty string-constant discriminators of locally referenced union variants. It
preserves required/default/const and omission semantics while avoiding the
pinned generator's string-to-pointer assignment problem. Operation-ID collisions
are resolved deterministically in sorted path/method order, preserving the first
ID and reserving all original IDs before adding suffixes.

The refreshed `openapi/spec.json` and generated client include custom
recommendation regeneration and streaming regeneration with
`RegenerateCustomRecommendationRequest.excluded_source_keys`, plus
recommendation-agent operation list/detail and operation-bound FOCUS
inspection/rows. Their Cobra commands remain pending; AWS-sourced
`recommendations list` / `show` is not the multi-cloud workflow. Bindings are
automatic, but intentional command UX/service changes remain hand-maintained and
must pass compilation and contract tests.

## Offline provenance

`version` prints the invoked executable name, existing version/commit/build-date
fields, channel, Backend SHA, schema SHA-256, API version, source SHA-256, and
source dirty state without loading config or contacting a service. Text,
`version -o json`, and `version -o yaml` are implemented. Structured keys are
`binary`, `version`, `commit`, `date`, `channel`, `backend_sha`,
`schema_sha256`, `api_version`, `source_sha256`, and `source_dirty`.

Local builds record the full CLI Git HEAD, a hash of the exact
`openapi/spec.json` bytes, its `info.version`, and a deterministic hash of
Git-listed current Go files plus `go.mod`/`go.sum` (including non-ignored
untracked sources). Dirty state includes tracked changes and non-ignored
untracked files, not just source changes. Source hashing is evidence of these
inputs, not a reproducible-build or runtime compatibility guarantee.

Backend SHA defaults to `unknown`: a sibling checkout is not proof of which
Backend produced a downloaded schema. Set `JETSCALE_LOCAL_BACKEND_SHA` only when
you have verified that association. No runtime SHA discovery is performed.

Release/branch-matched build automation can supply these exported string linker
fields directly (empty provenance values display as `unknown`):

```text
github.com/Jetscale-ai/cli/internal/buildinfo.Channel=unstable
github.com/Jetscale-ai/cli/internal/buildinfo.BackendSHA=<verified SHA>
github.com/Jetscale-ai/cli/internal/buildinfo.SchemaSHA256=<schema digest>
github.com/Jetscale-ai/cli/internal/buildinfo.APIVersion=<API version>
```

Use a `-X` assignment for each field in `go build -ldflags`. Existing
`internal/cmd.version`, `internal/cmd.commit`, and `internal/cmd.date` linker
fields are unchanged. Build-channel defaults do not depend on the output name;
help and command examples use the actual executable name.

## Unstable receiver implementation and validation limits

This heading is retained for existing links; the current receiver is the single
renamed `.github/workflows/build-cli.yml`. It accepts `workflow_dispatch` only
on exactly `main` or `unstable`, matching the verified Backend producer branch.
Local checks have been reported; human review/activation, the required Backend
two-lane dispatcher, remote CI, and a new two-lane Docker rehearsal remain
outstanding.

| Backend branch | CLI ref    | Channel    | Binary              | Artifact version             |
| -------------- | ---------- | ---------- | ------------------- | ---------------------------- |
| `main`         | `main`     | `live`     | `jetscale`          | `0.0.0-main.RUN.ATTEMPT`     |
| `unstable`     | `unstable` | `unstable` | `jetscale-unstable` | `0.0.0-unstable.RUN.ATTEMPT` |

`RUN` / `ATTEMPT` are the CLI consumer run ID and attempt. The exact ten inputs
are `upstream_repo`, `upstream_workflow`, `upstream_run_id`, `upstream_run_url`,
`upstream_sha`, `backend_run_attempt`, `backend_branch`, `spec_sha`,
`spec_version`, and `artifact_name`. `scripts/unstable_build.py` and
`scripts/package_unstable.py` retain their names but support both lanes. The
verifier independently checks successful completed Backend push evidence, branch
matching, exact run/attempt/SHA, manifest, and schema hash. Both CLI branches
must be reviewed trusted source because the host verifier runs with a Backend
Actions-read token; the later sandbox does not protect against untrusted host
verifier code.

Processing/generation/tests/packaging remain inside the network-disabled Docker
sandbox based on `golang:1.25.8-bookworm`. Packaging builds only Linux amd64 and
checks offline JSON version metadata and help. The untouched trusted host
validates package hashes and provenance without executing the binary, rechecks
producer evidence, and uploads the immutable
`<binary>-linux-amd64-<cli_run_id>-<attempt>` with 14-day retention and
overwrite disabled. Packages contain only the lane's binary, `SHA256SUMS`,
`raw-schema.json`, `processed-schema.json`, `manifest.json`, and
`provenance.json`.

The workflow checks unchanged modules, race-enabled Go tests, Mage tests, and
`go vet` before packaging. Its bounds include a 40-minute job, a 20-minute
sandbox process timeout, no network/credentials in the sandbox, narrow mounts,
and explicit resource limits and cleanup. The full proposed requirements are in
[AGENTS.md](../AGENTS.md#proposed-branch-matched-artifact-only-publication).

**Historical old-receiver evidence only:** The full Docker sandbox rehearsal
passed locally on 2026-09-14 using a real raw Backend export and the
then-current unstable-only receiver. Processing, generation, race-enabled Go
tests, Mage tests, vet, real packaging with version/help checks, and trusted
host validation passed within the 15-minute rehearsal budget. GitHub provenance
was synthetic. This does not validate the new `build-cli.yml`, branch matching,
or two-lane packaging, and is not proof of actual upstream completion, receiver
CI, publication, or authenticated semantic E2E behavior. The separate
[credential-free validation record](#credential-free-validation) below captures
reported local checks of the newer implementation, not a new Docker rehearsal.

The local `bin/.LOCAL-REHEARSAL-20260914-162741/RESULT.txt` records success and
explicitly says synthetic provenance only, not published. It and the rehearsal
artifacts are ignored, ephemeral agent outputs—not durable committed proof.
Canonical/local authority remains proposed. Human landing starts with CLI `main`
to register `build-cli.yml` and retire `sync-openapi.yml` as a fail-closed
manual tombstone with no writes or legacy `repository_dispatch` acceptance; then
CLI `unstable` receives the same complete sources, tools, and workflow. Backend
`pipeline.yml` / `build-release.yml` producers on `main` and `unstable` must
export the same manifest contract with the actual branch. The generalized
Backend dispatcher is required work, not implemented: it must live on `main`,
handle completed `workflow_run` events for both branches, and dispatch only
successful pushes. Removal of inline main legacy sync dispatch is also required.
Follow [the exact dispatch/landing handoff](backend-cli-builds.md). Human
ratification, scoped token/consumer verification, a new two-lane Docker
rehearsal, and real producer/receiver CI evidence for both lanes remain gates.
Historical rehearsal success does not satisfy them or grant agent write
permissions. Neither lane may call Backend application APIs, regenerate
recommendations, install binaries, update branches, or publish stable
tags/Releases/Homebrew.

**Ordinary CI versus the receiver:** `ci.yml` runs on PRs and pushes for
`main`/`unstable`, with docs-only push exclusions. It runs lint, Python contract
tests and race-enabled Go tests; PRs and unstable pushes also cross-build
Linux/macOS/Windows amd64/arm64 without publishing CLI binaries. That matrix
checks compilation, not Windows auth-runtime behavior. The new
`test-windows-auth` job runs natively on `windows-2025` with
`go test -count=1 -timeout=5m ./internal/auth ./internal/cmd`, a 10-minute job
limit, `contents: read`, checkout credential persistence disabled, and Go setup
caching disabled. Both `build` and `release` depend on it. It has not run yet;
the gate runs after human landing on an eligible CI event. This credential-free
job does not prove deployed Backend authentication. `build-cli.yml` remains a
separate workflow-dispatch receiver with Linux amd64 packaging and provenance
checks; a green ordinary CI run does not establish receiver success.

**Stable integration gap:** Only eligible main pushes enter `ci.yml`'s stable
release job. Its checkout now pins `github.sha` and fails if HEAD differs from
the tested event SHA, rather than following moving `main`. These releases
consume the committed schema/generated client, not the disposable workspace
output of the artifact builds. Main's live-channel artifact does not
auto-refresh those committed inputs or prove stable release parity. Durable
schema integration requires a separate reviewed change.

Schema exchange/publication stays in GitHub Actions artifacts, with no Unbrowse
or third-party capture service. Runtime credentials and billing records do not
belong in schema/build artifacts. Host GitHub verification credentials are never
passed to generation, tests, or the packaged binary.

## Credential-free validation

The pre-commit lint hook pins `GOTOOLCHAIN=go1.25.8` internally so normal
commits do not load Go 1.26 packages into the cached Go 1.25-built linter. The
pinned toolchain must be installed or downloadable through Go's normal toolchain
resolution. This does not change the shell's Go version or pin other commands;
use `GOTOOLCHAIN=go1.25.8 mage lint` with a Go 1.25-built standalone linter. The
Go validation commands listed below were also run with that explicit toolchain
selection.

**Final local validation (2026-09-14, observed in the parent session): PASSED.**
The completed broader rerun includes:

- `GOTOOLCHAIN=go1.25.8 mage test`.
- `go test -race -count=1 -timeout=180s ./...` and
  `go test -count=1 -tags mage .`.
- `go vet ./...` and `mage lint` (0 issues).
- `mage crossBuild`: all six Linux/Darwin/Windows amd64/arm64 targets.
- Windows amd64 and arm64 auth-test cross-compilation and `go vet ./...` for
  both architectures; no native Windows test execution.
- 125 Python tests.
- Actionlint for three workflows.
- Gitleaks redacted candidate scan.

**Final hook validation (2026-09-14, parent-observed): PASSED.** All 125 Python
tests and every hook passed, including codespell. The Unicode test filename
correction retained non-ASCII coverage. This records local validation, not a
staged-file check or native Windows/remote execution.

These are supplied parent-observed results, not checks rerun by this
documentation update. Native Windows runtime tests have **not** run; the new
`test-windows-auth` gate in `ci.yml` runs after human landing. Remote new
two-lane CI, a new two-lane Docker rehearsal, and unstable/production runtime
authentication remain unproved. Cross-compilation and vet do not establish
Windows ACL or locking behavior at runtime. The earlier operator-reported local
account-listing result remains separate runtime evidence, limited to the exact
invocation recorded above.

```sh
python3 -B -m unittest discover -s scripts -p 'test_*.py'
go test -count=1 ./internal/buildinfo ./internal/config ./internal/auth ./internal/api ./internal/cmd
go test -count=1 -tags=mage .
```

Mage tests exercise generator argument pinning with a stub process, trusted and
untrusted TLS with loopback test servers, and atomic installs in temporary
directories. Script tests cover verifier rejection, packaging, and embedded
workflow contracts without a full Docker/GitHub Actions run. These checks do not
contact a running Backend or cloud/inference service. For a repo-local temporary
sandbox, set `TMPDIR` to an existing absolute subdirectory under `bin/` when
running these tests.
