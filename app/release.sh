#!/usr/bin/env bash
# Publish what build.sh produced as a GitHub Release, so tandem update can offer it.
#   bash release.sh v1.1.0 ["notes file"]
# The tag must exist and be pushed. The token is whatever git already uses - nothing is
# stored, nothing is printed.
set -euo pipefail
cd "$(dirname "$0")"
VER=${1:?usage: release.sh v1.1.0 [notesfile]}
NOTES=${2:-}
TOKEN=$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill | sed -n 's/^password=//p')
REPO=$(git remote get-url origin | sed -E 's#.*github.com[:/](.*)\.git#\1#')
[ -f "dist/tandem-setup.exe" ] || { echo "run build.sh first - dist/tandem-setup.exe is missing"; exit 1; }

if [ ! -f "$NOTES" ]; then
  NOTES=$(mktemp)
  printf '%s\n\nBuilt from %s.\n' "$VER" "$(git rev-parse --short HEAD)" > "$NOTES"
fi

ID=$(VER="$VER" REPO="$REPO" TOKEN="$TOKEN" NOTES="$NOTES" python3 - <<'PY'
import json, os, urllib.request
body = open(os.environ["NOTES"], encoding="utf-8").read()
req = urllib.request.Request(
    "https://api.github.com/repos/%s/releases" % os.environ["REPO"],
    data=json.dumps({"tag_name": os.environ["VER"], "name": "Tandem " + os.environ["VER"].lstrip("v"),
                     "body": body, "draft": False, "prerelease": False}).encode(),
    headers={"Authorization": "Bearer " + os.environ["TOKEN"], "Accept": "application/vnd.github+json",
             "Content-Type": "application/json", "User-Agent": "tandem-release"})
print(json.load(urllib.request.urlopen(req))["id"])
PY
)
echo "release $ID for $VER"

for f in dist/tandem-setup.exe "dist/Tandem-${VER#v}.zip"; do
  [ -f "$f" ] || { echo "   (skipping $f - not there)"; continue; }
  NAME=$(basename "$f")
  code=$(curl -s --max-time 600 -o .up.json -w "%{http_code}" -X POST \
    -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/octet-stream" \
    -H "Accept: application/vnd.github+json" --data-binary "@$f" \
    "https://uploads.github.com/repos/$REPO/releases/$ID/assets?name=$NAME")
  [ "$code" = 201 ] || { echo "   $NAME failed with http $code: $(cat .up.json)"; exit 1; }
  echo "   $NAME $(python3 -c "import json;print(json.load(open('.up.json')).get('digest'))")"
done
rm -f .up.json
case "$NOTES" in /tmp/*|/tmp*|*"Temp"*) rm -f "$NOTES";; esac
echo "https://github.com/$REPO/releases/tag/$VER"
