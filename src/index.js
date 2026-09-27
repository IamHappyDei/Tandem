#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';
import { loadConfig, ROOT, RUN_DIR, CAPTURE_DIR, hostname, ensureDirs, slug, localAddresses } from './config.js';
import { Log } from './log.js';
import { GsxClient } from './gsx/client.js';
import { Link } from './net/link.js';
import { SyncEngine } from './sync/engine.js';
import { connect, serve } from './util/ws.js';
import { servicePhaseMap, canonicalize, SERVICE_VOCAB, unknownStates } from './sync/intents.js';
import { hash, summarize } from './sync/diff.js';

const argv = process.argv.slice(2);
const cmd = (argv.shift() || 'help').toLowerCase();
const flags = parseFlags(argv);

function parseFlags(list) {
  const out = { _: [] };
  for (let i = 0; i < list.length; i++) {
    const a = list[i];
    if (a.startsWith('--')) {
      const k = a.slice(2);
      const eq = k.indexOf('=');
      if (eq > 0) {
        out[k.slice(0, eq)] = k.slice(eq + 1);
        continue;
      }
      const nxt = list[i + 1];
      if (nxt !== undefined && !nxt.startsWith('--')) {
        out[k] = nxt;
        i++;
      } else out[k] = true;
    } else out._.push(a);
  }
  return out;
}

function cfgFromFlags() {
  const overrides = { gsx: {}, net: {}, sync: {}, admin: {}, log: {} };
  if (flags.gsx) overrides.gsx.url = flags.gsx;
  if (flags.port) overrides.net.port = Number(flags.port);
  if (flags.bind) overrides.net.bind = flags.bind;
  if (flags.room) overrides.net.room = flags.room;
  if (flags.pass) overrides.net.pass = flags.pass;
  if (flags.name) overrides.net.name = flags.name;
  if (flags.role) overrides.sync.role = flags.role;
  if (flags.admin) overrides.admin.port = Number(flags.admin);
  if (flags.level) overrides.log.level = flags.level;
  if (flags['no-reconcile']) overrides.sync.autoReconcile = false;
  if (flags['auto-stop']) overrides.sync.autoStop = true;
  if (flags['confirm']) overrides.sync.confirmDigests = Number(flags.confirm);
  if (flags['grace']) overrides.sync.graceSeconds = Number(flags.grace);
  if (flags['no-menu-mirror']) overrides.sync.mirrorMenuPicks = false;
  if (flags.reconcile) overrides.sync.reconcileSeconds = Number(flags.reconcile);
  const cfg = loadConfig({ overrides });
  if (cmd === 'host') cfg.net.mode = 'host';
  if (cmd === 'join') {
    cfg.net.mode = 'join';
    cfg.net.url = flags.url || flags._[0] || cfg.net.url;
  }
  cfg.net.name = cfg.net.name || flags.name || hostname();
  return cfg;
}

async function runInstance() {
  const cfg = cfgFromFlags();
  ensureDirs();
  const log = new Log({ level: cfg.log.level });
  const gsx = new GsxClient({ url: cfg.gsx.url, channels: cfg.gsx.channels, reconnect: cfg.gsx.reconnect, log: log.child('[gsx]'), debounceMs: cfg.sync.debounceMs });
  const link = new Link({ ...cfg.net, name: cfg.net.name, log: log.child('[net]') });
  const engine = new SyncEngine({ cfg, gsx, link, log: log.child('[syn]'), name: cfg.net.name });

  let down = false;
  const shutdown = () => {
    if (down) return;
    down = true;
    log.warn('shutting down');
    engine.stop();
    link.stop();
    gsx.stop();
    admin.close?.();
    setTimeout(() => process.exit(0), 200);
  };
  process.on('SIGINT', shutdown);
  process.on('SIGTERM', shutdown);

  const admin = serve({
    port: cfg.admin.port,
    host: '127.0.0.1',
    onConn: (conn) => {
      conn.on('message', async (m) => {
        try {
          if (m.cmd === 'status') conn.send({ ok: true, data: engine.status() });
          else if (m.cmd === 'trigger') conn.send({ ok: true, data: await gsx.triggerService(String(m.name)) });
          else if (m.cmd === 'pick') conn.send({ ok: true, data: await gsx.pickMenu(Number(m.index >= 0 ? m.index : engine._indexOfLabel(m.label))) });
          else if (m.cmd === 'note') {
            link.broadcast({ t: 'note', origin: link.origin, who: cfg.net.name, text: String(m.text || '') });
            conn.send({ ok: true });
          } else if (m.cmd === 'reconcile') {
            engine._askAndPublish();
            conn.send({ ok: true });
          } else if (m.cmd === 'dump') conn.send({ ok: true, data: gsx.state });
          else if (m.cmd === 'stop') {
            conn.send({ ok: true, data: 'stopping' });
            setTimeout(shutdown, 50);
          }
          else conn.send({ ok: false, error: 'unknown cmd' });
        } catch (e) {
          conn.send({ ok: false, error: e.message });
        }
      });
    },
    onError: (e) => {
      if (e.code === 'EADDRINUSE') log.warn(`admin port ${cfg.admin.port} busy (another instance running?) - status/trigger disabled here`);
    },
  });

  log.info(`gsx-sync ${cfg.net.mode} | gsx=${cfg.gsx.url} room=${cfg.net.room} role=${cfg.sync.role} reconc=${cfg.sync.autoReconcile ? cfg.sync.reconcileSeconds + 's' : 'off'}`);
  if (cfg.net.mode === 'host') {
    const addrs = localAddresses();
    const passHint = cfg.net.pass ? ' --pass <the same phrase>' : '';
    for (const a of addrs) log.info(`friend dials:  node src/index.js join ws://${a.address}:${cfg.net.port} --room ${cfg.net.room}${passHint}   (via ${a.name})`);
    if (!addrs.length) log.warn('no LAN address found - use the relay (node tools/relay.js on any box both can reach)');
  }
  gsx.start();
  link.start();
  engine.start();
  fs.writeFileSync(path.join(RUN_DIR, `last-run-${slug(cfg.net.name)}.json`), JSON.stringify({ at: new Date().toISOString(), cfg }, null, 2));
  return { cfg, gsx, link, engine, admin };
}

async function runProbe() {
  const cfg = cfgFromFlags();
  ensureDirs();
  const log = new Log({ level: 'info' });
  const seconds = Number(flags.seconds || 25);
  const gsx = new GsxClient({ url: cfg.gsx.url, log: log.child('[gsx]'), debounceMs: 30 });
  const events = [];
  const raw = [];
  const conn = connect(cfg.gsx.url);
  conn.on('open', () => {
    conn.send({ type: 'subscribe', channels: cfg.gsx.channels });
    log.info(`subscribed to ${cfg.gsx.url}; watching ${seconds}s of GSX traffic. Open the GSX menu and toggle a service.`);
  });
  conn.on('message', (m) => {
    raw.push({ ts: Date.now(), m });
    if (m.type === 'snapshot') events.push({ type: 'snapshot', keys: Object.keys(m).filter((k) => !['v', 'type', 'ts', 'id'].includes(k)) });
    if (m.type === 'patch') events.push({ type: 'patch', path: m.path, valueType: Array.isArray(m.value) ? `array(${m.value.length})` : typeof m.value });
  });
  if (!conn.ready) await new Promise((r) => conn.on('open', r));
  else await conn.ready(4000);
  await new Promise((r) => setTimeout(r, seconds * 1000));
  gsx.stop();
  conn.close();

  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const file = path.join(CAPTURE_DIR, `gsx-${stamp}.json`);
  fs.writeFileSync(file, JSON.stringify({ at: new Date().toISOString(), url: cfg.gsx.url, frames: raw }, null, 2));

  const store = new GsxClient({ url: 'none' });
  for (const { m } of raw) {
    if (m.type === 'snapshot') {
      const { v, type, ts, id, ...rest } = m;
      store.state = rest;
    } else if (m.type === 'patch') store._applyPatch(m.path, m.value);
  }
  reportProbe(store.state, events, file, log);
}

function reportProbe(state, events, file, log) {
  const top = Object.keys(state || {});
  log.info(`---- capture: ${file}`);
  log.info(`top-level state keys (${top.length}): ${top.join(', ')}`);
  const svc = servicePhaseMap(state?.services);
  const keys = Object.keys(svc);  log.info(`services rows: ${keys.length}`);
  for (const k of keys.slice(0, 60)) {
    const s = svc[k];
    log.info(`  ${s.state.padEnd(7)} ${String(k).slice(0, 46).padEnd(48)} canonical=${s.canonical || '-'} verb=${s.knownVerb ? 'yes' : 'no'} progress=${s.progress ?? '-'}`);
  }
  if (!keys.length && state?.services !== undefined) {
    log.warn('services exists but no rows parsed - shape:', summarize(state.services));
  }
  const patchPaths = [...new Set(events.filter((e) => e.type === 'patch').map((e) => e.path))];
  log.info(`distinct patch paths seen: ${patchPaths.length}`);
  for (const p of patchPaths.slice(0, 40)) log.info(`  ${p}`);
  log.info(`menu page: ${state?.menu?.title || '(closed)'} | rows: ${(state?.menu?.entries || []).length}`);
  const unk = unknownStates();
  if (unk.length) log.warn(`state words we could not classify: ${unk.map((u) => `"${u.token}" x${u.seen}`).join(', ')} -> add to PHASE_TOKENS in src/sync/intents.js`);
  log.info(`state hash: ${hash(state)} frames: ${events.length}`);
  log.info('Now: any canonical name above marked verb=yes is replayable by trigger;');
  log.info('verb=no rows are replayed by label through the menu walk.');
}

async function adminAsk(payload, cfg) {
  const url = `ws://127.0.0.1:${cfg.admin.port}`;
  const conn = connect(url);
  const got = await new Promise((res) => {
    const t = setTimeout(() => res(null), 4000);
    conn.on('message', (m) => {
      clearTimeout(t);
      res(m);
      conn.close();
    });
    conn.on('close', () => {
      clearTimeout(t);
      res(null);
    });
    conn.on('open', () => conn.send(payload));
  });
  if (!got) {
    console.error(`no running instance answered on ${url}\nstart one with: node src/index.js host`);
    process.exitCode = 1;
    return null;
  }
  return got;
}

async function runStatus() {
  const cfg = cfgFromFlags();
  const got = await adminAsk({ cmd: 'status' }, cfg);
  if (!got) return;
  if (!got.ok) return console.error(got.error);
  const s = got.data;
  console.log(`\n${s.self.name}  role=${s.self.role}  ${new Date(s.ts).toLocaleTimeString()}`);
  console.log(`  GSX      ${s.gsx.connected ? 'linked' : 'DOWN'} at ${s.gsx.url}  simReady=${s.gsx.simReady}  hash=${s.gsx.stateHash}`);
  console.log(`  menu     ${s.gsx.menu.shown ? 'open: ' + (s.gsx.menu.title || '') : 'closed'}`);
  console.log(`  peers    ${s.link.mode} room=${s.link.room} connected=${s.link.peers.map((p) => p.name).join(', ') || 'none'}`);
  console.log(`  counters ${Object.entries(s.sync.counters).filter(([, v]) => v).map(([k, v]) => `${k}=${v}`).join(' ') || 'all zero'}`);
  console.log('\n  service phases');
  if (s.gsx.unknownStates?.length) {
    console.log(`    \x1b[33m!\x1b[0m GSX used state words we do not classify yet: ${s.gsx.unknownStates.map((u) => `"${u.token}"`).join(', ')} - run \`probe\` and add them to PHASE_TOKENS`);
  }
  const keys = [...new Set([...Object.keys(s.gsx.phases || {}), ...Object.keys(s.peer?.phases || {})])];
  if (!keys.length) console.log('    (none yet - is GSX running and the Remote control server enabled?)');
  console.log(`    ${'SERVICE'.padEnd(34)}${'HERE'.padEnd(10)}THERE`.padEnd(54) + 'MATCH');
  for (const k of keys) {
    const a = s.gsx.phases[k];
    const b = s.peer?.phases?.[k];
    const same = a && b && a.state === b.state && a.phaseHash === b.phaseHash;
    const flag = a && b ? (same ? 'ok' : 'DRIFT') : s.peer ? 'partial' : '';
    console.log(`    ${String(k).slice(0, 32).padEnd(34)}${(a?.state || '-').padEnd(10)}${(b?.state || (s.peer ? '-' : 'no peer')).padEnd(10)}${flag}${a && b && !same ? '   <- ' + (a.progress ?? '') + '/' + (b.progress ?? '') : ''}`);
  }
  if (s.sync.recent?.length) {
    console.log('\n  last intents');
    for (const r of s.sync.recent.slice(-8)) console.log(`    ${new Date(r.ts).toLocaleTimeString()}  ${r.src.padEnd(12)} ${r.why}`);
  }
  console.log('');
}

async function runTrigger() {
  const cfg = cfgFromFlags();
  const name = flags._[0] || flags.name;
  if (!name) return console.error('usage: node src/index.js trigger Boarding');
  const got = await adminAsk({ cmd: 'trigger', name }, cfg);
  if (got) console.log(JSON.stringify(got, null, 2));
}

async function runNote() {
  const cfg = cfgFromFlags();
  const text = flags._.join(' ') || flags.text || '';
  const got = await adminAsk({ cmd: 'note', text }, cfg);
  if (got) console.log(got.ok ? 'sent' : got.error);
}

async function runStop() {
  const cfg = cfgFromFlags();
  const got = await adminAsk({ cmd: 'stop' }, cfg);
  if (got) console.log(got.ok ? `instance on admin port ${cfg.admin.port} shutting down` : got.error);
}

async function runDump() {
  const cfg = cfgFromFlags();
  const got = await adminAsk({ cmd: 'dump' }, cfg);
  if (got) console.log(JSON.stringify(got.data, null, 2));
}

function runHelp() {
  console.log(`gsx-sync - shared-cockpit ground sync for FSDT GSX Pro (MSFS 2024)

  host   [--port 8790] [--room CODE] [--pass PHRASE] [--role symmetric|driver|copilot]
  join   ws://HOST_IP:8790 [--room CODE] [--pass PHRASE]
  status                     table of local vs peer service phases
  trigger Boarding           fire one GSX service locally (bypasses detection)
  probe  --seconds 25        record the real Couatl state schema into capture/
  note   "text"              operator message to the peer
  dump                       print the mirrored GSX state as JSON
  stop                       ask a running instance to shut down cleanly
  serve                      internet relay (also: node tools/relay.js)

  common flags: --gsx ws://127.0.0.1:8744 --admin 8791 --level debug
                --no-reconcile --no-menu-mirror --reconcile 5

PLAYING TOGETHER
  Same house / LAN : one box runs 'host', the other runs
                     'join ws://<host box ip>:8790 --room <same code>'.
                     'host' prints the exact line to copy. First time only, on
                     the host box (as admin):
                       netsh advfirewall firewall add rule name=gsx-sync dir=in
                         action=allow protocol=TCP localport=8790
  Different ISPs   : no router port-forwarding needed if you use the relay: run
                     'node tools/relay.js --port 8790' on any reachable box
                     (a €3 VPS is fine) and have BOTH cockpits 'join' it with the
                     same --room code and --pass phrase.

  Either way: each cockpit enables GSX Settings -> Remote control server, and
  runs its own copy. Nothing about the sim itself is synced - only who asked
  for which ground service, replayed locally.

Setup on each cockpit: GSX Settings -> enable the Remote control server (Couatl
listens on ws://127.0.0.1:8744). Then run 'node src/index.js probe' once per box
to confirm GSX's service rows are recognised.

Canonical service names: ${SERVICE_VOCAB.map((v) => v.name).join(', ')}
`);
}

function runServe() {
  const cfg = cfgFromFlags();
  const log = new Log({ level: cfg.log.level });
  const relayImport = path.join(ROOT, 'tools', 'relay.js');
  if (!fs.existsSync(relayImport)) return console.error('tools/relay.js missing');
  import('file://' + relayImport)
    .then((m) => m.startRelay({ port: cfg.net.port, bind: cfg.net.bind, log }))
    .catch((e) => console.error(e.message));
}

switch (cmd) {
  case 'host':
  case 'join':
    runInstance();
    break;
  case 'probe':
    runProbe();
    break;
  case 'status':
    runStatus();
    break;
  case 'trigger':
    runTrigger();
    break;
  case 'note':
    runNote();
    break;
  case 'dump':
    runDump();
    break;
  case 'stop':
    runStop();
    break;
  case 'serve':
    runServe();
    break;
  default:
    runHelp();
}
