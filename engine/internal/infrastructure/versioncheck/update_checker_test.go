package versioncheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSemver(t *testing.T) {
	tests := []struct {
		name                       string
		version                    string
		wantMajor, wantMinor, wantPatch int
	}{
		{"simple", "1.2.3", 1, 2, 3},
		{"with v prefix", "v1.2.3", 1, 2, 3},
		{"with pre-release", "1.2.3-rc1", 1, 2, 3},
		{"with build metadata", "1.2.3+build.42", 1, 2, 3},
		{"zeros", "0.0.0", 0, 0, 0},
		{"large numbers", "10.20.30", 10, 20, 30},
		{"dev version", "dev-ce", 0, 0, 0},
		{"empty", "", 0, 0, 0},
		{"two parts", "1.2", 0, 0, 0},
		{"one part", "1", 0, 0, 0},
		{"non-numeric", "a.b.c", 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor, patch := parseSemver(tt.version)
			assert.Equal(t, tt.wantMajor, major, "major")
			assert.Equal(t, tt.wantMinor, minor, "minor")
			assert.Equal(t, tt.wantPatch, patch, "patch")
		})
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"newer patch", "1.0.1", "1.0.0", true},
		{"newer minor", "1.1.0", "1.0.0", true},
		{"newer major", "2.0.0", "1.9.9", true},
		{"same version", "1.0.0", "1.0.0", false},
		{"older version", "1.0.0", "1.0.1", false},
		{"dev vs release", "1.0.0", "dev-ce", true},
		{"dev vs dev", "dev-ce", "dev-ce", false},
		{"with v prefix", "v1.0.1", "v1.0.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNewerVersion(tt.a, tt.b))
		})
	}
}

func TestUpdateChecker_NoUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": map[string]string{
				"latest":       "1.0.0",
				"download_url": "https://syntheticbrew.ai/releases/v1.0.0/",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	uc := NewUpdateChecker("1.0.0", "")
	uc.checkFromURL(server.URL)

	assert.Equal(t, "1.0.0", uc.LatestVersion())
	assert.Empty(t, uc.UpdateAvailable())
}

func TestUpdateChecker_UpdateAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": map[string]string{
				"latest":       "1.0.1",
				"download_url": "https://syntheticbrew.ai/releases/v1.0.1/",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	uc := NewUpdateChecker("1.0.0", "")
	uc.checkFromURL(server.URL)

	assert.Equal(t, "1.0.1", uc.LatestVersion())
	assert.Equal(t, "1.0.1", uc.UpdateAvailable())
}

func TestUpdateChecker_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	uc := NewUpdateChecker("1.0.0", "")
	uc.checkFromURL(server.URL)

	assert.Empty(t, uc.LatestVersion())
	assert.Empty(t, uc.UpdateAvailable())
}

func TestUpdateChecker_NetworkError(t *testing.T) {
	uc := NewUpdateChecker("1.0.0", "")
	uc.checkFromURL("http://127.0.0.1:1") // connection refused

	assert.Empty(t, uc.LatestVersion())
	assert.Empty(t, uc.UpdateAvailable())
}

func TestUpdateChecker_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid`))
	}))
	defer server.Close()

	uc := NewUpdateChecker("1.0.0", "")
	uc.checkFromURL(server.URL)

	assert.Empty(t, uc.LatestVersion())
}

func TestUpdateChecker_EmptyLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": map[string]string{
				"latest": "",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	uc := NewUpdateChecker("1.0.0", "")
	uc.checkFromURL(server.URL)

	assert.Empty(t, uc.LatestVersion())
	assert.Empty(t, uc.UpdateAvailable())
}

func TestUpdateChecker_StartNonBlocking(t *testing.T) {
	uc := NewUpdateChecker("1.0.0", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start should return immediately (goroutine)
	done := make(chan struct{})
	go func() {
		uc.Start(ctx)
		close(done)
	}()

	select {
	case <-done:
		// OK — Start returned immediately
	case <-time.After(1 * time.Second):
		require.Fail(t, "Start() blocked for more than 1 second")
	}
}

func TestUpdateChecker_DevVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]interface{}{
			"data": map[string]string{
				"latest":       "1.0.0",
				"download_url": "https://syntheticbrew.ai/releases/v1.0.0/",
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	uc := NewUpdateChecker("dev-ce", "")
	uc.checkFromURL(server.URL)

	assert.Equal(t, "1.0.0", uc.LatestVersion())
	assert.Equal(t, "1.0.0", uc.UpdateAvailable())
}
