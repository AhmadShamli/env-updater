package env

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteNewFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".env")

	data := []byte("FOO=bar\n")
	if err := AtomicWrite(target, data); err != nil {
		t.Fatalf("AtomicWrite error: %v", err)
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}

	// Mode should be 0600
	if fi.Mode().Perm() != 0600 {
		t.Errorf("expected perm 0600, got %o", fi.Mode().Perm())
	}
}

func TestAtomicWriteExistingFileMode(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".env")

	// Create with 0640
	if err := os.WriteFile(target, []byte("OLD=1\n"), 0640); err != nil {
		t.Fatalf("write file: %v", err)
	}

	data := []byte("NEW=2\n")
	if err := AtomicWrite(target, data); err != nil {
		t.Fatalf("AtomicWrite error: %v", err)
	}

	fi, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}

	if fi.Mode().Perm() != 0640 {
		t.Errorf("expected perm 0640, got %o", fi.Mode().Perm())
	}
}

func TestAtomicWriteSymlink(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.env")
	symlink := filepath.Join(dir, ".env")

	if err := os.WriteFile(realFile, []byte("ORIG=1\n"), 0600); err != nil {
		t.Fatalf("write real file: %v", err)
	}

	if err := os.Symlink("real.env", symlink); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	newData := []byte("UPDATED=2\n")
	if err := AtomicWrite(symlink, newData); err != nil {
		t.Fatalf("AtomicWrite on symlink: %v", err)
	}

	// Verify symlink is still a symlink
	lfi, err := os.Lstat(symlink)
	if err != nil {
		t.Fatalf("lstat symlink: %v", err)
	}
	if lfi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("expected .env to remain a symlink")
	}

	// Verify real.env content was updated
	content, err := os.ReadFile(realFile)
	if err != nil {
		t.Fatalf("read real file: %v", err)
	}
	if string(content) != "UPDATED=2\n" {
		t.Errorf("expected real file content UPDATED=2, got %s", string(content))
	}
}

func TestIsWorldReadable(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "f1")
	f2 := filepath.Join(dir, "f2")

	os.WriteFile(f1, []byte("test"), 0600)
	os.WriteFile(f2, []byte("test"), 0644)

	if IsWorldReadable(f1) {
		t.Errorf("expected f1 (0600) not to be world readable")
	}
	if !IsWorldReadable(f2) {
		t.Errorf("expected f2 (0644) to be world readable")
	}
}
