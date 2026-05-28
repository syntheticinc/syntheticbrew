package agentregistry

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestArch_NoSwitchOverCapabilityType is the Этап 0 architectural gate.
// It scans the production capability-dispatch paths and asserts that no
// switch statement branches on a capability type value. Capability dispatch
// MUST go through the capabilities.Registry strategy lookup so that new
// capabilities can be added without modifying existing files (OCP).
//
// If this test fails after adding a new capability, you accidentally
// reintroduced the anti-pattern. The fix: create a new struct in
// internal/domain/capabilities/<name>.go implementing the Capability
// interface, and register it in app.NewServer via capabilities.NewRegistry.
func TestArch_NoSwitchOverCapabilityType(t *testing.T) {
	// Files that participate in capability dispatch at runtime. Anywhere
	// here, a `switch c.Type` (or similar) would mean we're hard-coding
	// behaviour instead of dispatching through the Registry.
	files := []string{
		"deriver.go",
		"derive_tools.go",
	}

	// Pattern matches Go switch-statements over a CapabilityRecord-shaped
	// value's Type field. We deliberately keep this regex narrow so that
	// switches over UNRELATED types (e.g. lifecycle, status, transport)
	// do not trip the gate.
	bannedPatterns := []*regexp.Regexp{
		regexp.MustCompile(`switch\s+\w+\.Type\b`),                        // switch c.Type, switch cap.Type, ...
		regexp.MustCompile(`switch\s+\w+\(\w+\.Type\)`),                   // switch CapabilityType(c.Type)
		regexp.MustCompile(`switch\s+string\(\w+\.Type\)`),                // switch string(c.Type)
		regexp.MustCompile(`case\s+"memory"\s*[,:]`),                      // case "memory":
		regexp.MustCompile(`case\s+CapabilityTypeMemory\s*[,:]`),          // case CapabilityTypeMemory:
		regexp.MustCompile(`case\s+capabilities\.CapabilityTypeMemory\b`), // cross-package reference
	}

	for _, rel := range files {
		path := filepath.Join(".", rel)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, pat := range bannedPatterns {
			if loc := pat.FindIndex(data); loc != nil {
				t.Errorf("ARCH VIOLATION in %s: forbidden capability-type switch pattern matched %q at offset %d. "+
					"Dispatch capability types through capabilities.Registry, not via switch.",
					path, pat.String(), loc[0])
			}
		}
	}
}
