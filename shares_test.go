package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareLinkDownloadAndFileChangeInvalidation(t *testing.T) {
	oldCfg, oldCfgPath := cfg, cfgPath
	oldShares := shares
	defer func() {
		cfg, cfgPath = oldCfg, oldCfgPath
		shares = oldShares
	}()

	root := t.TempDir()
	cfg = Config{Storage: root}
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	content := []byte("family photos")
	filePath := filepath.Join(root, "photos.txt")
	if err := os.WriteFile(filePath, content, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filePath)
	if err != nil {
		t.Fatal(err)
	}
	token, id, err := shareToken()
	if err != nil {
		t.Fatal(err)
	}
	shares = map[string]ShareRecord{
		id: {
			Path: "photos.txt", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
			Size: info.Size(), ModTimeNanos: info.ModTime().UnixNano(),
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/share/"+token, nil)
	rec := httptest.NewRecorder()
	publicShareHandler(rec, req)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("download status=%d body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got == "" {
		t.Fatal("download did not set attachment disposition")
	}

	if err := os.WriteFile(filePath, []byte("new content"), 0600); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/share/"+token, nil)
	rec = httptest.NewRecorder()
	publicShareHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("changed file should invalidate the link, got %d", rec.Code)
	}
}

func TestExpiredShareIsNotAccessible(t *testing.T) {
	oldCfg, oldShares := cfg, shares
	defer func() { cfg, shares = oldCfg, oldShares }()
	root := t.TempDir()
	cfg = Config{Storage: root}
	token, id, err := shareToken()
	if err != nil {
		t.Fatal(err)
	}
	shares = map[string]ShareRecord{id: {
		Path: "gone.txt", CreatedAt: time.Now().Add(-2 * time.Hour),
		ExpiresAt: time.Now().Add(-time.Hour),
	}}
	req := httptest.NewRequest(http.MethodGet, "/share/"+token, nil)
	rec := httptest.NewRecorder()
	publicShareHandler(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expired share should return 404, got %d", rec.Code)
	}
}

func TestShareStorePersistsRecords(t *testing.T) {
	oldCfgPath, oldShares := cfgPath, shares
	defer func() { cfgPath, shares = oldCfgPath, oldShares }()
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	record := ShareRecord{Path: "docs/readme.txt", CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().Add(time.Hour), Size: 12}
	shares = map[string]ShareRecord{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": record}
	shareMu.Lock()
	err := saveShareStoreLocked()
	shareMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	shares = make(map[string]ShareRecord)
	if err := loadShareStore(); err != nil {
		t.Fatal(err)
	}
	if got := shares["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]; got.Path != record.Path {
		t.Fatalf("loaded share = %+v, want path %q", got, record.Path)
	}
}

func TestCreateShareRejectsDirectoriesAndInvalidExpiry(t *testing.T) {
	oldCfg, oldCfgPath, oldShares := cfg, cfgPath, shares
	defer func() { cfg, cfgPath, shares = oldCfg, oldCfgPath, oldShares }()
	cfg = Config{Storage: t.TempDir()}
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	shares = make(map[string]ShareRecord)
	if err := os.Mkdir(filepath.Join(cfg.Storage, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
	}{
		{body: `{"path":"folder","expiresInHours":24}`},
		{body: `{"path":"missing.txt","expiresInHours":24}`},
		{body: `{"path":"folder","expiresInHours":9999}`},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/shares", bytes.NewBufferString(tc.body))
		rec := httptest.NewRecorder()
		createShare(rec, req)
		if rec.Code < 400 {
			t.Fatalf("expected invalid share request to fail, got %d", rec.Code)
		}
	}
}

func TestRevokeShareRemovesPersistedRecord(t *testing.T) {
	oldCfgPath, oldShares := cfgPath, shares
	defer func() { cfgPath, shares = oldCfgPath, oldShares }()
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	id := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	shares = map[string]ShareRecord{id: {Path: "x", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}}
	body, _ := json.Marshal(map[string]string{"id": id})
	req := httptest.NewRequest(http.MethodPost, "/api/shares/revoke", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	revokeShare(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, ok := shares[id]; ok {
		t.Fatal("share record remains after revocation")
	}
}

func TestCreateShareReturnsSecretLinkAndDoesNotPersistToken(t *testing.T) {
	oldCfg, oldCfgPath, oldShares := cfg, cfgPath, shares
	defer func() { cfg, cfgPath, shares = oldCfg, oldCfgPath, oldShares }()
	root := t.TempDir()
	cfg = Config{Storage: root}
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	shares = make(map[string]ShareRecord)
	filePath := filepath.Join(root, "family.txt")
	if err := os.WriteFile(filePath, []byte("hello family"), 0600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/shares", bytes.NewBufferString(`{"path":"family.txt","expiresInHours":24}`))
	rec := httptest.NewRecorder()
	createShare(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		URL string `json:"url"`
		Share ShareInfo `json:"share"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(response.URL, "/share/") {
		t.Fatalf("unexpected share URL %q", response.URL)
	}
	token := strings.TrimPrefix(response.URL, "/share/")
	if len(token) != 43 {
		t.Fatalf("unexpected token length %d", len(token))
	}

	store, err := os.ReadFile(shareStorePath())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(store, []byte(token)) {
		t.Fatal("raw share token must not be persisted")
	}

	download := httptest.NewRequest(http.MethodGet, response.URL, nil)
	downloadRec := httptest.NewRecorder()
	publicShareHandler(downloadRec, download)
	if downloadRec.Code != http.StatusOK || downloadRec.Body.String() != "hello family" {
		t.Fatalf("shared download status=%d body=%q", downloadRec.Code, downloadRec.Body.String())
	}
}

func TestRevokeSharesForPathRemovesSubtreeLinks(t *testing.T) {
	oldCfgPath, oldShares := cfgPath, shares
	defer func() { cfgPath, shares = oldCfgPath, oldShares }()
	cfgPath = filepath.Join(t.TempDir(), "config.json")
	shares = map[string]ShareRecord{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {Path: "photos/2024/a.jpg", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": {Path: "photos/family/b.jpg", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
		"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc": {Path: "documents/notes.txt", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
	}
	if err := revokeSharesForPath("photos"); err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Fatalf("expected only the unrelated share to remain, got %d", len(shares))
	}
	if _, ok := shares["cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"]; !ok {
		t.Fatal("revoke removed an unrelated share")
	}
	if _, err := os.Stat(shareStorePath()); err != nil {
		t.Fatalf("revoked shares were not persisted: %v", err)
	}
}
