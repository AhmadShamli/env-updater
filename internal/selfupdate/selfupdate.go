package selfupdate

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/AhmadShamli/env-updater/internal/env"
	"golang.org/x/mod/semver"
)

const (
	ModulePath = "github.com/AhmadShamli/env-updater"
	Marker     = "@env-updater-marker@"
)

var (
	markerVersionRegex = regexp.MustCompile(`@env-updater-version:([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+\-]*)@`)
	ldflagsVerRegex    = regexp.MustCompile(`-X\s+main\.Version=([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+\-]*)`)
	textVersionRegex   = regexp.MustCompile(`env-updater v([0-9]+\.[0-9]+\.[0-9]+[0-9A-Za-z.+\-]*)`)
	scriptVersionRegex = regexp.MustCompile(`(?m)^VERSION="([0-9]+\.[0-9]+\.[0-9]+[^"]*)"`)
)

// BinaryName returns the platform-appropriate executable filename.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "env-updater.exe"
	}
	return "env-updater"
}

func ArtifactName(goos, goarch string) string {
	name := "env-updater-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func DefaultDownloadURL() string {
	return "https://github.com/AhmadShamli/env-updater/releases/latest/download/" + ArtifactName(runtime.GOOS, runtime.GOARCH)
}

func ValidateBinary(path string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("downloaded file is not a native Go executable: %w", err)
	}
	if info.Path != ModulePath && info.Main.Path != ModulePath {
		return fmt.Errorf("downloaded file is not an env-updater executable")
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH {
		return fmt.Errorf("downloaded executable targets %s/%s; need %s/%s", settings["GOOS"], settings["GOARCH"], runtime.GOOS, runtime.GOARCH)
	}
	return nil
}

func normalizeSemver(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if v == "" {
		return ""
	}
	parts := strings.SplitN(v, "-", 2)
	nums := strings.Split(parts[0], ".")
	for len(nums) < 3 {
		nums = append(nums, "0")
	}
	norm := "v" + strings.Join(nums, ".")
	if len(parts) == 2 {
		norm += "-" + parts[1]
	}
	return norm
}

// CompareVersions compares two version strings using golang.org/x/mod/semver.
// Returns 1 if a > b, 0 if a == b, -1 if a < b.
func CompareVersions(a, b string) int {
	na := normalizeSemver(a)
	nb := normalizeSemver(b)
	if semver.IsValid(na) && semver.IsValid(nb) {
		return semver.Compare(na, nb)
	}

	partsA := strings.Split(strings.TrimPrefix(a, "v"), ".")
	partsB := strings.Split(strings.TrimPrefix(b, "v"), ".")
	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}
	for i := 0; i < maxLen; i++ {
		var numA, numB int
		if i < len(partsA) {
			numA, _ = strconv.Atoi(partsA[i])
		}
		if i < len(partsB) {
			numB, _ = strconv.Atoi(partsB[i])
		}
		if numA > numB {
			return 1
		}
		if numA < numB {
			return -1
		}
	}
	return 0
}

// InGitWorktree checks if the given file path is located inside a git repository.
func InGitWorktree(filePath string) bool {
	dir, err := filepath.Abs(filepath.Dir(filePath))
	if err != nil {
		return false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir || parent == "" {
			break
		}
		dir = parent
	}
	return false
}

// ResolveInstallDir determines the default install directory.
func ResolveInstallDir(customDir string) string {
	if customDir != "" {
		return customDir
	}

	if runtime.GOOS == "windows" {
		if localApp := os.Getenv("LocalAppData"); localApp != "" {
			return filepath.Join(localApp, "Programs", "env-updater")
		}
		return filepath.Join(os.Getenv("USERPROFILE"), "bin")
	}

	usrLocal := "/usr/local/bin"
	if fi, err := os.Stat(usrLocal); err == nil && fi.IsDir() {
		if f, err := os.CreateTemp(usrLocal, ".env-updater-write-test-*"); err == nil {
			f.Close()
			os.Remove(f.Name())
			return usrLocal
		}
	}

	if os.Geteuid() == 0 {
		return usrLocal
	}

	home := os.Getenv("HOME")
	if home == "" {
		home = "/tmp"
	}
	return filepath.Join(home, ".local", "bin")
}

// IsInPath checks whether dir is present in the current PATH environment variable.
func IsInPath(dir string) bool {
	cleanDir := filepath.Clean(dir)
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.Clean(p) == cleanDir {
			return true
		}
	}
	return false
}

// DownloadFile downloads a URL or copies a file:// path into dest with mode 0755.
func DownloadFile(url, dest string) error {
	if strings.HasPrefix(url, "file://") {
		srcPath := strings.TrimPrefix(url, "file://")
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0755)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return out.Chmod(0755)
}

func Sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// InstallBinary atomically installs srcPath to destBin with mode 0755.
// Handles Windows running-executable locks by moving the old binary to .old first if needed.
func InstallBinary(srcPath, destBin string, dryRun bool) error {
	destBin, err := env.ResolvePath(destBin)
	if err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("reading source binary %s: %w", srcPath, err)
	}

	dir := filepath.Dir(destBin)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dir, err)
	}

	tmpFile, err := os.CreateTemp(dir, ".env-updater.tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	if err := tmpFile.Chmod(0755); err != nil {
		tmpFile.Close()
		return err
	}
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpName, destBin); err != nil {
		if runtime.GOOS == "windows" {
			oldName := destBin + ".old"
			_ = os.Remove(oldName)
			if rErr := os.Rename(destBin, oldName); rErr == nil {
				if err2 := os.Rename(tmpName, destBin); err2 == nil {
					_ = os.Remove(oldName)
					return nil
				}
			}
		}
		return err
	}
	return nil
}

// IsEnvUpdater inspects targetPath without executing it to verify it is an env-updater binary or script.
func IsEnvUpdater(targetPath string) bool {
	if info, err := buildinfo.ReadFile(targetPath); err == nil {
		if info.Path == ModulePath || info.Main.Path == ModulePath {
			return true
		}
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return false
	}
	if bytes.Contains(data, []byte(Marker)) ||
		bytes.Contains(data, []byte(ModulePath)) ||
		textVersionRegex.Match(data) {
		return true
	}
	return false
}

// DetectVersion extracts the version of an env-updater binary or script without executing it.
func DetectVersion(targetPath string) string {
	if info, err := buildinfo.ReadFile(targetPath); err == nil {
		for _, s := range info.Settings {
			if s.Key == "-ldflags" {
				if m := ldflagsVerRegex.FindStringSubmatch(s.Value); len(m) == 2 {
					return m[1]
				}
			}
		}
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return ""
	}
	if m := markerVersionRegex.FindSubmatch(data); len(m) == 2 {
		return string(m[1])
	}
	if m := textVersionRegex.FindSubmatch(data); len(m) == 2 {
		return string(m[1])
	}
	if m := scriptVersionRegex.FindSubmatch(data); len(m) == 2 {
		return string(m[1])
	}
	return ""
}
