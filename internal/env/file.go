package env

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ResolvePath resolves symlinks to get the canonical file path, even if the final target does not exist yet.
func ResolvePath(path string) (string, error) {
	curr := path
	for hops := 0; hops < 40; hops++ {
		fi, err := os.Lstat(curr)
		if err != nil {
			if os.IsNotExist(err) {
				return curr, nil
			}
			return "", err
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return curr, nil
		}
		link, err := os.Readlink(curr)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(link) {
			curr = link
		} else {
			curr = filepath.Join(filepath.Dir(curr), link)
		}
	}
	return "", fmt.Errorf("too many levels of symbolic links: %s", path)
}

// AtomicWrite safely and atomically writes data to destPath.
// If destPath exists, its existing permissions are preserved.
// If destPath is newly created, it is given 0600 permissions.
// If destPath is a symlink, the target of the symlink is replaced so the symlink remains intact.
func AtomicWrite(destPath string, data []byte) error {
	realDest, err := ResolvePath(destPath)
	if err != nil {
		return fmt.Errorf("resolving path %s: %w", destPath, err)
	}

	dir := filepath.Dir(realDest)
	base := filepath.Base(realDest)

	mode := os.FileMode(0600)
	if fi, err := os.Stat(realDest); err == nil {
		mode = fi.Mode().Perm()
	}

	tmpFile, err := os.CreateTemp(dir, "."+base+".tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName) // safe no-op if renamed

	if err := tmpFile.Chmod(mode); err != nil {
		tmpFile.Close()
		return fmt.Errorf("setting permissions on temp file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return fmt.Errorf("writing to temp file: %w", err)
	}

	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("syncing temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpName, realDest); err != nil {
		return fmt.Errorf("atomically replacing %s: %w", realDest, err)
	}

	return nil
}

// IsWorldReadable checks whether the target file has world-read permission on POSIX systems.
func IsWorldReadable(path string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.Mode().Perm()&0004 != 0
}
