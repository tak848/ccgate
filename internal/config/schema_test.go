package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

// TestProviderSchemaDrift makes sure the hand-edited root
// `ccgate.schema.json` and the generator-driven per-target schemas
// agree on the set of `provider.*` keys. Adding a field to
// ProviderConfig regenerates the per-target schemas via
// `mise run schema`, but the root schema is hand-maintained for
// editor users who pin to it; this test guards against forgetting
// to update one and not the other.
func TestProviderSchemaDrift(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	manualKeys := readProviderKeys(t, filepath.Join(root, "ccgate.schema.json"))
	for _, name := range []string{"claude.schema.json", "codex.schema.json"} {
		generatedKeys := readProviderKeys(t, filepath.Join(root, "schemas", name))
		if !equalKeys(manualKeys, generatedKeys) {
			t.Fatalf("provider keys drift between ccgate.schema.json and schemas/%s\n  manual: %v\n  generated: %v\nrun `mise run schema` and update ccgate.schema.json",
				name, manualKeys, generatedKeys)
		}
	}
}

// TestRootSchemaTopLevelDrift extends the same idea to the
// top-level keys (`allow`, `deny`, `append_allow`, `log_path`, ...).
// The hand-edited root schema declared `additionalProperties:
// false`, so a missing top-level key would silently mark a valid
// config invalid for any editor pinning to the root schema. We
// allow the manual schema to add `$schema` (the editor-pointer key
// embedded in defaults templates) since the generator does not
// emit it for free.
func TestRootSchemaTopLevelDrift(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	manual := readTopLevelKeys(t, filepath.Join(root, "ccgate.schema.json"))
	// The generator emits the same Config struct for every target,
	// so checking one is sufficient.
	generated := readTopLevelKeys(t, filepath.Join(root, "schemas", "claude.schema.json"))

	// Manual root may legitimately carry "$schema" which the
	// generator script does not produce by default.
	manualNoSchema := filterOut(manual, "$schema")
	generatedNoSchema := filterOut(generated, "$schema")
	if !equalKeys(manualNoSchema, generatedNoSchema) {
		t.Fatalf("top-level keys drift between ccgate.schema.json and schemas/claude.schema.json\n  manual: %v\n  generated: %v\nrun `mise run schema` and update ccgate.schema.json",
			manualNoSchema, generatedNoSchema)
	}
}

// TestProviderAuthOneOfShape pins the discriminator-union shape of
// `provider.auth` in both the hand-edited root schema and the
// generator-driven per-target schemas. The motivation for nesting
// auth into a oneOf was so editors could surface the
// "type=exec means command is required, type=file means path is
// required, both branches forbid the other's fields" rule. If a
// future change collapses auth back to a permissive `object`
// schema (e.g. dropping the JSONSchema() override on AuthConfig),
// editors silently lose that feedback. This guard fails CI before
// that drift ships.
func TestProviderAuthOneOfShape(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, path := range []string{
		filepath.Join(root, "ccgate.schema.json"),
		filepath.Join(root, "schemas", "claude.schema.json"),
		filepath.Join(root, "schemas", "codex.schema.json"),
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			assertAuthOneOf(t, path)
		})
	}
}

// assertAuthOneOf checks that `provider.auth.oneOf` has exactly two
// branches whose `type.const` values are "exec" and "file" (in any
// order), each marked `additionalProperties: false`, with the
// expected `required` field set. We do NOT pin every property
// description here — those are allowed to differ between the
// richer hand-edited root and the sparser generator output.
func assertAuthOneOf(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Properties struct {
			Provider struct {
				Properties struct {
					Auth json.RawMessage `json:"auth"`
				} `json:"properties"`
			} `json:"provider"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(doc.Properties.Provider.Properties.Auth) == 0 {
		t.Fatalf("%s: provider.auth missing", path)
	}
	var auth struct {
		OneOf []json.RawMessage `json:"oneOf"`
	}
	if err := json.Unmarshal(doc.Properties.Provider.Properties.Auth, &auth); err != nil {
		t.Fatalf("%s: parse provider.auth: %v", path, err)
	}
	if len(auth.OneOf) != 2 {
		t.Fatalf("%s: provider.auth.oneOf must have 2 branches, got %d", path, len(auth.OneOf))
	}

	seen := map[string]bool{}
	for i, raw := range auth.OneOf {
		var branch struct {
			Type                 string                     `json:"type"`
			AdditionalProperties any                        `json:"additionalProperties"`
			Required             []string                   `json:"required"`
			Properties           map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(raw, &branch); err != nil {
			t.Fatalf("%s: parse branch %d: %v", path, i, err)
		}
		if branch.Type != "object" {
			t.Fatalf("%s branch %d: type = %q, want object", path, i, branch.Type)
		}
		if branch.AdditionalProperties != false {
			t.Fatalf("%s branch %d: additionalProperties must be false to enforce mutually exclusive fields, got %v", path, i, branch.AdditionalProperties)
		}
		var typeProp struct {
			Const string `json:"const"`
		}
		if raw, ok := branch.Properties["type"]; ok {
			_ = json.Unmarshal(raw, &typeProp)
		}
		if typeProp.Const != "exec" && typeProp.Const != "file" {
			t.Fatalf("%s branch %d: type.const must be \"exec\" or \"file\", got %q", path, i, typeProp.Const)
		}
		if seen[typeProp.Const] {
			t.Fatalf("%s: duplicate branch for type=%q", path, typeProp.Const)
		}
		seen[typeProp.Const] = true

		// Every branch must mark `type` required. exec additionally
		// requires `command`; file leaves `path` optional (the runner
		// falls back to a per-target default under StateDir).
		gotRequired := map[string]bool{}
		for _, r := range branch.Required {
			gotRequired[r] = true
		}
		if !gotRequired["type"] {
			t.Fatalf("%s branch type=%q: required must include \"type\", got %v",
				path, typeProp.Const, branch.Required)
		}
		if typeProp.Const == "exec" && !gotRequired["command"] {
			t.Fatalf("%s exec branch: required must include \"command\", got %v",
				path, branch.Required)
		}
	}
	if !seen["exec"] || !seen["file"] {
		t.Fatalf("%s: provider.auth.oneOf must cover both exec and file, got %v", path, seen)
	}
}

// readProviderKeys returns the sorted top-level field names under
// `properties.provider.properties`. We intentionally compare keys
// only — descriptions / formats are allowed to differ between the
// hand-edited root (richer prose) and the generator output (sparse).
func readProviderKeys(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Properties struct {
			Provider struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"provider"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	keys := make([]string, 0, len(doc.Properties.Provider.Properties))
	for k := range doc.Properties.Provider.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// readTopLevelKeys returns the sorted top-level keys under
// `properties` (i.e. the recognised root config keys).
func readTopLevelKeys(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	keys := make([]string, 0, len(doc.Properties))
	for k := range doc.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func filterOut(keys []string, drop string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == drop {
			continue
		}
		out = append(out, k)
	}
	return out
}

func equalKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// repoRoot walks up from this test file until it finds a directory
// containing go.mod. Avoids hardcoding "../.." which would silently
// break if the package layout moved.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate repo root (go.mod)")
		}
		dir = parent
	}
}
