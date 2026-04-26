# Contributing

Thank you for considering contributing to ccgate!

## Development setup

ccgate uses [mise](https://mise.jdx.dev/) to pin the Go toolchain and
the linter. Once mise is installed, every other tool is bootstrapped
for you:

```bash
mise install
```

This installs Go and `golangci-lint` (via aqua). All tasks below assume
mise is on your PATH.

## Day-to-day commands

| Task                | Command                                  |
|---------------------|------------------------------------------|
| Build local binary  | `mise run build` (outputs `bin/ccgate`)  |
| Run unit tests      | `mise run test` (race + cover)           |
| Run `go vet`        | `mise run vet`                           |
| Run lint suite      | `mise run lint` (vet + golangci-lint)    |
| Apply formatters    | `mise run fmt` (gofmt + goimports)       |
| Regenerate schemas  | `mise run schema`                        |
| Coverage HTML       | `mise run cover`                         |
| Clean build dir     | `mise run clean`                         |

`mise run lint` must pass at zero issues before opening a PR. The CI
matrix runs `mise run vet` + `mise run test` on Ubuntu / macOS /
Windows, and `mise run lint` on Ubuntu.

## Working on a target package

ccgate's per-target hook implementations live under
`internal/cmd/{claude,codex}/`. The shared primitives
(`internal/{prompt,llm,llm/anthropic,config,metrics,gitutil}`) are
target-agnostic. See [docs/architecture.md](docs/architecture.md)
for the package layout and how to add a new target.

If your change touches `internal/config.Config`, regenerate the public
JSON schemas before committing:

```bash
mise run schema
git diff schemas/
```

The `schemas/{claude,codex}.schema.json` files are committed so editor
users can reference them without a Go toolchain.

## Embedded defaults

The defaults for each target live in
`internal/cmd/<target>/defaults.jsonnet` (Codex) or
`internal/config/defaults.jsonnet` (Claude — moves into
`internal/cmd/claude/` in a follow-up). Adding or removing rules in
either should:

- be reflected in the matching `defaults_test.go` so the rule taxonomy
  cannot regress silently;
- preserve Claude Code parity (allow + deny + environment, similar
  category coverage on both sides) per the project philosophy.

## Documentation

Docs are bilingual on a 1:1 basis:

- English: `docs/{claude,codex,configuration,architecture}.md`
- Japanese: `docs/ja/{claude,codex,configuration,architecture}.md`
  plus `docs/ja/README.md`.

Any user-facing change must update both languages in the same PR.

## Pull requests

The PR template walks through the expected sections (Why / What / Test
plan / Checklist). Notable items the checklist asks for:

- public schema change → schemas regenerated and committed;
- breaking change → CHANGELOG entry under the right minor / major version;
- English doc change → matching `docs/ja/*` mirror updated;
- adapter / fixture / spec-citation change → link the relevant upstream
  Anthropic / OpenAI hook docs section in the PR body so reviewers
  can verify the change against current upstream docs.

PR / commit messages and review replies in this repository are written
in **English**. Chat / plan files / code comments may stay in any
language.

## Reporting issues

Please open an issue on GitHub with a clear description of the problem
and steps to reproduce it. For security-sensitive reports, contact the
maintainer privately rather than opening a public issue.

## License

By contributing, you agree that your contributions will be licensed under
the MIT License.
