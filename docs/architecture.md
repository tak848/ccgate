# ccgate — Architecture

[日本語版 (docs/ja/architecture.md)](ja/architecture.md)

Package layout and how to add a new target.

## Layout (v0.6)

```
ccgate/
├── main.go                      # cli.Run delegate only
├── schemas/                     # published JSON schemas (committed)
│   ├── claude.schema.json
│   └── codex.schema.json
├── scripts/genschema/           # generator for the above
└── internal/
    ├── cli/                     # kong subcommand wiring
    ├── cmd/
    │   ├── claude/              # Claude Code hook (Run / Init / Metrics)
    │   └── codex/               # Codex CLI hook (Run / Init / Metrics)
    ├── prompt/                  # shared prompt builder (target-aware section)
    ├── llm/                     # shared types (Provider / Prompt / Output / Decision)
    │   └── anthropic/           # Provider implementation
    ├── config/                  # shared LoadOptions + jsonnet merge
    ├── metrics/                 # shared writer / reader / report
    ├── gate/                    # claude-only legacy orchestration (under refactor)
    ├── hookctx/                 # claude-only HookInput / settings / transcript
    └── gitutil/                 # shared git helpers
```

> The `internal/gate/` and `internal/hookctx/` packages are still used by `cmd/claude/` today; lifting them down into `cmd/claude/` is tracked as a follow-up so this v0.6 PR stays bounded.

## Adding a new target

1. Create `internal/cmd/<target>/` with `Run`, `Init`, `Metrics`, and `LoadOptions()`.
2. Embed a `defaults.jsonnet` (Claude Code parity: allow + deny + environment).
3. Add `internal/cli/<target>_cmd.go` with kong-bound subcommand structs and dispatch entries in `internal/cli/cli.go`.
4. Generate a per-target schema via `mise run schema` (extend `scripts/genschema/main.go` if the target needs a different Config struct).
5. Add docs at `docs/<target>.md` + `docs/ja/<target>.md` (1:1 en/ja mirror).

> A fully fleshed-out walkthrough with code snippets is the planned content for this page. The bullets above describe the shape of the contribution; cross-reference `internal/cmd/codex/` as a worked example.

## Defaults parity (Claude vs Codex)

Both targets ship `allow + deny + environment` guidance per the project philosophy. The wording diverges because Claude classifies by tool kind (Read/Edit/Bash/etc.) while Codex hooks fire for Bash + apply_patch + MCP through the same surface, so the Codex defaults talk about command shape and MCP server trust instead.

Intentional gaps (one side only) and their reason:

| Side       | Category                                       | Why                                                                                                                          |
|------------|------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------|
| Claude     | `Sibling Checkout / Worktree Confusion`        | Claude Code surfaces `is_worktree`; Codex does not. Worktree-confusion failure mode is Claude-specific.                      |
| Claude     | `Library Source Read`, `Read-Only Operations`  | Claude exposes Read/Glob/Grep against package caches as their own tool surface; Codex routes the same ops through Bash and is covered by the read-only Bash rule. |
| Claude     | `Draft PR Creation`                            | Claude has the `draft` flag in `tool_input_raw` for `gh pr create`; Codex hooks do not surface the same field.               |
| Codex      | `MCP tools whose server is explicitly trusted` | MCP-specific allow path; Claude's hook does not currently dispatch MCP through ccgate.                                       |
| Codex      | `MCP tools that advertise destructive side effects` | Same reason — MCP-only deny lane.                                                                                       |
| Codex      | `sudo or other privilege escalation`           | Codex makes this explicit because the Codex hook routes privilege-escalation Bash through ccgate; Claude relies on `~/.claude/settings.json` for the same coverage. |
| Codex      | `Unrestricted network out`                     | Codex's auto-approval ladder reaches `nc` / `ssh` / `scp` / `ftp` more often than Claude's, so the deny rule is given prominence here. |
| Codex      | `apply_patch` is a write surface (environment) | Codex-specific tool surface. Claude has no equivalent because it dispatches `Edit` / `Write` directly.                       |

Categories that exist on both sides under different wording (`Download and Execute`, `Direct Tool Invocation` vs `Direct one-shot remote package execution bypassing project scripts`, `Git Destructive` vs `Git destructive`, `Out-of-Repo Deletion` vs `rm -rf or mv targeting paths outside the workspace`) cover the same intent. Mechanically asserting equality is fragile because the wording is intentionally re-phrased per target; reviewers should sanity-check parity manually when changing either defaults file.

## Upstream specs

ccgate's behavior is constrained by the upstream hook docs of each target. Treat these as the source of truth before changing adapter / fixture / spec-citation code:

- Claude Code hooks: <https://code.claude.com/docs/en/hooks>
- OpenAI Codex hooks: <https://developers.openai.com/codex/hooks>
- OpenAI Codex config reference: <https://developers.openai.com/codex/config-reference>

When a PR changes how ccgate parses a hook payload or what fields it relies on, link the relevant upstream section in the PR description so reviewers can verify against current docs (the upstream surfaces still move).
