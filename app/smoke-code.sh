#!/usr/bin/env bash
set -u
cd "$(dirname "$0")"
mkdir -p .smoke
taskkill //F //IM tandem.exe //IM b.exe >/dev/null 2>&1
sleep 0.5
cp dist/tandem.exe dist/b.exe
rm -f "$APPDATA/Tandem/config-"*.json

./dist/tandem.exe fake   --port 18744 > .smoke/fa.log 2>&1 &
./dist/b.exe      fake    --port 18745 > .smoke/fb.log 2>&1 &
./dist/tandem.exe discover --port 18788 --bind 127.0.0.1 > .smoke/pnp.log 2>&1 &
sleep 1
./dist/tandem.exe --ui 18795 --gsx ws://127.0.0.1:18744 --listen 18760 --udp 18760 --name Here  --nogui > .smoke/a.log 2>&1 &
sleep 2
./dist/b.exe      --ui 18896 --gsx ws://127.0.0.1:18745 --listen 18761 --udp 18761 --name There --nogui > .smoke/b.log 2>&1 &
sleep 3

pj() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

echo "=== both register with the phone book"
for p in 18795 18896; do
  curl -s --max-time 5 -X POST "http://127.0.0.1:$p/api/config" -d '{"rendezvous":"127.0.0.1:18788"}' >/dev/null
  curl -s --max-time 5 -X POST "http://127.0.0.1:$p/api/connect" -d '{"room":"CODE1"}' >/dev/null
done
sleep 3
for p in 18795 18896; do
  printf "    ui %-6s code=" "$p"
  curl -s --max-time 5 "http://127.0.0.1:$p/api/status" | pj "''+d['app']['code']+'  via='+str(d['app']['viaRendezvous'])"
done
CODE=$(curl -s --max-time 5 http://127.0.0.1:18795/api/status | python3 -c "import json,sys;print(json.load(sys.stdin)['app']['code'])")

echo "=== There joins by typing just the code: $CODE"
curl -s --max-time 8 -X POST http://127.0.0.1:18896/api/paste -d "{\"code\":\"$CODE\"}"; echo
sleep 8
for p in 18795 18896; do
  printf "    ui %-6s " "$p"
  curl -s --max-time 5 "http://127.0.0.1:$p/api/status" | pj "'peers='+str([x['name'] for x in d['status']['link']['peers']])+' paths='+str([x['paths'] for x in d['status']['link']['peers']])"
done

echo "=== order something"
curl -s --max-time 5 -X POST http://127.0.0.1:18795/api/trigger -d '{"name":"Chocks"}'
echo
sleep 3
for p in 18795 18896; do
  printf "    ui %-6s Chocks=" "$p"
  curl -s --max-time 5 "http://127.0.0.1:$p/api/status" | pj "str(d['status']['gsx']['phases'].get('Chocks',{}).get('state','NONE'))"
done
echo "=== the phone book saw:"
sed 's/\x1b\[[0-9;]*m//g' .smoke/pnp.log | tail -6
for p in 18795 18896; do curl -s --max-time 5 -X POST "http://127.0.0.1:$p/api/quit" -d '{}' >/dev/null; done
taskkill //F //IM tandem.exe //IM b.exe >/dev/null 2>&1
echo done
