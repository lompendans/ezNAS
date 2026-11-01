package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxShareLifetime = 30 * 24 * time.Hour

type ShareRecord struct {
	Path         string    `json:"path"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Size         int64     `json:"size"`
	ModTimeNanos int64     `json:"mod_time_nanos"`
}

type ShareInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

var (
	shareMu sync.RWMutex
	shares  = make(map[string]ShareRecord)
)

func shareStorePath() string {
	return filepath.Join(filepath.Dir(cfgPath), "shares.json")
}

func loadShareStore() error {
	path := shareStorePath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		shareMu.Lock()
		shares = make(map[string]ShareRecord)
		shareMu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > 4<<20 {
		return errors.New("share database is larger than 4 MiB")
	}
	loaded := make(map[string]ShareRecord)
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("parse share database: %w", err)
	}
	now := time.Now()
	for id, record := range loaded {
		if len(id) != 64 || record.Path == "" || !record.ExpiresAt.After(now) || record.Size < 0 {
			delete(loaded, id)
		}
	}
	shareMu.Lock()
	shares = loaded
	err = saveShareStoreLocked()
	shareMu.Unlock()
	return err
}

func saveShareStoreLocked() error {
	data, err := json.MarshalIndent(shares, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(shareStorePath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".shares-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0600); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, shareStorePath()); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// revokeSharesForPath removes links for a file or an entire folder subtree.
// A successful delete/rename must not leave a bearer link that can become live
// again if a file is later restored or recreated at the old path.
func revokeSharesForPath(relPath string) error {
	base := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relPath)))
	if base == "." || base == "" {
		return nil
	}
	prefix := base + "/"
	shareMu.Lock()
	removed := make(map[string]ShareRecord)
	for id, record := range shares {
		recordPath := filepath.ToSlash(filepath.Clean(filepath.FromSlash(record.Path)))
		if recordPath == base || strings.HasPrefix(recordPath, prefix) {
			removed[id] = record
			delete(shares, id)
		}
	}
	if len(removed) == 0 {
		shareMu.Unlock()
		return nil
	}
	if err := saveShareStoreLocked(); err != nil {
		for id, record := range removed {
			shares[id] = record
		}
		shareMu.Unlock()
		return err
	}
	shareMu.Unlock()
	return nil
}

func shareToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func listShares(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	shareMu.Lock()
	changed := false
	out := make([]ShareInfo, 0, len(shares))
	for id, record := range shares {
		if !record.ExpiresAt.After(now) {
			delete(shares, id)
			changed = true
			continue
		}
		out = append(out, ShareInfo{
			ID: id, Name: filepath.Base(record.Path), Path: record.Path,
			CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
		})
	}
	var saveErr error
	if changed {
		saveErr = saveShareStoreLocked()
	}
	shareMu.Unlock()
	if saveErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not persist expired share cleanup"})
		return
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ExpiresAt.Before(out[j].ExpiresAt) })
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func createShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path            string `json:"path"`
		ExpiresInHours  int    `json:"expiresInHours"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if in.ExpiresInHours < 1 || in.ExpiresInHours > int(maxShareLifetime/time.Hour) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "share expiry must be between 1 hour and 30 days"})
		return
	}
	absolute, err := safePath(in.Path)
	if err != nil || absolute == cfg.Storage {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file path"})
		return
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "only existing regular files can be shared"})
		return
	}
	rel, err := filepath.Rel(cfg.Storage, absolute)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid file path"})
		return
	}
	token, id, err := shareToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create secure share link"})
		return
	}
	now := time.Now().UTC()
	record := ShareRecord{
		Path: filepath.ToSlash(rel), CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(in.ExpiresInHours) * time.Hour),
		Size: info.Size(), ModTimeNanos: info.ModTime().UnixNano(),
	}
	shareMu.Lock()
	shares[id] = record
	err = saveShareStoreLocked()
	if err != nil {
		delete(shares, id)
	}
	shareMu.Unlock()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not save share link"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true,
		"share": ShareInfo{
			ID: id, Name: filepath.Base(rel), Path: filepath.ToSlash(rel),
			CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
		},
		"url": "/share/" + token,
	})
}

func revokeShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil || len(in.ID) != 64 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid share id"})
		return
	}
	if _, err := hex.DecodeString(in.ID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid share id"})
		return
	}
	shareMu.Lock()
	record, ok := shares[strings.ToLower(in.ID)]
	if !ok {
		shareMu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share link not found"})
		return
	}
	delete(shares, strings.ToLower(in.ID))
	if err := saveShareStoreLocked(); err != nil {
		shares[strings.ToLower(in.ID)] = record
		shareMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not revoke share link"})
		return
	}
	shareMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func publicShareHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/share/")
	if len(token) != 43 || strings.Contains(token, "/") {
		http.NotFound(w, r)
		return
	}
	if _, err := base64.RawURLEncoding.DecodeString(token); err != nil {
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256([]byte(token))
	id := hex.EncodeToString(sum[:])
	shareMu.RLock()
	record, ok := shares[id]
	shareMu.RUnlock()
	if !ok || !record.ExpiresAt.After(time.Now()) {
		http.NotFound(w, r)
		return
	}
	path, err := safePath(record.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != record.Size || info.ModTime().UnixNano() != record.ModTimeNanos {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(filepath.Base(path)))
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

