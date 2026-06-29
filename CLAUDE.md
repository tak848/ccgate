# ccgate

Claude Code PermissionRequest hook written in Go.

## Install

```bash
mise use -g aqua:tak848/ccgate
# or
go install github.com/tak848/ccgate@latest
```

## Development

```bash
mise run build    # Build binary (dev)
mise run test     # Run tests
mise run vet      # Run go vet
```

## Coding conventions

- Go 1.25
- Wrap errors with `fmt.Errorf("...: %w", err)`
- Never silently discard errors
- Table-driven tests using `map[string]struct{...}` (the map key is the subtest name)
- No tautological tests (e.g. asserting a fixed string is present in output); test behavior, not the implementation verbatim
- Named constants for magic numbers
