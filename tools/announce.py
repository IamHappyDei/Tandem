#!/usr/bin/env python3
# Announce a planned change on Discord before implementing it.
# Reads the webhook from $TANDEM_WEBHOOK, then ./.discord-webhook, else the local file next
# to this script. Nothing about the webhook is ever committed.
import json, os, sys, urllib.request

def hook():
    for p in (os.environ.get('TANDEM_WEBHOOK'),):
        if p:
            return p.strip()
    for f in (os.path.join(os.path.dirname(__file__), '..', '.discord-webhook'),
              os.path.expanduser('~/.tandem-webhook')):
        if os.path.exists(f):
            return open(f).read().strip()
    sys.exit('no webhook configured (TANDEM_WEBHOOK or .discord-webhook)')

def main():
    title = sys.argv[1] if len(sys.argv) > 1 else 'tandem'
    body = sys.argv[2] if len(sys.argv) > 2 else ' '.join(sys.argv[1:])
    if os.environ.get("TANDEM_MENTION", "").lower() == "everyone":
        payload = {"username": "Tandem (building)", "content": "@everyone",
                   "allowed_mentions": {"parse": ["everyone"]}, "embeds": [{
        "title": title, "description": body[:3800], "color": 344700,
        "footer": {"text": "about to implement"}}]}
    req = urllib.request.Request(hook(), data=json.dumps(payload).encode(),
                                headers={"Content-Type": "application/json", "User-Agent": "tandem-build"})
    try:
        r = urllib.request.urlopen(req, timeout=15)
        print('announced:', title, '->', r.status)
    except Exception as e:
        print('announce failed (%s) - carrying on, the work is not blocked by it' % e)

main()
