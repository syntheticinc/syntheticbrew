package lsp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedBinDir_NotEmpty(t *testing.T) {
	dir := ManagedBinDir()
	assert.NotEmpty(t, dir)
	assert.Contains(t, dir, "syntheticbrew")
	assert.True(t, filepath.IsAbs(dir))
}

func TestManagedBinDir_PlatformSpecific(t *testing.T) {
	dir := ManagedBinDir()
	switch runtime.GOOS {
	case "windows":
		assert.Contains(t, dir, "syntheticbrew\\bin")
	case "darwin":
		assert.Contains(t, dir, "syntheticbrew/bin")
		assert.Contains(t, dir, "Application Support")
	default:
		assert.Contains(t, dir, "syntheticbrew/bin")
	}
}

func TestEnsureBinDir_CreatesDirectory(t *testing.T) {
	// Use a temp dir to avoid polluting the real bin dir
	origFunc := os.Getenv("APPDATA")
	tmp := t.TempDir()

	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_DATA_HOME", tmp)
	}

	dir, err := EnsureBinDir()
	require.NoError(t, err)
	assert.DirExists(t, dir)

	// Restore
	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", origFunc)
	}
}

func TestLookInDir_FindsBinary(t *testing.T) {
	tmp := t.TempDir()

	binName := "test-tool"
	if runtime.GOOS == "windows" {
		binName = "test-tool.exe"
	}

	binPath := filepath.Join(tmp, binName)
	require.NoError(t, os.WriteFile(binPath, []byte("binary"), 0755))

	result := lookInDir(tmp, "test-tool")
	assert.Equal(t, binPath, result)
}

func TestLookInDir_NotFound(t *testing.T) {
	tmp := t.TempDir()
	result := lookInDir(tmp, "nonexistent-binary")
	assert.Empty(t, result)
}

func TestLookInDir_IgnoresDirectories(t *testing.T) {
	tmp := t.TempDir()
	dirPath := filepath.Join(tmp, "test-tool")
	require.NoError(t, os.Mkdir(dirPath, 0755))

	result := lookInDir(tmp, "test-tool")
	// On Windows lookInDir tries .exe first, so "test-tool" dir won't match
	if runtime.GOOS != "windows" {
		assert.Empty(t, result)
	}
}

func TestWhichBin_ChecksManagedDir(t *testing.T) {
	tmp := t.TempDir()

	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_DATA_HOME", tmp)
	}

	// Create managed bin dir and put a binary there
	binDir, err := EnsureBinDir()
	require.NoError(t, err)

	binName := "my-custom-lsp"
	if runtime.GOOS == "windows" {
		binName = "my-custom-lsp.exe"
	}

	binPath := filepath.Join(binDir, binName)
	require.NoError(t, os.WriteFile(binPath, []byte("binary"), 0755))

	result := whichBin("my-custom-lsp")
	assert.Equal(t, binPath, result)
}

func TestWhichBin_ChecksNodeModulesBin(t *testing.T) {
	tmp := t.TempDir()

	if runtime.GOOS == "windows" {
		t.Setenv("APPDATA", tmp)
	} else {
		t.Setenv("XDG_DATA_HOME", tmp)
	}

	binDir, err := EnsureBinDir()
	require.NoError(t, err)

	nmBin := filepath.Join(binDir, "node_modules", ".bin")
	require.NoError(t, os.MkdirAll(nmBin, 0755))

	binName := "ts-server"
	if runtime.GOOS == "windows" {
		binName = "ts-server.cmd"
	}

	binPath := filepath.Join(nmBin, binName)
	require.NoError(t, os.WriteFile(binPath, []byte("script"), 0755))

	result := whichBin("ts-server")
	assert.Equal(t, binPath, result)
}

func TestInstallSpec_InConfigs(t *testing.T) {
	configs := AllConfigs()

	installable := map[string]string{
		"go":         "go",
		"typescript": "npm",
		"python":     "npm",
		"rust":       "github-release",
		"cpp":        "github-release",
	}

	for _, cfg := range configs {
		t.Run(cfg.ID, func(t *testing.T) {
			expectedType, shouldInstall := installable[cfg.ID]
			if shouldInstall {
				require.NotNil(t, cfg.Install, "expected InstallSpec for %s", cfg.ID)
				assert.Equal(t, expectedType, cfg.Install.Type)
				assert.NotEmpty(t, cfg.Install.Package)
			}
		})
	}
}

func TestInstallSpec_NoInstallForSomeServers(t *testing.T) {
	noInstall := []string{"java", "dart", "ruby", "php", "csharp"}
	configs := AllConfigs()

	configMap := make(map[string]ServerConfig)
	for _, c := range configs {
		configMap[c.ID] = c
	}

	for _, id := range noInstall {
		t.Run(id, func(t *testing.T) {
			cfg, ok := configMap[id]
			require.True(t, ok, "config %s should exist", id)
			assert.Nil(t, cfg.Install, "expected no InstallSpec for %s", id)
		})
	}
}

func TestIsInstallDisabled(t *testing.T) {
	// Restore at end so other tests in the package see the default.
	t.Cleanup(func() { SetInstallDisabled(false) })

	t.Run("default false", func(t *testing.T) {
		SetInstallDisabled(false)
		assert.False(t, isInstallDisabled())
	})

	t.Run("set true", func(t *testing.T) {
		SetInstallDisabled(true)
		assert.True(t, isInstallDisabled())
	})

	t.Run("toggled back false", func(t *testing.T) {
		SetInstallDisabled(true)
		SetInstallDisabled(false)
		assert.False(t, isInstallDisabled())
	})
}

func TestFindMatchingAsset(t *testing.T) {
	assets := []ghAsset{
		{Name: "rust-analyzer-x86_64-unknown-linux-gnu.gz", BrowserDownloadURL: "https://example.com/linux-amd64"},
		{Name: "rust-analyzer-x86_64-pc-windows-msvc.gz", BrowserDownloadURL: "https://example.com/windows-amd64"},
		{Name: "rust-analyzer-aarch64-apple-darwin.gz", BrowserDownloadURL: "https://example.com/darwin-arm64"},
		{Name: "rust-analyzer-x86_64-apple-darwin.gz", BrowserDownloadURL: "https://example.com/darwin-amd64"},
		{Name: "rust-analyzer-x86_64-unknown-linux-gnu.gz.sha256", BrowserDownloadURL: "https://example.com/sha256"},
	}

	result := findMatchingAsset(assets)
	require.NotNil(t, result)

	// Should find an asset for the current platform, not checksum
	assert.NotContains(t, result.Name, "sha256")
}

func TestFindMatchingAsset_NoMatch(t *testing.T) {
	assets := []ghAsset{
		{Name: "tool-mips-linux.tar.gz", BrowserDownloadURL: "https://example.com/mips"},
	}

	// Only matches if we're on mips/linux, which is unlikely in CI
	if runtime.GOOS != "linux" || runtime.GOARCH != "mips" {
		result := findMatchingAsset(assets)
		assert.Nil(t, result)
	}
}

func TestFindMatchingAsset_SkipsChecksums(t *testing.T) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Build asset names that match current platform
	var realAsset, checksumAsset string
	switch {
	case goos == "linux" && goarch == "amd64":
		realAsset = "tool-linux-amd64.tar.gz"
		checksumAsset = "tool-linux-amd64.tar.gz.sha256"
	case goos == "darwin" && goarch == "arm64":
		realAsset = "tool-darwin-arm64.tar.gz"
		checksumAsset = "tool-darwin-arm64.tar.gz.sha256"
	case goos == "windows" && goarch == "amd64":
		realAsset = "tool-windows-amd64.zip"
		checksumAsset = "tool-windows-amd64.zip.sha256"
	default:
		t.Skipf("no test asset for %s/%s", goos, goarch)
	}

	assets := []ghAsset{
		{Name: checksumAsset, BrowserDownloadURL: "https://example.com/checksum"},
		{Name: realAsset, BrowserDownloadURL: "https://example.com/real"},
	}

	result := findMatchingAsset(assets)
	require.NotNil(t, result)
	assert.Equal(t, realAsset, result.Name)
}

func TestDetectPackageManager(t *testing.T) {
	name, path := detectPackageManager()
	// At least one of npm or bun should be available in most dev environments.
	// If neither is available, that's ok — just skip.
	if path == "" {
		t.Skip("neither npm nor bun found in PATH")
	}
	assert.NotEmpty(t, name)
	assert.NotEmpty(t, path)
}
