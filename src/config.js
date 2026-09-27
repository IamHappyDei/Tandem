import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';

export const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
export const RUN_DIR = path.join(ROOT, 'run');
export const CAPTURE_DIR = path.join(ROOT, 'capture');

export const DEFAULTS = {
  gsx: {

    url: 'ws://127.0.0.1:8744',
    channels: ['state', 'services', 'menu', 'prompts', 'billing'],
    reconnect: true,
  },
  net: {
    mode: 'host',
    bind: '0.0.0.0',
    port: 8790,
    url: '',
    room: 'GSX',
    pass: '',
    reconnect: true,
  },
  sync: {
    role: 'symmetric',
    echoMs: 4000,
    debounceMs: 60,
    mirrorMenuPicks: true,
    autoReconcile: true,
    reconcileSeconds: 5,
    confirmDigests: 2,
    autoStop: false,
    graceSeconds: 12,
    maxReplaysPerService: 3,
    captureState: true,
  },
  admin: {
    port: 8791,
  },
  log: {
    level: 'info',
  },
};

function isObj(v) {
  return v && typeof v === 'object' && !Array.isArray(v);
}

export function merge(base, over) {
  const out = structuredClone(base);
  for (const k of Object.keys(over || {})) {
    if (over[k] === undefined) continue;
    out[k] = isObj(over[k]) && isObj(out[k]) ? merge(out[k], over[k]) : over[k];
  }
  return out;
}

export function loadConfig({ file = path.join(ROOT, 'config.json'), overrides = {} } = {}) {
  let fromFile = {};
  try {
    fromFile = JSON.parse(fs.readFileSync(file, 'utf8'));
  } catch {

  }
  return merge(merge(DEFAULTS, fromFile), overrides);
}

export function saveConfig(cfg, file = path.join(ROOT, 'config.json')) {
  fs.writeFileSync(file, JSON.stringify(cfg, null, 2) + '\n');
  return file;
}

export function ensureDirs() {
  for (const d of [RUN_DIR, CAPTURE_DIR]) fs.mkdirSync(d, { recursive: true });
}

export function hostname() {
  return os.hostname();
}

export function localAddresses() {
  const out = [];
  for (const [name, list] of Object.entries(os.networkInterfaces())) {
    for (const a of list || []) {
      if (a.family === 'IPv4' && !a.internal) out.push({ name, address: a.address });
    }
  }
  return out;
}

export function slug(name) {
  return String(name || 'cockpit')
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/^[-.]+|[-.]+$/g, '')
    .slice(0, 40) || 'cockpit';
}
