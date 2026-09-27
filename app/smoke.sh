#!/usr/bin/env bash
# smoke.sh - two cockpits, two fake Couatls, one room, and the sim bridge with a stand-in sim.
# Every step that matters is asserted: the suite prints FAIL and exits 1 rather than drifting by.
set -u
cd "$(dirname "$0")"
mkdir -p .smoke
rm -rf .smoke/appdata && mkdir -p .smoke/appdata/Tandem
export APPDATA="$(cd .smoke/appdata && pwd -W)"
export PATH="/e/go/bin:$PATH" GOTOOLCHAIN=local
cp dist/tandem.exe dist/b.exe   # a second copy: Windows will not let two instances share one file handle for upgrades
go build -o .smoke/wsprobe.exe ./tools/wsprobe || { echo "FAIL wsprobe would not build"; exit 1; }
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
fails=0
need() { # need <what it is> <expected> <got>
  if [ "$2" != "$3" ]; then echo "    FAIL $1: wanted [$2] got [$3]"; fails=$((fails+1)); else echo "    ok   $1"; fi
}
get() { curl -s --max-time 5 "http://127.0.0.1:$1/api/status"; }
post() { curl -s --max-time 8 -X POST "http://127.0.0.1:$1/api/$2" -d "$3"; }

echo "=== shared cockpit on, both sides (off is the new default)"
for p in 18795 18896; do post $p config '{"shared":true}' >/dev/null; done
sleep 3
for p in 18795 18896; do
  need "ui $p says shared is on" "True" "$(get $p | pj 'str(d["app"]["shared"])')"
done

echo "=== A opens a room"
post 18795 connect '{"room":"SMOKE1"}' | pj "'room '+str(d.get('room'))" | sed 's/^/    /'
INV=$(curl -s --max-time 5 http://127.0.0.1:18795/api/invite | python3 -c "import json,sys;print(json.load(sys.stdin).get('code',''))")
need "an invite code came out" "yes" "$([ -n "$INV" ] && echo yes || echo no)"
echo "    invite: ${INV:0:56}..."

echo "=== B pastes the invite"
post 18896 paste "{\"code\":\"$INV\"}" | pj "'join ok, cands='+str(len(d.get('cands',[])))" | sed 's/^/    /'
sleep 4

echo "=== the room"
for p in 18795 18896; do
  printf "    ui %-6s " "$p"
  get $p | pj "'peers='+str([x['name'] for x in d['status']['link']['peers']])+' gsx='+str(d['status']['gsx']['connected'])+' here='+d['app']['room']"
done
need "A sees There" "['There']" "$(get 18795 | pj 'sorted(x["name"] for x in d["status"]["link"]["peers"])')"
need "B sees Here" "['Here']" "$(get 18896 | pj 'sorted(x["name"] for x in d["status"]["link"]["peers"])')"

echo "=== A orders Boarding (through A's own Couatl)"
post 18795 trigger '{"name":"Boarding"}' >/dev/null
sleep 4
a_state=$(get 18795 | pj "str(d['status']['gsx']['phases'].get('Boarding',{}).get('state','NONE'))")
b_state=$(get 18896 | pj "str(d['status']['gsx']['phases'].get('Boarding',{}).get('state','NONE'))")
printf "    A Boarding=%s   B Boarding=%s\n" "$a_state" "$b_state"
need "the same service reached the other cockpit" "$a_state" "$b_state"
need "B applied something it did not order" "yes" "$(get 18896 | pj 'str(int(d["status"]["sync"]["counters"]["appliedRemote"])>0 and "yes" or "no")')"

echo "=== turning shared off closes the door"
post 18896 config '{"shared":false}' >/dev/null
sleep 2
need "B closed its room port while off" "0" "$(netstat -ano | grep LISTENING | grep -c ':18770 ' | tr -d ' ')"
curl -s --max-time 5 http://127.0.0.1:18896/api/invite >/dev/null
need "asking for a code switched shared cockpit back on" "True" "$(get 18896 | pj 'str(d["app"]["shared"])')"
sleep 5
need "and the log says so" "1" "$(grep -hc 'you asked for the room' .smoke/b.log | tail -1 | tr -d ' ')"
sleep 3
need "B is back in the room" "['Here']" "$(get 18896 | pj 'sorted(x["name"] for x in d["status"]["link"]["peers"])')"

echo "=== the sim bridge, with a stand-in sim"
post 18795 sim '{"enabled":true,"watch":["L:AP_MASTER","L:KAP700_STANDBY_POWER"]}' >/dev/null
sleep 1
URL=$(get 18795 | pj 'd["app"]["sim"].get("url","")')
echo "    listening on $URL"
( .smoke/wsprobe.exe -listen -url "$URL" -title "Fenix Airbus A321neo" -vars "L:AP_MASTER=1,L:KAP700_STANDBY_POWER=2" > .smoke/probe.log 2>&1 & )
sleep 2
sleep 2
need "the sim bridge counts a sim talking to it" "True" "$(get 18795 | pj 'str(d["app"]["sim"]["connected"])')"
need "the sim said what it is flying" "Fenix Airbus A321neo" "$(get 18795 | pj 'd["app"]["sim"]["title"]')"
need "the switch the sim reported arrived" "1" "$(get 18795 | pj 'str(d["app"]["sim"]["vars"].get("L:AP_MASTER"))')"
post 18795 sim '{"write":{"L:AP_MASTER":0}}' >/dev/null
sleep 1
need "Tandem asked the sim to put a switch back" "yes" "$(grep -c 'L:AP_MASTER' .smoke/probe.log | awk '{print ($1>1)?"yes":"no"}')"
taskkill //F //IM wsprobe.exe >/dev/null 2>&1

echo "=== a note across the room"
post 18795 note '{"text":"chocks in, over"}' >/dev/null
sleep 1
grep -h "chocks in" .smoke/b.log | sed 's/\x1b\[[0-9;]*m//g' | sed 's/^/    /'
need "the note got through" "1" "$(grep -hc 'chocks in' .smoke/b.log | tail -1 | tr -d ' ')"

echo "=== settings are written down, not just remembered"
post 18795 config '{"shared":true}' >/dev/null
post 18795 config '{"name":"Here Renamed"}' >/dev/null
post 18795 aircraft '{"community":"D:\\MSFS\\Community","autoDetect":false}' >/dev/null
post 18795 sim '{"watch":["L:TEST_ONE","L:TEST_TWO, Bool"]}' >/dev/null
sleep 1
files=$(echo "$APPDATA")
need "the name landed in the config file" "yes" "$(python3 -c "
import json,glob,os,sys
hits=[f for f in glob.glob(os.environ['APPDATA']+r'/Tandem/*.json') if 'config' in os.path.basename(f)]
print('yes' if any(json.load(open(f)).get('net',{}).get('name')=='Here Renamed' for f in hits) else 'no')")"
need "the community folder landed in the profile file" "yes" "$(python3 -c "
import json,glob,os
hits=glob.glob(os.environ['APPDATA']+r'/Tandem/aircraft*.json')
print('yes' if hits and any(json.load(open(f)).get('community','').endswith('Community') for f in hits) else 'no')")"
need "the watch list landed too" "yes" "$(python3 -c "
import json,glob,os
hits=[f for f in glob.glob(os.environ['APPDATA']+r'/Tandem/*.json') if 'config' in os.path.basename(f)]
print('yes' if any(len(json.load(open(f)).get('sim',{}).get('watch',[]))==2 for f in hits) else 'no')")"
post 18795 quit '{}' >/dev/null
sleep 2
GSXTEST_LOCAL=1 ./dist/tandem.exe fake --port 18744 > .smoke/fa2.log 2>&1 &
GSXTEST_LOCAL=1 ./dist/tandem.exe --ui 18795 --gsx ws://127.0.0.1:18744 --nogui > .smoke/a2.log 2>&1 &
sleep 5
need "and the next start reads it back" "Here Renamed" "$(get 18795 | pj 'd["app"]["name"]')"
need "the auto-detect switch stayed where you left it" "False" "$(get 18795 | pj 'str(d["app"]["aircraft"].get("auto", True))')"
need "the watch list came back" "2" "$(get 18795 | pj 'len(d["app"]["sim"]["wanted"])')"

echo "=== quit"
post 18896 quit '{}' >/dev/null
post 18795 quit '{}' >/dev/null
sleep 1
taskkill //F //IM tandem.exe //IM b.exe >/dev/null 2>&1
if [ "$fails" -gt 0 ]; then echo "SMOKE: $fails thing(s) did not hold"; exit 1; fi
echo "done - every step held"
