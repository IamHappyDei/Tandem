#!/usr/bin/env bash
set -u
cd "$(dirname "$0")"
mkdir -p .smoke
cp dist/tandem.exe dist/b.exe   # a second copy: Windows will not let two instances share one file handle for upgrades
taskkill //F //IM tandem.exe //IM b.exe >/dev/null 2>&1
sleep 0.5

./dist/tandem.exe fake --port 18744 > .smoke/fa.log 2>&1 &
./dist/b.exe      fake --port 18745 > .smoke/fb.log 2>&1 &
sleep 1
GSXTEST_LOCAL=1 ./dist/tandem.exe --ui 18795 --gsx ws://127.0.0.1:18744 --listen 18760 --udp 18761 --name Here --nogui > .smoke/a.log 2>&1 &
sleep 2
GSXTEST_LOCAL=1 ./dist/b.exe --ui 18896 --gsx ws://127.0.0.1:18745 --listen 18770 --udp 18771 --name There --nogui > .smoke/b.log 2>&1 &
sleep 3

pj() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

echo "=== A opens a room"
curl -s --max-time 5 -X POST http://127.0.0.1:18795/api/connect -d '{"room":"SMOKE1"}' | pj "'room '+str(d['room'])"
INV=$(curl -s --max-time 5 http://127.0.0.1:18795/api/invite | python3 -c "import json,sys;print(json.load(sys.stdin)['code'])")
echo "    invite: ${INV:0:56}..."

echo "=== B pastes the invite"
curl -s --max-time 5 -X POST http://127.0.0.1:18896/api/paste -d "{\"code\":\"$INV\"}" | pj "'join ok, cands='+str(len(d.get('cands',[])))"
sleep 4

echo "=== the room"
for p in 18795 18896; do
  printf "    ui %-6s " "$p"
  curl -s --max-time 5 "http://127.0.0.1:$p/api/status" | pj "'peers='+str([x['name'] for x in d['status']['link']['peers']])+' gsx='+str(d['status']['gsx']['connected'])+' here='+d['app']['room']"
done

echo "=== A orders Boarding (through A's own Couatl)"
curl -s --max-time 5 -X POST http://127.0.0.1:18795/api/trigger -d '{"name":"Boarding"}'
echo
sleep 3
for p in 18795 18896; do
  printf "    ui %-6s Boarding=" "$p"
  curl -s --max-time 5 "http://127.0.0.1:$p/api/status" | pj "str(d['status']['gsx']['phases'].get('Boarding',{}).get('state','NONE'))+' applied='+str(d['status']['sync']['counters']['appliedRemote'])"
done

echo "=== a note across the room"
curl -s --max-time 5 -X POST http://127.0.0.1:18795/api/note -d '{"text":"chocks in, over"}' >/dev/null
sleep 1
grep -h "chocks in" .smoke/b.log | sed 's/\x1b\[[0-9;]*m//g' | sed 's/^/    /'

echo "=== quit"
curl -s --max-time 5 -X POST http://127.0.0.1:18896/api/quit -d '{}' >/dev/null
curl -s --max-time 5 -X POST http://127.0.0.1:18795/api/quit -d '{}' >/dev/null
sleep 1
taskkill //F //IM tandem.exe //IM b.exe >/dev/null 2>&1
echo done
