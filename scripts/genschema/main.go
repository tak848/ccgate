// Command genschema regenerates the per-target JSON schemas under
// schemas/. Invoked via `go generate ./...` (see internal/cmd/{claude,
// codex}/schema_gen.go) and from `mise run schema`.
//
// Both targets share config.Config today, but they get separate schema
// files anyway so editor users get a target-specific $id and so we can
// diverge the schema later (e.g. when codex grows codex-specific
// fields) without breaking claude users' editor integrations.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/invopop/jsonschema"

	"github.com/tak848/ccgate/internal/config"
)

const (
	repoBase = "https://raw.githubusercontent.com/tak848/ccgate/main/schemas"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "genschema: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	outDir := filepath.Join(root, "schemas")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	for _, t := range []struct{ name, file string }{
		{"claude", "claude.schema.json"},
		{"codex", "codex.schema.json"},
	} {
		if err := writeSchema(filepath.Join(outDir, t.file), t.name); err != nil {
			return fmt.Errorf("write %s: %w", t.file, err)
		}
		fmt.Fprintf(os.Stderr, "wrote schemas/%s\n", t.file)
	}
	return nil
}

func writeSchema(path, target string) error {
	r := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	schema := r.Reflect(config.Config{})
	schema.ID = jsonschema.ID(repoBase + "/" + target + ".schema.json")
	schema.Title = "ccgate " + target + " configuration"
	schema.Description = "Configuration schema for ccgate's " + target +
		" PermissionRequest hook. See https://github.com/tak848/ccgate."

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// Trailing newline so the file is POSIX-friendly.
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
