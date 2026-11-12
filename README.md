# env-updater

A fast, zero-dependency, cross-platform CLI tool written in Go to synchronize `.env` files from `.env.example` templates. Built to run natively everywhere—Linux, macOS, and Windows—as well as minimal Docker containers and CI/CD pipelines.

*(A legacy pure `/bin/sh` script is also available in `scripts/env-updater.sh`)*.

## Features

- **Structure & Comment Preservation**: Syncs `.env` using `.env.example` as the structural template while keeping all existing values in `.env` byte-for-byte, including **multi-line quoted values** (e.g. PEM private keys, certificates, JSON).
- **Extra Key Handling**: Preserves extra keys present only in `.env` (along with any comments directly preceding them) at the bottom of the file by default, or removes them when `--prune` is passed.
- **Safe Writes**: Atomic replacement (temp file + rename), symlinks followed, existing file permissions preserved, new `.env` and backups created with mode `0600`, CRLF line endings preserved.
- **Warnings, Never Values**: Duplicate keys, unterminated quotes, and dropped unattached lines in `.env` are reported with line numbers and key names only—never exposing secret values.
- **Common dotenv forms**: Accepts identifier keys (including lowercase, dots and dashes), `KEY=value`, empty `KEY`, optional `export`, whitespace around assignments, comments, single/double/backtick-quoted values and multiline quoted values. LF, CRLF and CR line endings and UTF-8 BOMs are recognized. Unrecognized target lines are warned about and preserved in an unparsed section instead of being discarded.
- **Safe Dry-Run (`-n, --dry-run`)**: Shows **only** the key names that would be added or removed—never revealing secret values or emitting raw diffs.
- **Automatic Timestamped Backups**: Creates `.env.bak.YYYYMMDD-HHMMSS` backups only when `.env` actually changes (skip with `--no-backup`). `restore` backs up the current file first, so a restore can be easily undone.
- **Interactive Mode (`-i, --interactive`)**: Optionally prompts for newly added keys or keys with empty values. Input is automatically masked for password/secret/token/key fields; values are safely quoted so `$` and `` ` `` are never accidentally expanded.
- **CI / Git Hook Validation (`check` / `status`)**: Exits non-zero if `.env` is missing keys from `.env.example`.
- **Backup Management (`restore`, `clean-backups`)**: Revert `.env` to the latest backup or prune old backup files (default: retains 5 newest backups; `--all` to remove all).
- **Self-Management (`install`, `update`, `uninstall`)**: Install into `PATH` and self-update from GitHub with **SHA-256 verification**, downgrade protection, and atomic replacement.
- **True Cross-Platform**: Native executable for Linux, macOS, and Windows.

---

## Installation

### Interactive installer (Linux/macOS)

Copy and run this command. It offers a SHA-256-verified prebuilt binary or a build from source (requires Go):

```sh
curl -fsSL https://raw.githubusercontent.com/AhmadShamli/env-updater/main/install.sh | sh
```

Choose a method directly for non-interactive use. Add `--dir "$HOME/.local/bin"` to set the destination:

```sh
curl -fsSL https://raw.githubusercontent.com/AhmadShamli/env-updater/main/install.sh | sh -s -- --binary --dir "$HOME/.local/bin" --yes
```

The installer targets the current user's `~/.local/bin` by default (`/usr/local/bin` when run as root). The prebuilt option requires a published GitHub release asset for Linux/macOS and the machine's architecture. The source option builds the latest Go module version. Windows users can use `go install` below or download the matching `.exe` from GitHub Releases.

### Via `go install`

```sh
go install github.com/AhmadShamli/env-updater@latest
```

### Build from Clone

```sh
git clone https://github.com/AhmadShamli/env-updater.git
cd env-updater
go build -o env-updater .
./env-updater install
```

By default, `install` places `env-updater` into `/usr/local/bin` (if writable or root) or `~/.local/bin` (use `--dir <path>` to customize).

---

## Usage

```text
env-updater [COMMAND] [OPTIONS]
```

### Commands

| Command | Description |
| :--- | :--- |
| `sync` *(default)* | Synchronize target `.env` with source `.env.example` |
| `check`, `status` | Check if `.env` is missing keys from `.env.example` (exits non-zero if out of sync) |
| `restore [FILE]` | Restore `.env` from the latest `.env.bak.*` backup (or a specific backup file) |
| `clean-backups` | Remove old `.env.bak.*` backup files, keeping the newest 5 (`--keep N` / `--all`) |
| `install` | Install `env-updater` into `PATH` (`/usr/local/bin` or `~/.local/bin`) |
| `update`, `self-update` | Fetch, verify (SHA-256) and update `env-updater` to the latest version from GitHub |
| `uninstall` | Remove the installed `env-updater` binary |

### Options

| Option | Description |
| :--- | :--- |
| `-s, --source <file>` | Source template file (default: `.env.example`) |
| `-t, --target <file>` | Target env file (default: `.env`) |
| `-n, --dry-run` | Preview changes without modifying files (shows only added/removed key names) |
| `-i, --interactive` | Prompt interactively for new keys or existing keys with empty values |
| `-p, --prune` | Remove keys in `.env` that do not exist in `.env.example` |
| `--no-backup` | Skip creating a timestamped backup before modifying `.env` |
| `-k, --keep <N>` | Number of newest backups to retain when running `clean-backups` (default: `5`) |
| `--all` | `clean-backups`: remove every backup |
| `-d, --dir <path>` | Target directory for `install` / `uninstall` |
| `-f, --force` | `update`: allow downgrade / overwriting a modified copy or git checkout; `uninstall`: skip identity check |
| `--skip-verify` | `update`/`install`: skip SHA-256 verification (unsafe) |
| `-q, --quiet` | Suppress the header and non-error output (failed `check` details are still shown) |
| `--no-header` | Suppress the header banner (printed to stderr) |
| `--no-color` | Disable ANSI color output |
| `-v, --version` | Display version information |
| `-h, --help` | Display help message |

---

## Examples

```sh
# Sync .env with .env.example (creates a timestamped backup if .env changes)
env-updater

# Preview which keys would be added or pruned without modifying .env or revealing values
env-updater --dry-run --prune

# Interactive sync: prompt for new or empty keys
env-updater sync -i

# Sync custom source and target files without creating a backup
env-updater sync -s .env.production.example -t .env.production --no-backup

# Validate that .env has all required keys in CI or git hooks
env-updater check

# Restore .env from the most recent backup
env-updater restore

# Clean up old backups, keeping only the 3 most recent (or --all)
env-updater clean-backups --keep 3

# Self-update env-updater from GitHub
env-updater update
```

---

## Running Tests

Run the Go unit tests:
```sh
go test -v ./...
```

Run the end-to-end integration test suite (covering all commands, flags, signals, and edge cases):
```sh
sh tests/test.sh
```

## Releasing

`update` selects `env-updater-<os>-<arch>[.exe]` from the latest GitHub release, verifies its adjacent `.sha256` file, and checks its module identity and OS/architecture without executing it. The `VERSION` file is the single source of truth:

```sh
scripts/release.sh 1.1.0
```

This stamps `main.go`, rebuilds the local binary and checksum, and produces static Linux, macOS, and Windows AMD64/ARM64 executables with per-artifact checksums in `dist/`. Attach every file in `dist/` to the GitHub release for that version so self-update can find the matching executable. Run the script (with or without a version) before publishing; the test suite fails if the local checksum is stale.

## License

[GNU Affero General Public License v3.0 (AGPL-3.0)](LICENSE)
