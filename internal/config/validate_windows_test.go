//go:build windows

package config

import "testing"

// TestValidateAuthPathWindowsAbsolute pins the Windows-shaped
// paths that validate accepts. Unix coverage is in config_test.go.
// Relative paths are accepted on every OS; they resolve from the
// hook's working directory (the project root for Claude Code /
// Codex CLI), matching how those tools resolve hook command paths.
func TestValidateAuthPathWindowsAbsolute(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		path    string
		wantErr bool
	}{
		"drive path":               {path: `C:\Users\alice\key.json`},
		"drive path forward slash": {path: `C:/Users/alice/key.json`},
		"unc path":                 {path: `\\server\share\key.json`},
		"home tilde":               {path: `~/key.json`},
		"relative":                 {path: `key.json`},
		"relative with slash":      {path: `subdir\key.json`},
		"relative dot prefix":      {path: `.\key.json`},
		// Empty is gated at validateAuthFile (it falls back to the
		// per-target default), not at validateAuthPath, so the
		// helper itself treats "" as a pass-through.
		"empty":            {path: ``},
		"bare tilde":       {path: `~`, wantErr: true},
		"bare tilde slash": {path: `~/`, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := validateAuthPath(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("path %q: expected validation error, got nil", tc.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("path %q: unexpected error: %v", tc.path, err)
			}
		})
	}
}
