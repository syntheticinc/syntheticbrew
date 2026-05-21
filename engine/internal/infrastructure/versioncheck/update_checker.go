package versioncheck

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// defaultVersionsURL is the canonical hosted endpoint that returns the
// current latest engine version. Used when no override is supplied.
const defaultVersionsURL = "https://api.syntheticbrew.ai/api/v1/versions/engine"

// UpdateChecker periodically checks api.syntheticbrew.ai for newer Engine versions.
// It checks immediately on start, then every 24 hours. Errors are silently
// ignored (air-gap safe — no internet = no problem).
type UpdateChecker struct {
	currentVersion string
	versionsURL    string
	latestVersion  string
	mu             sync.RWMutex
}

// NewUpdateChecker creates an UpdateChecker for the given current version.
// versionsURL overrides the default hosted endpoint when non-empty
// (sourced from BootstrapConfig.Updates.VersionsURL / env SYNTHETICBREW_VERSIONS_URL).
func NewUpdateChecker(currentVersion, versionsURL string) *UpdateChecker {
	url := strings.TrimSpace(versionsURL)
	if url == "" {
		url = defaultVersionsURL
	}
	return &UpdateChecker{
		currentVersion: currentVersion,
		versionsURL:    url,
	}
}

// Start launches a background goroutine that checks for updates immediately
// and then every 24 hours. The goroutine stops when ctx is cancelled.
func (uc *UpdateChecker) Start(ctx context.Context) {
	go func() {
		uc.checkFromURL(uc.versionsURL)

		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				uc.checkFromURL(uc.versionsURL)
			}
		}
	}()
}

// LatestVersion returns the latest known version, or empty if not checked yet.
func (uc *UpdateChecker) LatestVersion() string {
	uc.mu.RLock()
	defer uc.mu.RUnlock()
	return uc.latestVersion
}

// UpdateAvailable returns the latest version string if an update is available,
// or empty if the current version is up-to-date (or if the check hasn't completed).
func (uc *UpdateChecker) UpdateAvailable() string {
	latest := uc.LatestVersion()
	if latest == "" {
		return ""
	}
	if !isNewerVersion(latest, uc.currentVersion) {
		return ""
	}
	return latest
}

func (uc *UpdateChecker) checkFromURL(url string) {
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		slog.DebugContext(context.Background(), "update check: request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.DebugContext(context.Background(), "update check: unexpected status", "status", resp.StatusCode)
		return
	}

	var body struct {
		Data struct {
			Latest      string `json:"latest"`
			DownloadURL string `json:"download_url"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		slog.DebugContext(context.Background(), "update check: decode failed", "error", err)
		return
	}

	latest := body.Data.Latest
	if latest == "" {
		return
	}

	uc.mu.Lock()
	uc.latestVersion = latest
	uc.mu.Unlock()

	if isNewerVersion(latest, uc.currentVersion) {
		slog.Warn(
			fmt.Sprintf("A newer version of SyntheticBrew Engine is available: %s. Download: %s", latest, body.Data.DownloadURL),
		)
	}
}

// isNewerVersion returns true if a > b using semantic versioning (major.minor.patch).
// Non-parsable versions (e.g. "dev-ce") are treated as 0.0.0.
func isNewerVersion(a, b string) bool {
	aMajor, aMinor, aPatch := parseSemver(a)
	bMajor, bMinor, bPatch := parseSemver(b)

	if aMajor != bMajor {
		return aMajor > bMajor
	}
	if aMinor != bMinor {
		return aMinor > bMinor
	}
	return aPatch > bPatch
}

// parseSemver extracts major.minor.patch from a version string like "1.2.3" or "v1.2.3".
// Returns (0, 0, 0) on parse failure.
func parseSemver(v string) (int, int, int) {
	v = strings.TrimPrefix(v, "v")

	// Strip pre-release suffix (e.g. "1.0.0-rc1")
	if idx := strings.IndexAny(v, "-+"); idx >= 0 {
		v = v[:idx]
	}

	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0
	}

	return major, minor, patch
}
