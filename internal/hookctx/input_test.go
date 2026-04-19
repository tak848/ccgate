package hookctx

import "testing"

func TestMetricsFields(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		input                            HookToolInput
		wantCmd, wantFP, wantPath, wantP string
	}{
		"all empty": {
			input: HookToolInput{},
		},
		"bash command": {
			input:   HookToolInput{Command: "gh pr list"},
			wantCmd: "gh pr list",
		},
		"write file_path": {
			input:  HookToolInput{FilePath: "/tmp/foo.ts"},
			wantFP: "/tmp/foo.ts",
		},
		"grep pattern and path": {
			input:    HookToolInput{Path: "internal/", Pattern: "TODO"},
			wantPath: "internal/",
			wantP:    "TODO",
		},
		"glob path only": {
			input:    HookToolInput{Path: "**/*.go"},
			wantPath: "**/*.go",
		},
		"content and content_updates are ignored": {
			input: HookToolInput{
				Command: "echo hi",
				Content: "secret contents that should not leak",
				ContentUpdates: []HookContentUpdate{
					{OldString: "a", NewString: "b"},
				},
			},
			wantCmd: "echo hi",
		},
		"all fields populated returns all four verbatim": {
			input: HookToolInput{
				Command:  "c",
				FilePath: "fp",
				Path:     "p",
				Pattern:  "pat",
			},
			wantCmd:  "c",
			wantFP:   "fp",
			wantPath: "p",
			wantP:    "pat",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := HookInput{ToolInput: tc.input}
			cmd, fp, path, pat := h.MetricsFields()
			if cmd != tc.wantCmd || fp != tc.wantFP || path != tc.wantPath || pat != tc.wantP {
				t.Errorf("MetricsFields() = (%q, %q, %q, %q), want (%q, %q, %q, %q)",
					cmd, fp, path, pat, tc.wantCmd, tc.wantFP, tc.wantPath, tc.wantP)
			}
		})
	}
}
