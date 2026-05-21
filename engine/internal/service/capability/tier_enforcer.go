package capability

import (
	"fmt"

	"github.com/syntheticinc/syntheticbrew/internal/domain"
)

// DeploymentMode represents the deployment mode of the engine.
type DeploymentMode string

const (
	DeploymentModeCE    DeploymentMode = "ce"    // Community Edition (self-hosted)
	DeploymentModeCloud DeploymentMode = "cloud" // Cloud (managed)
)

// TierEnforcer validates whether a tool is allowed in the current deployment mode.
type TierEnforcer struct {
	mode DeploymentMode
}

// NewTierEnforcer creates a new TierEnforcer.
func NewTierEnforcer(mode DeploymentMode) *TierEnforcer {
	return &TierEnforcer{mode: mode}
}

// IsAllowed returns nil if the tool is allowed, or an error describing why it's blocked.
func (e *TierEnforcer) IsAllowed(toolName string) error {
	tier := domain.ClassifyToolTier(toolName)

	if tier == domain.ToolTierSelfHosted && e.mode == DeploymentModeCloud {
		return fmt.Errorf("tool %q is a Tier 3 (self-hosted only) tool and is blocked in Cloud deployment", toolName)
	}

	return nil
}

// FilterAllowed returns only the tools that are allowed in the current deployment mode.
// Blocked tools are logged and excluded.
func (e *TierEnforcer) FilterAllowed(toolNames []string) (allowed []string, blocked []string) {
	for _, name := range toolNames {
		if err := e.IsAllowed(name); err != nil {
			blocked = append(blocked, name)
			continue
		}
		allowed = append(allowed, name)
	}
	return allowed, blocked
}

// ClassifyTool returns the tier of a tool (delegates to domain).
func ClassifyTool(toolName string) domain.ToolTier {
	return domain.ClassifyToolTier(toolName)
}
