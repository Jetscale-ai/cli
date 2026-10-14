# Feature Flags

**Owner:** CLI Engineering, coordinated with Jetscale Platform and Backend.
**Reviewed:** 2026-10-13 against working-tree CLI source; no runtime
qualification or authority is established.

`flags show`, `flags plan` and `flags apply` inspect or enable four supported
non-secret feature flags through existing Backend APIs. They select one existing
company and optional existing BU/accounts; they do not provision entities,
disable flags, accept arbitrary keys or change Backend code or deployment.
Implementation support is not runtime authority or product readiness.

For Stack's fixed unstable target, use the
[feature-flags wrapper guide](../../stack/docs/operations/unstable-feature-flags.md).
Its runtime API/auth registration remains proposed, not active. Wrapper
qualification is a separate Stack-owner obligation, not proved by CLI tests.
Generic CLI targeting does not inherit that wrapper's authority or restrictions.

The proposed scope is exactly `https://unstable.jetscale.ai` as
`devops+unstable@jetscale.ai`, with the qualified Stack wrapper internally
resolving
`op://Everyone's Agents/JETSCALE_DEFAULT_ADMIN_PASSWORD (unstable)/password`
into an anonymous pipe for `--login EMAIL --password-stdin`. Temporary sessions
stay in memory; no secrets in args, environment, files, logs, capture or session
store. Human-reviewed publication, actual-capture synthetic qualification and
runtime provenance/isolation/effects/recovery gates remain required. Flags and
basics need separate bounded authorization. Dirty unstable-channel builds are
inputs, not provenance or authority. See the
[proposed CLI scope and unchanged exclusions](../AGENTS.md#proposed-stack-mediated-temporary-cli-authentication);
no clouds, FOCUS setup/ingestion, BU creation, external provisioning, production
or delivery rights are added. Published browser-only credential authority and
all narrower bans remain binding.

## Commands and targets

```text
jetscale flags show --profile FILE
jetscale flags plan --profile FILE
jetscale flags apply --profile FILE
jetscale flags show --profile FILE --validate-only
jetscale flags plan --profile FILE --validate-only
```

- `show` reads stored values and their effective ancestor chain, with actions
  showing what the profile would require.
- `plan` performs the same bounded metadata inspection and reports proposed
  changes, blockers and the full footprint of any planned company cascade.
- `apply` explicitly requests single-attempt writes without prompts or automatic
  retries. It inspects first, blocks writes on initial blockers, rechecks before
  each write and verifies readback. It is not an atomic transaction.
- `show/plan --validate-only` validates profile and target syntax offline,
  without authentication, credential/CA-file reads, probes or application calls.
  It rejects authentication options (`--login`, `--password-stdin`,
  `--credentials-file`, `--ca-file`). `apply --validate-only` is rejected before
  authentication. Offline acceptance says nothing about remote rows or
  privilege.

The command surface is shared across compiled channels; no fixed host or channel
is hardcoded into `flags`. Resolve an explicit target through the existing
`--api-url` or named-instance configuration. `auto` discovery is not supported
or probed. Require an HTTPS origin, except HTTP on `localhost` or a loopback IP.
Origins cannot contain credentials, an API path prefix, query or fragment.
`--account` is rejected; account UUIDs belong in the profile.

For example, this is an offline syntax check, not a loopback connection:

```sh
jetscale --api-url http://127.0.0.1:8000 flags plan \
  --profile flags-profile.json --validate-only --output json
```

`flags-profile.json` is an illustrative non-secret profile filename. Normal
runtime transport verifies TLS, ignores ambient proxies and rejects redirects,
including same-origin redirects. See
[authentication compatibility](local-dev.md#auth-migration-and-transport-compatibility)
for existing session modes and origin/channel binding. A compiled channel, HTTPS
URL, successful login or available API does not authorize its effects.
Authenticated `show/plan` do not write flags, but login/signout have separate
session effects and can invalidate other sessions for the user.

## Strict JSON profile

`FlagsProfile` uses version `1`. Profiles are regular JSON files of at most 1
MiB; neither the file nor its parent path may be a symlink. YAML, aliases,
case-folded field names, duplicate object keys, nulls at any level, unknown
fields, trailing JSON/data and excessive nesting are rejected. Use exact
lowercase metadata names and exact uppercase dotted flag keys. Values must be
JSON booleans `true`, not strings, numbers or `false`.

An illustrative profile, not authorization for these effects:

```json
{
  "version": 1,
  "company": {
    "name": "Example Company",
    "slug": "example-company"
  },
  "business_unit": {
    "name": "Example Clouds",
    "slug": "example-clouds"
  },
  "flags": {
    "root": { "AGENTS.EXPLORER": true },
    "company": { "AGENTS.EXPLORER": true },
    "business_unit": { "AGENTS.EXPLORER": true }
  },
  "company_cascade": "all_descendants"
}
```

- `company` requires exact `name` and `slug`; the company must already exist.
- Optional `business_unit` requires exact `name` and `slug` within that company.
  Selecting BU flags requires this selector. A missing/mismatched entity blocks;
  no company, license or BU is created.
- `flags.root`, `flags.company` and `flags.business_unit` are maps of supported
  keys to `true`. Omitted layers or keys request no direct change. Empty maps
  are permitted, but the complete profile must request at least one flag.
- Optional `flags.cloud_accounts` is an array of objects with `id` (a unique,
  nonzero UUID) and a nonempty `flags` map of supported keys to `true`. Each
  account must exist in the selected company and, when a BU selector exists,
  belong to that BU. No cloud account setup or provider probe occurs.
- Optional `company_cascade` accepts only `"all_descendants"`. It is explicit
  consent to the Backend company-enable footprint, not permission to modify
  unrelated tenants or proof of a human authorization record.

The complete allowlist is:

- `AGENTS.EXPLORER`
- `CLOUD.FOCUS`
- `CLOUD.AZURE.FOCUS`
- `INTEGRATIONS.AABHRA`

These match the four local capability keys, not a general settings or flag
editor. Secret/deprecated flags block and their keys/values are redacted.
Unknown keys are rejected without echoing potentially sensitive input. Missing
metadata, malformed pagination or failed reads are unknown, not evidence of
absence. Runtime inspection requires a valid actor UUID and explicit
`is_superuser: true` from `/api/v2/auth/me`; HTTP success or a role label alone
cannot establish full visibility, including rows hidden as not-found.

## Stored versus effective values

Stored state distinguishes a verified missing row, `false` and `true`. Effective
state uses strict AND, not nearest-value inheritance:

```text
root effective    = root stored true
company effective = root AND company
BU effective      = root AND company AND BU
account effective = root AND company AND BU AND account
```

A missing/false ancestor disables a stored-true child. An omitted prerequisite
is read but not automatically enabled: request it explicitly or retain the
blocker. Explorer is consumed at BU scope; a true root/company pair alone is not
enough for that BU. Requesting account-level flags additionally requires its BU
and account rows to be true.

Local parity means enabling the needed capability, not reproducing exact flag
rows. Local FOCUS configuration skips lower scopes; those skips are not disable
requests. The API may create BU/account rows when enabling a company, even when
the profile omits direct lower-scope FOCUS requests.

## Company cascades and application-wide roots

The current Backend company **create or set-to-true** API always cascades these
keys to all that company's BUs and cloud accounts, even when the company row was
already true. It can create missing children and overwrite stored-false child
overrides. This is not limited to the selected BU/accounts.

The CLI deliberately avoids retriggering that behavior:

- An already-true parent is skipped, not set again. No cascade is planned merely
  because a true company row appears in the profile.
- A false/missing company may be enabled only with
  `company_cascade: "all_descendants"`. Planning shows every affected descendant
  as a state/action, including stored-false overrides and unselected children.
  `cascade_enable` actions disclose Backend effects, not extra child requests.
- When the company is already true, an explicitly requested missing/false BU or
  account is enabled through its direct child endpoint, with prerequisites
  checked. Unrequested children remain unchanged. A direct BU enable does not
  cascade into accounts.
- Layer omissions remain unchanged except for an explicitly disclosed Backend
  company cascade. An omitted child is not a request to disable it or undo a
  previous cascade.

Other companies' flag rows are never written by this operation. **Root gates are
application-wide**, however: enabling a root can make existing true flags in
other companies effective without writing their rows. The caller must inspect
and authorize that application-wide impact; the selected-company footprint is
not proof of root isolation.

## Results, budgets and recovery

Table, JSON and YAML output expose safe IDs, allowlisted key booleans, actions
and fixed blocker codes, not raw API bodies or settings values. JSON/YAML retain
these names:

- `mode`, `company_id`, `business_unit_id`, `root_application_wide`
- `states`: `scope`, `entity_id`, `key`, `stored`, `effective`, `reason`
- `actions`: `scope`, `entity_id`, `key`, `operation`, `status`, `reason`,
  `cascade`
- `blockers`, `satisfied`, `outcome_uncertain`

A missing stored row uses `stored: null` and `reason: "stored_missing"`;
unknown/redacted evidence is not missing state. Action statuses are `noop`,
`planned`, `applied`, `blocked` or `outcome_uncertain`. `satisfied: true` means
requested flags are currently enabled through their needed ancestor chain, not
that agents, integrations, billing sources or recommendations are ready. A plan
may exit zero with `satisfied: false` and changes still planned. Apply requires
verified satisfaction and no pending actions; cleanup failure returns nonzero
even when the result reports satisfaction.

The CLI command context is 3 minutes, the API operation 2 minutes, flag requests
15 seconds, auth requests 30 seconds and detached signout 5 seconds. Topology
inspection enforces at most 100 combined BUs plus cloud accounts, not 100 of
each. The implemented plan/apply guard caps estimated total requests at 1,000,
reserving worst-case collection pagination, initial inspection, two full
inspections per direct write and the writes themselves. It reports
`request_budget_exceeded` before any flag write when the estimate exceeds that
ceiling; `show` remains a read-only reconciliation path. Verify the guard in the
inspected revision before runtime use. Request and time ceilings are limits, not
a guarantee that an allowed topology completes within its deadline. Synthetic
boundary tests do not prove deployed volume or latency.

After a rejected/timed-out write, partial cascade or unverifiable readback, stop
later writes and report uncertainty. Reconcile with separately authorized,
sanitized `flags show` reads within the remaining budget before considering any
new apply; never rerun apply to discover whether the first write succeeded. If a
company committed true before children failed, request only the needed children
for recovery rather than setting the parent again. Preserve retained effects and
use an owner-approved recovery plan; this enable-only command cannot roll back
by disabling or deleting rows.

Read-before-write and drift checks provide neither server compare-and-swap nor
exactly-once execution. Concurrent writers can race after the last read. The
Stack wrapper's same-user/same-host `flock` does not serialize other hosts,
browsers or server writers. Coordinate them separately. Do not chain basics,
provider setup, ingestion or evaluation from flag satisfaction.

## Implementation references

- [`internal/cmd/flags.go`](../internal/cmd/flags.go): parser, offline target
  resolution, command deadlines and result rendering.
- [`internal/api/flags.go`](../internal/api/flags.go),
  [`flags_read.go`](../internal/api/flags_read.go),
  [`flags_plan.go`](../internal/api/flags_plan.go),
  [`flags_apply.go`](../internal/api/flags_apply.go) and
  [`flags_transport.go`](../internal/api/flags_transport.go): bounded
  inspection, planning, generated Backend create/set endpoints and verified
  application.
- [`flags_behavior_test.go`](../internal/api/flags_behavior_test.go),
  [`flags_parity_test.go`](../internal/api/flags_parity_test.go),
  [`flags_safety_test.go`](../internal/api/flags_safety_test.go) and
  [`flags_budget_test.go`](../internal/api/flags_budget_test.go): synthetic
  chain, cascade, skip-true, direct-child, visibility, drift, partial-outcome
  and request-budget cases. These are not live qualification evidence.
