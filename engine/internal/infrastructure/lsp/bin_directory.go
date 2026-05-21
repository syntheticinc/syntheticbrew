package lsp

import (
	"os"

	"github.com/syntheticinc/syntheticbrew/internal/infrastructure/platform"
)

// ManagedBinDir returns the path to the managed binary directory for
// auto-installed LSP servers. Delegates to platform.LSPBinDir so platform
// path resolution lives in one place (the only OS env reader outside
// pkg/config). Returns an empty string on platform error — callers treat
// this as "no managed dir" and fall back to PATH lookups.
func ManagedBinDir() string {
	dir, err := platform.LSPBinDir()
	if err != nil {
		return ""
	}
	return dir
}

// EnsureBinDir creates the managed binary directory if it doesn't exist.
func EnsureBinDir() (string, error) {
	dir := ManagedBinDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}
