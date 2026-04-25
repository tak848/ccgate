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

## Spec Ledger

Per-target spec verification status is tracked in the v0.6 plan (`.claude/plans/codex-cli-hook-system-piped-badger.md`, section A2). When you change adapter / docs / fixture / spec-citation code, update the relevant Spec Ledger row in the same PR.
