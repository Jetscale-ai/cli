# Company-setting and license-capacity primitives

These generic commands reconcile one explicitly selected company's application
state. Stack owns unstable tenant restrictions, execution authority, identities,
shared impacts, and writer coordination. Implementing these commands does not
activate a live reconciliation or extend the published CLI authentication lanes.

## Command integration

The actual root registers `company-settings` and `users` before
session-lifecycle wrapping and executable-name help adaptation. `licenses`
registers `ensure-capacity` in `internal/cmd/billing.go`. The user
reconciliation contract is documented in [users.md](users.md).

Resource groups inherit the authenticated resource transport, company
selector/default resolution where applicable, `--require-superuser`, `--timeout`
(2 minutes by default, maximum 30 minutes), `-o json|yaml|table`, and `-q` ID
output. API requests have a 15-second cap and 4 MiB response limit. Writes have
one attempt and no automatic retry.

## HTTP and pagination bounds

`--max-http-requests N` is an opt-in total request cap. Omitting it preserves
the existing uncapped session behavior; explicitly passing zero is invalid. `N`
must be an integer of at least 2. It is supported only on resource commands,
including raw `api`, with paired `--login LOGIN --password-stdin`
authentication. Stored sessions, channel TOKEN variables, `--credentials-file`,
`auth`, `config`, `system`, `version`, and deprecated `prepare`/`flags` commands
are not supported with the cap. Invalid caps, incompatible authentication,
invalid page limits, and unsupported capped command combinations fail before
password consumption or login. Raw `api --input -` cannot share stdin with
password input.

The session-owned counter is installed before sign-in and shared by every
`Resources()` clone. Of the `N` requests, `N - 1` are available to ordinary
traffic and one is reserved exclusively for a single lifecycle signout attempt.
Ordinary traffic cannot spend that reserve, even by calling the raw signout
endpoint. Authentication, identity/superuser checks, all internal API reads and
writes, pages, failed attempts, and cleanup consume the same budget. Attempts
are charged before transport dispatch, including attempts that fail without
reaching the server; there are no refunds, resets, or automatic retries.

Capped sessions disable HTTP keepalive and HTTP/2, using fresh HTTP/1
connections to prevent `net/http`'s reused-connection and HTTP/2 transparent
retransmissions from exceeding the counter. This adds connection/TLS overhead
and requires an HTTP/1-capable origin. Existing TLS verification, exact-origin
enforcement, redirect rejection, proxy isolation, and secret-safe error handling
remain. The cap limits this CLI session's HTTP attempts, not server-side queues,
provider calls, downstream effects, or other clients.

In capped mode, `--timeout` covers password input, sign-in, verification, and
the command together. Authentication requests retain their 30-second client cap;
resource API requests retain their 15-second cap, both shortened by the overall
deadline. Failure/cancellation cleanup gets a separate 5-second grace period but
retains the same counter and reserved slot. Signout is attempted once, not
retried. A reservation guarantees an available attempt, not successful session
invalidation: failed cleanup or acquisition without a usable cookie remains
unverified and requires owner reconciliation. Temporary sessions are not saved.

An unsent exhausted request exits 1 with `error.reason: http_request_bound` and
`transient: false`. It is not an uncertain mutation. If a write was dispatched
but its readback cannot run or fails, the command instead retains
`outcome_uncertain`, also nontransient. Cleanup failure remains visible and
makes the overall command fail even if command data was already printed. Neither
failure authorizes replaying a write.

`--max-pages N` is available on resource groups and raw `api`, defaults to 20,
and accepts integers from 1 through 20. It applies independently to each
paginated collection, not to aggregate command traffic; in raw `api` it affects
only `--paginate`. Page size remains 100. An incomplete collection fails rather
than returning truncated data: reaching the bound exits 1 with
`error.reason: page_bound` and `transient: false`; malformed, inconsistent, or
known short-incomplete collections also fail. No accumulated partial collection
is printed. Completeness is checked against returned counts/page lengths, not an
atomic server snapshot; writer coordination remains necessary.

### Measured request costs

With stdin login and `--require-superuser`, the ordinary successful lifecycle
has **five overhead requests**: sign-in, login-verification `me`, exact-login
second `me`, required-superuser `me`, and signout. Command-specific checks,
selector lookups, each page, pre-write reads, mutations, and readbacks are
additional. Five is not a sufficient total cap for a command that calls an API.

| Fixture command                                                                      | Total HTTP requests | Additional command requests                                                    |
| ------------------------------------------------------------------------------------ | ------------------- | ------------------------------------------------------------------------------ |
| `companies list --require-superuser`                                                 | 6                   | One company-list page                                                          |
| `users get EMAIL --require-superuser`                                                | 7                   | ACTIVE-superuser actor check and one user-list page                            |
| `company-settings ensure KEY --company SLUG --yes --require-superuser` (missing row) | 10                  | Company lookup, initial setting read, pre-write setting read, create, readback |

For the last fixture, `N = 8` blocks the create before dispatch; `N = 9` allows
the create but blocks its readback, yielding `outcome_uncertain`; `N = 10`
completes the create, readback, and signout. Select caps from the full inspected
path, not just the lifecycle overhead. These costs are local synthetic evidence,
not deployed compatibility or live-execution authority.

## Company settings

```bash
jetscale company-settings get WORKOS_SSO_REQUIRED_COMPANY_SETTING_KEY \
  --company acme -o json
jetscale company-settings ensure WORKOS_SSO_ORG_ID_COMPANY_SETTING_KEY \
  --company acme --value org_example --dry-run -o json
jetscale company-settings ensure WORKOS_SSO_PROVIDER_TYPE_COMPANY_SETTING_KEY \
  --company acme --value OrganizationSSO --yes -o json
```

Only these known non-secret keys are supported:

- `WORKOS_SSO_ORG_ID_COMPANY_SETTING_KEY`: string get/ensure.
- `WORKOS_SSO_PROVIDER_TYPE_COMPANY_SETTING_KEY`: string get/ensure.
- `WORKOS_SSO_REQUIRED_COMPANY_SETTING_KEY`: read-only boolean or string policy.

The Backend provides no secret-classification metadata. Unknown keys are
rejected before authentication or API requests; there is no arbitrary-setting
read or write fallback. Values must be non-empty UTF-8 strings of at most 1024
bytes without control characters. Strings are compared exactly, without trimming
or case folding. Do not pass credentials through `--value`.

Reads use the generated by-key endpoint with `company_id`. Returned rows must
have a nonzero UUID, the exact company/key, a recognized declared type, and
exactly one nested value member (`data.value.value`) of the declared scalar
type. Get prints only `id`, `company`, `key`, `value_type`, and scalar `value`.
Required policy adds normalized `required`: boolean values and case-insensitive,
whitespace-trimmed `true`/`false` strings are accepted; unknown strings block.
Missing settings exit 3, never imply `required: false` or inherited root policy.

Ensure creates only a missing string row using the generated typed request. An
exact match is `noop`; a conflicting scalar value or type is `drift` without
PATCH or overwrite. Malformed/foreign rows fail rather than becoming create
candidates. A successful create requires both a valid response and a scoped
readback matching row ID, type, and value. Failed/mismatched post-write checks
are uncertain; no retry or automatic rollback occurs.

Creating an organization/provider mapping can activate existing SSO gates. The
caller must inspect password access, required policy (including inheritance),
company cascades, and shared provider effects under its own authorized scope.
This command does not configure WorkOS, invite users, or grant roles.

## License capacity

```bash
jetscale licenses ensure-capacity --company acme --min-seats 2 --dry-run -o json
jetscale licenses ensure-capacity --company acme --min-seats 2 --yes -o json
```

`--min-seats` must be an integer from 1 through 2147483647. The command lists
company-filtered licenses, rejects invalid/foreign list identities or unknown
statuses, and requires exactly one `ACTIVE` license. Known historical statuses
are excluded from selection. The selected detail must match the company and ID,
have a current validity interval, and expose explicit quota/commercial metadata.

- `max_seats: null` means unlimited and is a `noop`.
- A finite quota meeting the minimum is a `noop`; it is never reduced.
- A finite insufficient quota is planned or grown to the requested minimum.
- Missing, negative, fractional, string, or overflowing seat metadata blocks.

The only PATCH payload is `{"max_seats": INTEGER}`, serialized by the generated
`UpdateLicenseRequest`. No license creation, plan/catalog edit, billing action,
status/date change, fee override, or other quota update is requested. Existing
plan minimum seats are not a sandbox override and are not changed or
reinterpreted.

Before writing, the command rereads selection, quota, and preserved terms to
stop observed concurrent changes. It verifies the PATCH response and a fresh
company-scoped list/detail readback. Preserved metadata includes the plan ID,
status, validity, billing interval, currency, prices, performance-fee fields,
Stripe subscription ID, and account/BU/token quotas. Explicit null inherited
terms remain null. Name/description are compared when present. Commercial and
Stripe values are never printed. Token balance and update/audit timestamps may
change independently and are not treated as commercial drift.

Every successful capacity result (`noop`, `planned`, or `updated`) includes
`details.terms_sha256`: a lowercase, 64-character hex SHA-256 of the validated
preserved snapshot's compact, sorted-key JSON (`encoding/json` serialization).
The snapshot includes license `id`, company ID, all preserved fields above,
including the Stripe subscription link and explicit nulls, and name/description
when present. Expanded company/plan references use their IDs, not nested data.
Missing optional name/description differs from present null. `max_seats` is
excluded, so quota-only growth leaves the fingerprint unchanged. Only the digest
is exposed; no raw commercial or subscription values are added to output.

Stack can use `ensure-capacity --dry-run -o json` as a read-only capacity/terms
snapshot instead of an incomplete license-list projection. For a `planned`
result, current capacity is `previous_max_seats`; `max_seats` is the proposed
quota. For a `noop`, `max_seats` is current capacity (null means unlimited).
Compare `terms_sha256` for preserved-term drift; it is a change detector, not a
server revision, lock, or authorization to write.

The Backend has no atomic conditional PATCH. Serialize all writers of a license
(including UI and other automation) across the entire plan/confirm/apply
sequence. Pre-write rereads do not close the final read/write race or supply a
server lock. A post-write discrepancy is uncertain and requires owner
reconciliation, not a blind retry, compensating quota reduction, or automatic
rollback.

## Output and exits

JSON/YAML writes use the existing result shape:

```json
{
  "resource": "license_capacity",
  "status": "planned",
  "id": "33333333-3333-4333-8333-333333333333",
  "details": {
    "company": "11111111-1111-4111-8111-111111111111",
    "min_seats": 2,
    "previous_max_seats": 1,
    "max_seats": 2,
    "unlimited": false,
    "terms_sha256": "9056be3ded6b7396aeef5399adb82242c793d05ff669f9039c977718be3e407b",
    "operation": "grow"
  }
}
```

- Settings: `resource: company_setting`; statuses `planned`, `created`, `noop`,
  or `drift`. Details contain the scalar projection; create plans additionally
  contain `operation: create`. Missing create plans have no row ID.
- Capacity: `resource: license_capacity`; statuses `planned`, `updated`, or
  `noop`. Plans/updates include `operation: grow` and `previous_max_seats`.
  Noops contain actual `max_seats` (including explicit null) and `unlimited`.
  All three statuses include the preserved-snapshot fingerprint `terms_sha256`.
- Settings drift lists field names in `drift`, not raw Backend bodies.
- Quiet output contains only the existing/created row ID, or is empty for a
  missing-setting create plan. Machine callers must inspect the exit code.
- Table capacity plans say `Would grow`; success says `Grew`; noop reports
  sufficient unchanged capacity. Data goes to stdout; diagnostics/prompts go to
  stderr, without color or terminal escape sequences added by these commands.

`--dry-run` and `--yes` are mutually exclusive. Noop/drift need no confirmation.
A planned write needs `--yes` or an interactive terminal confirmation. Without
confirmation, a plan is printed and exit 5 returned; JSON stdout remains one
result document, not a result followed by a second error document.

Exit codes follow the existing contract: 0 success/noop/dry-run, 1 failure or
`outcome_uncertain`, 2 usage, 3 missing setting/company, 4 selection/concurrency
conflict, 5 forbidden/unconfirmed, 6 read deadline, and 7 setting drift.
Uncertain JSON errors have `error.reason: outcome_uncertain` and are not
transient. Reconcile through read-only get/dry-run before any new human-selected
write. Neither command retries mutations to discover whether they committed.

## Local evidence

HTTP fixture and CLI output tests cover typed payloads, create/grow reruns,
projection, policy representations, tenant/type/nesting checks, null unlimited
quotas, commercial inheritance, stable seat-independent fingerprints, Stripe
linkage/null-term fingerprint changes, selection conflicts, observed concurrent
drift, failed/foreign readback, lost responses, confirmation, offline help, and
invalid input rejection before login stdin consumption. Bound tests exercise
actual root registration/help/lifecycle, shared concurrent clone budgets,
reserved cleanup on failure/cancellation/acquisition, no transparent HTTP
replay, pre-credential rejection, login deadlines, page-bound/inconsistent-total
failure, and unchanged omitted-cap/default pagination behavior. Fixtures use
loopback servers and synthetic credentials only; they do not establish live
authentication, deployed compatibility, provider behavior, or authority for
Stack execution.
