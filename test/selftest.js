
import assert from 'node:assert/strict';
import process from 'node:process';
import { FakeGsx, ACTIVE, IDLE } from './fake-gsx.js';
import { GsxClient } from '../src/gsx/client.js';
import { Link } from '../src/net/link.js';
import { SyncEngine } from '../src/sync/engine.js';
import { Log } from '../src/log.js';
import { DEFAULTS, merge } from '../src/config.js';

const verbose = process.argv.includes('--verbose');
const log = new Log({ level: verbose ? 'debug' : 'warn' });
let step = 0;

const wait = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(fn, ms = 4000, what = 'condition') {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    if (fn()) return true;
    await wait(40);
  }
  throw new Error(`timed out after ${ms}ms waiting for ${what}`);
}

function rig({ gsxUrl, mode, port = 0, url = '', role = 'symmetric', reconcileSeconds = 2, graceSeconds = 1, tag = 'rig' }) {
  const rlog = new Log({ level: verbose ? 'debug' : 'error', tag: `[${tag}]` });
  rlog.ring = log.ring;
  const cfg = merge(DEFAULTS, {
    gsx: { url: gsxUrl },
    net: { mode, port, url, room: 'TEST', bind: '127.0.0.1', reconnect: false, name: `rig-${mode}-${port || 'join'}` },
    sync: { role, autoReconcile: true, reconcileSeconds, graceSeconds, echoMs: 1500, debounceMs: 20, captureState: false, mirrorMenuPicks: true },
    log: { level: 'warn' },
  });
  const gsx = new GsxClient({ url: cfg.gsx.url, reconnect: false, log: rlog.child('gsx'), debounceMs: cfg.sync.debounceMs });
  const link = new Link({ ...cfg.net, log: rlog.child('net') });
  const engine = new SyncEngine({ cfg, gsx, link, log: rlog.child('syn'), name: cfg.net.name });
  gsx.start();
  link.start();
  engine.start();
  return { cfg, gsx, link, engine, stop: () => (engine.stop(), link.stop(), gsx.stop()) };
}

function ok(name) {
  step++;
  console.log(`  \x1b[32m${String(step).padStart(2, ' ')}.\x1b[0m ${name}`);
}

console.log('\ngsx-sync selftest\n');

const fakeA = new FakeGsx({ port: 9744, name: 'A' });
const fakeB = new FakeGsx({ port: 9745, name: 'B', strictArgs: true });
await fakeA.start();
await fakeB.start();

const A = rig({ gsxUrl: 'ws://127.0.0.1:9744', mode: 'host', port: 9790, tag: 'A' });
const B = rig({ gsxUrl: 'ws://127.0.0.1:9745', mode: 'join', url: 'ws://127.0.0.1:9790', tag: 'B' });

await until(() => A.gsx.connected && B.gsx.connected && A.link.peers.size && B.link.peers.size, 5000, 'both links up');
ok('two cockpits connected to their own Couatl and to each other');
assert.equal(A.gsx.state.services.Boarding.stateText, IDLE);
assert.equal(B.gsx.state.services.Boarding.stateText, IDLE);

fakeA.toggleService('Boarding');
await until(() => fakeB.stateOf('Boarding') === ACTIVE, 5000, 'Boarding mirrored to B');
ok('service.trigger mirroring A->B (and the args-shape ladder survived a strict server)');
assert.ok(B.gsx.stats.commands >= 1, 'B should have issued a command');

const beforeA = fakeA.countOf('service.trigger');
const beforeB = fakeB.countOf('service.trigger');
await wait(1800);
assert.equal(fakeA.countOf('service.trigger'), beforeA, 'A must not receive replayed commands');
assert.equal(fakeB.countOf('service.trigger'), beforeB, 'B must not re-fire the same trigger');
assert.equal(fakeA.stateOf('Boarding'), ACTIVE);
assert.equal(fakeB.stateOf('Boarding'), ACTIVE);
assert.ok(B.engine.counters.suppressedEcho >= 1, 'B should have suppressed at least one echo');
ok(`echo suppression held the loop closed (suppressed=${B.engine.counters.suppressedEcho}, dupes=${A.engine.counters.droppedDupe + B.engine.counters.droppedDupe})`);

const cmdsB = fakeB.countOf('service.trigger');
for (const pct of [8, 21, 44, 67, 92]) {
  fakeA.advanceProgress('Boarding', pct);
  await wait(60);
}
await wait(600);
assert.equal(fakeB.countOf('service.trigger'), cmdsB, 'progress churn must not trigger anything');
ok('progress churn (% complete) correctly ignored as an intent');

fakeB.toggleService('Refueling');
await until(() => fakeA.stateOf('Refueling') === ACTIVE, 5000, 'Refueling mirrored B->A');
ok('mirroring works both directions (symmetric role)');

fakeB.openMenu(true);
await wait(150);
fakeA.toggleService('Chocks');
await until(() => fakeB.stateOf('Chocks') === ACTIVE, 5000, 'Chocks applied via label walk');
assert.ok(fakeB.commands.some((c) => c.verb === 'menu.pick'), 'expected a menu.pick on B');
ok('verb-less service (Chocks) replayed through a label-matched menu pick');

fakeA.toggleService('GPU');
await until(() => fakeB.stateOf('GPU') === ACTIVE, 4000, 'GPU mirrored');
B.cfg.sync.role = 'copilot';
await wait(200);
fakeB.setRaw('GPU', IDLE);
assert.equal(fakeA.stateOf('GPU'), ACTIVE, 'a copilot must not push its drift back onto the driver');
await until(() => fakeB.stateOf('GPU') === ACTIVE, 15000, 'GPU reconciled from digest');
assert.ok(B.engine.counters.reconcileFixes >= 1, 'reconcile should have replayed something');
ok('digest reconciliation repaired a one-sided drift without dragging the driver back');

const quietA = fakeA.commands.length;
const quietB = fakeB.commands.length;
await wait(Math.max(4000, B.cfg.sync.reconcileSeconds * 3500));
assert.equal(fakeA.commands.length, quietA, 'a matched rig must not fire any command');
assert.equal(fakeB.commands.length, quietB, 'a matched rig must not fire any command');
ok('steady state is silent (no command churn while both cockpits agree)');

await wait(1200);
assert.deepEqual(
  Object.fromEntries(Object.entries(fakeA.services).map(([k, v]) => [k, v.stateText === ACTIVE])),
  Object.fromEntries(Object.entries(fakeB.services).map(([k, v]) => [k, v.stateText === ACTIVE])),
  'final phases must match',
);
ok('converged: identical active/idle phase map on both cockpits');

A.stop();
B.stop();
fakeA.stop();
fakeB.stop();

await wait(200);
const { startRelay } = await import('../tools/relay.js');
const relayLog = new Log({ level: verbose ? 'debug' : 'error', tag: '[relay]' });
const relay = startRelay({ port: 9799, bind: '127.0.0.1', log: relayLog });
await wait(150);

const fakeC = new FakeGsx({ port: 9746, name: 'C' });
const fakeD = new FakeGsx({ port: 9747, name: 'D' });
await fakeC.start();
await fakeD.start();
const C = rig({ gsxUrl: 'ws://127.0.0.1:9746', mode: 'join', url: 'ws://127.0.0.1:9799', tag: 'C' });
const D = rig({ gsxUrl: 'ws://127.0.0.1:9747', mode: 'join', url: 'ws://127.0.0.1:9799', tag: 'D' });
await until(() => C.link.peers.size && D.link.peers.size, 4000, 'relay pair connected');
fakeC.toggleService('Catering');
await until(() => fakeD.stateOf('Catering') === ACTIVE, 5000, 'relay mirroring');
await wait(1200);
assert.equal(fakeD.countOf('service.trigger'), 1, 'relay must deliver an action exactly once');
assert.equal(fakeC.countOf('service.trigger'), 0, 'the origin must never receive its own action');
ok('relay room routing: exactly-once delivery, no origin echo');

C.stop();
D.stop();
fakeC.stop();
fakeD.stop();
relay.server.close();

const secs = ((Date.now() - (globalThis.__t0 ||= Date.now())) / 1000).toFixed(1);
console.log(`\n  \x1b[32mall ${step} checks passed\x1b[0m (${secs}s)  A=${JSON.stringify(A.engine.counters)}\n`);
process.exit(0);
