package metrics

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// Record appends a single Entry as a JSON line to the metrics file.
// Errors are logged but never returned — metrics must not block the hook.
func Record(path string, maxSize int64, entry Entry) {
	if path == "" {
		return
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("metrics: failed to create directory", "path", dir, "error", err)
		return
	}

	RotateIfNeeded(path, maxSize)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		slog.Warn("metrics: failed to open file", "path", path, "error", err)
		return
	}
	defer f.Close()

	line, err := json.Marshal(entry)
	if err != nil {
		slog.Warn("metrics: failed to marshal entry", "error", err)
		return
	}
	line = append(line, '\n')

	if _, err := f.Write(line); err != nil {
		slog.Warn("metrics: failed to write entry", "error", err)
	}
}

// RotateIfNeeded rotates the file at path if it exceeds maxSize.
// maxSize <= 0 disables rotation.
func RotateIfNeeded(path string, maxSize int64) {
	if maxSize <= 0 {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxSize {
		return
	}
	// Rename atomically replaces the target on Unix, no Remove needed.
	// This avoids a race where concurrent processes could delete each other's rotated file.
	prev := path + ".1"
	if err := os.Rename(path, prev); err != nil && !os.IsNotExist(err) {
		slog.Warn("metrics: failed to rotate file", "path", path, "error", err)
	}
}
