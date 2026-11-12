package backup

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentBackups(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".env")
	data := []byte("KEY=preserved\n")
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan string, 32)
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			<-start
			path, err := CreateBackup(target)
			if err != nil {
				t.Error(err)
			}
			results <- path
		})
	}
	close(start)
	wg.Wait()
	close(results)
	seen := make(map[string]bool)
	for path := range results {
		if seen[path] {
			t.Errorf("backup path reused: %s", path)
		}
		seen[path] = true
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(data) {
			t.Errorf("backup corrupted: %q %v", got, err)
		}
	}
	backups, err := FindBackups(target)
	if err != nil || len(backups) != 32 {
		t.Errorf("want 32 backups, got %d: %v", len(backups), err)
	}
}

func TestCreateAndFindBackups(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".env")

	os.WriteFile(target, []byte("V=1\n"), 0600)

	b1, err := CreateBackup(target)
	if err != nil {
		t.Fatalf("CreateBackup 1: %v", err)
	}

	b2, err := CreateBackup(target)
	if err != nil {
		t.Fatalf("CreateBackup 2: %v", err)
	}

	if b1 == b2 {
		t.Errorf("expected different backup filenames, got %s and %s", b1, b2)
	}

	// Verify permissions
	fi, err := os.Stat(b1)
	if err != nil {
		t.Fatalf("stat b1: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("expected 0600, got %o", fi.Mode().Perm())
	}

	// Unrelated file with nested .bak. should not match .env's backups
	os.WriteFile(filepath.Join(dir, ".env.bak.other.bak.20260101-000001"), []byte("other"), 0600)

	backups, err := FindBackups(target)
	if err != nil {
		t.Fatalf("FindBackups: %v", err)
	}
	if len(backups) != 2 {
		t.Fatalf("expected 2 backups, got %d (%v)", len(backups), backups)
	}
	if backups[0] != b1 || backups[1] != b2 {
		t.Errorf("unexpected backup order: %v", backups)
	}
}

func TestRestore(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".env")

	os.WriteFile(target, []byte("ORIGINAL\n"), 0600)
	CreateBackup(target)

	os.WriteFile(target, []byte("MODIFIED\n"), 0600)

	// Restore with createBackup=true
	restoredPath, createdBak, alreadyMatches, err := Restore(target, "", true, false)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if alreadyMatches {
		t.Errorf("expected not to match")
	}
	if createdBak == "" {
		t.Errorf("expected pre-restore backup to be created")
	}

	content, _ := os.ReadFile(target)
	if string(content) != "ORIGINAL\n" {
		t.Errorf("expected ORIGINAL, got %s", string(content))
	}

	// Verify that a backup was created of MODIFIED before restoring
	backups, _ := FindBackups(target)
	if len(backups) != 2 {
		t.Errorf("expected 2 backups after restore (1 orig, 1 before restore), got %d", len(backups))
	}

	// Second restore with createBackup=false (--no-backup) should revert back to MODIFIED without adding a 3rd backup
	_, createdBak2, _, err := Restore(target, "", false, false)
	if err != nil {
		t.Fatalf("Restore 2: %v", err)
	}
	if createdBak2 != "" {
		t.Errorf("expected no backup when createBackup=false, got %s", createdBak2)
	}
	content2, _ := os.ReadFile(target)
	if string(content2) != "MODIFIED\n" {
		t.Errorf("expected second restore to undo to MODIFIED, got %s", string(content2))
	}
	_ = restoredPath
}

func TestCleanBackups(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".env")

	b1 := filepath.Join(dir, ".env.bak.20260101-000001")
	b2 := filepath.Join(dir, ".env.bak.20260101-000002")
	b3 := filepath.Join(dir, ".env.bak.20260101-000003")
	os.WriteFile(b1, []byte("1"), 0600)
	os.WriteFile(b2, []byte("2"), 0600)
	os.WriteFile(b3, []byte("3"), 0600)

	// Keep 1
	deleted, err := CleanBackups(target, 1, false)
	if err != nil {
		t.Fatalf("CleanBackups: %v", err)
	}
	if len(deleted) != 2 {
		t.Fatalf("expected 2 deleted, got %d", len(deleted))
	}

	remaining, _ := FindBackups(target)
	if len(remaining) != 1 || remaining[0] != b3 {
		t.Errorf("expected only b3 remaining, got %v", remaining)
	}

	// Keep 0
	deletedAll, err := CleanBackups(target, 0, false)
	if err != nil {
		t.Fatalf("CleanBackups all: %v", err)
	}
	if len(deletedAll) != 1 || deletedAll[0] != b3 {
		t.Errorf("expected b3 deleted, got %v", deletedAll)
	}

	remainingAll, _ := FindBackups(target)
	if len(remainingAll) != 0 {
		t.Errorf("expected 0 remaining, got %v", remainingAll)
	}
}
