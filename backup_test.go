package main

import (
	"archive/zip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreateBackupAllowsParentDirectory(t *testing.T) {
	oldCfg, oldCfgPath := cfg, cfgPath
	defer func() { cfg, cfgPath = oldCfg, oldCfgPath }()

	base := t.TempDir()
	root := filepath.Join(base, "storage")
	backupDir := filepath.Join(base, "backups")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello family"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg = Config{Storage: root, BackupDir: backupDir}
	cfgPath = filepath.Join(base, "config", "config.json")

	req := httptest.NewRequest(http.MethodPost, "/api/backups", nil)
	rec := httptest.NewRecorder()
	createBackup(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("backup status=%d body=%s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one backup archive, got %d", len(entries))
	}
	archive, err := zip.OpenReader(filepath.Join(backupDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	found := false
	for _, file := range archive.File {
		if file.Name == "hello.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("backup archive did not contain the uploaded file")
	}
}

func TestCreateBackupRejectsSymlinkIntoStorage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating directory symlinks may require elevated privileges on Windows")
	}
	oldCfg, oldCfgPath := cfg, cfgPath
	defer func() { cfg, cfgPath = oldCfg, oldCfgPath }()

	base := t.TempDir()
	root := filepath.Join(base, "storage")
	inside := filepath.Join(root, "backups")
	if err := os.MkdirAll(inside, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "backup-alias")
	if err := os.Symlink(inside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	cfg = Config{Storage: root, BackupDir: link}
	cfgPath = filepath.Join(base, "config", "config.json")

	req := httptest.NewRequest(http.MethodPost, "/api/backups", nil)
	rec := httptest.NewRecorder()
	createBackup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected a symlink into storage to be rejected, got %d body=%s", rec.Code, rec.Body.String())
	}
}
