#!/usr/bin/env bash
#
# Builds remux into one file: the Vite UI is compiled into the Go binary, so
# bin/remux has no runtime dependency and nothing to install beside it.
#
#   ./scripts/build.sh          full build (UI + binary)
#   SKIP_UI=1 ./scripts/build.sh   Go only, reusing the last UI build
set -euo pipefail

cd "$(dirname "$0")/.."

# --match keeps this to real release tags, and dropping --always means a repo
# with no tags falls back to a readable placeholder instead of a bare commit
# hash. A hash is not something you can compare across two installs or quote in
# a bug report, and this string is not internal - it reaches Settings -> About.
#
# git describe yields valid semver in every state once the leading v is gone:
# on the tag "0.1.0", past it "0.1.0-3-gabc123", with local edits
# "...-dirty". Hyphens are legal inside a semver prerelease identifier.
VERSION="${VERSION:-$(git describe --tags --match 'v[0-9]*' --dirty 2>/dev/null || echo v0.0.0-dev)}"
VERSION="${VERSION#v}"
OUT="bin/remux"

if [ "${SKIP_UI:-0}" != "1" ]; then
  echo "==> building the UI"
  # Exported so vite.config.ts can bake it into the bundle as
  # __BUILD_VERSION__. The app compares that against the version the server
  # reports at /api/health to tell an upgrade from a pointless reload.
  (cd web && VERSION="$VERSION" npm run build)
fi

# go:embed cannot reach outside its own package directory, so the Vite output
# is staged into internal/web/dist rather than embedded from web/dist directly.
echo "==> staging the UI into internal/web/dist"
rm -rf internal/web/dist
mkdir -p internal/web
cp -R web/dist internal/web/dist
# Only .gitkeep is committed here; go:embed needs the directory to exist on a
# fresh clone, but not to hold a real build.
touch internal/web/dist/.gitkeep

# Stamp the version into the service worker's cache key.
#
# The key used to be a hardcoded "remux-shell-v1", which made the cleanup in
# the activate handler a no-op: it deletes every cache whose name is not the
# current one, and the name never changed between builds. Worse, a UI-only
# change left sw.js byte-identical, so the browser never saw a new worker and
# never ran activate at all. Every deploy added another content-hashed
# index-<hash>.js to the same cache and nothing ever removed the old ones.
#
# Substituting here rather than at Vite time means the file the browser
# compares is different on every build, which is what triggers the reinstall.
# No sed -i, so this stays portable to BSD sed.
sed "s/__VERSION__/$VERSION/" web/dist/sw.js > internal/web/dist/sw.js

echo "==> building $OUT ($VERSION)"
mkdir -p bin
# -s -w strips the symbol table and DWARF. Measured on this Mac: a tsnet
# binary is 31 MB plain and 21 MB stripped, so this is not optional.
go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$OUT" .

echo
echo "    $OUT   $(du -h "$OUT" | cut -f1)   $VERSION"
echo
