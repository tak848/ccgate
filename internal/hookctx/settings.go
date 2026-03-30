package hookctx

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tak848/ccgate/internal/gitutil"
)

type SettingsPermissions struct {
	Allow []string `json:"allow,omitempty"`
	Deny  []string `json:"deny,omitempty"`
}

// LoadSettingsPermissions reads permissions from settings.json files.
// File-not-found errors are expected and silently ignored.
// JSON parse errors are logged as warnings but do not fail the operation.
func LoadSettingsPermissions(cwd string) SettingsPermissions {
	home, err := os.UserHomeDir()
	if err != nil {
		return SettingsPermissions{}
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

	var merged SettingsPermissions
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("failed to read settings", "path", path, "error", err)
			}
			continue
		}
		var s struct {
			Permissions SettingsPermissions `json:"permissions"`
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
