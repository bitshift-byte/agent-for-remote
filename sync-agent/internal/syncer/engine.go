package syncer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	stateFileName  = ".super-agent-sync-state.json"
	deviceFileName = ".super-agent-device.json"
	maxFileSize    = 64 * 1024 * 1024
)

type DeviceIdentity struct {
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	OS         string `json:"os"`
}

type SyncState struct {
	Files map[string]FileEntry `json:"files"`
	Dirs  []string             `json:"dirs,omitempty"`
}

type SyncStats struct {
	Pushed    int
	Pulled    int
	Deleted   int
	Conflicts int
}

type Engine struct {
	rootDir      string
	client       *Client
	ignore       *IgnoreMatcher
	baseline     map[string]FileEntry
	baselineDirs map[string]bool
	hashCache    map[string]FileEntry
}

func NewEngine(rootDir string, client *Client) (*Engine, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		return nil, err
	}

	e := &Engine{
		rootDir:      absRoot,
		client:       client,
		ignore:       LoadIgnoreMatcher(absRoot),
		baseline:     make(map[string]FileEntry),
		baselineDirs: make(map[string]bool),
		hashCache:    make(map[string]FileEntry),
	}
	e.loadState()
	return e, nil
}

func appConfigDir() string {
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		dir := filepath.Join(base, "super-agent")
		if os.MkdirAll(dir, 0o700) == nil {
			return dir
		}
	}
	return ""
}

func stateFilePath(absRoot string) string {
	if cfgDir := appConfigDir(); cfgDir != "" {
		stateDir := filepath.Join(cfgDir, "state")
		if os.MkdirAll(stateDir, 0o700) == nil {
			sum := sha256.Sum256([]byte(absRoot))
			return filepath.Join(stateDir, hex.EncodeToString(sum[:8])+".json")
		}
	}
	return filepath.Join(absRoot, stateFileName)
}

func LoadOrCreateDeviceIdentity(rootDir, customName string) (*DeviceIdentity, error) {
	p := filepath.Join(rootDir, deviceFileName)
	if cfgDir := appConfigDir(); cfgDir != "" {
		p = filepath.Join(cfgDir, "device.json")
	}
	if raw, err := os.ReadFile(p); err == nil {
		var id DeviceIdentity
		if json.Unmarshal(raw, &id) == nil && id.DeviceID != "" {
			if customName != "" {
				id.DeviceName = customName
			}
			return &id, nil
		}
	}

	host, _ := os.Hostname()
	if customName != "" {
		host = customName
	}
	if host == "" {
		host = "local-device"
	}

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := &DeviceIdentity{
		DeviceID:   "dev-" + hex.EncodeToString(b),
		DeviceName: host,
		OS:         runtime.GOOS,
	}
	raw, _ := json.MarshalIndent(id, "", "  ")
	_ = os.WriteFile(p, raw, 0o600)
	return id, nil
}

// InitialTakeoverPull pulls all latest cloud files & directories onto this device after taking over the active device lock,
// preventing stale local files from overwriting newer cloud code.
func (e *Engine) InitialTakeoverPull(ctx context.Context) (SyncStats, error) {
	var stats SyncStats
	manifest, err := e.client.GetManifest(ctx)
	if err != nil {
		return stats, err
	}
	localFiles, localDirs, err := e.scanLocal()
	if err != nil {
		return stats, err
	}

	nextDirs := make(map[string]bool, len(manifest.Dirs))
	for _, relDir := range sortDirsShallowFirst(manifest.Dirs) {
		if !localDirs[relDir] {
			if err := os.MkdirAll(filepath.Join(e.rootDir, filepath.FromSlash(relDir)), 0o755); err != nil {
				return stats, err
			}
			stats.Pulled++
			log.Printf("[sync] ↓ mkdir locally %s", relDir)
		}
		nextDirs[relDir] = true
	}

	nextBaseline := make(map[string]FileEntry, len(manifest.Files))
	for _, rel := range sortedKeys(manifest.Files) {
		remote := manifest.Files[rel]
		if loc, ok := localFiles[rel]; ok && loc.SHA256 == remote.SHA256 {
			nextBaseline[rel] = remote
			continue
		}
		if err := e.pullFile(ctx, rel); err != nil {
			return stats, err
		}
		stats.Pulled++
		nextBaseline[rel] = remote
	}

	e.baseline = nextBaseline
	e.baselineDirs = nextDirs
	e.saveState()
	return stats, nil
}

// SyncOnce performs a single 3-way differential reconciliation for both files and empty directories.
func (e *Engine) SyncOnce(ctx context.Context) (SyncStats, error) {
	var stats SyncStats

	manifest, err := e.client.GetManifest(ctx)
	if err != nil {
		return stats, err
	}
	localFiles, localDirs, err := e.scanLocal()
	if err != nil {
		return stats, err
	}

	remoteDirs := make(map[string]bool, len(manifest.Dirs))
	for _, d := range manifest.Dirs {
		remoteDirs[d] = true
	}

	// Phase A: Reconcile directory creations (shallow-to-deep)
	allDirSet := make(map[string]struct{})
	for d := range localDirs {
		allDirSet[d] = struct{}{}
	}
	for d := range remoteDirs {
		allDirSet[d] = struct{}{}
	}
	for d := range e.baselineDirs {
		allDirSet[d] = struct{}{}
	}
	allDirs := make([]string, 0, len(allDirSet))
	for d := range allDirSet {
		allDirs = append(allDirs, d)
	}

	for _, relDir := range sortDirsShallowFirst(allDirs) {
		hasLocal := localDirs[relDir]
		hasRemote := remoteDirs[relDir]
		hasBase := e.baselineDirs[relDir]

		switch {
		case hasLocal && hasRemote:
			e.baselineDirs[relDir] = true
		case hasLocal && !hasRemote && !hasBase:
			if err := e.client.CreateDir(ctx, relDir); err != nil {
				return stats, err
			}
			remoteDirs[relDir] = true
			e.baselineDirs[relDir] = true
			stats.Pushed++
			log.Printf("[sync] ↑ mkdir on cloud %s", relDir)
		case !hasLocal && hasRemote && !hasBase:
			if err := os.MkdirAll(filepath.Join(e.rootDir, filepath.FromSlash(relDir)), 0o755); err != nil {
				return stats, err
			}
			localDirs[relDir] = true
			e.baselineDirs[relDir] = true
			stats.Pulled++
			log.Printf("[sync] ↓ mkdir locally %s", relDir)
		}
	}

	// Phase B: Reconcile files (push / pull / delete / conflict backup)
	allPaths := make(map[string]struct{})
	for p := range localFiles {
		allPaths[p] = struct{}{}
	}
	for p := range manifest.Files {
		allPaths[p] = struct{}{}
	}
	for p := range e.baseline {
		allPaths[p] = struct{}{}
	}

	paths := make([]string, 0, len(allPaths))
	for p := range allPaths {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, rel := range paths {
		loc, hasLocal := localFiles[rel]
		rem, hasRemote := manifest.Files[rel]
		base, hasBase := e.baseline[rel]

		switch {
		case hasLocal && hasRemote:
			if loc.SHA256 == rem.SHA256 {
				e.baseline[rel] = rem
				continue
			}
			localChanged := !hasBase || loc.SHA256 != base.SHA256
			remoteChanged := !hasBase || rem.SHA256 != base.SHA256

			switch {
			case localChanged && !remoteChanged:
				entry, err := e.pushFile(ctx, rel, loc)
				if err != nil {
					return stats, err
				}
				e.baseline[rel] = *entry
				stats.Pushed++
			case remoteChanged && !localChanged:
				if err := e.pullFile(ctx, rel); err != nil {
					return stats, err
				}
				e.baseline[rel] = rem
				stats.Pulled++
			default:
				if loc.ModTimeNano >= rem.ModTimeNano {
					if err := e.saveRemoteConflictCopy(ctx, rel); err == nil {
						stats.Conflicts++
					}
					entry, err := e.pushFile(ctx, rel, loc)
					if err != nil {
						return stats, err
					}
					e.baseline[rel] = *entry
					stats.Pushed++
				} else {
					if err := e.saveLocalConflictCopy(rel); err == nil {
						stats.Conflicts++
					}
					if err := e.pullFile(ctx, rel); err != nil {
						return stats, err
					}
					e.baseline[rel] = rem
					stats.Pulled++
				}
			}

		case hasLocal && !hasRemote:
			if hasBase && loc.SHA256 == base.SHA256 {
				if err := os.Remove(filepath.Join(e.rootDir, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
					return stats, err
				}
				delete(e.baseline, rel)
				delete(e.hashCache, rel)
				stats.Deleted++
				log.Printf("[sync] 🗑 deleted locally %s (removed on cloud)", rel)
			} else {
				entry, err := e.pushFile(ctx, rel, loc)
				if err != nil {
					return stats, err
				}
				e.baseline[rel] = *entry
				stats.Pushed++
			}

		case !hasLocal && hasRemote:
			if hasBase && rem.SHA256 == base.SHA256 {
				if err := e.client.DeleteFile(ctx, rel); err != nil {
					return stats, err
				}
				delete(e.baseline, rel)
				stats.Deleted++
				log.Printf("[sync] 🗑 deleted on cloud %s (removed locally)", rel)
			} else {
				if err := e.pullFile(ctx, rel); err != nil {
					return stats, err
				}
				e.baseline[rel] = rem
				stats.Pulled++
			}

		default:
			delete(e.baseline, rel)
		}
	}

	// Phase C: Reconcile directory deletions (deep-to-shallow)
	for _, relDir := range sortDirsDeepFirst(allDirs) {
		hasLocal := localDirs[relDir]
		hasRemote := remoteDirs[relDir]
		hasBase := e.baselineDirs[relDir]

		switch {
		case hasLocal && !hasRemote && hasBase:
			// Deleted on cloud -> remove locally if empty (safe against newly created local files)
			if err := os.Remove(filepath.Join(e.rootDir, filepath.FromSlash(relDir))); err == nil || errors.Is(err, os.ErrNotExist) {
				delete(e.baselineDirs, relDir)
				stats.Deleted++
				log.Printf("[sync] 🗑 removed dir locally %s (removed on cloud)", relDir)
			}
		case !hasLocal && hasRemote && hasBase:
			if err := e.client.DeleteDir(ctx, relDir); err != nil {
				return stats, err
			}
			delete(e.baselineDirs, relDir)
			stats.Deleted++
			log.Printf("[sync] 🗑 removed dir on cloud %s (removed locally)", relDir)
		case !hasLocal && !hasRemote:
			delete(e.baselineDirs, relDir)
		}
	}

	e.saveState()
	return stats, nil
}

func (e *Engine) pushFile(ctx context.Context, rel string, loc FileEntry) (*FileEntry, error) {
	fullPath := filepath.Join(e.rootDir, filepath.FromSlash(rel))
	entry, err := e.client.UploadFile(ctx, rel, fullPath, loc.ModTimeNano, os.FileMode(loc.Mode))
	if err != nil {
		return nil, err
	}
	log.Printf("[sync] ↑ pushed %s (%d bytes)", rel, entry.Size)
	return entry, nil
}

func (e *Engine) pullFile(ctx context.Context, rel string) error {
	data, modTimeNano, perm, err := e.client.DownloadFile(ctx, rel)
	if err != nil {
		return err
	}
	fullPath := filepath.Join(e.rootDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(fullPath), ".super-agent-dl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	_ = tmp.Close()
	_ = os.Chmod(tmpName, perm)
	if modTimeNano > 0 {
		mt := time.Unix(0, modTimeNano)
		_ = os.Chtimes(tmpName, mt, mt)
	}
	if err := os.Rename(tmpName, fullPath); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	delete(e.hashCache, rel)
	log.Printf("[sync] ↓ pulled %s (%d bytes)", rel, len(data))
	return nil
}

func (e *Engine) saveLocalConflictCopy(rel string) error {
	src := filepath.Join(e.rootDir, filepath.FromSlash(rel))
	dst := fmt.Sprintf("%s.conflict.%s", src, time.Now().Format("20060102-150405"))
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	log.Printf("[sync] ! conflict backup saved: %s", dst)
	return os.WriteFile(dst, raw, 0o644)
}

func (e *Engine) saveRemoteConflictCopy(ctx context.Context, rel string) error {
	data, _, _, err := e.client.DownloadFile(ctx, rel)
	if err != nil {
		return err
	}
	dst := fmt.Sprintf("%s.conflict.%s", filepath.Join(e.rootDir, filepath.FromSlash(rel)), time.Now().Format("20060102-150405"))
	log.Printf("[sync] ! remote conflict backup saved: %s", dst)
	return os.WriteFile(dst, data, 0o644)
}

func (e *Engine) scanLocal() (map[string]FileEntry, map[string]bool, error) {
	files := make(map[string]FileEntry)
	dirs := make(map[string]bool)
	err := filepath.WalkDir(e.rootDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if p == e.rootDir {
			return nil
		}
		rel, err := filepath.Rel(e.rootDir, p)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if e.ignore.IsIgnored(relSlash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			dirs[relSlash] = true
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
			return nil
		}

		mt := info.ModTime().UTC().UnixNano()
		if cached, ok := e.hashCache[relSlash]; ok && cached.Size == info.Size() && cached.ModTimeNano == mt {
			files[relSlash] = cached
			return nil
		}

		hash, err := hashFile(p)
		if err != nil {
			return nil
		}
		entry := FileEntry{
			Path:        relSlash,
			SHA256:      hash,
			Size:        info.Size(),
			ModTimeNano: mt,
			Mode:        uint32(info.Mode().Perm()),
		}
		e.hashCache[relSlash] = entry
		files[relSlash] = entry
		return nil
	})
	return files, dirs, err
}

func (e *Engine) loadState() {
	raw, err := os.ReadFile(stateFilePath(e.rootDir))
	if err != nil {
		return
	}
	var st SyncState
	if json.Unmarshal(raw, &st) == nil {
		if st.Files != nil {
			e.baseline = st.Files
		}
		for _, d := range st.Dirs {
			e.baselineDirs[d] = true
		}
	}
}

func (e *Engine) saveState() {
	dirs := make([]string, 0, len(e.baselineDirs))
	for d := range e.baselineDirs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	raw, err := json.MarshalIndent(SyncState{Files: e.baseline, Dirs: dirs}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(stateFilePath(e.rootDir), raw, 0o600)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sortedKeys(m map[string]FileEntry) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortDirsShallowFirst(dirs []string) []string {
	cp := append([]string(nil), dirs...)
	sort.Slice(cp, func(i, j int) bool {
		di := strings.Count(cp[i], "/")
		dj := strings.Count(cp[j], "/")
		if di != dj {
			return di < dj
		}
		return cp[i] < cp[j]
	})
	return cp
}

func sortDirsDeepFirst(dirs []string) []string {
	cp := append([]string(nil), dirs...)
	sort.Slice(cp, func(i, j int) bool {
		di := strings.Count(cp[i], "/")
		dj := strings.Count(cp[j], "/")
		if di != dj {
			return di > dj
		}
		return cp[i] > cp[j]
	})
	return cp
}
