# CLI Repository Operations

**Status:** Operational Details  
**Scope:** `Jetscale-ai/cli` only  
**Authority:** [Repository Constitution](../AGENTS.md) →
[Supreme Constitution](https://github.com/Jetscale-ai/Governance/blob/main/AGENTS.md)

---

## Commit Workflow

Agents prepare changes. Humans commit.

1. Agent makes file changes using Write/StrReplace/Delete tools.
2. Agent suggests a commit message following
   [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`,
   `fix:`, `chore:`, `docs:`, `refactor:`, `test:`, `ci:`).
3. Agent **STOPS** and waits for human to review and commit.

Material changes must include an `audit_log:` trailer:

```text
feat: add recommendations list command

audit_log:
  invariants: [Traceability, Contract-Driven]
  reason: First operator command backed by generated OpenAPI client
  issue: https://github.com/Jetscale-ai/cli/issues/NNN
```

## Verification Oracle

```bash
make test && make lint
```

Run this before suggesting any commit. If it fails, fix the issue first.

## Code Organization Rules

### Generated Code

- `internal/api/generated/` is machine-generated. Never hand-edit.
- Regenerate with `make generate` after updating `openapi/spec.json`.
- If regeneration breaks the build, fix in the spec or in wrapper code, not in
  the generated output.

### Command Structure

Each command lives in its own file under `internal/cmd/`:

```text
internal/cmd/
├── root.go            # Root command + global flags
├── version.go         # jetscale version
├── auth.go            # jetscale auth login|status|logout
├── config.go          # jetscale config set|get|list
├── analyze.go         # jetscale analyze <query>
├── recommendations.go # jetscale recommendations list|show|generate
└── plan.go            # jetscale plan list|show
```

### Adding a New Command

1. Create `internal/cmd/<name>.go` with a `newXxxCmd()` function.
2. Register it in `root.go` via `root.AddCommand(newXxxCmd())`.
3. Add a test in `internal/cmd/<name>_test.go`.
4. Verify: `make test && make lint`.

## Branch Strategy

- `main` is the release branch. Protected.
- Feature branches: `feat/<description>` or `fix/<description>`.
- OpenAPI sync branches: `chore/sync-openapi-*` (created by CI).

## Dependencies

- Use `go mod tidy` after adding/removing imports.
- Pin major versions. Avoid `@latest` in production code.
- `go.mod` and `go.sum` are authoritative lockfiles and must be committed.

## Release

Releases are tag-driven:

1. Create a tag: `git tag v0.1.0`
2. Push: `git push origin v0.1.0`
3. GoReleaser builds multi-platform binaries and creates a GitHub Release.
4. Homebrew tap is updated automatically.

Do not manually create GitHub Releases.
