#!/bin/sh
# ==============================================================================
# release.sh - build static Go binary, stamp version, and regenerate checksum.
#
#   scripts/release.sh            # build platform assets and regenerate checksums
#   scripts/release.sh 1.2.0      # set version everywhere, build & regenerate checksum
#
# The VERSION file is the single source of truth; this script stamps it into
# main.go, builds the static Go binary (CGO_ENABLED=0), and updates env-updater.sha256.
# ==============================================================================

set -eu

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/env-updater"

if [ $# -gt 1 ]; then
    echo "Usage: $0 [VERSION]" >&2
    exit 1
fi

if [ $# -eq 1 ]; then
    version="$1"
else
    version="$(tr -d ' \t\r\n' < "$ROOT/VERSION")"
fi

if ! printf '%s\n' "$version" | LC_ALL=C grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
    echo "Error: version must be MAJOR.MINOR.PATCH with optional prerelease/build metadata" >&2
    exit 1
fi
printf '%s\n' "$version" > "$ROOT/VERSION"

# Update source version strings; VersionMarker is injected at link time.
stamp="$(mktemp "$ROOT/.release-stamp.XXXXXX")"
trap 'rm -f "$stamp"' EXIT HUP INT TERM
sed "s/Version        = \"[^\"]*\"/Version        = \"${version}\"/" "$ROOT/main.go" > "$stamp"
cat "$stamp" > "$ROOT/main.go"
sed "s/^VERSION=\"[^\"]*\"/VERSION=\"${version}\"/" "$ROOT/scripts/env-updater.sh" > "$stamp"
cat "$stamp" > "$ROOT/scripts/env-updater.sh"

# Build static, stripped Go binary (runs in scratch / Alpine / distroless containers)
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-X main.Version=${version} -X main.VersionMarker=@env-updater-version:${version}@ -s -w" -o "$BIN" .)

sha() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}
sum="$(sha "$BIN")"
printf "%s  env-updater\n" "$sum" > "$ROOT/env-updater.sha256"
printf '%s  env-updater.sh\n' "$(sha "$ROOT/scripts/env-updater.sh")" > "$ROOT/scripts/env-updater.sh.sha256"

mkdir -p "$ROOT/dist"
for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
    goos="${platform%/*}"
    goarch="${platform#*/}"
    name="env-updater-${goos}-${goarch}"
    if [ "$goos" = windows ]; then name="${name}.exe"; fi
    (cd "$ROOT" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags "-X main.Version=${version} -X main.VersionMarker=@env-updater-version:${version}@ -s -w" -o "$ROOT/dist/$name" .)
    printf '%s  %s\n' "$(sha "$ROOT/dist/$name")" "$name" > "$ROOT/dist/$name.sha256"
done

echo "env-updater v${version} built successfully (static binary)"
echo "sha256: ${sum}"
echo "Platform release assets and checksums are in $ROOT/dist"
