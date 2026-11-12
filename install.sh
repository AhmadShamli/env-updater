#!/bin/sh
# Interactive installer for env-updater. Supports verified release binaries or
# a source build using the local Go toolchain.

set -eu

REPO="https://github.com/AhmadShamli/env-updater"
RELEASES="$REPO/releases/latest/download"
INSTALL_DIR=""
METHOD=""
NONINTERACTIVE=0

usage() {
    printf 'Usage: %s [--binary|--source] [--dir DIRECTORY] [--yes]\n' "$0"
}

fail() {
    printf 'Error: %s\n' "$*" >&2
    exit 1
}

while [ $# -gt 0 ]; do
    case "$1" in
        --binary) METHOD=binary ;;
        --source) METHOD=source ;;
        --dir)
            [ $# -ge 2 ] || fail "--dir requires a path"
            INSTALL_DIR=$2
            shift
            ;;
        --yes|-y) NONINTERACTIVE=1 ;;
        -h|--help) usage; exit 0 ;;
        *) usage >&2; fail "unknown option: $1" ;;
    esac
    shift
done

command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || fail "curl or wget is required"

fetch() {
    url=$1
    output=$2
    if command -v curl >/dev/null 2>&1; then
        curl --fail --location --silent --show-error "$url" -o "$output"
    else
        wget -q "$url" -O "$output"
    fi
}

sha256() {
    file=$1
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$file" | awk '{print $1}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$file" | awk '{print $NF}'
    else
        fail "sha256sum, shasum or openssl is required to verify downloads"
    fi
}

if [ -z "$METHOD" ]; then
    if [ "$NONINTERACTIVE" -eq 1 ]; then
        fail "choose --binary or --source when using --yes"
    fi
    printf 'Install env-updater using:\n  1) Prebuilt release binary (SHA-256 verified)\n  2) Build from source (requires Go)\nChoose [1/2]: '
    if [ -r /dev/tty ]; then
        IFS= read -r choice </dev/tty || fail "could not read choice"
    else
        IFS= read -r choice || fail "could not read choice"
    fi
    case "$choice" in
        1) METHOD=binary ;;
        2) METHOD=source ;;
        *) fail "choose 1 or 2" ;;
    esac
fi

if [ -z "$INSTALL_DIR" ]; then
    if [ "$(id -u)" -eq 0 ]; then
        INSTALL_DIR=/usr/local/bin
    elif [ -n "${HOME:-}" ]; then
        INSTALL_DIR=$HOME/.local/bin
    else
        fail "cannot determine installation directory; pass --dir"
    fi
fi

case "$INSTALL_DIR" in
    /*) ;;
    *) INSTALL_DIR="$(pwd)/$INSTALL_DIR" ;;
esac

case "$(uname -s)" in
    Linux) GOOS=linux ;;
    Darwin) GOOS=darwin ;;
    *) fail "unsupported operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
    x86_64|amd64) GOARCH=amd64 ;;
    aarch64|arm64) GOARCH=arm64 ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
esac

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/env-updater-install.XXXXXX") || fail "cannot create temporary directory"
install_tmp=
cleanup() {
    rm -rf "$tmpdir"
    [ -z "$install_tmp" ] || rm -f "$install_tmp"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
binary="$tmpdir/env-updater"

case "$METHOD" in
    binary)
        asset="env-updater-${GOOS}-${GOARCH}"
        printf 'Downloading %s/%s release binary...\n' "$GOOS" "$GOARCH"
        fetch "$RELEASES/$asset" "$binary" || fail "download failed; confirm a release exists for $asset"
        fetch "$RELEASES/$asset.sha256" "$tmpdir/checksum" || fail "could not download release checksum"
        expected=$(awk 'NR == 1 {print $1}' "$tmpdir/checksum")
        actual=$(sha256 "$binary")
        [ -n "$expected" ] && [ "$expected" = "$actual" ] || fail "SHA-256 verification failed"
        chmod 0755 "$binary"
        ;;
    source)
        command -v go >/dev/null 2>&1 || fail "Go is required to build from source"
        printf 'Building env-updater from source with Go...\n'
        GOBIN="$tmpdir" go install "$REPO@latest" || fail "source installation failed"
        if [ -x "$tmpdir/env-updater" ]; then
            [ "$tmpdir/env-updater" = "$binary" ] || cp "$tmpdir/env-updater" "$binary"
        elif [ -x "$tmpdir/env-updater.exe" ]; then
            cp "$tmpdir/env-updater.exe" "$binary"
        else
            fail "Go did not produce the env-updater executable"
        fi
        chmod 0755 "$binary"
        ;;
esac

mkdir -p "$INSTALL_DIR" || fail "cannot create install directory: $INSTALL_DIR"
destination="$INSTALL_DIR/env-updater"
install_tmp=$(mktemp "$INSTALL_DIR/.env-updater.install.XXXXXX") || fail "cannot create temporary file in $INSTALL_DIR"
cp "$binary" "$install_tmp" || fail "cannot copy binary into $INSTALL_DIR"
chmod 0755 "$install_tmp"
mv -f "$install_tmp" "$destination" || fail "cannot install into $INSTALL_DIR (check permissions)"

printf 'Installed env-updater to %s\n' "$destination"
case ":${PATH:-}:" in
    *":$INSTALL_DIR:"*) ;;
    *) printf 'Add this directory to PATH:\n  export PATH="%s:$PATH"\n' "$INSTALL_DIR" ;;
esac
printf 'Run: %s --version\n' "$destination"
