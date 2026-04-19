package metrics

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCapToolInput(t *testing.T) {
	t.Parallel()

	longASCII := strings.Repeat("a", maxToolInputFieldLen+1)
	longASCIIExpected := strings.Repeat("a", maxToolInputFieldLen)

	longMixed := strings.Repeat("あ", maxToolInputFieldLen+1) // each rune is 3 bytes
	longMixedExpectedRunes := maxToolInputFieldLen

	tests := map[string]struct {
		in   ToolInputFields
		want ToolInputFields
	}{
		"all empty": {
			in:   ToolInputFields{},
			want: ToolInputFields{},
		},
		"whitespace is preserved verbatim (no normalization)": {
			in: ToolInputFields{
				Command:  "echo \"a   b\"\nline2\t",
				FilePath: "  /path with  space  ",
				Path:     "p\n",
				Pattern:  "\tfoo\t",
			},
			want: ToolInputFields{
				Command:  "echo \"a   b\"\nline2\t",
				FilePath: "  /path with  space  ",
				Path:     "p\n",
				Pattern:  "\tfoo\t",
			},
		},
		"short ascii under cap is unchanged": {
			in:   ToolInputFields{Command: "gh pr list"},
			want: ToolInputFields{Command: "gh pr list"},
		},
		"ascii over cap is rune-truncated": {
			in:   ToolInputFields{Command: longASCII},
			want: ToolInputFields{Command: longASCIIExpected},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := CapToolInput(tc.in)
			if got != tc.want {
				t.Errorf("CapToolInput(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
		})
	}

	t.Run("unicode multi-byte over cap truncates by rune count", func(t *testing.T) {
		t.Parallel()
		got := CapToolInput(ToolInputFields{Command: longMixed})
		if n := utf8.RuneCountInString(got.Command); n != longMixedExpectedRunes {
			t.Errorf("rune count = %d, want %d", n, longMixedExpectedRunes)
		}
	})

	t.Run("exactly cap length is unchanged", func(t *testing.T) {
		t.Parallel()
		exact := strings.Repeat("x", maxToolInputFieldLen)
		got := CapToolInput(ToolInputFields{Command: exact})
		if got.Command != exact {
			t.Error("exactly cap-length input should not be truncated")
		}
	})
}
