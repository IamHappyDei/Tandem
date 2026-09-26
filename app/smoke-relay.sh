#!/usr/bin/env bash
set -u
cd "$(dirname "$0")"
mkdir -p .smoke
cp dist/tandem.exe dist/b.exe   # a second copy: Windows will not let two instances share one file handle for upgrades
taskkill //F //IM tandem.exe //IM b.exe >/dev/null 2>&1
sleep 0.5

./dist/tandem.exe fake  --port 18744 > .smoke/fa.log 2>&1 &
./dist/b.exe      fake  --port 18745 > .smoke/fb.log 2>&1 &
./dist/tandem.exe relay --port 18850 > .smoke/relay.log 2>&1 &
sleep 1
./dist/tandem.exe --ui 18795 --gsx ws://127.0.0.1:18744 --listen 18760 --udp 18761 --name Here --nogui > .smoke/a.log 2>&1 &
sleep 2
./dist/b.exe --ui 18896 --gsx ws://127.0.0.1:18745 --listen 18770 --udp 18771 --name There --nogui > .smoke/b.log 2>&1 &
sleep 3

pj() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }

echo "=== both attach to the relay and open the same room code"
for p in 18795 18896; do
  curl -s --max-time 5 -X POST "http://127.0.0.1:$p/api/config" -d '{"relay":"ws://127.0.0.1:18850"}' >/dev/null
  curl -s --max-time 5 -X POST "http://127.0.0.1:$p/api/connect" -d '{"room":"RELAY1"}' >/dev/null
done
sleep 4
for p in 18795 18896; do
  printf "    ui %-6s " "$p"
  curl -s --max-time 5 "http://127.0.0.1:$p/api/status" | pj "'peers='+str(sorted(x['name'] for x in d['status']['link']['peers']))+' paths='+str([x['paths'] for x in d['status']['link']['peers']])"
done

echo "=== order something here, watch it happen there"
curl -s --max-time 5 -X POST http://127.0.0.1:18795/api/trigger -d '{"name":"GPU"}'
echo
sleep 3
for p in 18795 18896; do
  printf "    ui %-6s GPU=" "$p"
  curl -s --max-time 5 "http://127.0.0.1:$p/api/status" | pj "str(d['status']['gsx']['phases'].get('GPU',{}).get('state','NONE'))+' appliedRemote='+str(d['status']['sync']['counters']['appliedRemote'])+' dupes='+str(d['status']['sync']['counters']['droppedDupe'])"
done
echo "=== relay traffic"
sed 's/\x1b\[[0-9;]*m//g' .smoke/relay.log | sed 's/^/    /' | tail -6
for p in 18795 18896; do curl -s --max-time 5 -X POST "http://127.0.0.1:$p/api/quit" -d '{}' >/dev/null; done
curl -s --max-time 5 -X POST http://127.0.0.1:18795/api/quit -d '{}' >/dev/null
taskkill //F //IM tandem.exe //IM b.exe >/dev/null 2>&1
echo done
