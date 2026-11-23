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
}

type SyncStats struct {
	Pushed    int
	Pulled    int
	Deleted   int
	Conflicts int
}

type Engine struct {
	rootDir  string
	client   *Client
	ignore   *IgnoreMatcher
	baseline map[string]FileEntry
	hashCache map[string]FileEntry
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
		rootDir:   absRoot,
		client:    client,
		ignore:    LoadIgnoreMatcher(absRoot),
		baseline:  make(map[string]FileEntry),
		hashCache: make(map[string]FileEntry),
	}
	e.loadState()
	return e, nil
}

func LoadOrCreateDeviceIdentity(rootDir, customName string) (*DeviceIdentity, error) {
	p := filepath.Join(rootDir, deviceFileName)
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

// InitialTakeoverPull pulls all latest cloud files onto this device after taking over the active device lock,
// preventing stale local files from overwriting newer cloud code.
func (e *Engine) InitialTakeoverPull(ctx context.Context) (SyncStats, error) {
	var stats SyncStats
	manifest, err := e.client.GetManifest(ctx)
	if err != nil {
		return stats, err
	}
	local, err := e.scanLocal()
	if err != nil {
		return stats, err
	}

	nextBaseline := make(map[string]FileEntry, len(manifest.Files))
	for _, rel := range sortedKeys(manifest.Files) {
		remote := manifest.Files[rel]
		if loc, ok := local[rel]; ok && loc.SHA256 == remote.SHA256 {
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
	e.saveState()
	return stats, nil
}

// SyncOnce performs a single 3-way differential reconciliation between Local, Baseline, and Cloud.
func (e *Engine) SyncOnce(ctx context.Context) (SyncStats, error) {
	var stats SyncStats

	manifest, err := e.client.GetManifest(ctx)
	if err != nil {
		return stats, err
	}
	local, err := e.scanLocal()
	if err != nil {
		return stats, err
	}

	allPaths := make(map[string]struct{})
	for p := range local {
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
		loc, hasLocal := local[rel]
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
				// Both sides modified concurrently: keep newer version and preserve backup copy of conflict
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
				// Deleted on cloud while unchanged locally -> delete locally
				if err := os.Remove(filepath.Join(e.rootDir, filepath.FromSlash(rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
					return stats, err
				}
				delete(e.baseline, rel)
				delete(e.hashCache, rel)
				stats.Deleted++
			} else {
				// Created or modified locally -> push to cloud
				entry, err := e.pushFile(ctx, rel, loc)
				if err != nil {
					return stats, err
				}
				e.baseline[rel] = *entry
				stats.Pushed++
			}

		case !hasLocal && hasRemote:
			if hasBase && rem.SHA256 == base.SHA256 {
				// Deleted locally while unchanged on cloud -> delete on cloud
				if err := e.client.DeleteFile(ctx, rel); err != nil {
					return stats, err
				}
				delete(e.baseline, rel)
				stats.Deleted++
			} else {
				// Created or modified on cloud -> pull locally
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

func (e *Engine) scanLocal() (map[string]FileEntry, error) {
	files := make(map[string]FileEntry)
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
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
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
	return files, err
}

func (e *Engine) loadState() {
	raw, err := os.ReadFile(filepath.Join(e.rootDir, stateFileName))
	if err != nil {
		return
	}
	var st SyncState
	if json.Unmarshal(raw, &st) == nil && st.Files != nil {
		e.baseline = st.Files
	}
}

func (e *Engine) saveState() {
	raw, err := json.MarshalIndent(SyncState{Files: e.baseline}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(e.rootDir, stateFileName), raw, 0o600)
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
