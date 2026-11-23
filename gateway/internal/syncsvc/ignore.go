package syncsvc

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

var (
	ErrInvalidPath = errors.New("invalid relative path")
	ErrIgnoredPath = errors.New("path is ignored by sync rules")
)

var defaultIgnoredDirs = map[string]bool{
	"node_modules":  true,
	".venv":         true,
	"venv":          true,
	"target":        true,
	".git":          true,
	"dist":          true,
	"build":         true,
	".next":         true,
	".nuxt":         true,
	"__pycache__":   true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".idea":         true,
	".gradle":       true,
	"vendor":        true,
}

var defaultIgnoredFiles = map[string]bool{
	".DS_Store":                  true,
	"Thumbs.db":                  true,
	".super-agent-sync-state.json": true,
	".super-agent-device.json":   true,
}

// NormalizeRelPath validates and converts a relative path to clean slash-separated form.
// Rejects empty paths, absolute paths, and any path traversal ("..").
func NormalizeRelPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" || strings.HasPrefix(p, "/") {
		return "", ErrInvalidPath
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrInvalidPath
	}
	return cleaned, nil
}

// IsIgnored checks whether a slash-separated relative path matches default ignore rules.
func IsIgnored(relPath string) bool {
	cleaned := path.Clean(strings.ReplaceAll(relPath, "\\", "/"))
	parts := strings.Split(cleaned, "/")
	for i, part := range parts {
		if i < len(parts)-1 && defaultIgnoredDirs[part] {
			return true
		}
		if i == len(parts)-1 {
			if defaultIgnoredDirs[part] || defaultIgnoredFiles[part] {
				return true
			}
			if strings.HasPrefix(part, ".super-agent-") || strings.HasSuffix(part, ".swp") || strings.HasSuffix(part, ".tmp") {
				return true
			}
		}
	}
	return false
}

// ResolveSafeJoin joins baseDir and relPath and verifies the result stays strictly inside baseDir.
func ResolveSafeJoin(baseDir, relPath string) (string, error) {
	norm, err := NormalizeRelPath(relPath)
	if err != nil {
		return "", err
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absBase, filepath.FromSlash(norm))
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if absTarget != absBase && !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	return absTarget, nil
}
