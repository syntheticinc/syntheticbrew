package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/syntheticinc/syntheticbrew/internal/embedded"
)

// EnsureManagedDefaults writes the embedded prompts.yaml and flows.yaml into
// dataDir if they are missing. Each file is written exactly once on first
// managed boot, never overwritten.
func EnsureManagedDefaults(dataDir string) error {
	managedPromptsPath := filepath.Join(dataDir, "prompts.yaml")
	if _, err := os.Stat(managedPromptsPath); os.IsNotExist(err) {
		if err := os.WriteFile(managedPromptsPath, embedded.DefaultPrompts, 0644); err != nil {
			return fmt.Errorf("write default prompts: %w", err)
		}
		slog.InfoContext(context.Background(), "Generated default prompts", "path", managedPromptsPath)
	}

	managedFlowsPath := filepath.Join(dataDir, "flows.yaml")
	if _, err := os.Stat(managedFlowsPath); os.IsNotExist(err) {
		if err := os.WriteFile(managedFlowsPath, embedded.DefaultFlows, 0644); err != nil {
			return fmt.Errorf("write default flows: %w", err)
		}
		slog.InfoContext(context.Background(), "Generated default flows", "path", managedFlowsPath)
	}
	return nil
}
