package backup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/AhmadShamli/env-updater/internal/env"
)

var backupSuffixRegex = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}(-[0-9]+)?$`)

// CreateBackup creates a timestamped backup of targetPath with mode 0600.
// If targetPath does not exist, it does nothing and returns "".
func CreateBackup(targetPath string) (string, error) {
	if _, err := os.Stat(targetPath); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return "", fmt.Errorf("reading %s for backup: %w", targetPath, err)
	}

	timestamp := time.Now().Format("20060102-150405")
	for suffix := 0; ; suffix++ {
		backupPath := fmt.Sprintf("%s.bak.%s", targetPath, timestamp)
		if suffix > 0 {
			backupPath += fmt.Sprintf("-%03d", suffix)
		}
		f, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("creating backup %s: %w", backupPath, err)
		}
		_, writeErr := f.Write(data)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		closeErr := f.Close()
		if writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			_ = os.Remove(backupPath)
			return "", fmt.Errorf("writing backup %s: %w", backupPath, writeErr)
		}
		return backupPath, nil
	}
}

// FindBackups returns all backup files for targetPath, sorted oldest to newest.
func FindBackups(targetPath string) ([]string, error) {
	dir := filepath.Dir(targetPath)
	base := filepath.Base(targetPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	prefix := base + ".bak."
	var backups []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) {
			suffix := name[len(prefix):]
			if backupSuffixRegex.MatchString(suffix) {
				backups = append(backups, filepath.Join(dir, name))
			}
		}
	}

	sort.Strings(backups)
	return backups, nil
}

// Restore restores targetPath from a backup file.
// If backupPath is empty, it uses the most recent backup.
// When createBackup is true, it backs up targetPath first so the restore can be undone.
func Restore(targetPath string, backupPath string, createBackup bool, dryRun bool) (restoredFrom string, createdBackup string, alreadyMatches bool, err error) {
	if backupPath == "" {
		backups, err := FindBackups(targetPath)
		if err != nil {
			return "", "", false, err
		}
		if len(backups) == 0 {
			return "", "", false, fmt.Errorf("no backup files found for %s", targetPath)
		}
		backupPath = backups[len(backups)-1]
	}

	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return "", "", false, fmt.Errorf("reading backup %s: %w", backupPath, err)
	}

	if dryRun {
		return backupPath, "", false, nil
	}

	if currentData, err := os.ReadFile(targetPath); err == nil {
		if bytes.Equal(currentData, backupData) {
			return backupPath, "", true, nil
		}
	}

	if createBackup {
		if _, err := os.Stat(targetPath); err == nil {
			bak, err := CreateBackup(targetPath)
			if err != nil {
				return "", "", false, fmt.Errorf("backing up current %s before restore: %w", targetPath, err)
			}
			createdBackup = bak
		}
	}

	if err := env.AtomicWrite(targetPath, backupData); err != nil {
		return "", createdBackup, false, fmt.Errorf("restoring %s from %s: %w", targetPath, backupPath, err)
	}

	return backupPath, createdBackup, false, nil
}

// CleanBackups deletes older backup files, retaining the newest 'keep' backups.
// If keep is 0 (or --all), all backups are deleted.
func CleanBackups(targetPath string, keep int, dryRun bool) ([]string, error) {
	if keep < 0 {
		return nil, fmt.Errorf("--keep requires a non-negative integer (got: %d)", keep)
	}

	backups, err := FindBackups(targetPath)
	if err != nil {
		return nil, err
	}

	if len(backups) == 0 {
		return nil, nil
	}

	toDeleteCount := len(backups) - keep
	if toDeleteCount <= 0 {
		return nil, nil
	}

	toDelete := backups[:toDeleteCount]

	if !dryRun {
		for _, b := range toDelete {
			if err := os.Remove(b); err != nil && !os.IsNotExist(err) {
				return nil, fmt.Errorf("deleting backup %s: %w", b, err)
			}
		}
	}

	return toDelete, nil
}
