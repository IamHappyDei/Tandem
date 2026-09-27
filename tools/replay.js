#!/usr/bin/env node

import fs from 'node:fs';
import path from 'node:path';
import { ROOT, CAPTURE_DIR } from '../src/config.js';
import { servicePhaseMap, unknownStates } from '../src/sync/intents.js';

const arg = process.argv[2];
const candidates = arg ? [arg] : (fs.existsSync(CAPTURE_DIR) ? fs.readdirSync(CAPTURE_DIR).filter((f) => f.endsWith('.json')).sort().reverse() : []);
let p = '';
for (const c of candidates) {
  const abs = path.isAbsolute(c) ? c : path.join(CAPTURE_DIR, c);
  const alt = path.isAbsolute(c) ? c : path.join(ROOT, c);
  const found = fs.existsSync(abs) ? abs : fs.existsSync(alt) ? alt : '';
  if (!found) continue;
  const frames = JSON.parse(fs.readFileSync(found, 'utf8')).frames?.length;
  if (arg || frames) {
    p = found;
    break;
  }
}
if (!p) {
  console.error('no capture with frames in capture/ - run: node src/index.js probe --seconds 20');
  process.exit(1);
}
const j = JSON.parse(fs.readFileSync(p, 'utf8'));

const GsxClient = (await import('../src/gsx/client.js')).GsxClient;
const store = new GsxClient({ url: 'none' });
for (const { m } of j.frames) {
  if (m.type === 'snapshot') {
    const { v, type, ts, id, ...rest } = m;
    store.state = rest;
  } else if (m.type === 'patch') store._applyPatch(m.path, m.value);
}

const pm = servicePhaseMap(store.state?.services);
console.log(`${path.basename(p)}  frames: ${j.frames.length}  rows: ${Object.keys(pm).length}`);
for (const [k, v] of Object.entries(pm)) {
  console.log(`  ${v.state.padEnd(8)} ${String(k).padEnd(16)} verb=${v.knownVerb ? 'yes' : 'no '} phase=${v.phaseHash} progress=${v.progress ?? '-'} "${String(v.label).slice(0, 40)}"`);
}
const unk = unknownStates();
console.log(unk.length ? `\nstate words to add to PHASE_TOKENS: ${unk.map((u) => `"${u.token}"`).join(', ')}` : '\nall state words recognised');
