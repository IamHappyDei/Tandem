#!/usr/bin/env node

import { spawn } from 'node:child_process';
import net from 'node:net';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { FakeGsx, ACTIVE } from './fake-gsx.js';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const BIN = path.join(ROOT, 'src', 'index.js');
const LOG_DIR = path.join(ROOT, 'run', 'smoke');

const P = { gsxA: 9744, gsxB: 9745, peer: 9790, hostAdmin: 9791, joinAdmin: 9792, lonePeer: 9796, loneAdmin: 9797 };

const NAMES = { host: 'host-pc', join: 'co-op' };

const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const strip = (s) => s.replace(/\x1b\[[0-9;]*m/g, '');

let passed = 0;
let failed = 0;
function check(name, cond, detail = '') {
  if (cond) {
    passed++;
    console.log(`  \x1b[32m${String(passed + failed).padStart(2, ' ')}.\x1b[0m ${name}`);
  } else {
    failed++;
    console.log(`  \x1b[31m${String(passed + failed).padStart(2, ' ')}.\x1b[0m ${name}${detail ? '\n      ' + detail : ''}`);
  }
}

async function until(fn, ms = 6000, what = 'condition') {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    if (fn()) return true;
    await wait(50);
  }
  return false;
}

async function untilAsync(fn, ms = 8000) {
  const t0 = Date.now();
  let last;
  for (;;) {
    last = await fn();
    if (last) return last;
    if (Date.now() - t0 > ms) return last;
    await wait(400);
  }
}

function portBusy(port) {
  return new Promise((res) => {
    const s = net.createServer();
    s.once('error', (e) => res(e.code === 'EADDRINUSE'));
    s.once('listening', () => s.close(() => res(false)));
    s.listen(port, '127.0.0.1');
  });
}

function cli(args) {
  return new Promise((res) => {
    const child = spawn(process.execPath, [BIN, ...args], { cwd: ROOT });
    let out = '';
    child.stdout.on('data', (d) => (out += d));
    child.stderr.on('data', (d) => (out += d));
    child.on('close', (code) => res({ code, out: strip(out) }));
  });
}

const kids = [];
function instance(name, args) {
  const kid = { name, args, out: [], child: null, pid: null, exited: null };
  kid.exited = new Promise((res) => (kid.resolveExit = res));
  return kid;
}
function start(kid) {
  const file = path.join(LOG_DIR, `${kid.name}.log`);
  const fh = fs.createWriteStream(file);
  kid.logFile = file;
  kid.child = spawn(process.execPath, [BIN, ...kid.args], { cwd: ROOT, stdio: ['ignore', 'pipe', 'pipe'] });
  kid.pid = kid.child.pid;
  kid.child.stdout.on('data', (d) => {
    const s = strip(String(d));
    kid.out.push(s);
    fh.write(s);
  });
  kid.child.stderr.on('data', (d) => {
    const s = strip(String(d));
    kid.out.push(s);
    fh.write(s);
  });
  kid.child.on('close', (code) => {
    kid.exitCode = code;
    kid.resolveExit?.(code);
    fh.end();
  });
  kids.push(kid);
  return kid;
}
const text = (kid) => kid.out.join('');

function killKid(kid) {
  if (kid.child && kid.exitCode === null && !kid.child.killed) {
    try {
      kid.child.kill('SIGKILL');
      console.log(`  (had to force-kill ${kid.name} pid=${kid.pid})`);
    } catch {

    }
  }
}

let fakeA, fakeB;
let cleaned = false;
function cleanup() {
  if (cleaned) return;
  cleaned = true;
  for (const k of kids) killKid(k);
  fakeA?.stop();
  fakeB?.stop();
}
setTimeout(() => {
  console.log('\n  \x1b[31mTIMED OUT\x1b[0m - cleaning up only our own PIDs: ' + kids.map((k) => k.pid).filter(Boolean).join(', '));
  cleanup();
  process.exit(3);
}, 90000).unref();

console.log('\ngsx-sync CLI smoke test\n');

const busy = [];
for (const [what, port] of Object.entries(P)) if (await portBusy(port)) busy.push(`${what}:${port}`);
if (busy.length) {
  console.log(`  \x1b[31maborting\x1b[0m - port(s) already in use: ${busy.join(', ')}`);
  const ports = Object.values(P).join(' ');
  console.log('  something from an earlier run is still up. Find its PID WITHOUT killing every node:');
  if (process.platform === 'win32') {
    const pat = Object.values(P).map((n) => ':' + n).join(' ');
    console.log('    netstat -ano | findstr "' + pat + '"');
    console.log('    taskkill //PID <that pid> //F     <- one number, never //IM node.exe');
  } else {
    console.log('    lsof -nP -iTCP -sTCP:LISTEN | grep -E "(' + ports.replace(/ /g, '|') + ')"');
  }
  process.exit(2);
}

fs.mkdirSync(LOG_DIR, { recursive: true });

{
  const r = await cli(['status', '--admin', String(P.hostAdmin)]);
  check('status with nothing running exits non-zero and says why', r.code === 1 && /no running instance answered/.test(r.out), `code=${r.code} out=${r.out.trim().slice(0, 120)}`);
}

fakeA = new FakeGsx({ port: P.gsxA, name: 'A' });
fakeB = new FakeGsx({ port: P.gsxB, name: 'B' });
await fakeA.start();
await fakeB.start();

const host = instance('host', [
  'host',
  '--gsx', `ws://127.0.0.1:${P.gsxA}`,
  '--bind', '127.0.0.1',
  '--port', String(P.peer),
  '--admin', String(P.hostAdmin),
  '--room', 'SMOKE',
  '--name', NAMES.host,
  '--reconcile', '2',
  '--grace', '1',
]);
const join = instance('join', [
  'join', `ws://127.0.0.1:${P.peer}`,
  '--gsx', `ws://127.0.0.1:${P.gsxB}`,
  '--admin', String(P.joinAdmin),
  '--room', 'SMOKE',
  '--name', NAMES.join,
  '--reconcile', '2',
  '--grace', '1',
]);
start(host);
start(join);

const joined = await until(
  () => host.out.join('').includes('engine up') && join.out.join('').includes('host is ' + NAMES.host),
  15000,
  'both instances up and paired',
);
check('host and join come up and find each other', joined, 'logs:\n' + text(host).slice(-400) + '\n---\n' + text(join).slice(-400));

{
  const r = await cli(['status', '--admin', String(P.hostAdmin)]);
  const okRun = r.code === 0;
  check('status prints a table from a running instance', okRun && /SERVICE/.test(r.out) && /linked/.test(r.out), r.out.slice(0, 200));
  if (okRun) console.log(indent(r.out));
  check('status shows the peer by name', r.out.includes(NAMES.join), firstLine(r.out, 'peers'));
}

{
  const r = await cli(['trigger', 'Boarding', '--admin', String(P.hostAdmin)]);
  const reachedPeer = await until(() => fakeB.stateOf('Boarding') === ACTIVE, 8000);
  check('trigger Boarding fires on this box', r.code === 0 && fakeA.stateOf('Boarding') === ACTIVE, r.out.trim().slice(0, 160));
  check('...and the co-pilot replays it on their own GSX', reachedPeer, `B=${fakeB.stateOf('Boarding')} join log: ` + text(join).slice(-240));
  check('the co-op suppressed its own echo', /echo dropped|applied:/.test(text(join)) || join.out.join('').includes('applied'), tail(text(join)));
}

{
  fakeB.toggleService('Catering');
  const reached = await until(() => fakeA.stateOf('Catering') === ACTIVE, 8000);
  check('a human on the co-op box is mirrored back to the host', reached, `A=${fakeA.stateOf('Catering')} host log: ` + tail(text(host)));
  const settled = await untilAsync(async () => {
    const r = await cli(['status', '--admin', String(P.hostAdmin)]);
    return r.out.includes('DRIFT') ? null : r;
  });
  const rows = settled ? settled.out.split('\n').filter((l) => /Catering|Boarding/.test(l)) : ['(still drifting)'];
  check('status converges: the peer table agrees, no DRIFT left', !!settled, rows.join('\n'));
}

{
  const r = await cli(['note', 'chocks', 'on', '--admin', String(P.hostAdmin)]);
  const seen = await until(() => text(join).includes('chocks on'), 4000);
  check('note crosses the link and lands in the peer log', r.code === 0 && r.out.includes('sent') && seen, tail(text(join)));
}

{
  const r = await cli(['dump', '--admin', String(P.hostAdmin)]);
  let parsed = null;
  try {
    parsed = JSON.parse(r.out);
  } catch {

  }
  check('dump returns the mirrored GSX state as JSON', !!parsed && !!parsed.services?.Boarding, r.out.slice(0, 120));
  const hostFile = path.join(ROOT, 'run', `state-${NAMES.host}.json`);
  const joinFile = path.join(ROOT, 'run', `state-${NAMES.join}.json`);
  await until(() => fs.existsSync(hostFile) && fs.existsSync(joinFile), 8000);
  check('each instance writes its own run file (no clobbering on one box)', fs.existsSync(hostFile) && fs.existsSync(joinFile), `${hostFile} ${fs.existsSync(hostFile)} | ${joinFile} ${fs.existsSync(joinFile)}`);
}

{
  const a0 = fakeA.commands.length;
  const b0 = fakeB.commands.length;
  await wait(6000);
  check('no command churn while both cockpits agree', fakeA.commands.length === a0 && fakeB.commands.length === b0, `A +${fakeA.commands.length - a0}, B +${fakeB.commands.length - b0}`);
}

{
  const lone = instance('lonely', ['host', '--gsx', 'ws://127.0.0.1:9999', '--bind', '127.0.0.1', '--port', String(P.lonePeer), '--admin', String(P.loneAdmin), '--room', 'OFFLINE', '--name', 'no-sim']);
  start(lone);
  await wait(1200);
  const r = await cli(['status', '--admin', String(P.loneAdmin)]);
  check('an instance with no GSX running stays up and says DOWN', r.code === 0 && /DOWN/.test(r.out), `code=${r.code} ${r.out.trim().slice(0, 200)}`);
  check('...and never crashed on the way', !/unhandled|TypeError|undefined is not/.test(text(lone)), tail(text(lone)));
  await cli(['stop', '--admin', String(P.loneAdmin)]);
  await until(() => lone.exitCode !== null, 5000);
}

{
  const r = await cli(['stop', '--admin', String(P.hostAdmin)]);
  const gone = await until(() => host.exitCode !== null, 6000);
  check('stop shuts the instance down by itself', r.code === 0 && gone, `code=${host.exitCode} ${tail(text(host))}`);
  const peerGone = await until(() => /peer link down|peer left/i.test(text(join)), 6000);
  check('the surviving cockpit notices the room broke up', peerGone, tail(text(join)));
  const r2 = await cli(['status', '--admin', String(P.hostAdmin)]);
  check('status after stop fails politely again', r2.code === 1, r2.out.trim().slice(0, 120));
}

const rs = await cli(['stop', '--admin', String(P.joinAdmin)]);
await until(() => join.exitCode !== null, 6000);
check('both instances are down and no stray process is left', host.exitCode !== null && join.exitCode !== null, `host=${host.exitCode} join=${join.exitCode} ${rs.out.trim()}`);

fakeA.stop();
fakeB.stop();

console.log(`\n  \x1b[${failed ? 31 : 32}m${passed} passed, ${failed} failed\x1b[0m  (instance logs: ${path.relative(ROOT, LOG_DIR)}/host.log, join.log)`);
console.log(`  PIDs used by this run: ${kids.map((k) => `${k.name}=${k.pid}`).join(', ')} - all reaped, nothing else was touched.\n`);

cleanup();
process.exit(failed ? 1 : 0);

function indent(s) {
  return s.split('\n').map((l) => '      ' + l).join('\n').replace(/\n {6}$/, '');
}
function firstLine(s, needle) {
  return (s.split('\n').find((l) => l.includes(needle)) || '(no such line)').trim();
}
function tail(s, n = 300) {
  return s.trim().split('\n').slice(-4).join(' / ').slice(-n);
}
