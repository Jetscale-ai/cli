# CLI agent workspace

CLI's agent capabilities are managed from the sibling `../governance/` checkout.
The [repository constitution](../AGENTS.md) and [local operations](agents.md)
remain CLI-owned. Published
[Governance main](https://github.com/Jetscale-AI/Governance/blob/main/AGENTS.md)
is governing law; local drafts do not gain authority by being linked here.

## Import selection

[`imports.json`](imports.json) declares:

| Import                | Purpose                                                                  |
| --------------------- | ------------------------------------------------------------------------ |
| `baseline`            | Shared planning, review, Git safety, documentation, and save guidance    |
| `go-service`          | Go debugging, test/review workflows, Markdown, and diagrams              |
| `terminal`            | Line-oriented CLI output contracts and optional full-screen TUI guidance |
| `gha-workflow-naming` | Explicit Governance-owned skill for CI workflow naming                   |

The extra skill uses `source: "repo"` to select Governance's authored skill; it
is not a CLI-local override. The broader `github-actions` bundle is not selected
because its additional auto-merge agent is unnecessary here. Ordinary Cobra
commands use `terminal-ux`; `tui-design` applies only to persistent-screen
interfaces. No CLI-specific save wrapper is introduced.

Only the `skills` and `agents` component bridges are requested. The materializer
also links the baseline workspace policy files and vendor companion resources
needed by imported guidance. Companion availability is not execution authority.
Manifest exclusions describe content scope; they are not security controls or
permissions to bypass generated-code protections.

## Ownership

- `imports.json` and this guide are CLI-owned.
- `agents.md` is the real CLI operating source. `AGENTS.md` is a relative
  compatibility symlink to it; existing references keep working.
- `skills/`, `agents/`, `.cursor/skills/`, `.claude/skills/`, and runtime agent
  projections are generated. Canonical skill directories include managed markers
  and file-level symlinks for Zed discovery. Host-specific capabilities may be
  intentionally omitted from Zed's canonical surface.
- Baseline `system-prompt.md`, `mcp.json`, `models.json`,
  `dotagents-settings.json`, and `layouts/ui.json` link to Governance sources.
- Imported content is not copied or vendored into CLI. Do not modify it through
  these links; edit the manifest or propose an owner-side change instead.

The selected MCP baseline has an empty `mcpServers` map and the dotagents
settings baseline is disabled. Materialization does not register an external
capture service, copy credentials, or submit CLI/API data. This is not a blanket
privacy guarantee for future tools: review any changed imports or integrations
before use. Existing editor/model data handling remains separate.

## Refresh from Governance

Keep the checkouts as sibling directories named `governance` and `cli`, with
Governance's vendored dependencies already available. From `governance/`:

```sh
pnpm materialize:runtimes
pnpm govern --repo ../cli --dry-run
pnpm govern --repo ../cli
pnpm check:runtimes
```

For routine refreshes, use the checked-in manifest rather than passing new
`--bundle` arguments, which replace the import selection. Review the dry-run's
resolved sources, host eligibility, and local-override preservation before
writing. Real local operating files are preserved by the materializer.

The bridge uses local relative links; updating Governance can change the
available guidance without a CLI source change. Review the Governance revision
and dirty state, and load current published law before acting. Re-governing is
not law ratification or authorization to activate proposed CI workflows.

## Validation and handoff

After refresh:

1. Confirm `agents.md` is still a real local file and `AGENTS.md` points to it.
2. Check generated symlinks resolve with the sibling checkout present.
3. Run `pnpm govern --repo ../cli` again and confirm the manifest, local
   instructions, and projection contents are unchanged.
4. Run targeted Markdown checks on CLI-owned documentation, not imported
   sources. Review changes in both repositories without staging or committing.

Humans review and commit law changes. CLI's existing agent commit/push/tag ban
still applies. No schema generation, binary installation, application login, API
test, release, or deployment is part of this setup. Governance is an
agent-development dependency only: ordinary Go builds and distributed binaries
do not require the sibling checkout.
