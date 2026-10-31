package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const releaseAPI = "https://api.github.com/repos/lompendans/ezNAS/releases/latest"

type githubRelease struct {
	TagName string `json:"tag_name"`
	Draft bool `json:"draft"`
	Prerelease bool `json:"prerelease"`
	HTMLURL string `json:"html_url"`
}

func runUpdate() {
	if err := stageLatestUpdate(); err != nil {
		fmt.Fprintln(os.Stderr, "ezNAS update:", err)
		os.Exit(1)
	}
}

func stageLatestUpdate() error {
	client := &http.Client{Timeout: 2 * time.Minute}
	req, err := http.NewRequest(http.MethodGet, releaseAPI, nil)
	if err != nil { return err }
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ezNAS-updater")
	resp, err := client.Do(req)
	if err != nil { return fmt.Errorf("check GitHub releases: %w", err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return fmt.Errorf("GitHub release check returned HTTP %d", resp.StatusCode) }
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&release); err != nil { return fmt.Errorf("read release metadata: %w", err) }
	if release.TagName == "" || release.Draft || release.Prerelease { return errors.New("no published stable release was found") }
	if version == release.TagName { fmt.Printf("ezNAS %s is already installed.\n", version); return nil }

	arch := runtime.GOARCH
	var asset string
	if runtime.GOOS == "windows" { asset = fmt.Sprintf("eznas-windows-%s.zip", arch) } else if runtime.GOOS == "linux" { asset = fmt.Sprintf("eznas-linux-%s.tar.gz", arch) } else { return fmt.Errorf("automatic updates are not supported on %s", runtime.GOOS) }
	base := "https://github.com/lompendans/ezNAS/releases/download/" + release.TagName
	dir, err := os.MkdirTemp("", "eznas-update-*")
	if err != nil { return err }
	defer os.RemoveAll(dir)
	assetPath := filepath.Join(dir, asset)
	if err := downloadReleaseFile(client, base+"/"+asset, assetPath); err != nil { return fmt.Errorf("download release package: %w", err) }
	checksumsPath := filepath.Join(dir, "checksums.txt")
	if err := downloadReleaseFile(client, base+"/checksums.txt", checksumsPath); err != nil { return fmt.Errorf("download checksums: %w", err) }
	expected, err := expectedChecksum(checksumsPath, asset)
	if err != nil { return err }
	actual, err := fileSHA256(assetPath)
	if err != nil { return err }
	if !strings.EqualFold(actual, expected) { return errors.New("SHA-256 verification failed; downloaded update was rejected") }

	stagedName := "eznas"
	if runtime.GOOS == "windows" { stagedName = "eznas.exe" }
	staged := filepath.Join(dir, stagedName)
	if runtime.GOOS == "windows" {
		if err := extractWindowsBinary(assetPath, staged); err != nil { return err }
	} else {
		if err := extractLinuxBinary(assetPath, staged); err != nil { return err }
	}
	if err := os.Chmod(staged, 0755); err != nil { return fmt.Errorf("prepare staged executable: %w", err) }
	cfgDir := filepath.Dir(cfgPath)
	stageDir := filepath.Join(cfgDir, "updates", release.TagName)
	if err := os.MkdirAll(stageDir, 0700); err != nil { return fmt.Errorf("create staging directory: %w", err) }
	dest := filepath.Join(stageDir, stagedName)
	tmp := dest + ".tmp"
	if err := copyFile(staged, tmp); err != nil { return fmt.Errorf("stage verified executable: %w", err) }
	if err := os.Chmod(tmp, 0755); err != nil { os.Remove(tmp); return err }
	if err := os.Rename(tmp, dest); err != nil { os.Remove(tmp); return fmt.Errorf("finalize staged update: %w", err) }
	fmt.Printf("Verified ezNAS %s and staged it at:\n%s\n", release.TagName, dest)
	fmt.Println("Your installed executable has not been changed. Applying updates safely is the next updater step.")
	return nil
}

func downloadReleaseFile(client *http.Client, url, path string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil { return err }
	req.Header.Set("User-Agent", "ezNAS-updater")
	resp, err := client.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return fmt.Errorf("download returned HTTP %d", resp.StatusCode) }
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil { return err }
	_, copyErr := io.Copy(f, io.LimitReader(resp.Body, 256<<20))
	closeErr := f.Close()
	if copyErr != nil { return copyErr }
	if closeErr != nil { return closeErr }
	return nil
}

func expectedChecksum(path, asset string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil { return "", err }
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == asset {
			if len(fields[0]) != 64 { return "", errors.New("malformed SHA-256 checksum entry") }
			if _, err := hex.DecodeString(fields[0]); err != nil { return "", errors.New("malformed SHA-256 checksum entry") }
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("no checksum entry found for %s", asset)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil { return "", err }
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil { return "", err }
	return hex.EncodeToString(h.Sum(nil)), nil
}

func extractWindowsBinary(path, dest string) error {
	z, err := zip.OpenReader(path)
	if err != nil { return fmt.Errorf("open Windows update archive: %w", err) }
	defer z.Close()
	for _, f := range z.File {
		if f.Name != "eznas.exe" { continue }
		rc, err := f.Open()
		if err != nil { return err }
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil { rc.Close(); return err }
		_, copyErr := io.Copy(out, io.LimitReader(rc, 128<<20))
		rcErr := rc.Close()
		outErr := out.Close()
		if copyErr != nil { return copyErr }
		if rcErr != nil { return rcErr }
		if outErr != nil { return outErr }
		return nil
	}
	return errors.New("Windows archive does not contain eznas.exe")
}

func extractLinuxBinary(path, dest string) error {
	f, err := os.Open(path)
	if err != nil { return err }
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil { return fmt.Errorf("open Linux update archive: %w", err) }
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF { break }
		if err != nil { return err }
		if h.Name != "eznas" || !h.FileInfo().Mode().IsRegular() { continue }
		if h.Size < 0 || h.Size > 128<<20 { return errors.New("Linux executable has an invalid size") }
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil { return err }
		_, copyErr := io.Copy(out, io.LimitReader(tr, (128<<20)+1))
		outErr := out.Close()
		if copyErr != nil { return copyErr }
		if outErr != nil { return outErr }
		return nil
	}
	return errors.New("Linux archive does not contain eznas")
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil { return err }
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil { return err }
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil { return copyErr }
	return closeErr
}
