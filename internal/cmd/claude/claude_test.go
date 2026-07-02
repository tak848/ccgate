package claude

import (
	"strings"
	"testing"

	"github.com/tak848/ccgate/internal/config"
)

func TestClaudeTargetSectionHonorsPromptContextFlags(t *testing.T) {
	t.Parallel()

	boolPtr := func(v bool) *bool { return &v }

	cases := map[string]struct {
		settings *bool
		recent   *bool
		want     []string
		notWant  []string
	}{
		"default includes both": {
			want: []string{"settings_permissions", "recent_transcript"},
		},
		"settings disabled": {
			settings: boolPtr(false),
			want:     []string{"recent_transcript"},
			notWant:  []string{"settings_permissions"},
		},
		"recent disabled": {
			recent:  boolPtr(false),
			want:    []string{"settings_permissions"},
			notWant: []string{"recent_transcript"},
		},
		"both disabled": {
			settings: boolPtr(false),
			recent:   boolPtr(false),
			notWant:  []string{"settings_permissions", "recent_transcript"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.IncludeSettingsPermissionsInPrompt = tc.settings
			cfg.IncludeRecentTranscriptInPrompt = tc.recent

			got := claudeTargetSection(cfg)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("target section missing %q:\n%s", want, got)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(got, notWant) {
					t.Fatalf("target section unexpectedly mentions %q:\n%s", notWant, got)
				}
			}
		})
	}
}
