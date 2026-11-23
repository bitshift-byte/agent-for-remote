package syncer

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
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
	".DS_Store":                    true,
	"Thumbs.db":                    true,
	".super-agent-sync-state.json": true,
	".super-agent-device.json":     true,
}

type IgnoreMatcher struct {
	extraPatterns []string
}

func LoadIgnoreMatcher(rootDir string) *IgnoreMatcher {
	m := &IgnoreMatcher{}
	f, err := os.Open(filepath.Join(rootDir, ".syncignore"))
	if err != nil {
		return m
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m.extraPatterns = append(m.extraPatterns, strings.TrimSuffix(line, "/"))
	}
	return m
}

func (m *IgnoreMatcher) IsIgnored(relPath string) bool {
	cleaned := path.Clean(strings.ReplaceAll(relPath, "\\", "/"))
	if cleaned == "." || cleaned == "" {
		return false
	}
	parts := strings.Split(cleaned, "/")
	for i, part := range parts {
		if defaultIgnoredDirs[part] {
			return true
		}
		if i == len(parts)-1 {
			if defaultIgnoredFiles[part] || strings.HasPrefix(part, ".super-agent-") || strings.HasSuffix(part, ".swp") || strings.HasSuffix(part, ".tmp") {
				return true
			}
		}
		for _, pat := range m.extraPatterns {
			if part == pat {
				return true
			}
			if ok, _ := path.Match(pat, part); ok {
				return true
			}
			if ok, _ := path.Match(pat, cleaned); ok {
				return true
			}
		}
	}
	return false
}
