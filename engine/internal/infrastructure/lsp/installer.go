package lsp

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// installerEnv is the package-private toggle for auto-install. Set via
// SetInstallDisabled at boot from BootstrapConfig.LSP.DisableDownload (env
// SYNTHETICBREW_DISABLE_LSP_DOWNLOAD). Package-private + setter rather than
// constructor parameter because the Installer is currently created in
// NewService() with no dependency on bootstrap; threading bootstrap through
// every Service factory would touch unrelated code paths.
var installerDisabled struct {
	sync.RWMutex
	disabled bool
}

// SetInstallDisabled sets the global flag that controls whether
// Install() short-circuits with an error. Callers (the bootstrap path)
// invoke this once at startup; everything else reads the flag.
func SetInstallDisabled(disabled bool) {
	installerDisabled.Lock()
	defer installerDisabled.Unlock()
	installerDisabled.disabled = disabled
}

// InstallSpec describes how to auto-install an LSP server binary.
type InstallSpec struct {
	Type    string // "npm", "go", "github-release"
	Package string // npm package name, go module path, or "owner/repo"
	Binary  string // binary name if differs from package (optional)
}

// Installer orchestrates auto-installation of LSP server binaries.
type Installer struct {
	inflight sync.Map // serverID -> *sync.Once
}

// NewInstaller creates a new Installer.
func NewInstaller() *Installer {
	return &Installer{}
}

// Install installs the LSP server binary described by spec.
// Multiple concurrent calls for the same serverID are coalesced into a single install.
func (ins *Installer) Install(ctx context.Context, serverID string, spec InstallSpec) error {
	if isInstallDisabled() {
		return fmt.Errorf("auto-install disabled via SYNTHETICBREW_DISABLE_LSP_DOWNLOAD")
	}

	type result struct {
		err error
	}

	// Use a lazy-init pattern: load or store a channel that carries the result.
	type inflightEntry struct {
		once   sync.Once
		result error
	}

	actual, _ := ins.inflight.LoadOrStore(serverID, &inflightEntry{})
	entry := actual.(*inflightEntry)

	entry.once.Do(func() {
		entry.result = ins.doInstall(ctx, serverID, spec)
	})

	// Clean up so future calls can retry on failure.
	if entry.result != nil {
		ins.inflight.Delete(serverID)
	}

	return entry.result
}

func (ins *Installer) doInstall(ctx context.Context, serverID string, spec InstallSpec) error {
	binDir, err := EnsureBinDir()
	if err != nil {
		return fmt.Errorf("ensure bin dir: %w", err)
	}

	slog.InfoContext(ctx, "auto-installing LSP server",
		"server", serverID, "strategy", spec.Type, "package", spec.Package)

	start := time.Now()

	switch spec.Type {
	case "npm":
		err = installNpm(ctx, binDir, spec)
	case "go":
		err = installGo(ctx, binDir, spec)
	case "github-release":
		err = installGitHubRelease(ctx, binDir, spec)
	default:
		err = fmt.Errorf("unknown install strategy: %s", spec.Type)
	}

	if err != nil {
		slog.ErrorContext(ctx, "LSP auto-install failed",
			"server", serverID, "strategy", spec.Type, "error", err,
			"duration", time.Since(start).Round(time.Millisecond))
		return fmt.Errorf("install %s: %w", serverID, err)
	}

	slog.InfoContext(ctx, "LSP auto-install completed",
		"server", serverID, "strategy", spec.Type,
		"duration", time.Since(start).Round(time.Millisecond))
	return nil
}

// isInstallDisabled reports whether the auto-install short-circuit is active.
// The flag is set once at boot via SetInstallDisabled from the bootstrap
// config (BootstrapConfig.LSP.DisableDownload, env SYNTHETICBREW_DISABLE_LSP_DOWNLOAD).
func isInstallDisabled() bool {
	installerDisabled.RLock()
	defer installerDisabled.RUnlock()
	return installerDisabled.disabled
}

// --- NPM strategy ---

func installNpm(ctx context.Context, binDir string, spec InstallSpec) error {
	packageManager, pmPath := detectPackageManager()
	if pmPath == "" {
		return fmt.Errorf("neither npm nor bun found in PATH")
	}

	installCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch packageManager {
	case "bun":
		cmd = exec.CommandContext(installCtx, pmPath, "add", spec.Package)
	default:
		cmd = exec.CommandContext(installCtx, pmPath, "install", spec.Package)
	}

	cmd.Dir = binDir

	// Ensure package.json exists (npm requires it)
	pkgJSON := filepath.Join(binDir, "package.json")
	if _, err := os.Stat(pkgJSON); os.IsNotExist(err) {
		if err := os.WriteFile(pkgJSON, []byte(`{"private":true}`), 0644); err != nil {
			return fmt.Errorf("create package.json: %w", err)
		}
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s install failed: %w\noutput: %s", packageManager, err, string(output))
	}

	return nil
}

func detectPackageManager() (name, path string) {
	if p := whichBin("npm"); p != "" {
		return "npm", p
	}
	if p := whichBin("bun"); p != "" {
		return "bun", p
	}
	return "", ""
}

// --- Go strategy ---

func installGo(ctx context.Context, binDir string, spec InstallSpec) error {
	goPath := whichBin("go")
	if goPath == "" {
		return fmt.Errorf("go not found in PATH")
	}

	installCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	pkg := spec.Package
	if !strings.Contains(pkg, "@") {
		pkg += "@latest"
	}

	cmd := exec.CommandContext(installCtx, goPath, "install", pkg)
	cmd.Env = append(os.Environ(), "GOBIN="+binDir)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go install failed: %w\noutput: %s", err, string(output))
	}

	return nil
}

// --- GitHub Release strategy ---

type ghRelease struct {
	Assets []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func installGitHubRelease(ctx context.Context, binDir string, spec InstallSpec) error {
	installCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	// Fetch latest release
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", spec.Package)

	req, err := http.NewRequestWithContext(installCtx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GitHub API returned %d: %s", resp.StatusCode, string(body))
	}

	var release ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("decode release: %w", err)
	}

	// Find matching asset
	asset := findMatchingAsset(release.Assets)
	if asset == nil {
		return fmt.Errorf("no matching asset for %s/%s in release of %s",
			runtime.GOOS, runtime.GOARCH, spec.Package)
	}

	slog.InfoContext(ctx, "downloading LSP binary",
		"asset", asset.Name, "url", asset.BrowserDownloadURL)

	// Download
	dlReq, err := http.NewRequestWithContext(installCtx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}

	dlResp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer dlResp.Body.Close()

	if dlResp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", dlResp.StatusCode)
	}

	// Save to temp file
	tmpFile, err := os.CreateTemp(binDir, "lsp-download-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmpFile, dlResp.Body); err != nil {
		tmpFile.Close()
		return fmt.Errorf("save download: %w", err)
	}
	tmpFile.Close()

	// Determine binary name
	binaryName := spec.Binary
	if binaryName == "" {
		// Extract from package: "owner/repo" -> "repo"
		parts := strings.Split(spec.Package, "/")
		binaryName = parts[len(parts)-1]
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(binaryName, ".exe") {
		binaryName += ".exe"
	}

	destPath := filepath.Join(binDir, binaryName)

	// Extract or copy
	assetName := strings.ToLower(asset.Name)
	switch {
	case strings.HasSuffix(assetName, ".tar.gz") || strings.HasSuffix(assetName, ".tgz"):
		if err := extractTarGz(tmpPath, binDir, binaryName); err != nil {
			return fmt.Errorf("extract tar.gz: %w", err)
		}
	case strings.HasSuffix(assetName, ".zip"):
		if err := extractZip(tmpPath, binDir, binaryName); err != nil {
			return fmt.Errorf("extract zip: %w", err)
		}
	case strings.HasSuffix(assetName, ".gz"):
		if err := extractGz(tmpPath, destPath); err != nil {
			return fmt.Errorf("extract gz: %w", err)
		}
	default:
		// Plain binary
		if err := os.Rename(tmpPath, destPath); err != nil {
			return fmt.Errorf("move binary: %w", err)
		}
	}

	// Make executable on Unix
	if runtime.GOOS != "windows" {
		if err := os.Chmod(destPath, 0755); err != nil {
			slog.WarnContext(ctx, "chmod failed", "path", destPath, "error", err)
		}
	}

	return nil
}

func findMatchingAsset(assets []ghAsset) *ghAsset {
	osName := runtime.GOOS
	archName := runtime.GOARCH

	// Map Go arch names to common release naming conventions
	archAliases := map[string][]string{
		"amd64": {"amd64", "x86_64", "x64"},
		"arm64": {"arm64", "aarch64"},
		"386":   {"386", "i386", "x86", "i686"},
	}

	osAliases := map[string][]string{
		"darwin":  {"darwin", "macos", "mac", "apple"},
		"linux":   {"linux"},
		"windows": {"windows", "win", "win64", "win32"},
	}

	aliases, ok := archAliases[archName]
	if !ok {
		aliases = []string{archName}
	}
	osNames, ok := osAliases[osName]
	if !ok {
		osNames = []string{osName}
	}

	for _, asset := range assets {
		lower := strings.ToLower(asset.Name)

		// Skip checksum / signature files
		if strings.HasSuffix(lower, ".sha256") || strings.HasSuffix(lower, ".asc") ||
			strings.HasSuffix(lower, ".sig") || strings.HasSuffix(lower, ".sha512") {
			continue
		}

		matchOS := false
		for _, name := range osNames {
			if strings.Contains(lower, name) {
				matchOS = true
				break
			}
		}

		matchArch := false
		for _, alias := range aliases {
			if strings.Contains(lower, alias) {
				matchArch = true
				break
			}
		}

		if matchOS && matchArch {
			return &asset
		}
	}

	return nil
}

func extractTarGz(archivePath, destDir, binaryName string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	// Strip the binary name extension for matching
	baseName := strings.TrimSuffix(binaryName, ".exe")

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		entryName := filepath.Base(header.Name)
		entryNameNoExt := strings.TrimSuffix(entryName, ".exe")

		if header.Typeflag != tar.TypeReg {
			continue
		}

		// Match the binary by name
		if entryNameNoExt != baseName {
			continue
		}

		destPath := filepath.Join(destDir, binaryName)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return err
		}

		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		out.Close()
		return nil
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractZip(archivePath, destDir, binaryName string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	baseName := strings.TrimSuffix(binaryName, ".exe")

	for _, f := range r.File {
		entryName := filepath.Base(f.Name)
		entryNameNoExt := strings.TrimSuffix(entryName, ".exe")

		if f.FileInfo().IsDir() {
			continue
		}

		if entryNameNoExt != baseName {
			continue
		}

		src, err := f.Open()
		if err != nil {
			return err
		}

		destPath := filepath.Join(destDir, binaryName)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			src.Close()
			return err
		}

		_, copyErr := io.Copy(out, src)
		src.Close()
		out.Close()

		if copyErr != nil {
			return copyErr
		}
		return nil
	}

	return fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractGz(gzPath, destPath string) error {
	f, err := os.Open(gzPath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, gz)
	return err
}
