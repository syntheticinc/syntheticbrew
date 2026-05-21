package indexing

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const MaxFileSize = 1048576 // 1MB

// extensionToLanguage maps file extensions to language identifiers.
var extensionToLanguage = map[string]string{
	".go":    "go",
	".ts":    "typescript",
	".tsx":   "typescript",
	".js":    "javascript",
	".jsx":   "javascript",
	".mjs":   "javascript",
	".py":    "python",
	".java":  "java",
	".rs":    "rust",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".cc":    "cpp",
	".cxx":   "cpp",
	".hpp":   "cpp",
	".hxx":   "cpp",
	".cs":    "csharp",
	".rb":    "ruby",
	".php":   "php",
	".swift": "swift",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".dart":  "dart",
	".lua":   "lua",
	".ex":    "elixir",
	".exs":   "elixir",
	".sh":    "bash",
	".bash":  "bash",
	".ml":    "ocaml",
	".mli":   "ocaml",
	".zig":   "zig",
	".scala": "scala",
	".sql":   "sql",
}

// alwaysIgnore contains directory/file names that are always excluded.
var alwaysIgnore = map[string]bool{
	".git":              true,
	".svn":              true,
	".hg":               true,
	"node_modules":      true,
	"vendor":            true,
	"__pycache__":       true,
	".idea":             true,
	".vscode":           true,
	".DS_Store":         true,
	"Thumbs.db":         true,
	"package-lock.json": true,
	"yarn.lock":         true,
	"pnpm-lock.yaml":   true,
	"bun.lockb":         true,
}

// defaultIgnore contains less critical entries excluded by default.
var defaultIgnore = map[string]bool{
	"dist":      true,
	"build":     true,
	"out":       true,
	"target":    true,
	"bin":       true,
	"obj":       true,
	".syntheticbrew": true,
	".next":     true,
	".nuxt":     true,
	"coverage":  true,
	".cache":    true,
	".venv":     true,
	"venv":      true,
	"env":       true,
}

// ScanResult represents a single scanned file.
type ScanResult struct {
	FilePath     string // absolute path
	RelativePath string // relative to root
	Language     string
}

// FileScanner enumerates source files in a project directory.
type FileScanner struct {
	rootPath string
}

// NewFileScanner creates a scanner rooted at the given path.
func NewFileScanner(rootPath string) *FileScanner {
	return &FileScanner{rootPath: rootPath}
}

// Scan walks the directory tree and returns all supported source files.
func (s *FileScanner) Scan(ctx context.Context) ([]ScanResult, error) {
	var results []ScanResult

	err := filepath.WalkDir(s.rootPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}

		name := d.Name()

		if d.IsDir() {
			if shouldIgnoreEntry(name) {
				return filepath.SkipDir
			}
			return nil
		}

		if shouldIgnoreEntry(name) {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(name))
		lang, ok := extensionToLanguage[ext]
		if !ok {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > MaxFileSize {
			return nil
		}

		relPath, err := filepath.Rel(s.rootPath, path)
		if err != nil {
			relPath = path
		}

		results = append(results, ScanResult{
			FilePath:     path,
			RelativePath: filepath.ToSlash(relPath),
			Language:     lang,
		})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory %s: %w", s.rootPath, err)
	}

	slog.InfoContext(ctx, "scan complete", "root", s.rootPath, "files", len(results))
	return results, nil
}

// ReadFile reads the content of a file.
func (s *FileScanner) ReadFile(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", filePath, err)
	}
	return string(data), nil
}

// GetFileMtime returns the file modification time as unix timestamp in milliseconds.
func (s *FileScanner) GetFileMtime(filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("stat file %s: %w", filePath, err)
	}
	return info.ModTime().UnixMilli(), nil
}

// shouldIgnoreEntry returns true if the entry name should be excluded.
func shouldIgnoreEntry(name string) bool {
	if alwaysIgnore[name] {
		return true
	}
	if defaultIgnore[name] {
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	return false
}
