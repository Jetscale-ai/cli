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

## Recommendation regeneration

`recommendations regenerate <recommendation-id>` reruns an existing resource
recommendation in place. Success overwrites its result while keeping the same
recommendation UUID and version; no new-version API exists yet. Use the same
recommendation UUID for browser, CLI and MCP comparisons with or without FOCUS.
The Backend selects eligible evidence sources by default. For a comparison
without FOCUS, exclude only the FOCUS source:

```sh
# Without FOCUS for this run only
jetscale recommendations regenerate <recommendation-id> \
  --exclude-source focus-cost-attribution --yes -o json

# Wait for completion and capture the first result and evidence before continuing.
# Same UUID/version, normal eligible sources (including FOCUS when available)
jetscale recommendations regenerate <recommendation-id> --yes -o json
```

`--exclude-source` is repeatable. The CLI rejects blank keys, duplicate keys and
more than 32 exclusions before authentication or API access. The Backend decides
which keys the resource supports and requires superuser authorization for
exclusions; the flag does not grant that permission.

Exclusions are not saved as CLI defaults, do not disable billing sources, and do
not delete or re-ingest FOCUS data. Omitting the flag does not force FOCUS to be
available or prove model consumption. Verify each run's operation-bound saved
evidence separately. Every accepted request starts inference; wait for the first
run to finish and capture its completed result and evidence before submitting
the second. The next successful run replaces them on the same recommendation, so
the shared UUID/version is not a comparison snapshot. Reconcile an uncertain
outcome rather than retrying.

For local testing, use `./bin/jetscale-local` from `cli/` with
`--api-url https://jetscale.internal` and
`--ca-file ../stack/certs/jetscale-dev-ca.pem`. Reuse the human-authenticated
session; browser or MCP reauthentication does not authenticate the CLI. These
examples document capability, not authorization to start inference on any
tenant.

## Known Backend limits

- Since Backend 13.41.2 a business unit's slug is unique per company: an
  identical create returns the existing unit and a differing one conflicts.
  Managed-agent provisioning still follows the unit's commit; never create again
  after an uncertain outcome.
- Company domain emails are not unique in the database. `companies create`
  refuses a name or domain that another slug already has (`name_in_use`,
  `domain_in_use`), but that check is a read before the write; only a Backend
  whose setup refuses a domain another company owns closes the race.
- A cloud account's stored credentials cannot be read back, so `link` reports
  `credentials_compared: false` for an existing link.
- Linking a provider account whose integration exists outside the caller's view
  is a conflict (`integration_exists_outside_selection`).
