package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestProviderAuthOneOfShape pins the discriminator-union shape of
// `provider.auth` in the generator-driven per-target schemas. The
// motivation for nesting auth into a oneOf was so editors could
// surface the "type=exec means command is required, type=file means
// path is required, both branches forbid the other's fields" rule.
// If a future change collapses auth back to a permissive `object`
// schema (e.g. dropping the JSONSchema() override on AuthConfig),
// editors silently lose that feedback. This guard fails CI before
// that drift ships.
func TestProviderAuthOneOfShape(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	for _, path := range []string{
		filepath.Join(root, "schemas", "claude.schema.json"),
		filepath.Join(root, "schemas", "codex.schema.json"),
	} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			assertAuthOneOf(t, path)
		})
	}
}

// assertAuthOneOf checks that `provider.auth.oneOf` has exactly
// three branches whose `type.const` values cover "exec", "file",
// and "profile" (in any order), each marked
// `additionalProperties: false`, with the expected `required`
// field set.
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
	if len(auth.OneOf) != 3 {
		t.Fatalf("%s: provider.auth.oneOf must have 3 branches, got %d", path, len(auth.OneOf))
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
		switch typeProp.Const {
		case "exec", "file", "profile":
		default:
			t.Fatalf("%s branch %d: type.const must be \"exec\", \"file\", or \"profile\", got %q", path, i, typeProp.Const)
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
	if !seen["exec"] || !seen["file"] || !seen["profile"] {
		t.Fatalf("%s: provider.auth.oneOf must cover exec, file, and profile, got %v", path, seen)
	}
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
