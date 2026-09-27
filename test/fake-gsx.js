
import { serve } from '../src/util/ws.js';
import { hash } from '../src/sync/diff.js';

const IDLE = 'Not requested';
const ACTIVE = 'In progress';
const DONE = 'Completed';

const MENU_ROWS = [
  'Boarding',
  'Deboarding',
  'Refueling',
  'Catering',
  'GPU',
  'Operate Jetways',
  'Chocks On',
  'Pushback',
];

const ROW_TO_SERVICE = {
  Boarding: 'Boarding',
  Deboarding: 'Deboarding',
  Refueling: 'Refueling',
  Catering: 'Catering',
  GPU: 'GPU',
  'Operate Jetways': 'Jetways',
  'Chocks On': 'Chocks',
  Pushback: 'Pushback',
};

export class FakeGsx {
  constructor({ port, host = '127.0.0.1', name = 'fake', strictArgs = false, log = null } = {}) {
    this.port = port;
    this.host = host;
    this.name = name;
    this.strictArgs = strictArgs;
    this.log = log;
    this.conns = new Set();
    this.services = {};
    for (const n of ['Boarding', 'Deboarding', 'Refueling', 'Catering', 'GPU', 'Jetways', 'Chocks', 'Pushback']) {
      this.services[n] = { stateText: IDLE, stateClass: 'idle', progress: 0, vehicles: [] };
    }
    this.state = {
      services: this.services,
      menu: { title: 'Ramp Manager', entries: MENU_ROWS, disabled: [], stateClass: [] },
      menuShown: false,
      statusHtml: '<div>status</div>',
      message: { visible: false, text: '' },
      simbrief: { status: 'loaded', gen: 1 },
      startup: { active: false, bars: [] },
    };
    this.commands = [];
    this.server = null;
  }

  start() {
    return new Promise((res) => {
      this.server = serve({
        port: this.port,
        host: this.host,
        onConn: (conn) => {
          this.conns.add(conn);
          conn.on('message', (m) => this._on(conn, m));
          conn.on('close', () => this.conns.delete(conn));
        },
      });
      this.server.on('listening', () => res(this));
    });
  }

  stop() {
    for (const c of this.conns) c.close();
    this.conns.clear();
    this.server?.close();
  }

  _on(conn, m) {
    if (m.type === 'subscribe') {
      conn.send({ type: 'hello', gsxRunning: true });
      conn.send({ type: 'snapshot', v: 1, ts: Date.now(), id: hash(this.state), ...this.state });
      return;
    }
    if (m.type === 'command') return this._command(conn, m);
    conn.send({ type: 'result', ok: false, error: { code: 'bad_message' } });
  }

  _command(conn, m) {
    const { verb, args = {} } = m;
    this.commands.push({ verb, args, at: Date.now() });
    if (verb === 'service.trigger') {
      const given = this.strictArgs ? args.name : args.service ?? args.name ?? args.id ?? args.command;
      const key = this._resolve(given);
      if (!key) return conn.send({ type: 'result', ok: false, error: { code: 'unknown_service', message: String(given) } });
      this.toggleService(key);
      return conn.send({ type: 'result', ok: true });
    }
    if (verb === 'menu.toggle') {
      this.state.menuShown = !this.state.menuShown;
      this._send({ type: 'patch', path: '/menuShown', value: this.state.menuShown });
      return conn.send({ type: 'result', ok: true });
    }
    if (verb === 'menu.close') {
      this.state.menuShown = false;
      this._send({ type: 'patch', path: '/menuShown', value: false });
      return conn.send({ type: 'result', ok: true });
    }
    if (verb === 'menu.pick') {
      const label = this.state.menu.entries[Number(args.index)];
      const key = ROW_TO_SERVICE[label];
      if (!key) return conn.send({ type: 'result', ok: false, error: { code: 'bad_index' } });
      this.state.menuShown = false;
      this._send({ type: 'patch', path: '/menuShown', value: false });
      this.toggleService(key);
      return conn.send({ type: 'result', ok: true });
    }
    if (verb === 'command.run') return conn.send({ type: 'result', ok: true });
    if (verb === 'input.submit' || verb === 'invoice.seen') return conn.send({ type: 'result', ok: true });
    return conn.send({ type: 'result', ok: false, error: { code: 'unknown_verb', message: String(verb) } });
  }

  _resolve(given) {
    const s = String(given || '').toLowerCase();
    if (!s) return null;
    if (this.services[given]) return given;
    for (const label of Object.keys(ROW_TO_SERVICE)) {
      if (label.toLowerCase() === s) return ROW_TO_SERVICE[label];
    }
    for (const [k, v] of Object.entries(ROW_TO_SERVICE)) {
      if (k.toLowerCase().includes(s) || s.includes(v.toLowerCase())) return v;
    }
    for (const key of Object.keys(this.services)) {
      if (key.toLowerCase() === s) return key;
    }
    return null;
  }

  toggleService(key, forceState = null) {
    const cur = this.services[key] || (this.services[key] = { stateText: IDLE, stateClass: 'idle', progress: 0, vehicles: [] });
    const next = forceState || (cur.stateText === ACTIVE ? IDLE : ACTIVE);
    cur.stateText = next;
    cur.stateClass = next === ACTIVE ? 'active' : next === DONE ? 'done' : 'idle';
    cur.progress = next === ACTIVE ? 1 : 0;
    this._send({ type: 'patch', path: '/services', value: structuredClone(this.services) });
    return next;
  }

  advanceProgress(key, pct) {
    const cur = this.services[key];
    if (!cur) return;
    cur.progress = pct;
    this._send({ type: 'patch', path: '/services', value: structuredClone(this.services) });
  }

  openMenu(open = true) {
    this.state.menuShown = open;
    this._send({ type: 'patch', path: '/menuShown', value: open });
  }

  setRaw(key, stateText) {
    const cur = this.services[key] || (this.services[key] = {});
    cur.stateText = stateText;
    cur.stateClass = stateText === ACTIVE ? 'active' : 'idle';
    cur.progress = stateText === ACTIVE ? 1 : 0;
    this._send({ type: 'patch', path: '/services', value: structuredClone(this.services) });
  }

  _send(m) {
    for (const c of this.conns) c.send(m);
  }

  stateOf(key) {
    return this.services[key]?.stateText;
  }
  countOf(verb) {
    return this.commands.filter((c) => c.verb === verb).length;
  }
}

export { IDLE, ACTIVE, DONE, MENU_ROWS };
