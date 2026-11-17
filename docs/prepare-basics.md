# Prepare Basics

**Owner:** CLI Engineering, coordinated with Jetscale Platform and Backend.
**Reviewed:** 2026-10-13. This documents the bounded `Client.PrepareBasics` API
and working-tree CLI command. It grants no runtime authority.

## Implementation and authority

Working-tree `internal/api/basics.go`, `internal/api/basics_settings.go` and
`internal/cmd/prepare.go` implement the safe subset and `prepare basics`
command. Generic `--login EMAIL --password-stdin` temporary auth is also
source-implemented. This CLI review verifies only CLI-owned synthetic tests,
offline contracts and generated consistency; Stack wrapper validation belongs to
its separate owner. Source, fixtures or offline validation are not proof of
deployed compatibility, live helper qualification or readiness.

For unstable, published browser-only registration remains binding. API execution
requires human-ratified Stack registration and the proposed Supreme Constitution
and secret-management protocol amendments for CLI authentication. Use the
[Stack workflow](../../stack/docs/operations/unstable-cli-basics.md) and
[separate Governance auth proposal](../../governance/docs/operations/unstable-cli-auth-proposal.md).
Neither a CLI flag nor a secret reference grants authority. The exact proposed
scope and publication gates are in the
[CLI constitution](../AGENTS.md#proposed-stack-mediated-temporary-cli-authentication).
Basics and flags require separate bounded authorization. A local uncommitted
unstable-channel build is allowed input, not provenance or authority. This
refactor includes no live execution, commit, push, installation or publication.

## Stack auth-stage provisioning

The
[CLI auth law](../AGENTS.md#proposed-stack-mediated-temporary-cli-authentication)
adds `sso_bootstrap` as a separate opt-in Stack stage, not a `prepare basics`
option or new CLI command. Existing typed `licenses ensure-capacity`, generic
`feature-flags`, `users get` / `users ensure-superuser` and company-setting
primitives supply its operations; Stack owns orchestration, not a CLI seed
engine. The basics manifest and its exclusions remain unchanged.

**Amendment:** 2026-11-16, accepted upon human review and commit. **Rationale:**
SOC 2 CC6.1/CC8.1 and web/MCP SSO readiness without manual provisioning
intervention. Effectiveness is automatic upon matching human-reviewed
publication of all revised Governance Constitution/secret-management, CLI law
and Stack seed adoption/registration/procedure scope on their respective `main`
branches, without another status edit, active marker or ceremony. Preserve the
2026-11-14 acceptance and existing grants until then; this preparation is not
self-ratification or execution. The helper must reject the new stage on old
three-stage law. `sso_bootstrap` cannot be mixed with `capacity`,
`auth_prepare`, `sso_activate` or the nine default seed stages; preserve all of
them, including legacy password-first requirements and their existing selection
restrictions.

**Proposed five-seat amendment (2026-11-17):** Stack's unstable seed desired
state starts with minimum five-seat quota-only capacity, including default
setup, `capacity` and `sso_bootstrap`. Stack owns the profile and ordering;
generic `licenses ensure-capacity` still requires explicit `--min-seats`; the
proposed profile passes `5`, not a CLI-owned tenant default. Commercial
pricing-plan minimums remain unchanged. This proposal requires matched
human-reviewed commits and publication of Governance
Constitution/secret-management, CLI law and Stack seed
adoption/registration/procedure on their respective `main` branches. Preserve
the 2026-11-14 acceptance, 2026-11-16 amendment and existing grants; the
published two-seat auth profile governs until then. Source/examples/tests grant
no live five-seat authority. Rationale: reproducible five-seat unstable setup
without changing billing terms (Business Goal; SOC 2 CC8.1).

Under that proposed profile, for only pinned `Unstable Jetscale` /
`unstable-jetscale` and `agent@jetscale.ai`, always:

1. Ensure minimum five seats on the existing eligible license, quota-only:
   finite 1, 2 or 4 grows exactly to 5; 5, larger quotas and null (unlimited)
   are noops. Repeated capacity applies write nothing. Grow only `max_seats`,
   never change billing, plans, commercial terms or other quotas. This is not
   occupancy proof.
2. Create absent disabled non-secret `AUTH.MULTI` / `AUTH.SSO.WORKOS`
   root/company rows; never disable existing rows.
3. Ensure the exact company-linked platform-wide superadmin: invite an absent
   target once with `SIGNUP_SUPERUSER`, or reuse a matching `PENDING_ACTIVATION`
   identity and valid unused invitation without resend. Preserve an existing
   completed identity; only a matching signup-complete `ACTIVE` ordinary user
   may be promoted. Conflicts block, not automatic reassignment or activation.

Optional SSO then follows effective MULTI -> exact `OrganizationSSO` provider ->
existing `[Console] Jetscale` organization -> SSO, using create-missing or
exact-match noops only. Mapping writes may activate existing true gates. Require
root-impact/full selected-company cascade evidence, globally exclusive writers
and optional-password protection in all newly activated companies, including
root-affected other companies. Required-SSO stays false with deployed absent-row
false-default proof; domains and trust remain unchanged. Other-company reads
stay limited to two-flag/required-SSO/password-impact and exact-email invitation
conflict metadata, never unrelated data or other-company writes.

Before SSO configuration, require deployed shared web/MCP handler proof that
genuine first SSO preserves company/superadmin, sets `ACTIVE` and
`signup_completed_at`, consumes invitations and leaves the password hash absent;
existing IdP eligibility; isolated `unstable`/`unstable_user` deployment; and
deployed WorkOS client/organization proof for both exact callbacks:
`https://unstable.jetscale.ai/auth/sso/callback` and
`https://unstable.jetscale.ai/oauth/sso/callback`. An undelivered local
email-only fix or source/tests do not prove deployed behavior. No prior target
password signup/login or prior web login is required; MCP can be the first
authentic SSO login.

Verified configuration means "config ready awaiting first SSO", not `ACTIVE`,
completed signup or login evidence. The current Stack `sso_bootstrap` helper
always configures optional SSO; it has no provisioning-only selector. The law's
provisioning-only outcome applies when SSO is omitted from authorized work, not
to a selectable mode of this helper. Direct user activation/forced signup
completion, password setting, signup-link scraping and SAML/OAuth bypass remain
prohibited; genuine first SSO completion requires separate login authority. This
stage adds no target credential source, provider administration, OAuth
grants/MCP client setup or round-trip tests.

Retain the same origin/operator/1Password reference and memory-only stdin auth,
actual-capture helper/binary qualification and runtime gates. Stack's 900-second
total/300-second cleanup reserve, 100-descendant/1,000 combined charged HTTP
ceilings include authentication, previews, reads, writes and cleanup. Preserve
explicit command HTTP/time caps, `--max-pages 1`, final process-exit proof after
signout, one foreground operation, zero retries, global writer coordination and
human-owned recovery. Reconcile uncertainty through scoped reads, never replay.

## Command interface

```text
jetscale-unstable prepare basics --manifest FILE --validate-only
jetscale-unstable prepare basics --manifest FILE
jetscale-unstable prepare basics --manifest FILE --apply
```

- `--validate-only`: offline validation only; no authentication, secret
  resolution, application requests or session files. It rejects `--apply` and
  explicitly supplied auth/CA flags. Validate before accessing credentials; it
  cannot verify an existing plan, license or tenant.
- Default: authenticated assessment/plan of existing metadata, no basics writes.
  Authentication still changes server session state.
- `--apply`: explicit opt-in to the safe missing writes after human/runtime
  gates, not an authority override. Never retry an uncertain submission.

The compiled unstable channel and exact `https://unstable.jetscale.ai` origin
must be verified for Stack use. The API method itself does not enforce channel
or destination; its caller owns that boundary. Do not use generic instance or
URL overrides to evade Stack scope. No network fallback is part of offline
validation.

### Stack wrapper

From Stack, use `python3 scripts/unstable-prepare-basics.py --check` for offline
validation. The default template selects `ESSENTIAL_ANNUAL` by internal name and
`Unstable Jetscale Test License`, with yearly billing and no settings/flags.
Runtime resolves the current catalog UUID after each reset; no historical UUID
or start date is pinned. Optional `--pricing-plan UUID` adds an approved nonzero
UUID pin, which must match the named plan. Optional `--manifest FILE` preserves
custom supported non-secret settings/flags and metadata. Legacy UUID-only
manifests retain their free-plan contract. Only a temporary copy changes; the
source file stays unchanged. `--execute` adds authority-gated assessment;
`--execute --apply` adds authorized safe writes. No install/publication,
credential file fallback or provider secrets are part of this wrapper.

## Non-secret manifest

Use strict JSON. Company and license are required, with an explicit company slug
and either an approved existing `license.pricing_plan_id` UUID or the supported
`pricing_plan.internal_name` selector. With the selector, the license UUID may
be null; a supplied UUID adds a matching pin. Unknown fields are rejected,
including a supplied BU ID. The manifest is not generic CRUD or a credential
file.

| Field           | Accepted scope                                                                                                          |
| --------------- | ----------------------------------------------------------------------------------------------------------------------- |
| `company`       | Generated company shape: explicit name, slug and domains; non-root only; approved optional identity/contact fields      |
| `license`       | Generated setup-license shape; resolved plan UUID or optional pin, with approved license metadata/dates/interval        |
| `pricing_plan`  | Optional selector with exactly `internal_name: ESSENTIAL_ANNUAL`; runtime resolves the current existing UUID            |
| `business_unit` | Exact `name` and `slug`, required by this command; optional at API-method level; resolves existing BU, never creates it |
| `settings`      | Optional `root`, `company`, `business_unit` maps of `value_type` plus scalar `value`                                    |
| `flags`         | Optional `root`, `company`, `business_unit` maps of supported leaf keys to `true`                                       |

For Stack, pin `Unstable Jetscale` / `unstable-jetscale`, `jetscale.ai` and
approved simulated contact/license fields. The command also requires
`Unstable Jetscale Clouds` / `unstable-jetscale-clouds`. No root company,
unrelated domains, invitations, billing-provider mutations or commercial
commitments. Omitted optional company fields are not instructions to erase
existing values; mismatching declared identities block rather than repair.

The named profile must be the active `ESSENTIAL_ANNUAL` /
`Essential (Annual Plan)` catalog record: `YEARLY`, `CAD`, 199/month, 2388/year,
performance fees enabled at 2500 bps, yearly allowance 500000 cents and a
12-month fee term. Its license name and description must match the default Stack
template, with yearly billing. License prices and fee overrides must be explicit
null inheriting the verified plan or exactly match its terms; missing metadata
blocks. Existing licenses must be active, match the resolved plan, have valid
dates and sufficient quotas and available tokens.

Both named and UUID-only plans need at least three accounts and sufficient
BU/seat/token quotas. Explicit null quota means unlimited; missing quota
metadata is not unlimited. Legacy UUID-only plans retain zero monthly/yearly
prices and disabled performance fees; their license prices must be zero or
explicit null inheriting that verified free plan.

Missing, multiple, inactive or conflicting records block. The method never
creates or repairs pricing plans, changes license terms or falls back to another
plan. Backend migrations own catalog provisioning. Offline validation cannot
certify any runtime properties. No checkout/payment/Stripe API command is
performed, but license creation invokes Backend's postcommit pending-fee
activation; runtime qualification must inspect billing effects, tenant/charge
fences and recovery.

### Settings and feature leaves

Accepted scalar settings in each supported scope:

| Key                          | `value_type` | `value`            |
| ---------------------------- | ------------ | ------------------ |
| `enable_langfuse`            | `boolean`    | `true` only        |
| `enable_caching`             | `boolean`    | `true` only        |
| `recommendation_temperature` | `number`     | Number in `[0, 1]` |

The manifest supplies a scalar, not Backend's nested `{"value": ...}` envelope;
the client constructs that envelope. Secret/model-routing/auth/debug settings,
objects, extra setting metadata, wrong types and generic overrides are excluded.
Existing shape/type/scope or effective-setting drift blocks. Do not bulk-read
custom root overrides. Root setting verification uses exactly the root entry in
`scope_chain`, with matching setting ID/source/value (or explicit unset root),
not the effective value selected for the authenticated caller's company.
Company/ BU settings still verify their scoped effective values. Declare
telemetry and downstream effects before enabling settings; implementation
support does not imply isolated or approved processing.

The conservative flag allowlist is:

- `AGENTS.EXPLORER`
- `AGENTS.RECOMMENDATION_ORCHESTRATOR`
- `CLOUD.AWS.FOCUS`
- `CLOUD.AZURE.FOCUS`
- `CLOUD.GCP.PLATFORM`
- `CLOUD.FOCUS`
- `PRODUCT.MANAGEMENT_DASHBOARD`

Authenticated basics inspection requires a nonzero actor UUID and explicit
`is_superuser: true` from `/api/v2/auth/me`, checked again with pre-write
inspection. Filtered collections or hidden not-found rows cannot prove absence
or the complete company cascade footprint.

Only `true` is accepted, and only needed, approved leaves belong in the
manifest. No disable, broad branch toggle, payment/SSO/integration/auth/
experimental or remediation key is supported. Root records must exist, be
non-secret and explicitly non-deprecated (`deprecated_at: null`); omitted
metadata blocks rather than proving safety. Descendant writes require
already-enabled parents, not merely planned enables. Company flag mutation
requires a proven single selected BU and no company cloud accounts to fence the
server cascade. An already-true company parent is a no-op only if the selected
BU child is present, true, non-secret and compatible with the same root flag. A
missing/false child blocks as `cascade_child_incomplete`; a secret/incompatible
child blocks as drift, not an implicit cascade retry. Missing parents or an
unsafe cascade block configuration rather than implicitly staging broader
writes.

## Apply, repeatability and partial results

`Client.PrepareBasics` uses bounded generated requests, strict metadata checks,
read-before-write, immediate prerequisite/target recheck and persisted/effective
readback. Satisfied records are `noop`; safe differences are `planned`, then
`applied` only after verification. Conflicting/duplicate identities or drift are
`blocked`, not overwritten. There are no automatic mutation retries.

Company/license setup uses one submission only after company absence and the
same approved plan are rechecked. The resolved current plan UUID is placed only
in the generated setup request; the supplied manifest is unchanged. This
recreates the named license after a full reset. An existing company without a
license remains blocked rather than activating pre-existing pending charges as
an implicit repair. Configuration for a newly created tenant is deferred to a
subsequent assessment. Company setup can be retained even when a requested BU or
later configuration is blocked; the run is not transactional across all basics.
Do not automatically rerun apply or chain prerequisites.

Idempotency is limited: rechecks are not server compare-and-swap, and another
writer can race between a read and write. Stack's `flock` covers only
cooperating processes by the same user on the same host, not other users or
browser/other-host/server writers. Coordinate concurrent writers and verify
scope. No exactly-once or distributed lock guarantee is claimed.

BU creation is always blocked as `unsafe_external_provisioning`. Backend lacks
BU uniqueness, commits the BU before remote managed-agent provisioning and may
retry remote creation, risking duplicate agents. Another BU is not recovery.
Backend owns uniqueness/atomicity/retry-safe provisioning; a manual/browser
operation needs separate existing authority and a recovery plan, not automatic
fallback. An existing local managed-agent FK does not attest an external agent
ID/version or readiness.

Any write/verification uncertainty sets `outcome_uncertain` and stops later
writes. A timeout is not cancellation. Reconcile with supported sanitized reads
before considering further apply, within the original request/deadline; do not
resubmit to find out whether the first write succeeded. Retain authorized
partial records and report them. Deletion or license replacement is not
automatic rollback.

## Results and bounds

Results contain IDs, fixed reason codes, gates and actions, not raw API bodies,
company contacts, setting values or external-agent assertions. Report `company`,
`license`, `pricing_plan`, `business_unit`, `managed_agent_local`,
`managed_agent_external` and `configuration` separately. A selected BU keeps
external state unknown and `ready` false even with a local reference. A result
without a requested BU may be ready for its narrower manifest, not full Stack
basics, cloud onboarding, worker readiness or FOCUS.

The command sets a 3-minute deadline before authentication; one API-method call
is bounded to 2 minutes, individual requests to 15 seconds, responses/manifests
to 1 MiB and collections to 20 pages of 100 records with pagination/identity
checks. Bounds are ceilings, not permission to scan unrelated tenants or repeat
calls. Stack's 15-minute assessment / 60-minute preparation budget, final
5-minute cleanup reserve, zero retries, data/resource bounds and
provenance/identity/isolation/recovery gates remain binding.

The wrapper imposes a stricter 15-minute total with a final 5-minute cleanup
reserve, 5-minute build, 30-second offline validation/credential resolution and
210-second runtime-child caps. These do not renew the task budget.

## Temporary stdin authentication

Generic source-implemented `--login EMAIL --password-stdin` accepts one complete
non-empty password line from non-terminal stdin, at most 4096 bytes within 5
seconds. It verifies `/api/v2/auth/me` identity before running the command,
keeps a temporary cookie session and does not save credentials. Sign-in and
identity verification use the caller's context with 30-second HTTP caps. Cleanup
uses a detached 5-second context on success, error or handled cancellation,
including failed acquisition/verification and cookies received with non-2xx
sign-in. Network/cancellation/redirect failures are explicitly uncertain; when
no cookie is available, cleanup remains unverified. Cookie-bearing non-2xx
sign-in is also explicitly uncertain and gets a cleanup attempt. No login retry
is automatic. It cannot be combined with `--credentials-file` or a set channel
token variable. Ordinary `auth login` storage is a different path and is
excluded from Stack automation.

Although the generic reader accepts pipes/files, proposed unstable automation
permits only a qualified Stack wrapper resolving
`op://Everyone's Agents/JETSCALE_DEFAULT_ADMIN_PASSWORD (unstable)/password`
internally and sending it through an anonymous pipe for
`devops+unstable@jetscale.ai` at `https://unstable.jetscale.ai`. Passwords and
cookies must never enter files, args, environment variables, history, logs,
traces, tool artifacts or exported browser state. Verify exact HTTPS
origin/certificates, rejected redirects, actual capture configuration,
identity/privilege, memory-only session handling and synthetic failure/cleanup
cases before real credential use. No provider credential transfer is covered.
Generic auth support across channels does not register additional live targets.

Login/signout may close all sessions for this user. Record that effect in the
human request and coordinate other work; temporary local storage does not imply
an independent server session. Cleanup failure, kill or ambiguous submission
requires an owner handoff, not a second login to guess state. Report sanitized
cleanup status honestly; source cleanup code is not proof it ran on a live
system. Ordinary non-prepare API methods still use `context.Background()`;
context-aware session acquisition does not promise cancellation of every
command. Protected seed sources, including example files, remain inaccessible
under `private_files`; this phase cannot adapt them and makes no seed-file
changes. No provider 1Password references are inferred from the sign-in
registration.
