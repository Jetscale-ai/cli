# Resource Commands

The `companies`, `business-units`, `plans`, `licenses`, `accounts`,
`recommendations`, `cost-sources`, `feature-flags` and `api` commands are
generic, tenant-agnostic primitives modeled on `gh` and `kubectl`. Environment
intent (which company, plan, flags or accounts) belongs to the caller, for
example Stack's `scripts/unstable-seed.py`.

## Contract

- **Shape:** `jetscale <resource> <verb>` with `list`, `get` (aliases `show`,
  `view`), `create`, `set`, `link`, `status`, `wait`, `validate` and `enable`.
  Help groups them into Core, Billing and Additional commands, and every command
  has Examples. Selectors accept an ID, slug, provider account ID or exact name,
  as each command states.
- **Scope:** `--company` defaults to the channel `COMPANY` variable, then to
  `config set company <slug>` for the instance. Reads without a company cover
  every account you can access. `--account` scopes a write only when given on
  the command line; the active account from `accounts use` applies to reads.
- **Output:** `-o json` prints curated fields only; `-q` prints IDs one per
  line. Lists print `[]` when empty; tables say "No ... found."
- **Errors:** stderr shows `Error: …` and a `Try: …` line with a corrected
  invocation. Under `-o json`, stdout also gets one
  `{"error": {"code", "reason", "message", "try", "transient", ...}}` object
  with the Backend's error name and required permission, never its free-text
  causes. `transient` is true only when repeating the same command is safe.
- **Idempotent writes:** `create`, `set` and `link` read first. Matching state
  is "already exists (unchanged)"; absent state is created and read back;
  differing state is drift (exit 7) and is never changed. `--dry-run` reports
  `planned` without writing.
- **One attempt:** no write is retried. A write whose outcome could not be
  observed, including a gateway 502/504, exits 1 with reason
  `outcome_uncertain`; read before doing anything else.
- **Confirmation:** flag writes at root, company or business-unit scope fan out,
  and `cost-sources enable` and `recommendations regenerate` start work. On a
  terminal they ask; with `-y/--yes` they skip the prompt; otherwise (no
  terminal, `CI` set, or `--password-stdin`) they exit 5,
  `confirmation_required`, after printing any plan.
- **Secrets:** provider secrets are read only from `--credential-file`, a JSON
  object from a private file or an inherited descriptor such as `/dev/fd/3`,
  accepting only the provider's known secret keys. They never appear in
  arguments, the environment or output.
- **Guards:** `--require-superuser` refuses (exit 5, `visibility_unproved`)
  unless `/auth/me` shows a nonzero actor ID and explicit `is_superuser: true`.
  `--timeout` bounds the whole command (default 2m, max 30m).
- **Escape hatch:** `jetscale api <path>` sends any request with the same
  session, like `gh api`: GET by default, POST when `-f`/`-F` fields or
  `--input` are given (use `-X GET` for query parameters), `--paginate` merges
  every page into one array. It prints the reply as received and exits by the
  same codes.

## Exit codes

| Code | Meaning                                                 |
| ---- | ------------------------------------------------------- |
| 0    | Success, including `noop` and `planned`                 |
| 1    | Backend or transport failure, or an uncertain write     |
| 2    | Usage error, including unknown flags                    |
| 3    | Not found                                               |
| 4    | Conflict or ambiguous selector                          |
| 5    | Unauthenticated, forbidden, refused or needs `--yes`    |
| 6    | Timeout                                                 |
| 7    | Drift: the resource exists but differs from the request |

## Known Backend limits

- Business units have no slug uniqueness in the database, and managed-agent
  provisioning follows the unit's commit. Serialize creates per company and
  never create again after an uncertain outcome.
- A cloud account's stored credentials cannot be read back, so `link` reports
  `credentials_compared: false` for an existing link.
- Linking a provider account whose integration exists outside the caller's view
  is a conflict (`integration_exists_outside_selection`).
