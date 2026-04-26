## Why

<!-- Why is this change needed? What problem does it solve? Write in English. -->

## What

<!-- What does this PR do? Keep it concise. Write in English. -->

## Test plan

<!--
- [ ] mise run vet
- [ ] mise run lint
- [ ] mise run test
- [ ] Manual smoke check (describe what you ran)
Drop bullets that don't apply. Add new ones for hook fixtures, binary E2E, etc.
-->

## Checklist

<!--
Tick what applies. Untick what doesn't and remove the line if not relevant.
-->

- [ ] Public schema (`internal/config.Config`) changed → `mise run schema` re-run, `schemas/{claude,codex}.schema.json` committed
- [ ] User-facing behavior change → docs (`README.md`, `docs/*.md`) updated
- [ ] English doc change → matching `docs/ja/*.md` mirror updated in the same PR
- [ ] Breaking change (CLI flag removal, config field removal, file path change) → called out in this PR body and PR labeled `breaking-change`
- [ ] New embedded `defaults.jsonnet` rule → covered by `internal/cmd/<target>/defaults_test.go`
- [ ] Adapter / fixture / spec-citation change → linked the relevant upstream Anthropic / OpenAI hook docs section in the PR body

## Notes

<!-- Out-of-scope follow-ups, known limitations, links to related issues. Optional. -->
