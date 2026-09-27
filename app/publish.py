#!/usr/bin/env python3
"""Publish what build.sh produced as a GitHub Release, so `tandem update` can offer it.

    python3 publish.py v1.1.0 dist/tandem-setup.exe dist/Tandem-1.1.0.zip --notes notes.md

The tag must exist and be pushed. The token is whatever git already uses (git credential
fill); nothing is stored and nothing is printed. Re-running replaces the files of the same
name and leaves the release alone, so a corrected build can be published without a new tag.
"""
import json
import os
import subprocess
import sys
import urllib.error
import urllib.request

API = "https://api.github.com"


class NotFound(Exception):
    pass
UPLOADS = "https://uploads.github.com"


def die(msg):
    sys.exit(msg)


def repo_slug():
    url = subprocess.check_output(["git", "remote", "get-url", "origin"], text=True).strip()
    if "github.com" not in url:
        die("origin is not a github repository: " + url)
    return url.split("github.com", 1)[1].lstrip(":/").removesuffix(".git")


def token():
    out = subprocess.run(["git", "credential", "fill"], input="protocol=https\nhost=github.com\n\n",
                         capture_output=True, text=True, check=True).stdout
    for line in out.splitlines():
        if line.startswith("password="):
            return line[9:]
    die("git has no credential for github.com - push once and try again")


class Hub:
    def __init__(self, tok, repo):
        self.tok, self.repo = tok, repo

    def req(self, path, method="GET", data=None, ctype="application/json", base=API, allow404=False):
        r = urllib.request.Request(base + path, data=data, method=method)
        r.add_header("Authorization", "Bearer " + self.tok)
        r.add_header("Accept", "application/vnd.github+json")
        r.add_header("User-Agent", "tandem-release")
        if data is not None:
            r.add_header("Content-Type", ctype)
        try:
            with urllib.request.urlopen(r, timeout=600) as res:
                raw = res.read()
            return json.loads(raw) if raw.strip() else {}
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", "replace")[:400]
            if e.code == 404:
                raise NotFound(body)
            die("github said %s for %s\n%s" % (e.code, path, body))


def main():
    args = [a for a in sys.argv[1:]]
    if not args:
        die(__doc__)
    tag = args[0]
    notes, files, skip = None, [], False
    for a in args[1:]:
        if skip:
            notes, skip = a, False
            continue
        if a == "--notes":
            skip = True
            continue
        if a.startswith("--"):
            continue
        files.append(a)
    if skip:
        die("--notes wants a file")
    if not files:
        die("no files to attach - run build.sh first")
    body = open(notes, encoding="utf-8").read() if notes and os.path.exists(notes) else "Tandem " + tag

    hub = Hub(token(), repo_slug())
    try:
        rel = hub.req("/repos/%s/releases/tags/%s" % (hub.repo, tag))
        print("release %s already exists for %s" % (rel["id"], tag))
        hub.req("/repos/%s/releases/%s" % (hub.repo, rel["id"]), "PATCH",
                json.dumps({"body": body}).encode())
    except NotFound:
        rel = hub.req("/repos/%s/releases" % hub.repo, "POST", json.dumps({
            "tag_name": tag, "name": "Tandem " + tag.lstrip("v"), "body": body,
            "draft": False, "prerelease": False}).encode())
        print("created release %s for %s" % (rel["id"], tag))

    have = {a["name"]: a["id"] for a in hub.req("/repos/%s/releases/%s/assets" % (hub.repo, rel["id"]))}
    for path in files:
        if not os.path.exists(path):
            print("   skipping %s - not there" % path)
            continue
        name = os.path.basename(path)
        if name in have:
            hub.req("/repos/%s/releases/assets/%s" % (hub.repo, have[name]), "DELETE")
            print("   removed the old %s" % name)
        with open(path, "rb") as f:
            data = f.read()
        up = hub.req("/repos/%s/releases/%s/assets?name=%s" % (hub.repo, rel["id"], name), "POST",
                     data, "application/octet-stream", base=UPLOADS)
        print("   %s %s bytes %s" % (name, len(data), up.get("digest", "no digest"))
              if up.get("digest") else "   %s %s bytes (no digest published)" % (name, len(data)))
    print("https://github.com/%s/releases/tag/%s" % (hub.repo, tag))


main()
