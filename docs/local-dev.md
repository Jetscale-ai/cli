# Local Development

How to build, test, and run the CLI against your local Backend.

## Prerequisites

- Go 1.22+
- [mage](https://magefile.org/) (`go install github.com/magefile/mage@latest`)
- [golangci-lint](https://golangci-lint.run/usage/install/)
- A running JetScale Backend (via Tilt or docker-compose)

## Build

```bash
mage build        # → bin/jetscale
mage test         # go test -race ./...
mage lint         # golangci-lint run
mage smoke        # build + quick sanity checks
```

The edit → build → run cycle is:

```bash
# Edit code, then:
mage build
./bin/jetscale config instances
./bin/jetscale --local recommendations list --format json
```

## API target resolution

**The CLI always targets production SaaS by default.** Every override is
explicit and non-sticky (no hidden state to forget about).

Resolution order (first match wins):

| Priority | Mechanism           | Scope       | Example                                              |
| -------- | ------------------- | ----------- | ---------------------------------------------------- |
| 1        | `--api-url <url>`   | per-command | `jetscale --api-url http://localhost:9999 recs list` |
| 2        | `JETSCALE_API_URL`  | per-shell   | `export JETSCALE_API_URL=http://localhost:9999`      |
| 3        | `-i` / `--instance` | per-command | `jetscale -i staging recs list`                      |
| 4        | `JETSCALE_INSTANCE` | per-shell   | `export JETSCALE_INSTANCE=local`                     |
| 5        | `default_instance`  | config file | `default_instance: acme` (enterprise)                |
| 6        | hardcoded fallback  | always      | `https://console.jetscale.ai`                        |

The `--local` flag is syntactic sugar for `-i local`.

## Connecting to a local backend

```bash
jetscale --local recommendations list
# or equivalently:
jetscale -i local recommendations list
# or for the whole shell session:
export JETSCALE_INSTANCE=local
jetscale recommendations list
```

The `local` instance has `api_url: auto`, which probes `localhost:8000` then
`localhost:8010` (500ms timeout each) via `/api/v2/system/live`. It does not
matter whether you started the stack Tilt or the backend-only Tilt.

If nothing is running, you get an actionable error:

```text
Error: no local backend found (tried [http://localhost:8000 http://localhost:8010])

Start one with:
  cd ../stack && tilt up          # full stack on :8000
  cd ../backend && just tilt-setup # backend-only on :8010

Or drop the --local / -i local flag to hit production.
```

## Built-in instances

```bash
$ jetscale config instances
* production      https://console.jetscale.ai
  staging         https://jetscale.staging.jetscale.ai
  local           auto-detect (localhost:8000 / :8010)
```

The `*` marks the default instance (initially `production`).

### Add a custom instance (enterprise)

```bash
jetscale config set instance.acme https://jetscale.acme-corp.com
jetscale config set default-instance acme
```

### Check what the CLI would target

```bash
jetscale config show
# production → https://console.jetscale.ai

jetscale --local config show
# local → http://localhost:8000

JETSCALE_INSTANCE=staging jetscale config show
# staging → https://jetscale.staging.jetscale.ai
```

## Port convention (from Backend `tilt/DEVCLUSTER.md`)

The Backend Tiltfile uses offset **+10** from default ports to avoid collisions
with docker-compose:

| Service     | docker-compose | Backend Tilt | Stack Tilt |
| ----------- | -------------- | ------------ | ---------- |
| Backend API | `:8000`        | `:8010`      | `:8000`    |
| WebSocket   | —              | —            | `:8001`    |
| PostgreSQL  | `:5432`        | `:5442`      | `:5432`    |
| Redis       | `:6379`        | `:6389`      | `:6379`    |

The CLI only talks to the Backend API port. It does not need direct DB or Redis
access. The auto-detect probes both `:8000` and `:8010` so you never need to
remember which setup you started.

## Config file

Stored at `~/.config/jetscale/config.yaml` (permissions `0600`):

```yaml
default_instance: production
instances:
  production:
    api_url: https://console.jetscale.ai
  staging:
    api_url: https://jetscale.staging.jetscale.ai
  local:
    api_url: auto
```

Override the config directory with `JETSCALE_CONFIG_DIR`.

The config file is created on first write (`jetscale config set`). Until then,
built-in defaults are used in-memory.

## Typical dev session

```bash
# Terminal 1: start the stack
cd ../stack && tilt up

# Terminal 2: build and test the CLI
cd ../cli
mage build
./bin/jetscale --local config show
# local → http://localhost:8000

./bin/jetscale version
# jetscale dev (commit: abc1234, built: 2026-03-11T00:00:00Z)

# Once auth is implemented:
# ./bin/jetscale --local auth login
# ./bin/jetscale --local recommendations list --format table
```

## Environment variables reference

| Variable              | Purpose                             | Example                 |
| --------------------- | ----------------------------------- | ----------------------- |
| `JETSCALE_API_URL`    | Override API URL (highest priority) | `http://localhost:9999` |
| `JETSCALE_INSTANCE`   | Override default instance by name   | `local`                 |
| `JETSCALE_TOKEN`      | Auth token for non-interactive use  | `eyJ...` (future)       |
| `JETSCALE_CONFIG_DIR` | Override config directory           | `/tmp/jetscale-test`    |
| `JETSCALE_NO_COLOR`   | Disable colored output              | `1` (future)            |
| `JETSCALE_DEBUG`      | Enable debug logging                | `1` (future)            |
