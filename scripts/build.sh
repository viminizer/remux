#!/usr/bin/env bash
#
# Builds remux into one file: the Vite UI is compiled into the Go binary, so
# bin/remux has no runtime dependency and nothing to install beside it.
#
#   ./scripts/build.sh          full build (UI + binary)
#   SKIP_UI=1 ./scripts/build.sh   Go only, reusing the last UI build
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT="bin/remux"

if [ "${SKIP_UI:-0}" != "1" ]; then
  echo "==> building the UI"
  (cd web && npm run build)
fi

# go:embed cannot reach outside its own package directory, so the Vite output
# is staged into internal/web/dist rather than embedded from web/dist directly.
echo "==> staging the UI into internal/web/dist"
rm -rf internal/web/dist
mkdir -p internal/web
cp -R web/dist internal/web/dist

echo "==> building $OUT ($VERSION)"
mkdir -p bin
# -s -w strips the symbol table and DWARF. Measured on this Mac: a tsnet
# binary is 31 MB plain and 21 MB stripped, so this is not optional.
go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o "$OUT" .

echo
echo "    $OUT   $(du -h "$OUT" | cut -f1)   $VERSION"
echo
