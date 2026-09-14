# Backend-triggered CLI builds

Status: CLI receiver source implemented with reported local validation. The
Backend two-lane completion dispatcher is **required work, not implemented**.
Human review, branch placement, token access, a new two-lane Docker rehearsal,
and real upstream CI verification remain outstanding. This document is a Backend
handoff, not a claim that the intended dispatcher exists. No Backend files are
changed by this CLI documentation update.

## Branch and artifact contract

| Backend successful push | CLI dispatch ref | Compiled channel | Binary              | Build version                           |
| ----------------------- | ---------------- | ---------------- | ------------------- | --------------------------------------- |
| `main`                  | `main`           | `live`           | `jetscale`          | `0.0.0-main.<cli_run_id>.<attempt>`     |
| `unstable`              | `unstable`       | `unstable`       | `jetscale-unstable` | `0.0.0-unstable.<cli_run_id>.<attempt>` |

Both use `.github/workflows/build-cli.yml`. The workflow must be registered on
CLI's default branch (`main`) and present on the selected branch. GitHub selects
that branch's commit at dispatch; checkout and provenance pin the resulting
`GITHUB_SHA`, never a later branch tip. Cross-branch inputs are rejected.

Each build generates bindings in a disposable workspace, checks unchanged module
files, runs race tests, Mage tests and vet, then packages a Linux amd64 binary.
It does not commit generated code or call the application API.

Artifacts are named `<binary>-linux-amd64-<cli_run_id>-<attempt>` and retain
`SHA256SUMS`, raw/processed schemas, producer manifest and provenance alongside
the binary. Retention is 14 days; overwrite is disabled. Repeated dispatches can
produce separate attributable CLI runs; concurrency does not deduplicate them.
There are no automatic retries, installations, Git tags, Releases, Homebrew
updates or deployments in either lane.

A main build has live defaults and shares live configuration, but its version
explicitly identifies a CI build, not a stable release. Do not automatically
replace an installed stable `jetscale`. Ordinary `ci.yml` remains separate: it
handles PRs and pushes for `main`/`unstable` (with docs-only push exclusions),
runs lint, Python contract tests and race-enabled Go tests, and cross-builds
Linux/macOS/Windows amd64/arm64 for PRs and unstable pushes. Those cross-builds
publish no CLI binary and do not prove Windows auth behavior. Only eligible main
pushes enter its stable tag/Release/Homebrew path, using the committed
schema/generated client. Workspace generation in this pipeline does not update
that schema or certify those releases against the latest Backend. Stable-release
integration needs a separately reviewed change.

## Required producer artifact

The verifier admits only completed, successful **push** runs of Backend's
`.github/workflows/pipeline.yml`, whose display name is `Build & Release`, in
`Jetscale-ai/Backend`. PRs, manual Backend runs and forks are rejected.

Exactly one non-expired `openapi-spec` artifact must belong to the verified run,
SHA and branch. Its ZIP must contain exactly:

- `openapi.json`: raw exported schema.
- `openapi-manifest.json`: the following contract (values shown descriptively).

```json
{
  "schema_version": 1,
  "backend_repository": "Jetscale-ai/Backend",
  "backend_workflow": ".github/workflows/pipeline.yml",
  "backend_event": "push",
  "backend_branch": "main or unstable",
  "backend_sha": "40-character source SHA",
  "backend_run_id": "decimal run ID string",
  "backend_run_attempt": "decimal attempt string",
  "spec_sha": "64-character SHA-256 of raw openapi.json",
  "spec_version": "openapi.json info.version",
  "artifact_name": "openapi-spec"
}
```

Use the actual source event SHA, branch and producer attempt, not the dispatcher
SHA, a generated release commit, image version or CLI source SHA. Keep schema
export free of application credentials and customer data. The required producer
contract belongs in Backend's `build-release.yml`, called by `pipeline.yml`.
Verify the actual sources and artifacts on both remote branches before enabling
dispatch; CLI's receiver implementation does not establish producer readiness.

The verifier checks GitHub's artifact metadata/digest and manifest, then
rechecks them before publication. A rerun changing the producer attempt or
replacing its artifact invalidates an in-flight consumer. Until a reviewed
producer rerun policy is verified, use a fresh successful push run rather than
assuming an existing artifact can be overwritten or relaxing the verifier.

## Dispatch request

The following is the required dispatch contract, not current Backend behavior.
The not-yet-implemented two-lane completion workflow must call:

```text
POST /repos/Jetscale-ai/cli/actions/workflows/build-cli.yml/dispatches
```

Set top-level `ref` to the verified Backend branch name (`main` or `unstable`),
not a SHA or `refs/heads/...`. Send exactly these ten string inputs:

| Input                 | Source                                                         |
| --------------------- | -------------------------------------------------------------- |
| `upstream_repo`       | Verified Backend repository full name                          |
| `upstream_workflow`   | `.github/workflows/pipeline.yml`                               |
| `upstream_run_id`     | Completed producer run ID                                      |
| `upstream_run_url`    | `https://github.com/Jetscale-ai/Backend/actions/runs/<run_id>` |
| `upstream_sha`        | Producer's verified `head_sha`                                 |
| `backend_run_attempt` | Completed producer attempt                                     |
| `backend_branch`      | Verified `main` or `unstable`, identical to `ref`              |
| `spec_sha`            | Verified producer manifest's raw schema hash                   |
| `spec_version`        | Verified manifest's `spec_version`, not image/release version  |
| `artifact_name`       | `openapi-spec`                                                 |

The `upstream_*` UI defaults are empty for naming-convention compatibility;
empty values are rejected by the verifier. There is no unverified manual mode.
Do not interpolate arbitrary dispatch inputs into shell commands or select the
latest artifact. Dispatch acceptance alone is not build success: link and verify
the downstream run and artifact separately.

## Credential and source trust

- Backend's dispatcher requires an approved credential with **Actions write** on
  CLI. Repository-dispatch permission is not sufficient proof of this access.
- CLI's `JETSCALEBOT_GITHUB_TOKEN` requires **Actions read** on Backend and
  enough repository visibility to verify its workflow/run/artifact metadata.
  There is no fallback credential. Prefer separately scoped credentials where
  available; do not broaden one merely to satisfy the workflow.
- Both CLI branches are trusted-code boundaries: checked-out host verifier code
  handles the Backend-read credential. Restrict who may modify those workflows
  and scripts, and review branch admission before making the credential
  available. Branch names and successful unit tests do not establish trust.
- Schema-driven generation, tests and packaging run without credentials or
  network in a disposable Docker sandbox. Only fixed-host verification steps
  receive the token. The untouched host validates output without executing it.
- No runtime auth, browser cookies, billing rows, Unbrowse or third-party
  capture services are involved. Source/tool downloads use GitHub/module and
  image registries; source/schema/artifacts remain within the reviewed CI
  boundary.

## Landing order and Backend handoff

1. **CLI `main` first:** human-review and land the receiver, verifier, packager,
   tests, documentation and proposed authority. This registers `build-cli.yml`
   and turns `sync-openapi.yml` into a non-mutating manual tombstone. The legacy
   `openapi-spec-updated` and `unstable-openapi-spec-updated` events are no
   longer consumed; do not rely on them during transition. Existing main-push CI
   still has its stable release effects, so review the landing accordingly.
2. **CLI `unstable`:** land the same complete receiver/tooling and required CLI
   implementation, not just the workflow. Branch creation and pushes are human
   operations. Verify trusted-source admission and the Backend-read credential
   on both branches. No Backend dispatch should be enabled before this step.
3. **Backend producers on both `main` and `unstable`:** review/land matching
   `pipeline.yml` and reusable `build-release.yml` schema export and manifest
   handling. Remove `Dispatch OpenAPI Sync Workflow` (the legacy inline
   `repository_dispatch`) from `build-release.yml`, particularly on `main`. Keep
   the parent workflow identity and tested SHA intact. Schema availability alone
   must not trigger the CLI before the whole producer pipeline succeeds.
4. **Backend dispatcher on `main`:** generalize
   `.github/workflows/unstable-cli-dispatch.yml` to watch completed
   `Build & Release` runs on `[main, unstable]`. Retain exact successful-push,
   repository, workflow, attempt, SHA and artifact verification. Replace
   `POST /dispatches` with the workflow-dispatch contract above. This
   `workflow_run` dispatcher must live on Backend's default branch; a copy only
   on `unstable` will not activate it. Mirroring it to `unstable` is optional
   for source consistency, not required for receiving completion events.
5. **Verify unstable first:** enable only the unstable dispatch path initially,
   observe one real successful Backend push and matching CLI unstable build, and
   verify checksums, both SHAs, branch, attempts, pre-upload recheck and
   cleanup. No application API calls are necessary.
6. **Enable main dispatch on Backend `main`:** after the unstable proof, admit
   successful main pushes and verify the main artifact separately. Failed
   Backend runs and cross-branch fixtures must produce no runnable artifact.

The [local validation record](local-dev.md#credential-free-validation) records
parent-reported passes for 125 Python tests, Go/Mage checks, vet, and
actionlint. These checks and the historical old-receiver synthetic-provenance
rehearsal do not prove a new two-lane Docker rehearsal or any remote activation
step. A source/API evolution that breaks hand-written Cobra adapters must fail
the build and receive an ordinary reviewed CLI fix; autogeneration does not
guarantee semantic compatibility.
