//go:build windows

package config

import "testing"

// TestValidateAuthPathWindowsAbsolute pins that Windows-style
// absolute paths are accepted by validate. POSIX `/foo` is not an
// absolute path on Windows, so we only assert the OS-native shapes
// here; Unix coverage is in config_test.go.
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
		"relative":                 {path: `key.json`, wantErr: true},
		"relative with slash":      {path: `subdir\key.json`, wantErr: true},
		"empty":                    {path: ``, wantErr: true},
		"bare tilde":               {path: `~`, wantErr: true},
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
