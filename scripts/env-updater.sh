#!/bin/sh
# ==============================================================================
# env-updater - .env synchronization and management utility
# Targets POSIX sh plus common utilities (awk, sed, cmp, mktemp, readlink).
# Repository: https://github.com/AhmadShamli/env-updater
# License: GNU Affero General Public License v3.0 (AGPL-3.0)
# @env-updater-marker@
# ==============================================================================

set -eu

# Secrets live in the files we create: default every new file to owner-only.
umask 077

VERSION="1.0.0"
LICENSE_NAME="GNU Affero General Public License v3.0 (AGPL-3.0)"
REPO_URL="https://github.com/AhmadShamli/env-updater"
DEFAULT_REPO_RAW_URL="https://raw.githubusercontent.com/AhmadShamli/env-updater/main/scripts/env-updater.sh"
REPO_RAW_URL="${ENV_UPDATER_REPO_URL:-$DEFAULT_REPO_RAW_URL}"
CHECKSUM_URL="${ENV_UPDATER_CHECKSUM_URL:-${REPO_RAW_URL}.sha256}"
MARKER="@env-updater-marker@"
HEADER_WIDTH=60

# Default options
COMMAND=""
SOURCE_FILE=".env.example"
TARGET_FILE=".env"
DRY_RUN=0
INTERACTIVE=0
PRUNE=0
BACKUP=1
KEEP_BACKUPS=5
INSTALL_DIR="${INSTALL_DIR:-}"
RESTORE_FILE=""
QUIET=0
NO_HEADER=0
FORCE=0
SKIP_VERIFY=0
NO_COLOR="${NO_COLOR:-0}"
HEADER_SHOWN=0

# Colors start empty so logging is safe before setup_colors runs (set -u).
C_RESET=""
C_RED=""
C_GREEN=""
C_YELLOW=""
C_BLUE=""
C_BOLD=""

# Runtime state used by cleanup
TMP_DIR=""
PENDING_TMP=""
STTY_SAVED=""

cleanup() {
    if [ -n "$STTY_SAVED" ]; then
        stty "$STTY_SAVED" 2>/dev/null || true
        STTY_SAVED=""
    fi
    if [ -n "$PENDING_TMP" ] && [ -f "$PENDING_TMP" ]; then
        rm -f "$PENDING_TMP"
    fi
    if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
        rm -rf "$TMP_DIR"
    fi
}

# EXIT handles cleanup; INT/TERM must actually terminate (dash resumes otherwise).
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

setup_colors() {
    if [ "$NO_COLOR" = "0" ] && [ -t 2 ]; then
        C_RESET="$(printf '\033[0m')"
        C_RED="$(printf '\033[31m')"
        C_GREEN="$(printf '\033[32m')"
        C_YELLOW="$(printf '\033[33m')"
        C_BLUE="$(printf '\033[34m')"
        C_BOLD="$(printf '\033[1m')"
    else
        C_RESET=""
        C_RED=""
        C_GREEN=""
        C_YELLOW=""
        C_BLUE=""
        C_BOLD=""
    fi
}

log_info() {
    if [ "$QUIET" -eq 0 ]; then
        printf "%s\n" "$*"
    fi
}

log_success() {
    if [ "$QUIET" -eq 0 ]; then
        printf "%s%s%s\n" "$C_GREEN" "$*" "$C_RESET"
    fi
}

log_warn() {
    if [ "$QUIET" -eq 0 ]; then
        printf "%sWarning: %s%s\n" "$C_YELLOW" "$*" "$C_RESET" >&2
    fi
}

# Diagnostics for a failing check: always printed, even with --quiet.
log_fail() {
    printf "%s%s%s\n" "$C_YELLOW" "$*" "$C_RESET" >&2
}

log_error() {
    printf "%sError: %s%s\n" "$C_RED" "$*" "$C_RESET" >&2
}

die() {
    log_error "$*"
    exit 1
}

make_temp_dir() {
    if [ -n "$TMP_DIR" ]; then
        return 0
    fi
    if command -v mktemp >/dev/null 2>&1; then
        TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/env-updater.XXXXXX")"
    else
        TMP_DIR="${TMPDIR:-/tmp}/env-updater.$$"
        mkdir "$TMP_DIR" || die "Cannot create temp directory: $TMP_DIR"
    fi
}

# Print the file's lines as an indented list with a prefix symbol.
print_list() {
    list_file="$1"
    list_prefix="$2"
    list_fn="$3"
    while IFS= read -r list_item; do
        if [ -n "$list_item" ]; then
            "$list_fn" "  ${list_prefix} ${list_item}"
        fi
    done < "$list_file"
}

# Fit a string into width chars, keeping the tail (most useful part of paths).
fit_text() {
    printf "%s" "$1" | awk -v w="$2" '{
        if (length($0) > w) print "..." substr($0, length($0) - w + 4)
        else print $0
    }'
}

header_row() {
    printf "%s|%s %-${HEADER_WIDTH}s %s|%s\n" "$C_BLUE" "$C_RESET" "$(fit_text "$1" "$HEADER_WIDTH")" "$C_BLUE" "$C_RESET" >&2
}

header_rule() {
    printf "%s+--------------------------------------------------------------+%s\n" "$C_BLUE" "$C_RESET" >&2
}

# Header goes to stderr so stdout stays machine-readable (e.g. --version).
show_header() {
    if [ "$HEADER_SHOWN" -eq 1 ] || [ "$QUIET" -eq 1 ] || [ "$NO_HEADER" -eq 1 ]; then
        return 0
    fi
    HEADER_SHOWN=1

    mode_str="${COMMAND:-sync}"
    mode_flags=""
    if [ "$DRY_RUN" -eq 1 ]; then
        mode_flags="dry-run"
    fi
    if [ "$INTERACTIVE" -eq 1 ]; then
        mode_flags="${mode_flags:+$mode_flags, }interactive"
    fi
    if [ "$PRUNE" -eq 1 ]; then
        mode_flags="${mode_flags:+$mode_flags, }prune"
    fi
    if [ -n "$mode_flags" ]; then
        mode_str="${mode_str} (${mode_flags})"
    fi

    header_rule
    header_row "env-updater"
    header_row ".env synchronization and management utility"
    header_row "Version: ${VERSION}"
    header_row "License: ${LICENSE_NAME}"
    header_row "Repo:    ${REPO_URL}"
    header_rule
    header_row "Mode:    ${mode_str}"
    case "${COMMAND:-sync}" in
        sync|check|status)
            header_row "Source:  ${SOURCE_FILE}"
            header_row "Target:  ${TARGET_FILE}"
            ;;
        restore|clean-backups)
            header_row "Target:  ${TARGET_FILE}"
            ;;
    esac
    header_rule
    printf "\n" >&2
}

usage() {
    cat <<EOF
${C_BOLD}USAGE:${C_RESET}
    env-updater [COMMAND] [OPTIONS]

${C_BOLD}COMMANDS:${C_RESET}
    sync            Synchronize target .env with source .env.example (default)
    check, status   Check if .env is missing keys from .env.example (CI-friendly)
    restore [FILE]  Restore .env from the latest backup (or specified backup file)
    clean-backups   Remove old .env.bak.* files (keeps newest 5; see --keep/--all)
    install         Install env-updater into PATH (/usr/local/bin or ~/.local/bin)
    update          Update env-updater to the latest verified version from GitHub
    uninstall       Remove installed env-updater binary from PATH

${C_BOLD}OPTIONS:${C_RESET}
    -s, --source <file>   Source template file (default: .env.example)
    -t, --target <file>   Target env file (default: .env)
    -n, --dry-run         Preview changes (shows only key names, never values)
    -i, --interactive     Prompt for new or empty keys during sync
    -p, --prune           Remove keys in .env that are not present in .env.example
        --no-backup       Do not create a timestamped backup before modifying .env
    -k, --keep <N>        Backups to keep on clean-backups (default: 5)
        --all             clean-backups: remove all backups
    -d, --dir <path>      Target directory for install / uninstall
    -f, --force           update: allow downgrade / git work tree; uninstall: skip marker check
        --skip-verify     update/install: skip SHA-256 checksum verification (unsafe)
    -q, --quiet           Suppress header and non-error output
        --no-header       Suppress the header banner
        --no-color        Disable colored output
    -v, --version         Print version and exit
    -h, --help            Print this help message and exit

${C_BOLD}EXAMPLES:${C_RESET}
    env-updater                          # Sync .env from .env.example with backup
    env-updater --dry-run                # Show keys that would be added/removed
    env-updater sync -i                  # Sync and interactively prompt for new/empty keys
    env-updater sync --prune             # Sync and remove extra keys not in .env.example
    env-updater check                    # Exit non-zero if .env is missing keys
    env-updater restore                  # Revert .env to the latest backup
    env-updater clean-backups --keep 3   # Keep only the 3 most recent backups
    env-updater install                  # Install env-updater to PATH
    env-updater update                   # Self-update from GitHub (checksum verified)
EOF
}

# ==============================================================================
# Env parser / merger (single awk program, POSIX awk compatible)
#
# Each file is parsed into records: K (key, possibly multi-line quoted value),
# C (comment), B (blank) or M (malformed). Records keep their raw lines so
# values are always written back byte-for-byte.
#
# mode=analyze writes into out_dir:
#   added_keys, extra_keys  - one key per line
#   prompt_keys             - pairs of lines: KEY, default-for-display
#   warnings                - human-readable warnings (no values)
# mode=merge prints the merged file to stdout, applying out_dir/overrides
# (pairs of lines: KEY, then "T" = use template entry or "V<value>").
# ==============================================================================

ENV_AWK='
function warn(msg) { if (mode == "analyze") print msg > (out_dir "/warnings") }
function fname(f) { return (f == 1 ? src_file : tgt_file) }

function load(f, file,    line, L) {
    L = 0
    while ((getline line < file) > 0) {
        L++
        ending[f, L] = (line ~ /\r$/ ? "\r\n" : "\n")
        if (line ~ /\r$/) crlf[f] = 1
        sub(/\r$/, "", line)
        lines[f, L] = line
    }
    close(file)
    nlines[f] = L
}

# Position of the closing quote qc in s at or after start (0 if none).
function find_close(s, start, qc,    i, c, esc, following) {
    esc = 0
    for (i = start; i <= length(s); i++) {
        c = substr(s, i, 1)
        if (qc == DQ) {
            if (esc) { esc = 0; continue }
            if (c == "\\") { esc = 1; continue }
        } else if (qc == SQ && c == "\\" && i < length(s)) {
            following = substr(s, i + 1, 1)
            if (following == SQ || following == "\\") { i++; continue }
        }
        if (c == qc) return i
    }
    return 0
}

function unescape_sq(s,    out, i, c, following) {
    out = ""
    for (i = 1; i <= length(s); i++) {
        c = substr(s, i, 1)
        if (c == "\\" && i < length(s)) {
            following = substr(s, i + 1, 1)
            if (following == SQ || following == "\\") { c = following; i++ }
        }
        out = out c
    }
    return out
}

function parse(f,    i, n, t, has_exp, eq, key, rest, qc, cpos, j, found, val) {
    n = 0
    i = 1
    while (i <= nlines[f]) {
        n++
        rstart[f, n] = i
        rlen[f, n] = 1
        t = lines[f, i]
        if (i == 1) sub(/^\xef\xbb\xbf/, "", t)
        sub(/^[ \t]+/, "", t)
        if (t == "") { rtype[f, n] = "B"; i++; continue }
        if (substr(t, 1, 1) == "#") { rtype[f, n] = "C"; rtext[f, n] = t; i++; continue }
        has_exp = 0
        if (t ~ /^export[ \t]+/) { has_exp = 1; sub(/^export[ \t]+/, "", t) }
        if (index(t, "=") == 0) {
            key = t
            if (key ~ /^[A-Za-z_][A-Za-z0-9_.-]*$/) {
                rtype[f, n] = "K"; rkey[f, n] = key; rexp[f, n] = has_exp; rval[f, n] = ""
            } else { rtype[f, n] = "M" }
            i++; continue
        }
        if (t !~ /^[A-Za-z_][A-Za-z0-9_.-]*[ \t]*=/) { rtype[f, n] = "M"; i++; continue }
        eq = index(t, "=")
        key = substr(t, 1, eq - 1)
        sub(/[ \t]+$/, "", key)
        rest = substr(t, eq + 1)
        sub(/^[ \t]+/, "", rest)
        rtype[f, n] = "K"
        rkey[f, n] = key
        rexp[f, n] = has_exp
        qc = substr(rest, 1, 1)
        if (qc == DQ || qc == SQ) {
            cpos = find_close(rest, 2, qc)
            if (cpos > 0) {
                rval[f, n] = substr(rest, 2, cpos - 2)
                if (qc == SQ) rval[f, n] = unescape_sq(rval[f, n])
            } else {
                found = 0
                for (j = i + 1; j <= nlines[f]; j++) {
                    cpos = find_close(lines[f, j], 1, qc)
                    if (cpos > 0) { found = j; break }
                }
                if (found) {
                    val = substr(rest, 2)
                    for (j = i + 1; j < found; j++) val = val "\n" lines[f, j]
                    rval[f, n] = val "\n" substr(lines[f, found], 1, cpos - 1)
                    if (qc == SQ) rval[f, n] = unescape_sq(rval[f, n])
                    rlen[f, n] = found - i + 1
                } else {
                    rval[f, n] = substr(rest, 2)
                    warn(fname(f) ": line " i ": unterminated quote in " key " (treated as single line)")
                }
            }
        } else {
            val = rest
            if (substr(val, 1, 1) == "#") val = ""
            sub(/[ \t]+#.*$/, "", val)
            sub(/[ \t]+$/, "", val)
            rval[f, n] = val
        }
        i += rlen[f, n]
    }
    nrec[f] = n
}

function print_rec(f, n,    j) {
    for (j = 0; j < rlen[f, n]; j++) {
        printf "%s%s", lines[f, rstart[f, n] + j], (j < rlen[f, n] - 1 ? ending[f, rstart[f, n] + j] : ORS)
    }
}

function escape_dq(v,    out, i, c) {
    out = ""
    for (i = 1; i <= length(v); i++) {
        c = substr(v, i, 1)
        if (c == "\\" || c == DQ || c == "$" || c == "`") out = out "\\"
        out = out c
    }
    return out
}

# Format a user-supplied value: bare if safe, single quotes (no expansion)
# when possible, otherwise escaped double quotes.
function fmt(k, v, e,    p) {
    p = (e ? "export " : "")
    if (v !~ /[^A-Za-z0-9_.,:@+%=\/-]/) return p k "=" v
    if (index(v, SQ) == 0) return p k "=" SQ v SQ
    return p k "=" DQ escape_dq(v) DQ
}

BEGIN {
    DQ = "\""
    SQ = sq
    EXTRA_HDR = "# Extra keys preserved from .env"
    if (mode == "analyze") printf "" > (out_dir "/warnings")
    load(1, src_file); parse(1)
    if (tgt_exists == "1") { load(2, tgt_file); parse(2) }
    use_crlf = (tgt_exists == "1") ? crlf[2] : crlf[1]
    ORS = (use_crlf ? "\r\n" : "\n")

    # Source index: first occurrence wins, later duplicates are skipped.
    sc = 0
    for (i = 1; i <= nrec[1]; i++) {
        if (rtype[1, i] == "C") src_comment[rtext[1, i]] = 1
        if (rtype[1, i] != "K") continue
        k = rkey[1, i]
        if (k in src_idx) {
            src_dup[i] = 1
            if (mode == "analyze") warn(src_file ": line " rstart[1, i] ": duplicate key " k " (ignored; first occurrence kept)")
        } else {
            src_idx[k] = i
            src_order[++sc] = k
        }
    }

    # Target index: last occurrence wins (shell semantics). Track the comment
    # block directly above each key so it can travel with extra keys.
    tc = 0
    cstart = 0
    for (i = 1; i <= nrec[2]; i++) {
        t = rtype[2, i]
        if (t == "C" && rtext[2, i] == EXTRA_HDR) { cstart = 0; continue }
        if (t == "C") { if (!cstart) cstart = i; continue }
        if (t == "K") {
            k = rkey[2, i]
            if (k in tgt_idx) {
                if (mode == "analyze") warn(tgt_file ": line " rstart[2, i] ": duplicate key " k " (last occurrence wins)")
            } else {
                tgt_order[++tc] = k
            }
            tgt_idx[k] = i
            attach[i] = cstart
            attach_m[i] = cstart
        } else if (t == "M") {
            attach_m[i] = cstart
        }
        cstart = 0
    }

    if (mode == "analyze") {
        f_add = out_dir "/added_keys"; f_ext = out_dir "/extra_keys"; f_prm = out_dir "/prompt_keys"
        printf "" > f_add; printf "" > f_ext; printf "" > f_prm
        for (i = 1; i <= sc; i++) {
            k = src_order[i]
            d = rval[1, src_idx[k]]
            if (index(d, "\n")) d = "<multi-line>"
            if (!(k in tgt_idx)) {
                print k > f_add
                print k > f_prm; print d > f_prm
            } else if (rval[2, tgt_idx[k]] == "") {
                print k > f_prm; print d > f_prm
            }
        }
        for (i = 1; i <= tc; i++) {
            k = tgt_order[i]
            if (!(k in src_idx)) {
                print k > f_ext
                if (prune == "0") {
                    for (j = attach[tgt_idx[k]]; j && j < tgt_idx[k]; j++) kept_comment[j] = 1
                }
            }
        }
        # Report malformed lines; sync retains them in an unparsed section.
        for (i = 1; i <= nrec[2]; i++) {
            if (rtype[2, i] == "M") {
                warn(tgt_file ": line " rstart[2, i] ": not a KEY=VALUE line, will be preserved as unparsed content")
            } else if (rtype[2, i] == "C" && !(i in kept_comment) && !(rtext[2, i] in src_comment) && rtext[2, i] != EXTRA_HDR) {
                warn(tgt_file ": line " rstart[2, i] ": comment not in template, will be dropped")
            }
        }
        exit
    }

    # mode == "merge"
    while ((getline line < (out_dir "/overrides")) > 0) {
        k = line
        if ((getline line < (out_dir "/overrides")) <= 0) break
        ovr[k] = line
    }
    close(out_dir "/overrides")

    last_blank = 1
    for (i = 1; i <= nrec[1]; i++) {
        if (rtype[1, i] != "K") {
            if (rtype[1, i] == "M") warn(src_file ": line " rstart[1, i] ": not a KEY=VALUE line in template (preserved)")
            print_rec(1, i)
            last_blank = (rtype[1, i] == "B")
            continue
        }
        if (i in src_dup) continue
        k = rkey[1, i]
        if ((k in ovr) && substr(ovr[k], 1, 1) == "V") {
            e = (k in tgt_idx) ? rexp[2, tgt_idx[k]] : rexp[1, i]
            print fmt(k, substr(ovr[k], 2), e)
        } else if (k in ovr) {
            print_rec(1, i)
        } else if (k in tgt_idx) {
            print_rec(2, tgt_idx[k])
        } else {
            print_rec(1, i)
        }
        last_blank = 0
    }

    if (prune == "0") {
        header_done = 0
        for (i = 1; i <= tc; i++) {
            k = tgt_order[i]
            if (k in src_idx) continue
            if (!header_done) {
                if (!last_blank) print ""
                print EXTRA_HDR
                header_done = 1
            }
            n = tgt_idx[k]
            for (j = attach[n]; j && j < n; j++) print_rec(2, j)
            print_rec(2, n)
        }
    }

    has_malformed = 0
    for (i = 1; i <= nrec[2]; i++) if (rtype[2, i] == "M") has_malformed = 1
    if (has_malformed) {
        if (!last_blank) print ""
        print "# Unparsed content preserved from .env"
        for (i = 1; i <= nrec[2]; i++) {
            if (rtype[2, i] != "M") continue
            for (j = attach_m[i]; j && j < i; j++) print_rec(2, j)
            print_rec(2, i)
        }
    }
}
'

# run_env_awk <mode> <target-exists>
run_env_awk() {
    awk \
        -v mode="$1" \
        -v tgt_exists="$2" \
        -v src_file="$SOURCE_FILE" \
        -v tgt_file="$TARGET_FILE" \
        -v prune="$PRUNE" \
        -v out_dir="$TMP_DIR" \
        -v sq="'" \
        "$ENV_AWK"
}

count_lines() {
    if [ ! -f "$1" ]; then
        echo 0
        return
    fi
    wc -l < "$1" | tr -d ' \t'
}

show_warnings() {
    if [ -s "$TMP_DIR/warnings" ]; then
        while IFS= read -r w_line; do
            log_warn "$w_line"
        done < "$TMP_DIR/warnings"
    fi
}

# ==============================================================================
# File helpers
# ==============================================================================

# Follow symlinks so atomic replacement updates the real file, not the link.
resolve_path() {
    rp_path="$1"
    rp_hops=0
    while [ -L "$rp_path" ]; do
        if ! command -v readlink >/dev/null 2>&1; then
            break
        fi
        rp_link="$(readlink "$rp_path")"
        case "$rp_link" in
            /*) rp_path="$rp_link" ;;
            *) rp_path="$(dirname "$rp_path")/$rp_link" ;;
        esac
        rp_hops=$((rp_hops + 1))
        if [ "$rp_hops" -gt 40 ]; then
            die "Too many levels of symbolic links: $1"
        fi
    done
    printf "%s\n" "$rp_path"
}

# Atomically replace <dest> with the content of <src>, keeping dest's mode
# when it already exists (new files get 0600 via umask).
atomic_write() {
    aw_src="$1"
    aw_dest="$(resolve_path "$2")"
    aw_dir="$(dirname "$aw_dest")"
    PENDING_TMP="$(mktemp "${aw_dir}/.$(basename "$aw_dest").tmp.XXXXXX")" || die "Cannot create temporary file in $aw_dir"
    if [ -f "$aw_dest" ]; then
        cp -p "$aw_dest" "$PENDING_TMP" 2>/dev/null || : > "$PENDING_TMP"
    else
        : > "$PENDING_TMP"
    fi
    cat "$aw_src" > "$PENDING_TMP"
    mv -f "$PENDING_TMP" "$aw_dest"
    PENDING_TMP=""
}

warn_if_world_readable() {
    if [ -f "$1" ]; then
        wr_perms="$(ls -lL "$1" 2>/dev/null | cut -c8)"
        if [ "$wr_perms" = "r" ]; then
            log_warn "$1 is world-readable; consider: chmod 600 $1"
        fi
    fi
}

create_backup() {
    if [ ! -f "$TARGET_FILE" ]; then
        return 0
    fi
    timestamp="$(date +%Y%m%d-%H%M%S)"
    backup_path="${TARGET_FILE}.bak.${timestamp}"
    suffix=1
    while ! (set -C; : > "$backup_path") 2>/dev/null; do
        if [ ! -e "$backup_path" ] && [ ! -L "$backup_path" ]; then
            die "Cannot create backup: $backup_path"
        fi
        # Zero-padded suffix keeps lexical order == chronological order.
        backup_path="$(printf "%s.bak.%s-%03d" "$TARGET_FILE" "$timestamp" "$suffix")"
        suffix=$((suffix + 1))
    done
    if ! cat "$TARGET_FILE" > "$backup_path"; then
        rm -f "$backup_path"
        die "Cannot write backup: $backup_path"
    fi
    log_info "Backup created: ${backup_path}"
}

# List backups oldest -> newest (portable glob instead of find -maxdepth).
find_backups() {
    fb_dir="$(dirname "$TARGET_FILE")"
    fb_base="$(basename "$TARGET_FILE")"
    for fb_file in "$fb_dir"/"$fb_base".bak.*; do
        case "$fb_file" in
            *.bak.[0-9]*)
                if [ -f "$fb_file" ]; then
                    printf "%s\n" "$fb_file"
                fi
                ;;
        esac
    done | LC_ALL=C sort
}

# ==============================================================================
# Commands
# ==============================================================================

prompt_overrides() {
    : > "$TMP_DIR/overrides"
    if [ "$INTERACTIVE" -ne 1 ] || [ ! -s "$TMP_DIR/prompt_keys" ]; then
        return 0
    fi
    while IFS= read -r p_key <&3 && IFS= read -r p_def <&3; do
        if [ -z "$p_key" ]; then
            continue
        fi
        if [ -n "$p_def" ]; then
            printf "Enter value for %s [%s]: " "$p_key" "$p_def" >&2
        else
            printf "Enter value for %s: " "$p_key" >&2
        fi

        hide=0
        case "$(printf "%s" "$p_key" | tr '[:lower:]' '[:upper:]')" in
            *PASS*|*SECRET*|*TOKEN*|*KEY*|*PRIVATE*|*CREDENTIAL*|*AUTH*|*SALT*) hide=1 ;;
        esac
        if [ "$hide" -eq 1 ] && [ -t 0 ]; then
            STTY_SAVED="$(stty -g 2>/dev/null || true)"
            stty -echo 2>/dev/null || true
        fi

        user_val=""
        read_ok=1
        IFS= read -r user_val || read_ok=0

        if [ -n "$STTY_SAVED" ]; then
            stty "$STTY_SAVED" 2>/dev/null || true
            STTY_SAVED=""
            printf "\n" >&2
        fi
        if [ "$read_ok" -eq 0 ] && [ -z "$user_val" ]; then
            break
        fi

        if [ -n "$user_val" ]; then
            printf "%s\nV%s\n" "$p_key" "$user_val" >> "$TMP_DIR/overrides"
        elif [ -n "$p_def" ]; then
            # Enter accepts the template entry verbatim.
            printf "%s\nT\n" "$p_key" >> "$TMP_DIR/overrides"
        fi
    done 3< "$TMP_DIR/prompt_keys"
}

cmd_sync() {
    if [ ! -f "$SOURCE_FILE" ]; then
        die "Source file not found: $SOURCE_FILE"
    fi

    tgt_exists=0
    if [ -f "$TARGET_FILE" ]; then
        tgt_exists=1
    fi

    make_temp_dir
    run_env_awk analyze "$tgt_exists"

    added_count="$(count_lines "$TMP_DIR/added_keys")"
    extra_count="$(count_lines "$TMP_DIR/extra_keys")"
    if [ "$PRUNE" -eq 1 ]; then
        removed_count="$extra_count"
    else
        removed_count=0
    fi

    show_warnings

    # --dry-run: only key names, never values or diffs.
    if [ "$DRY_RUN" -eq 1 ]; then
        log_info "${C_BOLD}Dry run (${SOURCE_FILE} -> ${TARGET_FILE}):${C_RESET}"
        if [ "$tgt_exists" -eq 0 ]; then
            log_info "${TARGET_FILE} does not exist and would be created."
        fi
        if [ "$added_count" -eq 0 ] && [ "$removed_count" -eq 0 ]; then
            log_info "No keys would be added or removed."
            return 0
        fi
        if [ "$added_count" -gt 0 ]; then
            log_info "Keys to be added (${added_count}):"
            print_list "$TMP_DIR/added_keys" "+" log_info
        fi
        if [ "$removed_count" -gt 0 ]; then
            log_info "Keys to be removed (${removed_count}):"
            print_list "$TMP_DIR/extra_keys" "-" log_info
        fi
        return 0
    fi

    prompt_overrides

    run_env_awk merge "$tgt_exists" > "$TMP_DIR/merged_env"

    if [ "$tgt_exists" -eq 1 ] && cmp -s "$TMP_DIR/merged_env" "$TARGET_FILE"; then
        log_success "${TARGET_FILE} is already up to date."
        warn_if_world_readable "$TARGET_FILE"
        return 0
    fi

    if [ "$tgt_exists" -eq 1 ] && [ "$BACKUP" -eq 1 ]; then
        create_backup
    fi

    atomic_write "$TMP_DIR/merged_env" "$TARGET_FILE"

    if [ "$tgt_exists" -eq 0 ]; then
        log_success "Created ${TARGET_FILE} from ${SOURCE_FILE} (${added_count} keys)."
    else
        log_success "Updated ${TARGET_FILE} (added: ${added_count}, removed: ${removed_count})."
        warn_if_world_readable "$TARGET_FILE"
    fi
}

cmd_check() {
    if [ ! -f "$SOURCE_FILE" ]; then
        die "Source file not found: $SOURCE_FILE"
    fi
    if [ ! -f "$TARGET_FILE" ]; then
        die "Target file not found: $TARGET_FILE"
    fi

    make_temp_dir
    run_env_awk analyze 1
    show_warnings

    added_count="$(count_lines "$TMP_DIR/added_keys")"
    extra_count="$(count_lines "$TMP_DIR/extra_keys")"

    if [ "$added_count" -gt 0 ]; then
        log_fail "${TARGET_FILE} is missing ${added_count} key(s) from ${SOURCE_FILE}:"
        print_list "$TMP_DIR/added_keys" "-" log_fail
        exit 1
    fi

    if [ "$PRUNE" -eq 1 ] && [ "$extra_count" -gt 0 ]; then
        log_fail "${TARGET_FILE} has ${extra_count} extra key(s) not in ${SOURCE_FILE}:"
        print_list "$TMP_DIR/extra_keys" "+" log_fail
        exit 1
    fi

    log_success "${TARGET_FILE} is in sync with ${SOURCE_FILE}."
    if [ "$extra_count" -gt 0 ]; then
        log_info "Extra keys in ${TARGET_FILE} (${extra_count}):"
        print_list "$TMP_DIR/extra_keys" "+" log_info
    fi
}

cmd_restore() {
    make_temp_dir
    backup_to_use="$RESTORE_FILE"
    if [ -z "$backup_to_use" ]; then
        find_backups > "$TMP_DIR/backups_list"
        if [ ! -s "$TMP_DIR/backups_list" ]; then
            die "No backup files found for ${TARGET_FILE}."
        fi
        backup_to_use="$(tail -n 1 "$TMP_DIR/backups_list")"
    fi

    if [ ! -f "$backup_to_use" ]; then
        die "Backup file not found: ${backup_to_use}"
    fi

    if [ "$DRY_RUN" -eq 1 ]; then
        log_info "Dry run: would restore ${TARGET_FILE} from ${backup_to_use}"
        return 0
    fi

    if [ -f "$TARGET_FILE" ] && cmp -s "$backup_to_use" "$TARGET_FILE"; then
        log_success "${TARGET_FILE} already matches ${backup_to_use}."
        return 0
    fi

    # Save the current state first so a restore can itself be undone.
    if [ -f "$TARGET_FILE" ] && [ "$BACKUP" -eq 1 ]; then
        create_backup
    fi

    atomic_write "$backup_to_use" "$TARGET_FILE"
    log_success "Restored ${TARGET_FILE} from ${backup_to_use}"
}

cmd_clean_backups() {
    case "$KEEP_BACKUPS" in
        ''|*[!0-9]*)
            die "--keep requires a non-negative integer (got: $KEEP_BACKUPS)"
            ;;
    esac

    make_temp_dir
    find_backups > "$TMP_DIR/backups_list"
    total_backups="$(count_lines "$TMP_DIR/backups_list")"

    if [ "$total_backups" -eq 0 ]; then
        log_info "No backup files found for ${TARGET_FILE}."
        return 0
    fi

    to_delete_count=$((total_backups - KEEP_BACKUPS))
    if [ "$to_delete_count" -le 0 ]; then
        log_info "Nothing to clean (${total_backups} backup(s) found, keeping ${KEEP_BACKUPS})."
        return 0
    fi

    head -n "$to_delete_count" "$TMP_DIR/backups_list" > "$TMP_DIR/backups_to_delete"

    if [ "$DRY_RUN" -eq 1 ]; then
        log_info "Dry run: would remove ${to_delete_count} backup file(s):"
        print_list "$TMP_DIR/backups_to_delete" "-" log_info
        return 0
    fi

    while IFS= read -r bfile; do
        if [ -n "$bfile" ] && [ -f "$bfile" ]; then
            rm -f "$bfile"
            log_info "Removed backup: ${bfile}"
        fi
    done < "$TMP_DIR/backups_to_delete"

    log_success "Cleaned ${to_delete_count} backup file(s) (kept ${KEEP_BACKUPS})."
}

# ==============================================================================
# Self-management (install / update / uninstall)
# ==============================================================================

has_marker() {
    [ -f "$1" ] && grep -q "^# ${MARKER}\$" "$1" 2>/dev/null
}

download_file() {
    dl_url="$1"
    dl_dest="$2"
    case "$dl_url" in
        file://*)
            cp "${dl_url#file://}" "$dl_dest"
            return $?
            ;;
    esac
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$dl_url" -o "$dl_dest"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$dl_dest" "$dl_url"
    else
        die "Neither curl nor wget is installed."
    fi
}

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$1" | awk '{print $NF}'
    else
        return 1
    fi
}

# Download the script to <dest> and verify checksum, shebang and marker.
fetch_verified() {
    fv_dest="$1"
    download_file "$REPO_RAW_URL" "$fv_dest" || die "Download failed: $REPO_RAW_URL"

    if [ "$SKIP_VERIFY" -eq 1 ]; then
        log_warn "Skipping checksum verification (--skip-verify)."
    else
        download_file "$CHECKSUM_URL" "$TMP_DIR/remote.sha256" || die "Could not download checksum: $CHECKSUM_URL (use --skip-verify to bypass)"
        expected="$(awk 'NR == 1 {print $1}' "$TMP_DIR/remote.sha256")"
        actual="$(sha256_of "$fv_dest")" || die "No SHA-256 tool found (sha256sum, shasum or openssl)."
        if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
            die "Checksum mismatch for downloaded script (expected ${expected:-none}, got ${actual})."
        fi
    fi

    if ! head -n 1 "$fv_dest" | grep -q '^#!/bin/.*sh' || ! has_marker "$fv_dest"; then
        die "Downloaded file is not a valid env-updater script."
    fi
}

# Prints 1, 0 or -1 for a > b, a == b, a < b (numeric dotted versions).
version_cmp() {
    awk -v a="$1" -v b="$2" 'BEGIN {
        na = split(a, x, "."); nb = split(b, y, ".")
        n = (na > nb ? na : nb)
        for (i = 1; i <= n; i++) {
            p = x[i] + 0; q = y[i] + 0
            if (p > q) { print 1; exit }
            if (p < q) { print -1; exit }
        }
        print 0
    }'
}

in_git_worktree() {
    gw_dir="$(cd "$(dirname "$1")" 2>/dev/null && pwd -P)" || return 1
    while :; do
        if [ -e "$gw_dir/.git" ]; then
            return 0
        fi
        if [ "$gw_dir" = "/" ] || [ -z "$gw_dir" ]; then
            return 1
        fi
        gw_dir="$(dirname "$gw_dir")"
    done
}

resolve_install_dir() {
    if [ -n "$INSTALL_DIR" ]; then
        printf "%s\n" "$INSTALL_DIR"
        return
    fi
    if [ -d "/usr/local/bin" ] && [ -w "/usr/local/bin" ]; then
        printf "/usr/local/bin\n"
        return
    fi
    if [ "$(id -u 2>/dev/null || echo 1)" -eq 0 ]; then
        printf "/usr/local/bin\n"
        return
    fi
    printf "%s/.local/bin\n" "${HOME:-/tmp}"
}

# Replace an executable atomically (new inode), so a running copy is never
# rewritten underneath the shell that is executing it.
install_binary() {
    ib_src="$1"
    ib_dest="$(resolve_path "$2")"
    PENDING_TMP="$(mktemp "$(dirname "$ib_dest")/.env-updater.tmp.XXXXXX")" || die "Cannot create temporary executable"
    cat "$ib_src" > "$PENDING_TMP"
    chmod 755 "$PENDING_TMP"
    mv -f "$PENDING_TMP" "$ib_dest"
    PENDING_TMP=""
}

cmd_install() {
    target_dir="$(resolve_install_dir)"
    dest_bin="${target_dir}/env-updater"

    if [ "$DRY_RUN" -eq 1 ]; then
        log_info "Dry run: would install env-updater to ${dest_bin}"
        return 0
    fi

    if [ ! -d "$target_dir" ]; then
        (umask 022 && mkdir -p "$target_dir")
    fi

    make_temp_dir
    if has_marker "$0"; then
        cp "$0" "$TMP_DIR/env-updater.src"
    else
        fetch_verified "$TMP_DIR/env-updater.src"
    fi
    install_binary "$TMP_DIR/env-updater.src" "$dest_bin"
    log_success "Installed env-updater to ${dest_bin}"

    case ":${PATH:-}:" in
        *":${target_dir}:"*) ;;
        *)
            log_warn "${target_dir} is not in your PATH. Add it with:"
            log_warn "  export PATH=\"${target_dir}:\$PATH\""
            ;;
    esac
}

cmd_update() {
    make_temp_dir
    dl_file="$TMP_DIR/env-updater.latest"

    log_info "Checking for updates from ${REPO_RAW_URL}..."
    fetch_verified "$dl_file"

    remote_version="$(sed -n 's/^VERSION="\([^"]*\)".*/\1/p' "$dl_file" | head -n 1)"
    if [ -z "$remote_version" ]; then
        die "Could not determine version of downloaded script."
    fi

    if has_marker "$0"; then
        self_path="$0"
    elif [ -n "$INSTALL_DIR" ] && has_marker "$INSTALL_DIR/env-updater"; then
        self_path="$INSTALL_DIR/env-updater"
    elif command -v env-updater >/dev/null 2>&1; then
        self_path="$(command -v env-updater)"
    else
        die "Cannot locate the installed env-updater to update."
    fi
    self_path="$(resolve_path "$self_path")"

    if cmp -s "$dl_file" "$self_path"; then
        log_success "env-updater is already at the latest version (v${VERSION})."
        return 0
    fi

    cmp_result="$(version_cmp "$remote_version" "$VERSION")"
    if [ "$cmp_result" -lt 0 ] && [ "$FORCE" -eq 0 ]; then
        die "Remote version v${remote_version} is older than local v${VERSION} (use --force to downgrade)."
    fi
    if [ "$cmp_result" -eq 0 ] && [ "$FORCE" -eq 0 ]; then
        die "Local copy differs from remote v${remote_version} (locally modified?). Use --force to overwrite."
    fi
    if in_git_worktree "$self_path" && [ "$FORCE" -eq 0 ]; then
        die "${self_path} is inside a git work tree; use 'git pull' instead (or --force)."
    fi

    if [ "$DRY_RUN" -eq 1 ]; then
        log_info "Dry run: would update ${self_path} from v${VERSION} to v${remote_version}"
        return 0
    fi

    install_binary "$dl_file" "$self_path"
    log_success "Updated env-updater from v${VERSION} to v${remote_version} at ${self_path}"
    # Exit immediately: never execute further from a replaced script file.
    exit 0
}

cmd_uninstall() {
    target_bin=""
    if [ -n "$INSTALL_DIR" ]; then
        target_bin="${INSTALL_DIR}/env-updater"
    elif [ -f "/usr/local/bin/env-updater" ] && [ -w "/usr/local/bin" ]; then
        target_bin="/usr/local/bin/env-updater"
    elif [ -f "${HOME:-}/.local/bin/env-updater" ]; then
        target_bin="${HOME}/.local/bin/env-updater"
    elif command -v env-updater >/dev/null 2>&1; then
        target_bin="$(command -v env-updater)"
    fi

    if [ -z "$target_bin" ] || [ ! -f "$target_bin" ]; then
        die "env-updater is not installed in PATH or specified directory."
    fi
    if ! has_marker "$target_bin" && [ "$FORCE" -eq 0 ]; then
        die "${target_bin} does not look like env-updater; refusing to remove (use --force)."
    fi

    if [ "$DRY_RUN" -eq 1 ]; then
        log_info "Dry run: would remove ${target_bin}"
        return 0
    fi

    rm -f "$target_bin"
    log_success "Uninstalled env-updater from ${target_bin}"
}

# ==============================================================================
# Argument Parsing
# ==============================================================================

require_arg() {
    if [ "$2" -lt 2 ]; then
        die "Option $1 requires a value."
    fi
}

parse_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            sync|check|status|restore|clean-backups|install|update|self-update|uninstall)
                if [ -n "$COMMAND" ]; then
                    die "Unexpected command argument: $1"
                fi
                COMMAND="$1"
                shift
                if [ "$COMMAND" = "restore" ] && [ $# -gt 0 ]; then
                    case "$1" in
                        -*) ;;
                        *)
                            RESTORE_FILE="$1"
                            shift
                            ;;
                    esac
                fi
                ;;
            -s|--source) require_arg "$1" $#; SOURCE_FILE="$2"; shift 2 ;;
            -t|--target) require_arg "$1" $#; TARGET_FILE="$2"; shift 2 ;;
            -k|--keep) require_arg "$1" $#; KEEP_BACKUPS="$2"; shift 2 ;;
            -d|--dir|--prefix) require_arg "$1" $#; INSTALL_DIR="$2"; shift 2 ;;
            -n|--dry-run) DRY_RUN=1; shift ;;
            -i|--interactive) INTERACTIVE=1; shift ;;
            -p|--prune) PRUNE=1; shift ;;
            --no-backup) BACKUP=0; shift ;;
            --all) KEEP_BACKUPS=0; shift ;;
            -f|--force) FORCE=1; shift ;;
            --skip-verify) SKIP_VERIFY=1; shift ;;
            -q|--quiet) QUIET=1; shift ;;
            --no-header) NO_HEADER=1; shift ;;
            --no-color) NO_COLOR=1; shift ;;
            -v|--version) COMMAND="version"; shift ;;
            -h|--help) COMMAND="help"; shift ;;
            *)
                setup_colors
                log_error "Unknown option or command: $1"
                usage >&2
                exit 1
                ;;
        esac
    done

    if [ -z "$COMMAND" ]; then
        COMMAND="sync"
    fi
}

main() {
    parse_args "$@"
    setup_colors
    show_header

    case "$COMMAND" in
        version) printf "env-updater v%s\n" "$VERSION" ;;
        help) usage ;;
        sync) cmd_sync ;;
        check|status) cmd_check ;;
        restore) cmd_restore ;;
        clean-backups) cmd_clean_backups ;;
        install) cmd_install ;;
        update|self-update) cmd_update ;;
        uninstall) cmd_uninstall ;;
    esac
}

main "$@"
