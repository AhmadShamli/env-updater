package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/AhmadShamli/env-updater/internal/backup"
	"github.com/AhmadShamli/env-updater/internal/env"
	"github.com/AhmadShamli/env-updater/internal/selfupdate"
	"github.com/AhmadShamli/env-updater/internal/ui"
)

var (
	Version        = "1.0.0"
	VersionMarker  = ""
	IdentityMarker = "@env-updater-marker@"
	LicenseName    = "GNU Affero General Public License v3.0 (AGPL-3.0)"
	RepoURL        = "https://github.com/AhmadShamli/env-updater"
)

type Config struct {
	Command     string
	SourceFile  string
	TargetFile  string
	RestoreFile string
	InstallDir  string
	KeepBackups int
	DryRun      bool
	Interactive bool
	Prune       bool
	Backup      bool
	Quiet       bool
	NoHeader    bool
	NoColor     bool
	Force       bool
	SkipVerify  bool
}

var errUnknownOption = errors.New("unknown option")

func main() {
	if (VersionMarker != "" && strings.TrimSuffix(strings.TrimPrefix(VersionMarker, "@env-updater-version:"), "@") != Version) || IdentityMarker != selfupdate.Marker {
		fmt.Fprintln(os.Stderr, "Error: inconsistent build version metadata")
		os.Exit(1)
	}

	cfg, parseErr := parseArgs(os.Args[1:])
	appUI := ui.NewUI(cfg.Quiet, cfg.NoHeader, cfg.NoColor)

	// Signal handling for graceful exit (restoring terminal state if interrupted during prompt)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		appUI.RestoreTerminal()
		if sig == os.Interrupt {
			os.Exit(130)
		}
		os.Exit(143)
	}()

	if parseErr != nil {
		appUI.ShowHeader(Version, LicenseName, RepoURL, "error", cfg.SourceFile, cfg.TargetFile, cfg.DryRun, cfg.Interactive, cfg.Prune)
		appUI.LogError("%v", parseErr)
		if errors.Is(parseErr, errUnknownOption) {
			printUsage(appUI, os.Stderr)
		}
		os.Exit(1)
	}

	if cfg.Command == "version" {
		appUI.ShowHeader(Version, LicenseName, RepoURL, cfg.Command, cfg.SourceFile, cfg.TargetFile, cfg.DryRun, cfg.Interactive, cfg.Prune)
		fmt.Printf("env-updater v%s\n", Version)
		return
	}

	if cfg.Command == "help" {
		appUI.ShowHeader(Version, LicenseName, RepoURL, cfg.Command, cfg.SourceFile, cfg.TargetFile, cfg.DryRun, cfg.Interactive, cfg.Prune)
		printUsage(appUI, os.Stdout)
		return
	}

	appUI.ShowHeader(Version, LicenseName, RepoURL, cfg.Command, cfg.SourceFile, cfg.TargetFile, cfg.DryRun, cfg.Interactive, cfg.Prune)

	var err error
	switch cfg.Command {
	case "sync":
		err = runSync(cfg, appUI)
	case "check", "status":
		err = runCheck(cfg, appUI)
	case "restore":
		err = runRestore(cfg, appUI)
	case "clean-backups":
		err = runCleanBackups(cfg, appUI)
	case "install":
		err = runInstall(cfg, appUI)
	case "update", "self-update":
		err = runUpdate(cfg, appUI)
	case "uninstall":
		err = runUninstall(cfg, appUI)
	default:
		appUI.LogError("Unknown command: %s", cfg.Command)
		printUsage(appUI, os.Stderr)
		os.Exit(1)
	}

	if err != nil {
		appUI.LogError("%v", err)
		os.Exit(1)
	}
}

func printUsage(u *ui.UI, out *os.File) {
	fmt.Fprintf(out, `%s
    env-updater [COMMAND] [OPTIONS]

%s
    sync            Synchronize target .env with source .env.example (default)
    check, status   Check if .env is missing keys from .env.example (CI-friendly)
    restore [FILE]  Restore .env from the latest backup (or specified backup file)
    clean-backups   Remove old .env.bak.* files (keeps newest 5; see --keep/--all)
    install         Install env-updater into PATH (/usr/local/bin or ~/.local/bin)
    update          Update env-updater to the latest verified version from GitHub
    uninstall       Remove installed env-updater binary from PATH

%s
    -s, --source <file>   Source template file (default: .env.example)
    -t, --target <file>   Target env file (default: .env)
    -n, --dry-run         Preview changes (shows only key names, never values)
    -i, --interactive     Prompt for new or empty keys during sync
    -p, --prune           Remove keys in .env that are not present in .env.example
        --no-backup       Do not create a timestamped backup before modifying .env
    -k, --keep <N>        Backups to keep on clean-backups (default: 5)
        --all             clean-backups: remove all backups
    -d, --dir <path>      Target directory for install / uninstall
    -f, --force           update: allow downgrade / git work tree; uninstall: skip identity check
        --skip-verify     update/install: skip SHA-256 checksum verification (unsafe)
    -q, --quiet           Suppress header and non-error output
        --no-header       Suppress the header banner
        --no-color        Disable colored output
    -v, --version         Print version and exit
    -h, --help            Print this help message and exit

%s
    env-updater                          # Sync .env from .env.example with backup
    env-updater --dry-run                # Show keys that would be added/removed
    env-updater sync -i                  # Sync and interactively prompt for new/empty keys
    env-updater sync --prune             # Sync and remove extra keys not in .env.example
    env-updater check                    # Exit non-zero if .env is missing keys
    env-updater restore                  # Revert .env to the latest backup
    env-updater clean-backups --keep 3   # Keep only the 3 most recent backups
    env-updater install                  # Install env-updater to PATH
    env-updater update                   # Self-update from GitHub
`, u.Bold("USAGE:"), u.Bold("COMMANDS:"), u.Bold("OPTIONS:"), u.Bold("EXAMPLES:"))
}

func parseArgs(args []string) (Config, error) {
	cfg := Config{
		Command:     "",
		SourceFile:  ".env.example",
		TargetFile:  ".env",
		InstallDir:  os.Getenv("INSTALL_DIR"),
		KeepBackups: 5,
		Backup:      true,
	}

	var firstErr error
	setErr := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "sync", "check", "status", "restore", "clean-backups", "install", "update", "self-update", "uninstall":
			if cfg.Command != "" {
				setErr(fmt.Errorf("Unexpected command argument: %s", arg))
				continue
			}
			cfg.Command = arg
			if cfg.Command == "restore" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				cfg.RestoreFile = args[i+1]
				i++
			}
		case "-s", "--source":
			if i+1 >= len(args) {
				setErr(fmt.Errorf("Option %s requires a value.", arg))
				continue
			}
			cfg.SourceFile = args[i+1]
			i++
		case "-t", "--target":
			if i+1 >= len(args) {
				setErr(fmt.Errorf("Option %s requires a value.", arg))
				continue
			}
			cfg.TargetFile = args[i+1]
			i++
		case "-k", "--keep":
			if i+1 >= len(args) {
				setErr(fmt.Errorf("Option %s requires a value.", arg))
				continue
			}
			val, err := strconv.Atoi(args[i+1])
			if err != nil || val < 0 {
				setErr(fmt.Errorf("--keep requires a non-negative integer (got: %s)", args[i+1]))
				i++
				continue
			}
			cfg.KeepBackups = val
			i++
		case "-d", "--dir", "--prefix":
			if i+1 >= len(args) {
				setErr(fmt.Errorf("Option %s requires a value.", arg))
				continue
			}
			cfg.InstallDir = args[i+1]
			i++
		case "-n", "--dry-run":
			cfg.DryRun = true
		case "-i", "--interactive":
			cfg.Interactive = true
		case "-p", "--prune":
			cfg.Prune = true
		case "--no-backup":
			cfg.Backup = false
		case "--all":
			cfg.KeepBackups = 0
		case "-f", "--force":
			cfg.Force = true
		case "--skip-verify":
			cfg.SkipVerify = true
		case "-q", "--quiet":
			cfg.Quiet = true
		case "--no-header":
			cfg.NoHeader = true
		case "--no-color":
			cfg.NoColor = true
		case "-v", "--version":
			cfg.Command = "version"
		case "-h", "--help":
			cfg.Command = "help"
		default:
			setErr(fmt.Errorf("%w: Unknown option or command: %s", errUnknownOption, arg))
		}
	}

	if cfg.Command == "" {
		cfg.Command = "sync"
	}
	return cfg, firstErr
}

func runSync(cfg Config, u *ui.UI) error {
	src, srcWarn, err := env.ParseFile(cfg.SourceFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source file not found: %s", cfg.SourceFile)
		}
		return fmt.Errorf("reading source file %s: %w", cfg.SourceFile, err)
	}

	var tgt *env.EnvFile
	var tgtWarn []string
	tgtExists := false

	if fi, err := os.Stat(cfg.TargetFile); err == nil && !fi.IsDir() {
		tgtExists = true
		t, tw, err := env.ParseFile(cfg.TargetFile)
		if err != nil {
			return fmt.Errorf("reading target file %s: %w", cfg.TargetFile, err)
		}
		tgt = t
		tgtWarn = tw
	}

	analysis := env.Analyze(src, tgt, cfg.Prune)
	for _, w := range srcWarn {
		u.LogWarn("%s", w)
	}
	for _, w := range tgtWarn {
		u.LogWarn("%s", w)
	}
	for _, w := range analysis.Warnings {
		u.LogWarn("%s", w)
	}

	addedCount := len(analysis.AddedKeys)
	extraCount := len(analysis.ExtraKeys)
	removedCount := 0
	if cfg.Prune {
		removedCount = extraCount
	}

	if cfg.DryRun {
		u.LogInfo("%s", u.Bold(fmt.Sprintf("Dry run (%s -> %s):", cfg.SourceFile, cfg.TargetFile)))
		if !tgtExists {
			u.LogInfo("%s does not exist and would be created.", cfg.TargetFile)
		}
		if addedCount == 0 && removedCount == 0 {
			u.LogInfo("No keys would be added or removed.")
			return nil
		}
		if addedCount > 0 {
			u.LogInfo("Keys to be added (%d):", addedCount)
			for _, k := range analysis.AddedKeys {
				u.LogInfo("  + %s", k)
			}
		}
		if removedCount > 0 {
			u.LogInfo("Keys to be removed (%d):", removedCount)
			for _, k := range analysis.ExtraKeys {
				u.LogInfo("  - %s", k)
			}
		}
		return nil
	}

	overrides := make(map[string]env.Override)
	if cfg.Interactive && len(analysis.PromptKeys) > 0 {
		for _, pk := range analysis.PromptKeys {
			userVal, eof, err := u.Prompt(pk.Key, pk.DefaultValue, os.Stdin)
			if err != nil {
				return err
			}
			if eof && userVal == "" {
				break
			}
			if userVal != "" {
				overrides[pk.Key] = env.Override{
					Type:  env.OverrideCustomValue,
					Value: userVal,
				}
			} else if pk.DefaultValue != "" {
				overrides[pk.Key] = env.Override{
					Type: env.OverrideVerbatimTemplate,
				}
			}
		}
	}

	mergedData := env.Merge(src, tgt, cfg.Prune, overrides)

	if tgtExists {
		currentData, err := os.ReadFile(cfg.TargetFile)
		if err == nil && bytes.Equal(currentData, mergedData) {
			u.LogSuccess("%s is already up to date.", cfg.TargetFile)
			if env.IsWorldReadable(cfg.TargetFile) {
				u.LogWarn("%s is world-readable; consider: chmod 600 %s", cfg.TargetFile, cfg.TargetFile)
			}
			return nil
		}

		if cfg.Backup {
			bakPath, err := backup.CreateBackup(cfg.TargetFile)
			if err != nil {
				return err
			}
			u.LogInfo("Backup created: %s", bakPath)
		}
	}

	if err := env.AtomicWrite(cfg.TargetFile, mergedData); err != nil {
		return err
	}

	if !tgtExists {
		u.LogSuccess("Created %s from %s (%d keys).", cfg.TargetFile, cfg.SourceFile, addedCount)
	} else {
		u.LogSuccess("Updated %s (added: %d, removed: %d).", cfg.TargetFile, addedCount, removedCount)
		if env.IsWorldReadable(cfg.TargetFile) {
			u.LogWarn("%s is world-readable; consider: chmod 600 %s", cfg.TargetFile, cfg.TargetFile)
		}
	}
	return nil
}

func runCheck(cfg Config, u *ui.UI) error {
	src, srcWarn, err := env.ParseFile(cfg.SourceFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source file not found: %s", cfg.SourceFile)
		}
		return fmt.Errorf("reading source file %s: %w", cfg.SourceFile, err)
	}

	tgt, tgtWarn, err := env.ParseFile(cfg.TargetFile)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("target file not found: %s", cfg.TargetFile)
		}
		return fmt.Errorf("reading target file %s: %w", cfg.TargetFile, err)
	}

	for _, w := range srcWarn {
		u.LogWarn("%s", w)
	}
	for _, w := range tgtWarn {
		u.LogWarn("%s", w)
	}

	analysis := env.Analyze(src, tgt, cfg.Prune)
	for _, w := range analysis.Warnings {
		u.LogWarn("%s", w)
	}

	addedCount := len(analysis.AddedKeys)
	extraCount := len(analysis.ExtraKeys)

	if addedCount > 0 {
		u.LogFail("%s is missing %d key(s) from %s:", cfg.TargetFile, addedCount, cfg.SourceFile)
		for _, k := range analysis.AddedKeys {
			u.LogFail("  - %s", k)
		}
		os.Exit(1)
	}

	if cfg.Prune && extraCount > 0 {
		u.LogFail("%s has %d extra key(s) not in %s:", cfg.TargetFile, extraCount, cfg.SourceFile)
		for _, k := range analysis.ExtraKeys {
			u.LogFail("  + %s", k)
		}
		os.Exit(1)
	}

	u.LogSuccess("%s is in sync with %s.", cfg.TargetFile, cfg.SourceFile)
	if extraCount > 0 {
		u.LogInfo("Extra keys in %s (%d):", cfg.TargetFile, extraCount)
		for _, k := range analysis.ExtraKeys {
			u.LogInfo("  + %s", k)
		}
	}
	return nil
}

func runRestore(cfg Config, u *ui.UI) error {
	restoredPath, createdBak, alreadyMatches, err := backup.Restore(cfg.TargetFile, cfg.RestoreFile, cfg.Backup, cfg.DryRun)
	if err != nil {
		return err
	}

	if cfg.DryRun {
		u.LogInfo("Dry run: would restore %s from %s", cfg.TargetFile, restoredPath)
		return nil
	}

	if alreadyMatches {
		u.LogSuccess("%s already matches %s.", cfg.TargetFile, restoredPath)
		return nil
	}

	if createdBak != "" {
		u.LogInfo("Backup created: %s", createdBak)
	}
	u.LogSuccess("Restored %s from %s", cfg.TargetFile, restoredPath)
	return nil
}

func runCleanBackups(cfg Config, u *ui.UI) error {
	backups, err := backup.FindBackups(cfg.TargetFile)
	if err != nil {
		return err
	}
	if len(backups) == 0 {
		u.LogInfo("No backup files found for %s.", cfg.TargetFile)
		return nil
	}

	deleted, err := backup.CleanBackups(cfg.TargetFile, cfg.KeepBackups, cfg.DryRun)
	if err != nil {
		return err
	}

	if len(deleted) == 0 {
		u.LogInfo("Nothing to clean (%d backup(s) found, keeping %d).", len(backups), cfg.KeepBackups)
		return nil
	}

	if cfg.DryRun {
		u.LogInfo("Dry run: would remove %d backup file(s):", len(deleted))
		for _, b := range deleted {
			u.LogInfo("  - %s", b)
		}
		return nil
	}

	for _, b := range deleted {
		u.LogInfo("Removed backup: %s", b)
	}
	u.LogSuccess("Cleaned %d backup file(s) (kept %d).", len(deleted), cfg.KeepBackups)
	return nil
}

func runInstall(cfg Config, u *ui.UI) error {
	targetDir := selfupdate.ResolveInstallDir(cfg.InstallDir)
	destBin := filepath.Join(targetDir, selfupdate.BinaryName())

	if cfg.DryRun {
		u.LogInfo("Dry run: would install env-updater to %s", destBin)
		return nil
	}

	selfExe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating current executable: %w", err)
	}

	if err := selfupdate.InstallBinary(selfExe, destBin, false); err != nil {
		return err
	}

	u.LogSuccess("Installed env-updater to %s", destBin)

	if !selfupdate.IsInPath(targetDir) {
		u.LogWarn("%s is not in your PATH. Add it with:", targetDir)
		u.LogWarn("  export PATH=\"%s:$PATH\"", targetDir)
	}
	return nil
}

func runUpdate(cfg Config, u *ui.UI) error {
	repoURL := os.Getenv("ENV_UPDATER_REPO_URL")
	if repoURL == "" {
		repoURL = selfupdate.DefaultDownloadURL()
	}
	checksumURL := os.Getenv("ENV_UPDATER_CHECKSUM_URL")
	if checksumURL == "" {
		checksumURL = repoURL + ".sha256"
	}

	tmpDir, err := os.MkdirTemp("", "env-updater-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	dlFile := filepath.Join(tmpDir, selfupdate.BinaryName())
	u.LogInfo("Checking for updates from %s...", repoURL)

	if err := selfupdate.DownloadFile(repoURL, dlFile); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if !cfg.SkipVerify {
		csFile := filepath.Join(tmpDir, "remote.sha256")
		if err := selfupdate.DownloadFile(checksumURL, csFile); err != nil {
			return fmt.Errorf("could not download checksum from %s (use --skip-verify to bypass): %w", checksumURL, err)
		}
		csData, err := os.ReadFile(csFile)
		if err != nil {
			return err
		}
		parts := strings.Fields(string(csData))
		if len(parts) == 0 {
			return fmt.Errorf("invalid checksum file from %s", checksumURL)
		}
		expectedSha := parts[0]
		actualSha, err := selfupdate.Sha256File(dlFile)
		if err != nil {
			return err
		}
		if actualSha != expectedSha {
			return fmt.Errorf("Checksum mismatch for downloaded binary (expected %s, got %s)", expectedSha, actualSha)
		}
	} else {
		u.LogWarn("Skipping checksum verification (--skip-verify).")
	}

	if err := selfupdate.ValidateBinary(dlFile); err != nil {
		return err
	}

	selfPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding running executable: %w", err)
	}
	selfPath, err = env.ResolvePath(selfPath)
	if err != nil {
		return err
	}

	remoteVer := selfupdate.DetectVersion(dlFile)
	if remoteVer == "" {
		return fmt.Errorf("could not determine version of downloaded binary")
	}

	currentData, err := os.ReadFile(selfPath)
	if err == nil {
		newData, err := os.ReadFile(dlFile)
		if err == nil && bytes.Equal(currentData, newData) {
			u.LogSuccess("env-updater is already at the latest version (v%s).", Version)
			return nil
		}
	}

	cmpRes := selfupdate.CompareVersions(remoteVer, Version)
	if cmpRes < 0 && !cfg.Force {
		return fmt.Errorf("remote version v%s is older than local v%s (use --force to downgrade)", remoteVer, Version)
	}
	if cmpRes == 0 && !cfg.Force {
		return fmt.Errorf("local copy differs from remote v%s (locally modified?); use --force to overwrite", remoteVer)
	}

	if selfupdate.InGitWorktree(selfPath) && !cfg.Force {
		return fmt.Errorf("%s is inside a git work tree; use 'git pull' instead (or --force)", selfPath)
	}

	if cfg.DryRun {
		u.LogInfo("Dry run: would update %s from v%s to v%s", selfPath, Version, remoteVer)
		return nil
	}

	if err := selfupdate.InstallBinary(dlFile, selfPath, false); err != nil {
		return err
	}

	u.LogSuccess("Updated env-updater from v%s to v%s at %s", Version, remoteVer, selfPath)
	return nil
}

func runUninstall(cfg Config, u *ui.UI) error {
	binName := selfupdate.BinaryName()
	var targetBin string
	if cfg.InstallDir != "" {
		targetBin = filepath.Join(cfg.InstallDir, binName)
	} else {
		usrLocal := filepath.Join("/usr/local/bin", binName)
		homeLocal := filepath.Join(os.Getenv("HOME"), ".local", "bin", binName)
		if fi, err := os.Stat(usrLocal); err == nil && !fi.IsDir() {
			targetBin = usrLocal
		} else if fi, err := os.Stat(homeLocal); err == nil && !fi.IsDir() {
			targetBin = homeLocal
		} else if pathBin, err := exec.LookPath(binName); err == nil {
			targetBin = pathBin
		}
	}

	if targetBin == "" {
		return fmt.Errorf("env-updater is not installed in PATH or specified directory")
	}

	targetBin, err := env.ResolvePath(targetBin)
	if err != nil {
		return err
	}

	if _, err := os.Stat(targetBin); os.IsNotExist(err) {
		return fmt.Errorf("binary not found at %s", targetBin)
	}

	if !cfg.Force && !selfupdate.IsEnvUpdater(targetBin) {
		return fmt.Errorf("%s does not look like env-updater; refusing to remove (use --force)", targetBin)
	}

	if cfg.DryRun {
		u.LogInfo("Dry run: would remove %s", targetBin)
		return nil
	}

	if err := os.Remove(targetBin); err != nil {
		return fmt.Errorf("removing %s: %w", targetBin, err)
	}

	u.LogSuccess("Uninstalled env-updater from %s", targetBin)
	return nil
}
