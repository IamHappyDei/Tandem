#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
export PATH="/e/go/bin:$PATH" GOTOOLCHAIN=local GOFLAGS=-trimpath
LDFLAGS="-s -w"
VER=$(grep -m1 'Version *=' internal/ui/app.go | sed 's/.*"\(.*\)".*/\1/')

go build -ldflags="$LDFLAGS" -o dist/tandem.exe ./cmd/tandem
go build -ldflags="$LDFLAGS" -o dist/tandem-setup.exe ./cmd/tandem-setup

cp ../GUIDE.md ../README.md dist/
cp tools/icon/tandem.ico dist/tandem.ico
cp packaging/allow-in-firewall.bat dist/

for f in tandem.exe tandem-setup.exe tandem.ico GUIDE.md README.md allow-in-firewall.bat; do
  [ -f "dist/$f" ] || { echo "dist/$f missing - the archive would be broken"; exit 1; }
done

rm -f "dist/Tandem-$VER.zip"
files=$(for f in tandem.exe tandem-setup.exe tandem.ico GUIDE.md README.md allow-in-firewall.bat; do printf '%s,' "$(cygpath -w "dist/$f")"; done)
powershell -NoProfile -Command "Compress-Archive -Force -Path ${files%,} -DestinationPath '$(cygpath -w "dist/Tandem-$VER.zip")'"
ls -la "dist/Tandem-$VER.zip"

if [ "${1:-}" = "--test" ]; then
  go test ./... 2>&1 | grep -v "no test files" || true
fi
