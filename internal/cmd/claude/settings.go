package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tak848/ccgate/internal/gitutil"
)

type settingsPermissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

func (s settingsPermissions) empty() bool { return len(s.Allow) == 0 && len(s.Deny) == 0 }

// loadSettingsPermissions reads permissions from settings.json files.
// File-not-found errors are expected and silently ignored.
// JSON parse errors are logged as warnings but do not fail the operation.
func loadSettingsPermissions(cwd string) settingsPermissions {
	home, err := os.UserHomeDir()
	if err != nil {
		return settingsPermissions{}
	}

	repoRoot := cwd
	if root, err := gitutil.RepoRoot(cwd); err == nil {
		repoRoot = root
	}

	paths := []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(repoRoot, ".claude", "settings.json"),
		filepath.Join(repoRoot, ".claude", "settings.local.json"),
	}

	var merged settingsPermissions
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("failed to read settings", "path", path, "error", err)
			}
			continue
		}
		var s struct {
			Permissions settingsPermissions `json:"permissions"`
		}
		if err := json.Unmarshal(data, &s); err != nil {
			slog.Warn("failed to parse settings", "path", path, "error", fmt.Errorf("unmarshal: %w", err))
			continue
		}
		merged.Allow = append(merged.Allow, s.Permissions.Allow...)
		merged.Deny = append(merged.Deny, s.Permissions.Deny...)
	}
	return merged
}
