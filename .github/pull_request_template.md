## Why

<!-- Why is this change needed? What problem does it solve? Write in English. -->

## What

<!-- What does this PR do? Keep it concise. Write in English. -->

## Test plan

<!--
- [ ] mise run vet
- [ ] mise run lint
- [ ] mise run test
- [ ] mise run test:e2e (when applicable)
- [ ] Manual smoke check (describe what you ran)
Drop bullets that don't apply. Add new ones for hook fixtures, binary E2E, etc.
-->

## Checklist

<!--
Tick what applies. Untick what doesn't and remove the line if not relevant.
-->

- [ ] Adapter / docs / fixture / spec-citation change → Spec Ledger updated (or noted as N/A)
- [ ] Public schema (`internal/config.Config`) changed → `mise run schema` re-run, `schemas/{claude,codex}.schema.json` committed
- [ ] User-facing behavior change → docs (`README.md`, `docs/*.md`) updated
- [ ] English doc change → matching `docs/ja/*.md` mirror updated in the same PR
- [ ] Breaking change (CLI flag removal, config field removal, file path change) → CHANGELOG entry added under the right minor / major version
- [ ] New embedded `defaults.jsonnet` rule → covered by `internal/cmd/<target>/defaults_test.go`

## Notes

<!-- Out-of-scope follow-ups, known limitations, links to related issues. Optional. -->
