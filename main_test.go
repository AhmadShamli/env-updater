package main

import (
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/AhmadShamli/env-updater/internal/selfupdate"
)

func buildTestBinary(t *testing.T, path, version, goos, goarch string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags", "-s -w -X main.Version="+version+" -X main.VersionMarker=@env-updater-version:"+version+"@", "-o", path, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s/%s: %v: %s", goos, goarch, err, out)
	}
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateNativeArtifacts(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, selfupdate.BinaryName())
	remote := filepath.Join(dir, "remote")
	downloads := filepath.Join(dir, "downloads")
	if err := os.Mkdir(downloads, 0700); err != nil {
		t.Fatal(err)
	}
	buildTestBinary(t, local, "1.0.0", runtime.GOOS, runtime.GOARCH)
	original, err := os.ReadFile(local)
	if err != nil {
		t.Fatal(err)
	}
	if got := selfupdate.DetectVersion(local); got != "1.0.0" {
		t.Fatalf("trimmed release version detection: %q", got)
	}
	otherOS := "linux"
	if runtime.GOOS == otherOS {
		otherOS = "darwin"
	}
	otherArch := "amd64"
	if runtime.GOARCH == otherArch {
		otherArch = "arm64"
	}
	for _, test := range []struct {
		name, version, goos, goarch, message string
		dryRun, success                      bool
	}{
		{"downgrade", "0.0.1", runtime.GOOS, runtime.GOARCH, "older than local", false, false},
		{"wrong platform", "99.0.0", otherOS, runtime.GOARCH, "targets", false, false},
		{"wrong architecture", "99.0.0", runtime.GOOS, otherArch, "targets", false, false},
		{"dry run", "99.0.0", runtime.GOOS, runtime.GOARCH, "would update", true, true},
		{"successful update", "99.0.0", runtime.GOOS, runtime.GOARCH, "Updated", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			buildTestBinary(t, remote, test.version, test.goos, test.goarch)
			data, err := os.ReadFile(remote)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, remote+".sha256", fmt.Appendf(nil, "%x  remote\n", sha256.Sum256(data)))
			args := []string{"update", "--no-header"}
			if test.dryRun {
				args = append(args, "--dry-run")
			}
			cmd := exec.Command(local, args...)
			cmd.Env = append(os.Environ(), "ENV_UPDATER_REPO_URL=file://"+remote, "ENV_UPDATER_CHECKSUM_URL=file://"+remote+".sha256", "TMPDIR="+downloads, "TMP="+downloads, "TEMP="+downloads)
			out, err := cmd.CombinedOutput()
			if (err == nil) != test.success || !strings.Contains(string(out), test.message) {
				t.Fatalf("update: %v: %s", err, out)
			}
			if !test.success || test.dryRun {
				got, err := os.ReadFile(local)
				if err != nil || string(got) != string(original) {
					t.Fatal("update unexpectedly changed local executable")
				}
			} else if out, err := exec.Command(local, "--version", "--no-header").CombinedOutput(); err != nil || string(out) != "env-updater v99.0.0\n" {
				t.Fatalf("updated executable: %v: %s", err, out)
			}
			entries, err := os.ReadDir(downloads)
			if err != nil || len(entries) != 0 {
				t.Fatalf("download directories leaked: %v %v", entries, err)
			}
		})
	}
}

func TestUpdateNeverExecutesScript(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, selfupdate.BinaryName())
	buildTestBinary(t, local, "1.0.0", runtime.GOOS, runtime.GOARCH)
	remote := filepath.Join(dir, "remote")
	canary := filepath.Join(dir, "executed")
	data := []byte("#!/bin/sh\n# @env-updater-marker@\ntouch '" + canary + "'\nprintf '%s%s\\n' 'env-updater v' '99.0.0'\n")
	writeTestFile(t, remote, data)
	writeTestFile(t, remote+".sha256", fmt.Appendf(nil, "%x  remote\n", sha256.Sum256(data)))
	for _, args := range [][]string{{"update"}, {"update", "--dry-run"}, {"update", "--force", "--skip-verify"}} {
		cmd := exec.Command(local, append(args, "--no-header")...)
		cmd.Env = append(os.Environ(), "ENV_UPDATER_REPO_URL=file://"+remote, "ENV_UPDATER_CHECKSUM_URL=file://"+remote+".sha256")
		if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "not a native Go executable") {
			t.Fatalf("script accepted: %v %s", err, out)
		}
		if _, err := os.Stat(canary); !os.IsNotExist(err) {
			t.Fatal("downloaded script executed")
		}
	}
}

func TestLegacyTemporarySymlink(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires POSIX shell")
	}
	dir := t.TempDir()
	source, target, victim := filepath.Join(dir, "source"), filepath.Join(dir, ".env"), filepath.Join(dir, "victim")
	writeTestFile(t, source, []byte("A=default\n"))
	writeTestFile(t, target, []byte("A=\n"))
	writeTestFile(t, victim, []byte("UNRELATED DATA\n"))
	cmd := exec.Command("sh", "scripts/env-updater.sh", "-q", "-i", "--no-backup", "-s", source, "-t", target)
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	errOut, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	if _, err := io.ReadFull(errOut, make([]byte, len("Enter value for A [default]: "))); err != nil {
		t.Fatal(err)
	}
	oldTemp := filepath.Join(dir, fmt.Sprintf("..env.tmp.%d", cmd.Process.Pid))
	if err := os.Symlink(victim, oldTemp); err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(in, "it's $(printf EXPANDED)\n")
	if err != nil {
		t.Fatal(err)
	}
	in.Close()
	out, _ := io.ReadAll(errOut)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("legacy sync: %v: %s", err, out)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "UNRELATED DATA\n" {
		t.Fatalf("victim changed: %q %v", got, err)
	}
	out, err = exec.Command("sh", "-c", `. "$1"; printf '%s' "$A"`, "sh", target).CombinedOutput()
	if err != nil || string(out) != "it's $(printf EXPANDED)" {
		t.Fatalf("legacy value expanded: %q %v", out, err)
	}
}

func TestLegacyMultilinePreservation(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("requires POSIX shell")
	}
	for _, input := range []string{
		"A=\"first\\\n\"\nB=existing\n",
		"A=\"first\nsecond\"\nB=existing\r\n",
		"A='first\r\nsecond\nthird'\r\nB=existing\n",
	} {
		dir := t.TempDir()
		source, target := filepath.Join(dir, "source"), filepath.Join(dir, ".env")
		writeTestFile(t, source, []byte("A=default\nB=default\n"))
		writeTestFile(t, target, []byte(input))
		for range 2 {
			cmd := exec.Command("sh", "scripts/env-updater.sh", "-q", "--no-backup", "-s", source, "-t", target)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("legacy sync: %v: %s", err, out)
			}
			got, err := os.ReadFile(target)
			end := strings.Index(input, "B=existing")
			if err != nil || !strings.HasPrefix(string(got), input[:end-1]) {
				t.Fatalf("multiline bytes changed: %q -> %q: %v", input, got, err)
			}
		}
	}
}

func TestPublishedArtifacts(t *testing.T) {
	paths := []string{}
	assets, err := filepath.Glob("dist/env-updater-*")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range assets {
		if !strings.HasSuffix(path, ".sha256") {
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		info, err := buildinfo.ReadFile(path)
		if err != nil || info.Path != selfupdate.ModulePath {
			t.Fatalf("invalid release executable %s: %v", path, err)
		}
		if got := selfupdate.DetectVersion(path); got != Version {
			t.Errorf("%s version=%q, want %s", path, got, Version)
		}
		actual, err := selfupdate.Sha256File(path)
		if err != nil {
			t.Fatal(err)
		}
		sum, err := os.ReadFile(path + ".sha256")
		fields := strings.Fields(string(sum))
		if err != nil || len(fields) != 2 || fields[0] != actual || fields[1] != filepath.Base(path) {
			t.Errorf("stale checksum for %s: %v", path, err)
		}
	}
}
