// Package schemas hosts the published JSON schemas for ccgate's
// per-target configuration. The actual files (claude.schema.json,
// codex.schema.json) are committed to the repo so editor users can
// reference them without needing a Go toolchain.
//
// Regenerate with `mise run schema` (or `go generate ./schemas/...`)
// whenever internal/config.Config changes.
package schemas

//go:generate go run ../scripts/genschema
