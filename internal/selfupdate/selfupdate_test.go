package selfupdate

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlatformArtifactURLs(t *testing.T) {
	if got := ArtifactName("windows", "arm64"); got != "env-updater-windows-arm64.exe" {
		t.Fatal(got)
	}
	if got := ArtifactName("darwin", "amd64"); got != "env-updater-darwin-amd64" {
		t.Fatal(got)
	}
	if got := DefaultDownloadURL(); !strings.HasSuffix(got, "/releases/latest/download/"+ArtifactName(runtime.GOOS, runtime.GOARCH)) {
		t.Fatal(got)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"1.1.0", "1.0.0", 1},
		{"1.0.0", "1.1.0", -1},
		{"2.0", "1.9.9", 1},
		{"0.9.1", "1.0.0", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0-alpha.1", "1.0.0", -1},
		{"1.0.0-beta.2", "1.0.0-alpha.1", 1},
	}

	for _, tt := range tests {
		got := CompareVersions(tt.a, tt.b)
		if got != tt.expected {
			t.Errorf("CompareVersions(%q, %q) = %d, expected %d", tt.a, tt.b, got, tt.expected)
		}
	}
}

func TestInGitWorktree(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if !InGitWorktree(filepath.Join(cwd, "main.go")) {
		t.Errorf("expected %s to be in git worktree", cwd)
	}

	tmp := t.TempDir()
	if InGitWorktree(filepath.Join(tmp, "dummy")) {
		t.Errorf("expected %s not to be in git worktree", tmp)
	}
}

func TestInstallBinary(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src-bin")
	dest := filepath.Join(dir, "bin", "dest-bin")

	os.WriteFile(src, []byte("echo binary"), 0755)

	if err := InstallBinary(src, dest, false); err != nil {
		t.Fatalf("InstallBinary: %v", err)
	}

	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("expected 0755, got %o", fi.Mode().Perm())
	}
}

func TestIsEnvUpdaterAndDetectVersionWithoutExecution(t *testing.T) {
	dir := t.TempDir()
	canary := filepath.Join(dir, "executed_canary")

	// Create a malicious script that creates 'executed_canary' if executed
	badBin := filepath.Join(dir, "bad-bin")
	os.WriteFile(badBin, []byte("#!/bin/sh\ntouch "+canary+"\necho other\n"), 0755)

	if IsEnvUpdater(badBin) {
		t.Errorf("expected badBin not to be recognized as env-updater")
	}
	if _, err := os.Stat(canary); err == nil {
		t.Fatalf("IsEnvUpdater executed badBin! Security vulnerability!")
	}

	// Create a valid marker binary
	goodBin := filepath.Join(dir, "good-bin")
	os.WriteFile(goodBin, []byte("#!/bin/sh\n# @env-updater-version:2.3.4@\nprintf 'env-updater v2.3.4\\n'\n"), 0755)
	if !IsEnvUpdater(goodBin) {
		t.Errorf("expected goodBin to be recognized as env-updater")
	}
	if ver := DetectVersion(goodBin); ver != "2.3.4" {
		t.Errorf("expected DetectVersion=2.3.4, got %q", ver)
	}
}

func TestDownloadFileHTTPMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("#!/bin/sh\necho 'env-updater v1.2.3'\n"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "dl-bin")
	if err := DownloadFile(srv.URL, dest); err != nil {
		t.Fatalf("DownloadFile HTTP: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if fi.Mode().Perm() != 0755 {
		t.Errorf("expected HTTP downloaded binary to have mode 0755, got %o", fi.Mode().Perm())
	}
}
