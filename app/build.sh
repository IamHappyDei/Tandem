#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
export PATH="/e/go/bin:$PATH" GOTOOLCHAIN=local GOFLAGS=-trimpath
LDFLAGS="-s -w"
VER=$(grep -m1 'Version *=' internal/ui/app.go | tr -d '"' | awk '{print $NF}')

go build -ldflags="$LDFLAGS" -o dist/tandem.exe ./cmd/tandem

cp ../GUIDE.md ../README.md dist/
cp packaging/tandem.ico dist/tandem.ico
cp packaging/allow-in-firewall.bat dist/

stage=cmd/tandem-setup/payload
rm -rf "$stage" && mkdir -p "$stage"
touch "$stage/.keep"
cp dist/tandem.exe dist/tandem.ico dist/GUIDE.md dist/README.md dist/allow-in-firewall.bat "$stage/"
for f in tandem.exe tandem.ico GUIDE.md README.md allow-in-firewall.bat; do
  [ -f "$stage/$f" ] || { echo "$stage/$f missing - the installer would ship incomplete"; exit 1; }
done

for d in cmd/tandem cmd/tandem-setup; do
  PATH="/e/go/bin:$PATH" GOTOOLCHAIN=local go run github.com/akavel/rsrc@latest     -ico packaging/tandem.ico -manifest "$d/manifest.xml" -o "$d/resource.syso" >/dev/null
done

go build -ldflags="$LDFLAGS" -o dist/tandem-setup.exe ./cmd/tandem-setup

rm -rf dist/stage
mkdir -p dist/stage
cp dist/tandem-setup.exe dist/stage/tandem-setup.exe

files=$(cygpath -w "$(pwd)/dist/stage/tandem-setup.exe")
powershell -NoProfile -Command "Compress-Archive -Force -Path $files -DestinationPath '$(cygpath -w "dist/Tandem-$VER.zip")'"
ls -la "dist/Tandem-$VER.zip" "dist/tandem-setup.exe" "dist/tandem.exe"

if [ "${1:-}" = "--test" ]; then
  go test ./... 2>&1 | grep -v "no test files" || true
fi
