package keystore

import (
	"slices"
	"strings"
	"testing"
)

// TestShellCommand pins the shell-name -> (binary, flag) mapping
// that runs auth.command. It is build-tag-free so the regression
// fires on every OS in CI; the powershell branch tolerates either
// `pwsh` or `powershell` because we fall back when pwsh is not on
// PATH (stock Windows ships powershell.exe but not pwsh.exe).
func TestShellCommand(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		shell      string
		wantBin    []string // any of these is acceptable
		wantFlag   string
		wantSubstr string
	}{
		"empty defaults to bash": {shell: "", wantBin: []string{"bash"}, wantFlag: "-c"},
		"bash":                   {shell: "bash", wantBin: []string{"bash"}, wantFlag: "-c"},
		"powershell":             {shell: "powershell", wantBin: []string{"pwsh", "powershell"}, wantFlag: "-Command"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			bin, flag := shellCommand(tc.shell)
			if !slices.Contains(tc.wantBin, bin) {
				t.Fatalf("bin = %q, want one of %v", bin, tc.wantBin)
			}
			if flag != tc.wantFlag {
				t.Fatalf("flag = %q, want %q", flag, tc.wantFlag)
			}
		})
	}
}

// TestParseHelperJSONKeyShape pins both the new validation that the
// JSON `key` field cannot contain CR/LF (which would have leaked
// past the previous trim-only check and produced a confused 401)
// and the existing "key required, non-empty" contract.
func TestParseHelperJSONKeyShape(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		input   string
		wantOK  bool
		wantSub string
	}{
		"clean":             {input: `{"key":"sk-x"}`, wantOK: true},
		"missing key":       {input: `{}`, wantOK: false, wantSub: "missing key"},
		"empty key":         {input: `{"key":""}`, wantOK: false, wantSub: "missing key"},
		"whitespace key":    {input: `{"key":"   "}`, wantOK: false, wantSub: "missing key"},
		"trailing newline":  {input: `{"key":"sk\n"}`, wantOK: false, wantSub: "single line"},
		"embedded newline":  {input: `{"key":"sk\nx"}`, wantOK: false, wantSub: "single line"},
		"embedded carriage": {input: `{"key":"sk\rx"}`, wantOK: false, wantSub: "single line"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, err := parseHelperJSON(tc.input)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}
