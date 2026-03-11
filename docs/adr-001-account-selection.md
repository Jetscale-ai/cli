# ADR-001: Cloud Account Selection Model

- **Status:** Accepted
- **Date:** 2026-03-11
- **Authors:** @paul

## Context

JetScale is a multi-tenant cloud cost optimization platform. The backend
organises resources in a three-level hierarchy:

```text
Company (tenant root, 1 per user)
  └── BusinessUnit (organisational grouping, N per company)
        └── CloudAccount (AWS account / Azure sub / GCP project, N per BU)
```

Every domain operation — listing recommendations, triggering discovery,
generating remediation plans — is scoped to a **CloudAccount**. The backend
requires an explicit `cloud_account_id` on each request; there is no server-side
concept of "active" or "default" account.

CLI tools that operate against scoped resources (gcloud, aws, az, kubectl) all
solve this with a local context that is injected into API calls automatically.
Without one, every command would need a `--cloud-account-id <uuid>` flag, which
is impractical for interactive use.

## Decision

The CLI will implement a **local account selection model** with the following
properties:

### 1. Hierarchical discovery, flat selection

The `accounts list` command will internally chain three API calls:

1. `GET /api/v2/auth/me` → user's company UUID
2. `GET /api/v2/organization/business-units?company=<uuid>` → BUs
3. `GET /api/v2/cloud/cloud-accounts?business_unit=<bu1>,<bu2>,...` → accounts

The result is displayed as a flat list grouped by business unit:

```text
$ jetscale accounts list
  BU: Engineering
    prod-us-east-1       AWS     a9f3b2c1-...
    staging-eu-west-1    AWS     d4e5f6a7-...
  BU: Data Platform
  * analytics-gcp        GCP     c8d9e0f1-...
```

Users select accounts by **name** (human-readable, unique within a company) or
by **UUID**. The business unit is implicit — it is never selected directly.

### 2. Sticky per-instance selection

The active account is stored per-instance in `~/.config/jetscale/config.yaml`:

```yaml
default_instance: production
instances:
  production:
    api_url: https://console.jetscale.ai
    active_account: prod-us-east-1
  local:
    api_url: auto
    active_account: staging-eu-west-1
```

This is intentionally **sticky** (unlike instance selection, which is
non-sticky). Cloud account is a working context you stay in across commands —
the same mental model as `gcloud config set project`.

### 3. Resolution order

When a command needs a cloud account, the CLI resolves it as follows (first
match wins):

| Priority | Mechanism          | Scope       | Example                                           |
| -------- | ------------------ | ----------- | ------------------------------------------------- |
| 1        | `--account` flag   | per-command | `jetscale --account staging-eu-west-1 recs list`  |
| 2        | `JETSCALE_ACCOUNT` | per-shell   | `export JETSCALE_ACCOUNT=staging-eu-west-1`       |
| 3        | `active_account`   | config file | `jetscale accounts use prod-us-east-1`            |
| 4        | auto-select        | implicit    | If exactly one account exists, use it             |
| 5        | error              | —           | "no account selected — run jetscale accounts use" |

### 4. Account identifier is the name, not the UUID

Users interact with account names (`prod-us-east-1`), not UUIDs. The CLI
resolves name → UUID at call time. This mirrors `gcloud` (project ID, not
number), `kubectl` (context name, not cluster ARN), and `az` (subscription name,
not GUID).

If a name is ambiguous (unlikely given uniqueness within a company), the CLI
will prompt or error with candidates.

### 5. Auto-select for single-account users

If the authenticated user has access to exactly one cloud account, the CLI will
use it automatically without requiring `accounts use`. This covers the
onboarding case where a new user links their first AWS account and immediately
wants to run commands.

### 6. Commands

```bash
jetscale accounts list              # discover all accessible accounts
jetscale accounts use <name>        # set active account (sticky)
jetscale accounts current           # show active account
```

### 7. Analogy table

| CLI        | Scope concept | Selection command               | Sticky | Stored where                     |
| ---------- | ------------- | ------------------------------- | ------ | -------------------------------- |
| `gcloud`   | project       | `gcloud config set project`     | Yes    | `~/.config/gcloud/properties`    |
| `aws`      | profile       | `AWS_PROFILE=x` / `--profile`   | No     | `~/.aws/config`                  |
| `az`       | subscription  | `az account set --subscription` | Yes    | `~/.azure/azureProfile.json`     |
| `kubectl`  | context       | `kubectl config use-context`    | Yes    | `~/.kube/config`                 |
| `jetscale` | cloud account | `jetscale accounts use`         | Yes    | `~/.config/jetscale/config.yaml` |

## Consequences

- **Positive:** Users never type UUIDs in interactive use. The flat-list model
  hides the Company → BU → Account hierarchy for the common case.
- **Positive:** Per-instance storage means `--local` and production contexts
  maintain independent account selections.
- **Positive:** Auto-select eliminates a setup step for single-account users.
- **Negative:** The 3-call discovery chain adds latency to `accounts list`. This
  is acceptable because it runs infrequently (once per session). Results could
  be cached in a future iteration.
- **Negative:** If the backend later enforces `allowed_cloud_account_ids`
  server-side, the CLI's client-side filtering becomes redundant. This is fine —
  defence in depth.
- **Trade-off:** Account selection is sticky (persisted), unlike instance
  selection (non-sticky). This is deliberate: switching instances is a safety
  concern (local vs production), while switching accounts is a workflow concern
  (which project am I working on). The mental model matches gcloud and kubectl.
