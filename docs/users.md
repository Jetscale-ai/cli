# User reconciliation commands

Owner: CLI Engineering. Semantics inspected against Backend source on
2026-11-13. This guide describes code and offline tests, not live configuration
or verified Unstable compatibility. The actual root registers `newUsersCmd()`
before session lifecycle wrapping and help adaptation.

## Intent and safety boundary

`ensure-superuser` explicitly requests **platform-wide superuser privilege**.
`--company` records the requested company association; it does not restrict that
privilege to one company. This is not a company-admin role or a least-privilege
membership invitation. No other users, roles, business units, or accounts are
changed.

Both commands require an authenticated actor with a nonzero UUID, explicit
`is_superuser: true`, and `status: ACTIVE`. Authentication uses the existing CLI
session lifecycle. These commands do not implement a credential helper, SSO,
password setup, activation, password reset, invitation resend, or company
reassignment. Code and examples grant no runtime or credential-use authority.
The registered Stack stdin-auth lanes do not automatically include these user
commands; a wrapper needs applicable authority and qualified authentication.

Email selection is exact and case-sensitive: no prefix, display name,
surrounding whitespace, or case normalization. `ensure-superuser` requires
`--company` on the current invocation, using a UUID, slug, or exact name.
Environment and remembered company defaults are ignored. Names containing spaces
must be quoted. Resolved company responses must match the selector; machine
output always uses the UUID.

## Future wrapper invocations

The following invocations are examples for a separately authorized,
authenticated session. They were not executed against Unstable:

```bash
jetscale-unstable --api-url https://unstable.jetscale.ai users get agent@jetscale.ai -o json
jetscale-unstable --api-url https://unstable.jetscale.ai users ensure-superuser agent@jetscale.ai --company "Unstable Jetscale" --dry-run -o json
jetscale-unstable --api-url https://unstable.jetscale.ai users ensure-superuser agent@jetscale.ai --company "Unstable Jetscale" --yes -o json
```

For subsequent runs, pass the resolved company UUID rather than relying on a
mutable name. `--dry-run` and `--yes` are mutually exclusive. Without either,
writes require terminal confirmation; noninteractive commands refuse with
exit 5. Noop and already-pending outcomes require no confirmation because they
write nothing. Password-stdin mode never prompts for confirmation.

## Stable output

`get` returns exactly these six fields. Missing nullable evidence remains
`null`; company expansion objects, password fields, invitation tokens, and other
user metadata are never projected:

```json
{
  "id": "22222222-2222-4222-8222-222222222222",
  "email": "agent@jetscale.ai",
  "is_superuser": true,
  "status": "PENDING_ACTIVATION",
  "company": "11111111-1111-4111-8111-111111111111",
  "signup_completed_at": null
}
```

These UUIDs are synthetic examples, not live identifiers. `is_superuser` is
boolean or null when unproved; `company` is a UUID string or null;
`signup_completed_at` is an RFC3339 timestamp or null. An unknown user-status
value or malformed UUID/timestamp fails closed.

`ensure-superuser` returns the shared resource Result envelope. `details`
contains the same six fields, never an invitation document. A queued invitation,
including a rerun that observes the same pending state, returns:

```json
{
  "resource": "user",
  "status": "pending",
  "id": "22222222-2222-4222-8222-222222222222",
  "details": {
    "id": "22222222-2222-4222-8222-222222222222",
    "email": "agent@jetscale.ai",
    "is_superuser": true,
    "status": "PENDING_ACTIVATION",
    "company": "11111111-1111-4111-8111-111111111111",
    "signup_completed_at": null
  }
}
```

Exit 0 means reconciliation returned an observed or planned result, **not that
signup or access is complete**. Wrapper branching must use the outer Result
`status` plus `details`, rather than treating every exit 0 as provisioned
access:

- `planned`: intended state only, no writes. Missing-user plans have an empty
  `details.id` and omit the outer `id`; signup remains null. Promotion plans
  retain the existing ID/timestamp and project `is_superuser: true`.
- `noop`: matching ACTIVE superuser with non-null signup-completion evidence.
- `updated`: one verified promotion of a matching signup-complete ACTIVE user.
- `pending`: matching pending superuser and an unused, unexpired, company-linked
  superuser invitation were observed. No activation occurred. Email delivery,
  invitation usability, SSO/MCP access, and completed signup are not proved.

A wrapper may treat only `noop` or `updated`, with exact email/company UUID,
`is_superuser: true`, `details.status: ACTIVE`, and non-null
`signup_completed_at`, as user reconciliation complete. This still does not
prove an authentication round trip. `pending` requires human completion or a
separately supported signup/SSO workflow, followed by a read.

`-o json` is recommended for wrappers. YAML retains the curated detail keys;
table mode prints the result status and curated fields. `-q` emits only the user
ID, so it cannot distinguish pending from complete and is unsuitable as a
readiness check. Pending guidance goes to stderr, not into JSON results.

## Reconciliation behavior

| Observed state                                     | Behavior                                                     |
| -------------------------------------------------- | ------------------------------------------------------------ |
| ACTIVE, completed, matching superuser              | Noop, no mutation                                            |
| ACTIVE, completed, matching ordinary user          | Plan or PATCH only `is_superuser: true`                      |
| Matching pending superuser and current invite      | Pending, no resend or activation                             |
| Missing email, username, and invitation            | Plan or one company-linked superuser invitation              |
| Company mismatch or absent association             | Conflict; never infer linkage or reassign company            |
| ACTIVE but missing/null completion evidence        | Conflict `signup_completion_unproved`                        |
| Pending/inactive without a matching current invite | Conflict `signup_reconciliation_required`                    |
| Missing user with any invitation history           | Conflict `invitation_without_user`, including expired/used   |
| Another company or incompatible current invitation | Conflict `invitation_conflict`; no role conversion or resend |
| Exact email matches multiple users                 | Conflict `multiple_matches`                                  |
| Email absent but matching username exists          | Conflict `username_in_use`; no implicit identity reuse       |

All exact-email invitation metadata is inspected across companies. Conflicting
history blocks reconciliation. For an existing signup-complete ACTIVE user,
same-company used or expired invitation history does not block promotion or
trigger resend, including an earlier member/owner signup or password reset.
Current incompatible invitations still block. Pending users require a current
superuser invitation; no type is converted. Multiple current invitations,
malformed lifecycle metadata, and an ACTIVE user with a current unused
invitation fail closed.

## Verified Backend semantics and gaps

### Supported writes and capacity

The generated `UpdateUserApiV2AuthUsersUserIdPatch` binding supports
`UpdateUserRequest{IsSuperuser: &true}`. Backend partial update omits unset
fields and performs no seat/license check for this promotion. The CLI never
includes password, hashed password, status, owner, username, or company fields.

For creation, the generated
`SendSignupInvitationApiV2AuthActionSendSignupInvitationPost` binding receives
only `company_id`, `invitee_email`, and `invitation_type: SIGNUP_SUPERUSER`.
Backend creates a company-linked, superuser placeholder in `PENDING_ACTIVATION`,
creates the invitation, and queues email asynchronously. No role assignments are
sent. The separate platform superuser invitation endpoint is deliberately not
used: it creates a null company and cannot satisfy this request.

**Superuser creation is not quota-free.** The company invitation service
requires an active license, a matching company email domain, and available
seats. Its count includes ACTIVE and PENDING_ACTIVATION users, including
superusers. Superuser sign-in bypasses license enforcement, but that does not
bypass invitation capacity. Dry-run does not independently prove license,
domain, or seat eligibility. Backend enforces these when the single invitation
request is sent; this command never edits a license or activates charges to make
it succeed.

Relevant inspected Backend sources:

- `api_v2/Modules/Authentication/Modules/User/Services/UserService.py`: partial
  elevation, authorization, and password-session side effects.
- `api_v2/Modules/Authentication/Services/AuthenticationService.py`:
  `send_signup_invitation`, license/domain/capacity checks, placeholder
  creation, invitation persistence, and asynchronous email queueing.
- `database_v2/Modules/Authentication/models/local_user/LocalUserManager.py`:
  exact email-or-username invitation lookup and seat counting.
- `api_v2/Modules/Authentication/Modules/UserInvitation/Services/UserInvitationService.py`:
  ACTIVE superuser global invitation visibility.
- `api_v2/Modules/Authentication/Modules/SSO/Workos/Services/WorkosSsoService.py`:
  SSO company matching and pending owner/superuser signup completion. The CLI
  does not execute that flow.

### Signup-completion projection requires Backend delivery

The coordinated Backend source change removes `Field(exclude=True)` from
`User.signup_completed_at` in
`api_v2/Modules/Authentication/Modules/User/Models/User.py`. Authorized user
reads, PATCH and other shared user projections serialize the actual non-secret
timestamp or null. It adds no writable auth input or permission change; password
and token exclusions remain unchanged. A focused Backend contract test covers
complete/incomplete projections and field selection.

Until that Backend change is delivered, an omitted timestamp still projects as
null and reconciliation of an existing ACTIVE user blocks with exit 4,
`signup_completion_unproved`, even when `is_superuser` is already true. The CLI
does not invent a status/password substitute or hand-edit generated bindings.
Source and offline fixtures do not prove deployed response compatibility. The
timestamp itself is not proof of a password or SAML login.

Company association is not supported by the user PATCH request. Existing null or
mismatched linkage requires a separately reviewed Backend operation or owner
workflow; the CLI does not work around it with invitations or role grants.

## Bounds, failures, and recovery

- Overall user-command deadline: default/max 2 minutes; `--timeout` may narrow
  it to at least 1 second. Existing session cleanup retains its separate bounds.
- Requests: existing bounded transport, 15 seconds each, no redirects, response
  bodies at most 4 MiB, lists at most 20 pages of 100 records each by default.
  Wrappers may narrow this with `--max-pages` and, for paired stdin login,
  `--max-http-requests`. The shared counter includes authentication and a
  reserved signout attempt; see
  [the transport contract](reconciliation-primitives.md#http-and-pagination-bounds).
- Writes: at most one PATCH or invitation POST per invocation, zero retries or
  polling. Commands re-read after confirmation; this is not a retry of a write.
- Errors: existing exit taxonomy and sanitized Backend identifiers; no raw error
  causes, response bodies, or credential fields are printed.
- Exit 3: absent user/company. Exit 4: conflict or ambiguous state. Exit 5:
  unproved/forbidden actor or missing confirmation. Exit 1:
  Backend/transport/invalid-response failure or `outcome_uncertain`. Exit 6:
  read deadline. Exit 2: invalid arguments/flags.

Serialize **all** writers for the email, including browser/admin activity. The
Backend invitation operation is not an atomic ensure transaction, and PATCH has
no conditional company/status version guard. No CLI-local lock can exclude other
application writers. Unknown writer coordination blocks a live wrapper run; this
implementation does not claim distributed locking.

A failed invitation can have partially created a user/invitation before its
response fails. Server/gateway or malformed-success invitation responses are
reported as uncertain. A promotion success response that fails identity/state
verification is also uncertain. Read current user and invitation metadata before
any new write; never resend to discover whether a previous operation succeeded.
The CLI performs no automatic rollback, privilege removal, deletion, license
change, password change, or activation. Those require separate owner action.

## Offline validation

Focused tests use loopback HTTP servers with synthetic records/credentials only:

```bash
GOPROXY=off GOTOOLCHAIN=local go test -count=1 -timeout=60s ./internal/api ./internal/cmd -run '^TestUsers'
```

Command tests exercise both focused fixtures and actual root registration,
output, exit codes, confirmation, dry-run, invitation reruns, conflicts and
credential-free payloads. They do not sign in to Unstable, send real email,
invoke a provider or validate SSO/MCP readiness.
