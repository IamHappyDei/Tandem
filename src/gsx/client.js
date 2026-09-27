
import { EventEmitter } from 'node:events';
import { connect } from '../util/ws.js';
import { diff, hash } from '../sync/diff.js';
import { cheapMenu, servicePhaseMap } from '../sync/intents.js';

const RESULT_WINDOW_MS = 500;

const SHAPE_KEYS = ['service', 'name', 'id', 'command'];

export class GsxClient extends EventEmitter {
  constructor({ url = 'ws://127.0.0.1:8744', channels = ['state', 'prompts', 'toasts'], reconnect = true, log = null, debounceMs = 60 } = {}) {
    super();
    this.url = url;
    this.channels = channels;
    this.reconnect = reconnect;
    this.log = log;
    this.debounceMs = debounceMs;
    this.state = {};
    this.gsxRunning = false;
    this.capabilities = [];
    this.connected = false;
    this.simReady = false;
    this.primed = false;
    this._conn = null;
    this._retry = null;
    this._before = null;
    this._timer = null;
    this._resultWaiter = null;
    this._chain = Promise.resolve();
    this.shape = {};
    this.stats = { messages: 0, patches: 0, snapshots: 0, commands: 0, commandFails: 0, serialized: 0 };
  }

  start() {
    this._open();
    return this;
  }

  stop() {
    this.reconnect = false;
    clearTimeout(this._retry);
    clearTimeout(this._timer);
    this._flushChange(true);
    this._conn?.close();
    this._conn = null;
  }

  _open() {
    const conn = connect(this.url);
    this._conn = conn;
    conn.on('open', () => {
      this.connected = true;
      this.log?.info(`GSX link up ${this.url}`);
      conn.send({ type: 'subscribe', channels: this.channels });
      this.emit('link', true);
    });
    conn.on('message', (m) => this._onMessage(m));
    conn.on('close', (why) => {
      this.connected = false;
      this.primed = false;
      this._before = null;
      clearTimeout(this._timer);
      this._flushChange(true);
      this.emit('link', false);
      this.log?.warn(`GSX link down (${why})`);
      const w = this._resultWaiter;
      this._resultWaiter = null;
      if (w) w.reject(new Error('link down'));
      if (this.reconnect) {
        clearTimeout(this._retry);
        this._retry = setTimeout(() => this._open(), 1000);
      }
    });
    conn.on('error', (e) => this.log?.debug('gsx ws error', e.message));
    return conn;
  }

  _onMessage(m) {
    if (!m || typeof m !== 'object') return;
    this.stats.messages++;
    switch (m.type) {
      case 'hello':
        this.gsxRunning = !!m.gsxRunning;
        this.capabilities = Array.isArray(m.capabilities) ? m.capabilities : [];
        if (m.authRequired) this.log?.warn('GSX says this server needs auth - enable the Remote control server without a token, or the sync cannot read state');
        if (this.capabilities.length) this.log?.debug('GSX capabilities:', this.capabilities.join(', '));
        this.emit('hello', m);
        break;
      case 'snapshot': {

        this._before = null;
        clearTimeout(this._timer);
        this._timer = null;
        const { v, type, ts, id, ...rest } = m;
        this.state = rest;
        this.primed = true;
        this.stats.snapshots++;
        this.simReady = true;
        this.emit('snapshot', { ts, id, v });
        break;
      }
      case 'patch':
        if (!this.primed) break;
        this._mark();
        this._applyPatch(m.path, m.value);
        this.stats.patches++;
        break;
      case 'event':
        if (m.topic === 'engine') this.gsxRunning = !!m.gsxRunning;
        this.emit('event', m);
        break;
      case 'result': {

        const waiter = this._resultWaiter;
        if (waiter) {
          this._resultWaiter = null;
          (m.ok === false ? waiter.reject : waiter.resolve)(m);
        }
        if (m.ok === false) this.emit('commandError', m);
        break;
      }
      default:
        this.emit('unknown', m);
    }
  }

  _mark() {
    if (this._before) return;
    this._before = {
      services: structuredClone(this.state?.services ?? {}),
      menu: cheapMenu(this.state?.menu),
      menuShown: !!this.state?.menuShown,
      message: { ...(this.state?.message || {}) },
      top: Object.keys(this.state || {}).sort().join(','),
      at: Date.now(),
    };
    clearTimeout(this._timer);
    this._timer = setTimeout(() => this._flushChange(), this.debounceMs);
  }

  _flushChange(discard = false) {
    clearTimeout(this._timer);
    this._timer = null;
    const before = this._before;
    this._before = null;
    if (!before || discard) return;
    const after = {
      services: structuredClone(this.state?.services ?? {}),
      menu: cheapMenu(this.state?.menu),
      menuShown: !!this.state?.menuShown,
      message: { ...(this.state?.message || {}) },
      top: Object.keys(this.state || {}).sort().join(','),
    };
    const changed =
      hash(before.services) !== hash(after.services) ||
      hash(before.menu) !== hash(after.menu) ||
      before.menuShown !== after.menuShown ||
      hash(before.message) !== hash(after.message) ||
      before.top !== after.top;
    if (!changed) return;
    const deltas = [
      ...diff(before.services, after.services, 'services'),
      ...diff(before.menu, after.menu, 'menu'),
    ];
    this.emit('change', { before: { ...before, raw: this.state }, after: { ...after, raw: this.state }, deltas, at: Date.now() });
  }

  _applyPatch(path, value) {
    if (!path) return;
    const segs = String(path).split('/').filter((s) => s !== '');
    if (!segs.length) return;
    if (!this.state || typeof this.state !== 'object') this.state = {};
    let node = this.state;
    for (let i = 0; i < segs.length - 1; i++) {
      const k = segs[i];
      if (typeof node[k] !== 'object' || node[k] === null) node[k] = {};
      node = node[k];
    }
    const last = segs.at(-1);
    if (value === null || value === undefined) delete node[last];
    else node[last] = value;
  }

  async command(verb, args) {
    if (!this.connected) throw new Error('GSX link is down');

    const run = () => this._send(verb, args);
    const p = this._chain.then(run, run);
    this._chain = p.then(
      () => {},
      () => {},
    );
    this.stats.serialized++;
    return p;
  }

  async _send(verb, args) {
    if (!this.connected) throw new Error('GSX link is down');
    this.stats.commands++;
    this._conn.send({ type: 'command', verb, args });

    const verdict = await new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        if (this._resultWaiter?.timer === timer) this._resultWaiter = null;
        resolve({ ok: true, silent: true });
      }, RESULT_WINDOW_MS);
      this._resultWaiter = {
        timer,
        resolve: (m) => {
          clearTimeout(timer);
          if (this._resultWaiter?.timer === timer) this._resultWaiter = null;
          resolve(m);
        },
        reject: (e) => {
          clearTimeout(timer);
          if (this._resultWaiter?.timer === timer) this._resultWaiter = null;
          reject(e);
        },
      };
    }).catch((e) => ({ ok: false, error: (e && (e.error || e.message)) || String(e) }));
    if (verdict.ok === false) this.stats.commandFails++;
    return verdict;
  }

  async triggerService(name) {
    if (!name) return { ok: false, tried: [], error: 'no service name' };
    const known = this.shape['service.trigger'];
    const order = known ? [known, ...SHAPE_KEYS.filter((k) => k !== known)] : [...SHAPE_KEYS];
    const tried = [];
    for (const key of order) {
      const res = await this.command('service.trigger', { [key]: name });
      if (res.ok !== false) {
        this.shape['service.trigger'] = key;
        return { ok: true, key, value: name, tried };
      }
      tried.push({ key, error: res.error });
    }
    return { ok: false, tried };
  }

  pickMenu(index) {
    return this.command('menu.pick', { index });
  }
  toggleMenu() {
    return this.command('menu.toggle');
  }
  closeMenu() {
    return this.command('menu.close');
  }
  searchMenu(text) {
    return this.command('menu.search', { text });
  }
  runCommand(command) {
    return this.command('command.run', { command });
  }
  submitInput(gen, text) {
    return this.command('input.submit', { gen, text });
  }
  settingsGet() {
    return this.command('settings.get', {});
  }
  settingsSet(key, value) {
    return this.command('settings.set', { key, value });
  }

  get phases() {
    return servicePhaseMap(this.state?.services);
  }
  get stateHash() {
    return hash(this.phases);
  }
  get menuLabels() {
    return (this.state?.menu?.entries || []).map((e) => String(e ?? '').trim()).filter((s, i) => s && s !== '[c]');
  }

  get menuActiveIndices() {
    const sc = this.state?.menu?.stateClass || [];
    return sc.map((c, i) => (c && c !== '' ? i : -1)).filter((i) => i >= 0);
  }
}
