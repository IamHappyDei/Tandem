// Renders the real dashboard script against a real running app, in Node, with a
'use strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const __dirname = path.dirname(fileURLToPath(import.meta.url));

const PORT = process.argv[2] || '19945';
const realFetch = global.fetch;
global.fetch = (u, o) => realFetch(String(u).startsWith('/') ? BASE + u : u, o); // the page uses relative URLs
const BASE = `http://127.0.0.1:${PORT}`;

const html = fs.readFileSync(path.join(__dirname, '..', 'internal', 'ui', 'web', 'index.html'), 'utf8');
const script = html.match(/<script>([\s\S]*)<\/script>/)[1];

const nodes = new Map();
function node(id) {
  if (!nodes.has(id)) {
    nodes.set(id, {
      id, textContent: '', innerHTML: '', value: '', className: '', placeholder: '',
      hidden: false, disabled: false, scrollTop: 0, scrollHeight: 0, style: {},
      listeners: {},
      addEventListener(t, f) { this.listeners[t] = f; },
      click() { if (this.onclick) this.onclick(); },
      classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
      appendChild() {}, selectNode() {},
    });
  }
  return nodes.get(id);
}
global.document = {
  querySelector: sel => (sel.startsWith('#') ? node(sel.slice(1)) : node('other:' + sel)),
  querySelectorAll: () => [],
  createElement: () => node('created'),
  activeElement: node('nobody'),
  get body() { return node('body'); },
};
global.getSelection = () => ({ removeAllRanges() {}, addRange() {} });
const define = (k, v) => { try { Object.defineProperty(globalThis, k, { value: v, configurable: true, writable: true }); } catch { globalThis[k] = v; } };
define('navigator', { clipboard: { writeText: async () => {} } });
global.EventSource = class {
  constructor(url) { this.url = url; global.__es = this; }
  addEventListener(t, f) { (this.h = this.h || {})[t] = f; }
};
global.window = global;

const timers = [];
const realSetInterval = setInterval;
global.setInterval = (f, ms) => { timers.push(f); return 0; };

const errors = [];
process.on('uncaughtException', e => errors.push('uncaught: ' + e.message));

new Function(script)();

async function main() {
  const status = await (await fetch(BASE + '/api/status')).json();
  if (global.__es && global.__es.h && global.__es.h.status) {
    global.__es.h.status({ data: JSON.stringify(status) });
  }
  if (global.__es && global.__es.h && global.__es.h.log) {
    global.__es.h.log({ data: JSON.stringify({ ts: Date.now(), lvl: 'info', tag: '[syn]', msg: 'in room with There' }) });
  }
  for (const f of timers) { try { f(); } catch (e) { errors.push('tick: ' + e.message); } }

  const show = id => {
    const n = nodes.get(id) || {};
    const t = (n.innerHTML || n.textContent || '').replace(/\s+/g, ' ').trim();
    return `${id.padEnd(12)} ${t.slice(0, 108) || (n.hidden ? '(hidden)' : '(empty)')}`;
  };
  console.log('--- what a user would see ---');
  ['mark', 'live', 'eyebrow', 'big', 'sub', 'stringVal', 'chips', 'who', 'act', 'myCode', 'diag', 'fRoom'].forEach(k => console.log('  ' + show(k)));
  console.log('  trouble    ' + (nodes.get('trouble').hidden ? '(hidden)' : JSON.stringify((nodes.get('trouble').innerHTML || '').slice(0, 90))));
  console.log('  stringRow  ' + (nodes.get('stringRow').hidden ? '(hidden)' : 'shown'));
  console.log('  ' + show('formTag'));
  console.log('  veil       ' + (nodes.get('veil').hidden ? '(hidden)' : 'SHOWN - first run warning'));

  if ((status.app.link || {}).hide) { await nodes.get('revealBtn').onclick(); }  // start unhidden, whatever the box remembers
  const plain = nodes.get('stringVal').textContent;
  try { await nodes.get('revealBtn').onclick(); } catch (e) { errors.push('revealBtn: ' + e.message); }
  const maskedTxt = nodes.get('stringVal').textContent;
  const readable = /\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+|127\.0\.0\.1:\d+/.test(maskedTxt);
  const m = !readable && /•/.test(maskedTxt);
  console.log(`  ${m ? 'ok  ' : 'FAIL'} Hide masks the address   ${plain.slice(0, 26)} -> ${maskedTxt.slice(0, 34)}`);
  if (!m) errors.push('the masked string still contains a dialable address: ' + maskedTxt);
  try { await nodes.get('revealBtn').onclick(); } catch (e) { errors.push('revealBtn: ' + e.message); }
  const restored = nodes.get('stringVal').textContent === plain;
  console.log(`  ${restored ? 'ok  ' : 'FAIL'} Show puts it back`);
  if (!restored) errors.push('Show did not restore the real string');
  if ((status.app.link || {}).hide) await nodes.get('revealBtn').onclick();  // leave the box as it was

  nodes.get('pasteBox').value = 'gsx1/127.0.0.1:8790/TESTROOM';
  try { nodes.get('joinBtn').onclick(); } catch (e) { errors.push('joinBtn: ' + e.message); }
  nodes.get('noteText').value = 'hello';
  try { nodes.get('noteSend').onclick(); } catch (e) { errors.push('noteSend: ' + e.message); }
  try { nodes.get('leave').onclick(); } catch (e) { errors.push('leave: ' + e.message); }
  try { nodes.get('copyBtn').onclick(); } catch (e) { errors.push('copyBtn: ' + e.message); }
  await new Promise(r => setTimeout(r, 900));

  {
    const c = JSON.parse(JSON.stringify(status)); c.app.link = { opaque: false, hide: false, warnAck: false };
    global.__es.h.status({ data: JSON.stringify(c) });
    const shown = !nodes.get('veil').hidden;
    console.log(`  ${shown ? 'ok  ' : 'FAIL'} an unseen warning blocks the page`);
    if (!shown) errors.push('first run showed no warning');
    const d = JSON.parse(JSON.stringify(status)); d.app.link = { opaque: true, hide: false, warnAck: true };
    global.__es.h.status({ data: JSON.stringify(d) });
    try { await global.ensureStringForTest?.(); } catch (e) {}
  }

  const shapes = {
    'no GSX': JSON.parse(JSON.stringify(status, (k, v) => (k === 'connected' ? false : v))),
    'with a peer': (() => { const c = JSON.parse(JSON.stringify(status)); c.status.link.peers = [{ name: 'There', paths: ['udp:1.2.3.4:5'], ts: Date.now() }]; return c; })(),
    'no public address': (() => { const c = JSON.parse(JSON.stringify(status)); c.app.public = []; return c; })(),
    'stalled, no dial': (() => { const c = JSON.parse(JSON.stringify(status)); c.app.public = ['9.9.9.9:8790']; return c; })(),
    'empty phases': (() => { const c = JSON.parse(JSON.stringify(status)); c.status.gsx.phases = {}; return c; })(),
  };
  for (const [name, payload] of Object.entries(shapes)) {
    try {
      global.__es.h.status({ data: JSON.stringify(payload) });
      console.log(`  ok  ${name} -> ${JSON.stringify((nodes.get('big').textContent || ''))}`);
    } catch (e) { errors.push(`${name}: ${e.message}`); }
  }

  if (errors.length) {
    console.log('\nFAILURES:');
    errors.forEach(e => console.log('  ' + e));
    process.exit(1);
  }
  console.log('\nno runtime errors in the dashboard script');
}
main().catch(e => { console.log('harness died: ' + e.message); process.exit(1); });
