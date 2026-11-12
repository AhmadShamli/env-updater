#!/bin/sh
# ==============================================================================
# Automated Test Suite for env-updater (POSIX /bin/sh)
#
# Usage: sh tests/test.sh
#   TEST_AWK=/usr/bin/nawk sh tests/test.sh   # force a specific awk
#   TEST_SHELL="bash --posix" sh tests/test.sh # run the script under a shell
# ==============================================================================

set -eu

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="$REPO_ROOT/env-updater"
REAL_SCRIPT="$SCRIPT"
TEST_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/env-updater-tests.XXXXXX")"
VERSION="$(tr -d ' \t\r\n' < "$REPO_ROOT/VERSION")"

# Optionally force a specific awk implementation via a PATH shim.
if [ -n "${TEST_AWK:-}" ]; then
    mkdir -p "$TEST_ROOT/awkbin"
    printf '#!/bin/sh\nexec %s "$@"\n' "$TEST_AWK" > "$TEST_ROOT/awkbin/awk"
    chmod +x "$TEST_ROOT/awkbin/awk"
    PATH="$TEST_ROOT/awkbin:$PATH"
    export PATH
fi

# Optionally run the script itself under a specific shell (default: shebang).
if [ -n "${TEST_SHELL:-}" ]; then
    printf '#!/bin/sh\nexec %s %s "$@"\n' "$TEST_SHELL" "$SCRIPT" > "$TEST_ROOT/shellwrap"
    chmod +x "$TEST_ROOT/shellwrap"
    SCRIPT="$TEST_ROOT/shellwrap"
fi

PASS_COUNT=0
FAIL_COUNT=0

cleanup() {
    rm -rf "$TEST_ROOT"
}
trap cleanup EXIT INT TERM

pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf "  [PASS] %s\n" "$1"
}

fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    printf "  [FAIL] %s\n" "$1" >&2
}

assert_eq() {
    if [ "$1" = "$2" ]; then
        pass "$3"
    else
        fail "$3"
        printf "         Expected: %s\n         Actual:   %s\n" "$1" "$2" >&2
    fi
}

assert_contains() {
    case "$1" in
        *"$2"*) pass "$3" ;;
        *)
            fail "$3"
            printf "         Expected to contain: %s\n         Actual: %s\n" "$2" "$1" >&2
            ;;
    esac
}

assert_not_contains() {
    case "$1" in
        *"$2"*)
            fail "$3"
            printf "         Did NOT expect: %s\n         Actual: %s\n" "$2" "$1" >&2
            ;;
        *) pass "$3" ;;
    esac
}

count_files() {
    n=0
    for f in "$1"/$2; do
        if [ -e "$f" ]; then
            n=$((n + 1))
        fi
    done
    echo "$n"
}

newdir() {
    d="$TEST_ROOT/$1"
    mkdir -p "$d"
    echo "$d"
}

printf "Running env-updater test suite (shell: %s, awk: %s)...\n" "${TEST_SHELL:-shebang}" "${TEST_AWK:-default}"

# ------------------------------------------------------------------------------
printf "\nTest 1: Header, version and help\n"
out="$("$SCRIPT" --version 2>/dev/null)"
assert_eq "env-updater v${VERSION}" "$out" "--version stdout is a single machine-readable line"
err="$("$SCRIPT" --version 2>&1 >/dev/null)"
assert_contains "$err" "+--------------------------------------------------------------+" "Header border printed to stderr"
assert_contains "$err" "Version: ${VERSION}" "Header shows version"
assert_contains "$err" "License: GNU Affero General Public License v3.0 (AGPL-3.0)" "Header shows license"
assert_contains "$err" "https://github.com/AhmadShamli/env-updater" "Header shows repo URL"
assert_eq "1" "$(printf "%s\n" "$err" | grep -c "Repo:")" "Header shown exactly once"
out="$("$SCRIPT" --help 2>&1)"
assert_contains "$out" "Mode:    help" "--help shows mode in header"
assert_contains "$out" "USAGE:" "--help prints usage"
assert_eq "" "$("$SCRIPT" --version -q 2>&1 >/dev/null)" "-q suppresses header"
assert_eq "" "$("$SCRIPT" --version --no-header 2>&1 >/dev/null)" "--no-header suppresses header"
assert_eq "$VERSION" "$(sed -n 's/.*Version[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "$REPO_ROOT/main.go")" "VERSION file matches main.go version"

# ------------------------------------------------------------------------------
printf "\nTest 2: Argument errors are clean (no set -u crash)\n"
for opt in -s -t -k -d; do
    set +e
    err="$("$SCRIPT" "$opt" 2>&1)"
    rc=$?
    set -e
    assert_eq "1" "$rc" "$opt without value exits 1"
    assert_contains "$err" "requires a value" "$opt without value reports a clear error"
done
set +e
err="$("$SCRIPT" sync check 2>&1)"; rc=$?
set -e
assert_eq "1" "$rc" "Two commands exit 1"
assert_not_contains "$err" "parameter not set" "No unbound-variable crash"
set +e
err="$("$SCRIPT" --bogus 2>&1)"; rc=$?
set -e
assert_eq "1" "$rc" "Unknown option exits 1"

# ------------------------------------------------------------------------------
printf "\nTest 3: Create .env when missing\n"
d="$(newdir t3)"
printf '# Database settings\nDB_HOST=localhost\nDB_PORT=5432\n' > "$d/.env.example"
(cd "$d" && umask 022 && "$SCRIPT" -q)
assert_eq "$(cat "$d/.env.example")" "$(cat "$d/.env")" ".env matches template when created"
assert_eq "0" "$(count_files "$d" ".env.bak.*")" "No backup when .env did not exist"
assert_eq "-rw-------" "$(ls -l "$d/.env" | cut -c1-10)" "New .env is 0600 even with umask 022"

# ------------------------------------------------------------------------------
printf "\nTest 4: Merge, preserve values, backups, idempotency\n"
d="$(newdir t4)"
cat > "$d/.env.example" <<'EOF'
# App Configuration
APP_NAME=DefaultApp
APP_ENV=local
export API_URL="https://example.com/api"
EMPTY_IN_ENV=default_val
NEW_KEY=new_default
EOF
cat > "$d/.env" <<'EOF'
APP_NAME="My Production App #1"
export API_URL="https://prod.example.com/api"
EMPTY_IN_ENV=
CUSTOM_EXTRA=secret_extra_123
EOF
chmod 640 "$d/.env"
(cd "$d" && "$SCRIPT" -q)
assert_eq "1" "$(count_files "$d" ".env.bak.*")" "Backup created on update"
m="$(cat "$d/.env")"
assert_contains "$m" "# App Configuration" "Template comments preserved"
assert_contains "$m" 'APP_NAME="My Production App #1"' "Existing quoted value with # preserved"
assert_contains "$m" "APP_ENV=local" "Missing key added with default"
assert_contains "$m" 'export API_URL="https://prod.example.com/api"' "export prefix preserved"
assert_contains "$m" "EMPTY_IN_ENV=" "Existing empty value preserved"
assert_contains "$m" "NEW_KEY=new_default" "New key added"
assert_contains "$m" "CUSTOM_EXTRA=secret_extra_123" "Extra key appended"
assert_eq "-rw-r-----" "$(ls -l "$d/.env" | cut -c1-10)" "Existing file mode preserved on update"
assert_eq "-rw-------" "$(ls -l "$d"/.env.bak.* | cut -c1-10)" "Backup is 0600"
out="$(cd "$d" && "$SCRIPT" 2>/dev/null)"
assert_contains "$out" "already up to date" "Second sync is idempotent"
assert_eq "1" "$(count_files "$d" ".env.bak.*")" "No backup when nothing changed"

# ------------------------------------------------------------------------------
printf "\nTest 5: Multi-line values are preserved (no data loss)\n"
d="$(newdir t5)"
printf 'PRIVATE_KEY=\nB=1\nJSON=\n' > "$d/.env.example"
cat > "$d/.env" <<'EOF'
PRIVATE_KEY="-----BEGIN KEY-----
abc123
-----END KEY-----"
B=2
JSON='{
  "a": 1
}'
EXTRA_ML="line1
line2"
EOF
cp "$d/.env" "$d/orig"
(cd "$d" && "$SCRIPT" -q --no-backup)
m="$(cat "$d/.env")"
assert_contains "$m" 'PRIVATE_KEY="-----BEGIN KEY-----
abc123
-----END KEY-----"' "Multi-line double-quoted value intact"
assert_contains "$m" "JSON='{
  \"a\": 1
}'" "Multi-line single-quoted value intact"
assert_contains "$m" 'EXTRA_ML="line1
line2"' "Multi-line extra key intact"
assert_contains "$m" "B=2" "Key after multi-line value preserved"
set +e
(cd "$d" && "$SCRIPT" check -q); rc=$?
set -e
assert_eq "0" "$rc" "check passes on multi-line file"
d2="$(newdir t5b)"
printf 'A=1\nB=2\n' > "$d2/.env.example"
printf 'A="unterminated\nB=x\n' > "$d2/.env"
err="$(cd "$d2" && "$SCRIPT" --no-backup 2>&1)"
assert_contains "$err" "unterminated quote" "Unterminated quote is warned about"
assert_contains "$(cat "$d2/.env")" "B=x" "Unterminated quote does not swallow following keys"
assert_eq "1" "$(grep -c '^B=x$' "$d2/.env")" "Unterminated quote does not duplicate following keys"
d3="$(newdir t5c)"
python3 -c 'print("BIG_KEY=\"" + "A"*70000 + "\"")' > "$d3/.env.example"
(cd "$d3" && "$SCRIPT" -q --no-backup)
assert_eq "1" "$(grep -c '^BIG_KEY=' "$d3/.env")" ">64KB single-line value syncs without error"

# ------------------------------------------------------------------------------
printf "\nTest 5b: Common dotenv syntax and opaque-line preservation\n"
d_fmt="$(newdir format)"
printf '\357\273\277' > "$d_fmt/.env.example"
printf '# BOM\r\n_LOWER_key=default\nDOT.KEY-2=default\nEMPTY\nSINGLE_ESCAPED=\047it\\\047s literal\047\nBACKTICK=`literal $HOME`\n' >> "$d_fmt/.env.example"
printf '\357\273\277' > "$d_fmt/.env"
printf '# BOM\r\n_LOWER_key=live\nDOT.KEY-2=\nEMPTY\n# external-tool comment\n${OPAQUE_DIRECTIVE}\n' >> "$d_fmt/.env"
(cd "$d_fmt" && "$SCRIPT" -q --no-backup)
fmt_out="$(cat "$d_fmt/.env")"
assert_contains "$fmt_out" "_LOWER_key=live" "Underscore-prefixed keys remain recognized"
assert_contains "$fmt_out" "DOT.KEY-2=" "Keys with dots and dashes remain recognized"
assert_contains "$fmt_out" "EMPTY" "KEY without equals is recognized as empty"
assert_contains "$fmt_out" '${OPAQUE_DIRECTIVE}' "Unknown dotenv lines are preserved"
assert_contains "$fmt_out" "SINGLE_ESCAPED='it\\'s literal'" "Escaped quote in single quotes parses"
assert_contains "$fmt_out" 'BACKTICK=`literal $HOME`' "Backtick-quoted values parse literally"

# ------------------------------------------------------------------------------
printf "\nTest 6: Duplicates, dropped lines and attached comments\n"
d="$(newdir t6)"
printf 'A=1\nA=2\nB=\n' > "$d/.env.example"
cat > "$d/.env" <<'EOF'
A=first
A=last
# rotate monthly
B=secret
not a key line
# note about extra
EXTRA=1
EOF
err="$(cd "$d" && "$SCRIPT" --no-backup 2>&1)"
m="$(cat "$d/.env")"
assert_eq "1" "$(grep -c '^A=' "$d/.env")" "Duplicate template key emitted once"
assert_contains "$m" "A=last" "Duplicate target key: last occurrence wins"
assert_contains "$err" "duplicate key A (ignored" "Template duplicate warned"
assert_contains "$err" "duplicate key A (last occurrence wins)" "Target duplicate warned"
assert_contains "$err" "line 5: not a KEY=VALUE line" "Malformed line preservation warned"
assert_contains "$err" "line 3: comment not in template" "Unattached comment drop warned"
assert_contains "$m" "# note about extra
EXTRA=1" "Comment attached to extra key travels with it"
assert_contains "$m" "# Unparsed content preserved from .env
not a key line" "Malformed target content preserved"
assert_not_contains "$err" "secret" "Warnings never contain values"

# ------------------------------------------------------------------------------
printf "\nTest 7: Dry-run shows only key names\n"
d="$(newdir t7)"
printf 'EXISTING_KEY=default_secret\nADDED_KEY_1=super_secret_default_1\nADDED_KEY_2=x\n' > "$d/.env.example"
printf 'EXISTING_KEY=my_live_secret_value\nDEPRECATED_KEY=old_secret_value\n' > "$d/.env"
orig="$(cat "$d/.env")"
out="$(cd "$d" && "$SCRIPT" --dry-run --prune 2>&1)"
assert_contains "$out" "+ ADDED_KEY_1" "Lists added key"
assert_contains "$out" "- DEPRECATED_KEY" "Lists pruned key"
assert_not_contains "$out" "super_secret_default_1" "No template values"
assert_not_contains "$out" "my_live_secret_value" "No .env values"
assert_eq "$orig" "$(cat "$d/.env")" "Dry-run does not modify .env"
d2="$(newdir t7b)"; printf 'A=1\n' > "$d2/.env.example"
out="$(cd "$d2" && "$SCRIPT" -n 2>&1)"
assert_contains "$out" "would be created" "Dry-run reports file creation"

# ------------------------------------------------------------------------------
printf "\nTest 8: --prune and --no-backup\n"
(cd "$d" && "$SCRIPT" -q --prune --no-backup)
m="$(cat "$d/.env")"
assert_contains "$m" "EXISTING_KEY=my_live_secret_value" "Keeps existing key"
assert_not_contains "$m" "DEPRECATED_KEY" "Prunes extra key"
assert_eq "0" "$(count_files "$d" ".env.bak.*")" "No backup with --no-backup"

# ------------------------------------------------------------------------------
printf "\nTest 9: Interactive mode and safe quoting\n"
d="$(newdir t9)"
printf 'KEEP_ME=default\nEMPTY_KEY=fallback_default\nBRAND_NEW=initial\nDOLLAR=\nQUOTE=\nTAB=\n' > "$d/.env.example"
printf 'KEEP_ME=already_set\nEMPTY_KEY=\n' > "$d/.env"
printf '\ncustom value\npa$word\nit'"'"'s $HOME\na\tb\n' | (cd "$d" && "$SCRIPT" -q sync -i --no-backup 2>/dev/null)
m="$(cat "$d/.env")"
assert_contains "$m" "KEEP_ME=already_set" "Non-empty key not prompted/overwritten"
assert_contains "$m" "EMPTY_KEY=fallback_default" "Enter accepts template default"
assert_contains "$m" "BRAND_NEW='custom value'" "Spaces single-quoted"
assert_contains "$m" "DOLLAR='pa\$word'" "\$ single-quoted (no expansion)"
assert_contains "$m" "QUOTE=\"it's \\\$HOME\"" "Value with ' escapes shell expansion inside double quotes"
assert_eq "it's \$HOME" "$(sh -c '. "$1"; printf "%s" "$QUOTE"' sh "$d/.env")" "Interactive value stays literal when sourced"
assert_contains "$m" "TAB='a	b'" "Value containing a TAB stored intact"
d_eof="$(newdir t9eof)"
printf 'EMPTY_IN_ENV=template_default\n' > "$d_eof/.env.example"
printf 'EMPTY_IN_ENV=\n' > "$d_eof/.env"
(cd "$d_eof" && "$SCRIPT" -q -i --no-backup < /dev/null 2>/dev/null)
assert_eq "EMPTY_IN_ENV=" "$(cat "$d_eof/.env")" "Interactive EOF (< /dev/null) does not overwrite existing empty key"

# ------------------------------------------------------------------------------
printf "\nTest 10: check / status\n"
d="$(newdir t10)"
printf 'REQ_KEY_1=a\nREQ_KEY_2=b\n' > "$d/.env.example"
printf 'REQ_KEY_1=1\n' > "$d/.env"
set +e
out="$(cd "$d" && "$SCRIPT" check -q 2>&1)"; rc=$?
set -e
assert_eq "1" "$rc" "check exits 1 when keys missing"
assert_contains "$out" "REQ_KEY_2" "Missing key reported even with -q"
(cd "$d" && "$SCRIPT" -q --no-backup)
set +e
(cd "$d" && "$SCRIPT" status -q); rc=$?
set -e
assert_eq "0" "$rc" "status exits 0 when in sync"

# ------------------------------------------------------------------------------
printf "\nTest 11: restore and clean-backups\n"
d="$(newdir t11)"
printf 'A=1\nB=2\n' > "$d/custom.example"
printf 'A=original_a\n' > "$d/custom.env"
"$SCRIPT" -q -s "$d/custom.example" -t "$d/custom.env"
assert_contains "$(cat "$d/custom.env")" "B=2" "Custom -s/-t flags work"
assert_eq "1" "$(count_files "$d" "custom.env.bak.*")" "One backup after sync"
"$SCRIPT" -q restore -t "$d/custom.env"
assert_eq "A=original_a" "$(cat "$d/custom.env")" "restore reverts to latest backup"
assert_eq "2" "$(count_files "$d" "custom.env.bak.*")" "restore backs up current state first"
"$SCRIPT" -q restore -t "$d/custom.env"
assert_contains "$(cat "$d/custom.env")" "B=2" "Second restore undoes the first"
for i in 1 2 3 4 5 6 7; do printf 'x\n' > "$d/custom.env.bak.20200101-00000$i"; done
"$SCRIPT" -q clean-backups -t "$d/custom.env"
assert_eq "5" "$(count_files "$d" "custom.env.bak.*")" "clean-backups keeps 5 by default"
"$SCRIPT" -q clean-backups -t "$d/custom.env" --keep 1
remaining="$(basename "$(ls "$d"/custom.env.bak.*)")"
assert_eq "1" "$(count_files "$d" "custom.env.bak.*")" "--keep 1 leaves one backup"
assert_not_contains "$remaining" "20200101" "Newest backup is the one kept"
"$SCRIPT" -q clean-backups -t "$d/custom.env" --all
assert_eq "0" "$(count_files "$d" "custom.env.bak.*")" "--all removes every backup"

# ------------------------------------------------------------------------------
printf "\nTest 12: Symlinks, CRLF, atomic write, long paths\n"
d="$(newdir t12)"
printf 'A=1\nB=2\n' > "$d/.env.example"
printf 'A=9\n' > "$d/real.env"
ln -s real.env "$d/.env"
(cd "$d" && "$SCRIPT" -q --no-backup)
assert_eq "0" "$([ -L "$d/.env" ] && echo 0 || echo 1)" "Symlinked .env stays a symlink"
assert_contains "$(cat "$d/real.env")" "B=2" "Symlink target updated"
assert_eq "0" "$(count_files "$d" ".*.tmp.*")" "No temp files left behind"
d2="$(newdir t12b)"
printf 'A=1\r\nB=2\r\n' > "$d2/.env.example"
printf 'A=x\r\n' > "$d2/.env"
(cd "$d2" && "$SCRIPT" -q --no-backup)
assert_eq "2" "$(grep -c "$(printf '\r')\$" "$d2/.env")" "CRLF line endings preserved"
out="$(cd "$d2" && "$SCRIPT" 2>/dev/null)"
assert_contains "$out" "already up to date" "CRLF file is idempotent"
long="$TEST_ROOT/a/very/long/directory/name/used/for/testing/the/header/box"
mkdir -p "$long"; printf 'A=1\n' > "$long/.env.example"
err="$("$SCRIPT" check -s "$long/.env.example" -t "$long/.env.example" 2>&1 >/dev/null)"
bad="$(printf "%s\n" "$err" | awk '/^\|/ && length($0) != 64' | wc -l | tr -d ' ')"
assert_eq "0" "$bad" "Header box lines stay 64 chars with long paths"

# ------------------------------------------------------------------------------
printf "\nTest 13: Ctrl-C aborts instead of continuing\n"
d_sig="$(newdir tsig)"
printf 'A=\n' > "$d_sig/.env.example"
mkfifo "$d_sig/fifo"
(sleep 5 > "$d_sig/fifo" &)
"$SCRIPT" -i -s "$d_sig/.env.example" -t "$d_sig/.env" < "$d_sig/fifo" >/dev/null 2>&1 &
sig_pid=$!
sleep 0.2
kill -INT "$sig_pid" 2>/dev/null || true
set +e
wait "$sig_pid"
sig_rc=$?
set -e
assert_eq "130" "$sig_rc" "SIGINT terminates process with exit code 130"
assert_contains "$(cat "$REPO_ROOT/main.go")" "os.Interrupt" "Binary handles SIGINT cleanly"

# ------------------------------------------------------------------------------
printf "\nTest 14: install, update (verified, atomic) and uninstall\n"
bin="$TEST_ROOT/bin"
"$SCRIPT" -q install --dir "$bin"
assert_eq "0" "$([ -x "$bin/env-updater" ] && echo 0 || echo 1)" "install creates executable"
assert_eq "-rwxr-xr-x" "$(ls -l "$bin/env-updater" | cut -c1-10)" "Installed binary is 0755"

remote="$TEST_ROOT/remote/env-updater"
mkdir -p "$(dirname "$remote")"
(cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags '-X main.Version=0.0.1 -X main.VersionMarker=@env-updater-version:0.0.1@ -s -w' -o "$remote" .)
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi; }

echo "0000  env-updater" > "$remote.sha256"
set +e
out="$(ENV_UPDATER_REPO_URL="file://$remote" "$bin/env-updater" -q update 2>&1)"; rc=$?
set -e
assert_eq "1" "$rc" "update refuses on checksum mismatch"
assert_contains "$out" "Checksum mismatch" "Checksum mismatch reported"

printf "%s  env-updater\n" "$(sha "$remote")" > "$remote.sha256"
set +e
out="$(ENV_UPDATER_REPO_URL="file://$remote" "$bin/env-updater" -q update 2>&1)"; rc=$?
set -e
assert_eq "1" "$rc" "update refuses downgrade"
assert_contains "$out" "older than local" "Downgrade refusal explained"

(cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags '-X main.Version=99.0.0 -X main.VersionMarker=@env-updater-version:99.0.0@ -s -w' -o "$remote" .)
printf "%s  env-updater\n" "$(sha "$remote")" > "$remote.sha256"
out="$(ENV_UPDATER_REPO_URL="file://$remote" "$bin/env-updater" -q update 2>&1)"
assert_not_contains "$out" "INJECTED" "Running script is not overwritten in place"
assert_contains "$("$bin/env-updater" --version 2>/dev/null)" "v99.0.0" "update installs new version"

gitdir="$(newdir gitclone)"; mkdir "$gitdir/.git"; cp "$REAL_SCRIPT" "$gitdir/env-updater"
set +e
out="$(ENV_UPDATER_REPO_URL="file://$remote" "$gitdir/env-updater" -q update 2>&1)"; rc=$?
set -e
assert_eq "1" "$rc" "update refuses inside a git work tree"

other="$(newdir otherbin)"; printf '#!/bin/sh\necho other\n' > "$other/env-updater"
set +e
"$SCRIPT" -q uninstall --dir "$other" 2>/dev/null; rc=$?
set -e
assert_eq "1" "$rc" "uninstall refuses a non-env-updater binary"
assert_eq "0" "$([ -e "$other/env-updater" ] && echo 0 || echo 1)" "Unrelated binary left untouched"
"$SCRIPT" -q uninstall --dir "$bin"
assert_eq "0" "$([ ! -e "$bin/env-updater" ] && echo 0 || echo 1)" "uninstall removes installed binary"

# ------------------------------------------------------------------------------
printf "\nTest 15: Published checksum matches script\n"
if [ -f "$REPO_ROOT/env-updater.sha256" ]; then
    assert_eq "$(sha "$REAL_SCRIPT")" "$(awk '{print $1}' "$REPO_ROOT/env-updater.sha256")" "env-updater.sha256 is current (run scripts/release.sh)"
else
    fail "env-updater.sha256 missing (run scripts/release.sh)"
fi

# ------------------------------------------------------------------------------
printf "\nTest 16: Standalone installer binary and source paths\n"
installer_tmp="$(newdir installer)"
installer_mock="$installer_tmp/mock-bin"
VERSION="$(tr -d ' \t\r\n' < "$REPO_ROOT/VERSION")"
sha() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; else shasum -a 256 "$1" | awk '{print $1}'; fi
}
mkdir -p "$installer_mock"
cat > "$installer_mock/curl" <<'EOF'
#!/bin/sh
set -eu
output=
url=
while [ $# -gt 0 ]; do
    case "$1" in
        -o) shift; output=$1 ;;
        -*) ;;
        *) url=$1 ;;
    esac
    shift
done
case "$url" in
    *.sha256) cp "$TEST_INSTALL_CHECKSUM" "$output" ;;
    *) cp "$TEST_INSTALL_BINARY" "$output" ;;
esac
EOF
cat > "$installer_mock/go" <<'EOF'
#!/bin/sh
set -eu
if [ "$TEST_INSTALL_BINARY" != "$GOBIN/env-updater" ]; then
    cp "$TEST_INSTALL_BINARY" "$GOBIN/env-updater"
fi
chmod 0755 "$GOBIN/env-updater"
EOF
chmod 0755 "$installer_mock/curl" "$installer_mock/go"
asset="env-updater-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)"
case "$asset" in
    env-updater-linux-x86_64) asset=env-updater-linux-amd64 ;;
    env-updater-linux-aarch64) asset=env-updater-linux-arm64 ;;
    env-updater-darwin-x86_64) asset=env-updater-darwin-amd64 ;;
    env-updater-darwin-aarch64) asset=env-updater-darwin-arm64 ;;
esac
TEST_INSTALL_BINARY="$REPO_ROOT/env-updater"; export TEST_INSTALL_BINARY
printf '%s  %s\n' "$(sha "$TEST_INSTALL_BINARY")" "$asset" > "$installer_tmp/binary.sha256"
TEST_INSTALL_CHECKSUM="$installer_tmp/binary.sha256"; export TEST_INSTALL_CHECKSUM
PATH="$installer_mock:$PATH"; export PATH
sh "$REPO_ROOT/install.sh" --binary --dir "$installer_tmp/prebuilt" --yes >/dev/null
assert_eq "0" "$([ -x "$installer_tmp/prebuilt/env-updater" ] && echo 0 || echo 1)" "Installer downloads and installs verified binary"
assert_eq "$("$installer_tmp/prebuilt/env-updater" --version --no-header)" "env-updater v$VERSION" "Installed prebuilt executable runs"
printf '%s  %s\n' "0000" "$asset" > "$installer_tmp/bad.sha256"
TEST_INSTALL_CHECKSUM="$installer_tmp/bad.sha256"; export TEST_INSTALL_CHECKSUM
set +e
sh "$REPO_ROOT/install.sh" --binary --dir "$installer_tmp/rejected" --yes >/dev/null 2>&1
installer_rc=$?
set -e
assert_eq "1" "$installer_rc" "Installer rejects a checksum mismatch"
assert_eq "1" "$([ ! -e "$installer_tmp/rejected/env-updater" ] && echo 1 || echo 0)" "Checksum failure leaves no installed binary"
TEST_INSTALL_CHECKSUM="$installer_tmp/binary.sha256"; export TEST_INSTALL_CHECKSUM
sh "$REPO_ROOT/install.sh" --source --dir "$installer_tmp/source" --yes >/dev/null
assert_eq "0" "$([ -x "$installer_tmp/source/env-updater" ] && echo 0 || echo 1)" "Installer builds and installs from source"
assert_eq "$("$installer_tmp/source/env-updater" --version --no-header)" "env-updater v$VERSION" "Installed source executable runs"

# ------------------------------------------------------------------------------
printf "\n========================================\n"
printf "Tests passed: %d\n" "$PASS_COUNT"
printf "Tests failed: %d\n" "$FAIL_COUNT"
printf "========================================\n"

if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
fi
