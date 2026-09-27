#!/usr/bin/env bash
# bash release.sh v1.1.0 [notes.md] - builds nothing, it publishes what build.sh left in dist/.
set -euo pipefail
cd "$(dirname "$0")"
VER=${1:?usage: release.sh v1.1.0 [notes.md]}
shift
NOTES=""
[ $# -gt 0 ] && NOTES="--notes $1"
python3 publish.py "$VER" dist/tandem-setup.exe "dist/Tandem-${VER#v}.zip" $NOTES
