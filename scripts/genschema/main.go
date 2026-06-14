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
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/tak848/ccgate/internal/config"
)

// draft2020 is the JSON Schema dialect the generated files advertise.
// google/jsonschema-go does not emit $schema during reflection, so the
// generator sets it explicitly to match what editors expect.
const draft2020 = "https://json-schema.org/draft/2020-12/schema"

const (
	repoBase = "https://raw.githubusercontent.com/tak848/ccgate/main/schemas"
)

// claudeOnlyConfigKeys lists Config struct json keys that are
// meaningful only for the Claude Code target today. They live on
// the shared Config struct so the loader / merger does not need
// per-target plumbing, but writing them in a codex config has no
// effect, so the codex schema strips them to avoid suggesting
// otherwise to editor users.
var claudeOnlyConfigKeys = []string{
	"include_settings_permissions_in_prompt",
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "genschema: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := repoRoot()
	if err != nil {
		return fmt.Errorf("locate repo root: %w", err)
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
	// For inlines nested structs (no $defs/$ref) and emits
	// additionalProperties:false plus a per-field required list for every
	// struct, matching the strict, dereferenced shape the editor schemas
	// have always shipped.
	schema, err := jsonschema.For[config.Config](nil)
	if err != nil {
		return fmt.Errorf("reflect config: %w", err)
	}
	// google/jsonschema-go widens Go pointers and slices/maps to the
	// nullable spelling (`"type": ["null", T]`). The config loader treats
	// an omitted key and an explicit null identically, and the schema has
	// always rejected a literal null, so collapse it back to the bare type
	// to keep the editor schema strict and the migration behavior-neutral.
	stripNullable(schema)
	schema.Schema = draft2020
	schema.ID = repoBase + "/" + target + ".schema.json"
	schema.Title = "ccgate " + target + " configuration"
	schema.Description = "Configuration schema for ccgate's " + target +
		" PermissionRequest hook. See https://github.com/tak848/ccgate."

	// Project-local config files (`{repo_root}/.<target>/ccgate.local.jsonnet`)
	// intentionally do not carry `provider` — they only append allow / deny /
	// environment on top of the global base. Marking `provider` as required
	// at the root would make those files fail editor validation. Drop the
	// root-level required list entirely; per-field required (e.g. provider's
	// nested name / model) is still emitted by the reflector.
	schema.Required = nil

	// provider.auth is a discriminated union. google/jsonschema-go has no
	// custom-schema interface, and the field is a pointer (reflection would
	// otherwise widen it with a "null" type that makes the oneOf
	// unsatisfiable), so overwrite the reflected property with the
	// hand-built oneOf from config.AuthConfigSchema.
	if prov := schema.Properties["provider"]; prov != nil && prov.Properties != nil {
		if _, ok := prov.Properties["auth"]; ok {
			prov.Properties["auth"] = config.AuthConfigSchema()
		}
	}

	// AdditionalProperties=false at the root rejects any key not in
	// properties. The Config struct already carries a `$schema` field
	// (so editors can pick the right schema via the templates' leading
	// `['$schema']: '...'`); overwrite the plainly-reflected string with
	// a format:uri + description variant. PropertyOrder already lists it
	// first, as it is the struct's first field.
	if schema.Properties != nil {
		schema.Properties["$schema"] = &jsonschema.Schema{
			Type:        "string",
			Format:      "uri",
			Description: "JSON schema reference. Editors use this to enable validation; ccgate ignores it at runtime.",
		}
		if target == "codex" {
			for _, key := range claudeOnlyConfigKeys {
				delete(schema.Properties, key)
				schema.PropertyOrder = slices.DeleteFunc(schema.PropertyOrder, func(k string) bool { return k == key })
			}
		}
	}

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// Trailing newline so the file is POSIX-friendly.
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// stripNullable rewrites every schema node that lists "null" alongside a
// single concrete type (google/jsonschema-go's spelling for Go pointers
// and slices/maps) down to that bare type, recursing through the schema
// tree. It leaves multi-type unions untouched.
func stripNullable(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if slices.Contains(s.Types, "null") {
		rest := slices.DeleteFunc(slices.Clone(s.Types), func(t string) bool { return t == "null" })
		switch len(rest) {
		case 0:
			s.Types = nil
		case 1:
			s.Type, s.Types = rest[0], nil
		default:
			s.Types = rest
		}
	}
	for _, p := range s.Properties {
		stripNullable(p)
	}
	stripNullable(s.Items)
	stripNullable(s.AdditionalProperties)
	stripNullable(s.Not)
	for _, b := range s.PrefixItems {
		stripNullable(b)
	}
	for _, b := range s.OneOf {
		stripNullable(b)
	}
	for _, b := range s.AnyOf {
		stripNullable(b)
	}
	for _, b := range s.AllOf {
		stripNullable(b)
	}
}

// repoRoot returns the directory containing go.mod so the generator can be
// invoked from anywhere (`go generate ./...` from a sub-package, mise tasks
// from a worktree, etc.) without writing schemas next to the caller's cwd.
func repoRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("go env GOMOD: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("not inside a Go module (go env GOMOD = %q)", gomod)
	}
	return filepath.Dir(gomod), nil
}
