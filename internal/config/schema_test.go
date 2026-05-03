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
