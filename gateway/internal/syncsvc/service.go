package syncsvc

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"super-agent/gateway/internal/docker"
)

const MaxSyncFileSize = 64 * 1024 * 1024 // 64 MB per file safety cap for MVP

type FileEntry struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
	ModTimeNano int64  `json:"mod_time_nano"`
	Mode        uint32 `json:"mode"`
}

type ManifestResponse struct {
	Username   string               `json:"username"`
	Files      map[string]FileEntry `json:"files"`
	ActiveLock *DeviceLock          `json:"active_lock,omitempty"`
	ServerTime int64                `json:"server_time_nano"`
}

type Service struct {
	workspaceRoot string
	locks         *LockManager
}

func NewService(workspaceRoot string, locks *LockManager) *Service {
	return &Service{
		workspaceRoot: workspaceRoot,
		locks:         locks,
	}
}

func (s *Service) Locks() *LockManager {
	return s.locks
}

func (s *Service) UserWorkspaceDir(username string) (string, error) {
	safeUser := docker.SanitizeUsername(username)
	dir := filepath.Join(s.workspaceRoot, safeUser)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create workspace dir: %w", err)
	}
	return dir, nil
}

func (s *Service) BuildManifest(username string) (*ManifestResponse, error) {
	root, err := s.UserWorkspaceDir(username)
	if err != nil {
		return nil, err
	}

	files := make(map[string]FileEntry)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if IsIgnored(relSlash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > MaxSyncFileSize {
			return nil
		}
		hash, err := HashFile(p)
		if err != nil {
			return nil
		}
		files[relSlash] = FileEntry{
			Path:        relSlash,
			SHA256:      hash,
			Size:        info.Size(),
			ModTimeNano: info.ModTime().UTC().UnixNano(),
			Mode:        uint32(info.Mode().Perm()),
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &ManifestResponse{
		Username:   username,
		Files:      files,
		ActiveLock: s.locks.Get(username),
		ServerTime: time.Now().UTC().UnixNano(),
	}, nil
}

func (s *Service) OpenFile(username, relPath string) (*os.File, *FileEntry, error) {
	norm, err := NormalizeRelPath(relPath)
	if err != nil {
		return nil, nil, err
	}
	if IsIgnored(norm) {
		return nil, nil, ErrIgnoredPath
	}

	root, err := s.UserWorkspaceDir(username)
	if err != nil {
		return nil, nil, err
	}
	fullPath, err := ResolveSafeJoin(root, norm)
	if err != nil {
		return nil, nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, ErrInvalidPath
	}
	hash, err := HashFile(fullPath)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, nil, err
	}

	entry := &FileEntry{
		Path:        norm,
		SHA256:      hash,
		Size:        info.Size(),
		ModTimeNano: info.ModTime().UTC().UnixNano(),
		Mode:        uint32(info.Mode().Perm()),
	}
	return f, entry, nil
}

func (s *Service) WriteFile(username, relPath string, body io.Reader, modTimeNano int64, perm os.FileMode) (*FileEntry, error) {
	norm, err := NormalizeRelPath(relPath)
	if err != nil {
		return nil, err
	}
	if IsIgnored(norm) {
		return nil, ErrIgnoredPath
	}

	root, err := s.UserWorkspaceDir(username)
	if err != nil {
		return nil, err
	}
	fullPath, err := ResolveSafeJoin(root, norm)
	if err != nil {
		return nil, err
	}

	parentDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp(parentDir, ".super-agent-upload-*")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	hasher := sha256.New()
	limited := io.LimitReader(body, MaxSyncFileSize+1)
	written, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if written > MaxSyncFileSize {
		_ = tmp.Close()
		return nil, fmt.Errorf("file exceeds max size of %d bytes", MaxSyncFileSize)
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	if perm == 0 {
		perm = 0o644
	}
	_ = os.Chmod(tmpName, perm)
	if modTimeNano > 0 {
		mt := time.Unix(0, modTimeNano)
		_ = os.Chtimes(tmpName, mt, mt)
	}

	if err := os.Rename(tmpName, fullPath); err != nil {
		return nil, err
	}
	cleanup = false

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	return &FileEntry{
		Path:        norm,
		SHA256:      hex.EncodeToString(hasher.Sum(nil)),
		Size:        info.Size(),
		ModTimeNano: info.ModTime().UTC().UnixNano(),
		Mode:        uint32(info.Mode().Perm()),
	}, nil
}

func (s *Service) DeleteFile(username, relPath string) error {
	norm, err := NormalizeRelPath(relPath)
	if err != nil {
		return err
	}
	if IsIgnored(norm) {
		return ErrIgnoredPath
	}

	root, err := s.UserWorkspaceDir(username)
	if err != nil {
		return err
	}
	fullPath, err := ResolveSafeJoin(root, norm)
	if err != nil {
		return err
	}

	err = os.Remove(fullPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func HashFile(path string) (string, error) {
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
