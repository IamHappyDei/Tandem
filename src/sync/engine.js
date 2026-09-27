
import { EventEmitter } from 'node:events';
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { classify, servicePhaseMap, hasVerb, unknownStates } from './intents.js';
import { hash } from './diff.js';
import { RUN_DIR, slug } from '../config.js';

const MAX_SEEN = 800;

export class SyncEngine extends EventEmitter {
  constructor({ cfg, gsx, link, log, name }) {
    super();
    this.cfg = cfg;
    this.gsx = gsx;
    this.link = link;
    this.log = log;
    this.name = name;
    this.id = crypto.randomBytes(4).toString('hex');
    this.lamport = 0;
    this.seen = new Set();
    this.seenOrder = [];
    this.suppress = new Map();
    this.pendingMenu = [];
    this.lastTouched = new Map();
    this.drift = new Map();
    this.replayCount = new Map();
    this.timers = [];
    this.counters = {
      localIntents: 0,
      broadcast: 0,
      rxActions: 0,
      appliedRemote: 0,
      suppressedEcho: 0,
      droppedDupe: 0,
      reconcileFixes: 0,
      reconcileSkipped: 0,
      failed: 0,
    };
    this.recent = [];
  }

  start() {
    this.gsx.on('change', (c) => this._onLocalChange(c));
    this.gsx.on('snapshot', () => {
      this.log?.info('GSX snapshot received - full state mirrored');
      this._askAndPublish();
    });
    this.link.on('message', (m) => this._onPeer(m));
    this.link.on('join', () => this._askAndPublish());

    const rec = Math.max(1, this.cfg.sync.reconcileSeconds) * 1000;
    this.timers.push(setInterval(() => this._publishDigest(), rec));
    this.timers.push(setInterval(() => this._sweep(), 1000));
    if (this.cfg.sync.captureState) this.timers.push(setInterval(() => this._writeState(), 2000));
    this.timers.forEach((t) => t.unref?.());
    this.log?.info(`engine up: role=${this.cfg.sync.role} mode=${this.cfg.net.mode} id=${this.id}`);
    return this;
  }

  stop() {
    for (const t of this.timers) clearInterval(t);
    this.timers = [];
    clearTimeout(this._digestTimer);
    this._writeState();
  }

  _onLocalChange(change) {
    let intents;
    try {
      intents = classify(change);
    } catch (e) {
      this.log?.error('classify failed:', e.message);
      return;
    }
    if (!intents.length) return;
    const now = Date.now();
    let phaseMoved = false;

    for (const intent of intents) {
      if (intent.kind === 'service') {
        this.lastTouched.set(intent.key, now);
        phaseMoved = true;
      }
      if (intent.replay === 'none') {
        this.log?.debug(intent.why);
        continue;
      }
      const sig = this._signature(intent);
      if (this._isSuppressed(sig)) {
        this.counters.suppressedEcho++;
        this.log?.debug(`echo dropped: ${intent.why}`);
        continue;
      }
      this.counters.localIntents++;
      this._note(intent, 'local');
      if (this.cfg.sync.role === 'copilot') {
        this.log?.debug(`copilot role: not broadcasting ${intent.why}`);
        continue;
      }
      this.lamport++;
      const msg = {
        t: 'action',
        id: `${this.id}-${this.lamport}`,
        origin: this.link.origin,
        who: this.name,
        lamport: this.lamport,
        ts: now,
        intent: slimIntent(intent),
      };
      const n = this.link.broadcast(msg);
      this.counters.broadcast += n;
      this.log?.info(`-> peers(${n}) ${intent.why}`);
    }

    if (phaseMoved) this._digestSoon();
  }

  _digestSoon() {
    if (this._digestTimer) return;
    this._digestTimer = setTimeout(() => {
      this._digestTimer = null;
      this._publishDigest();
    }, 250);
    this._digestTimer.unref?.();
  }

  _onPeer(m) {
    if (m.t === 'action') return this._onRemoteAction(m);
    if (m.t === 'digest') return this._onDigest(m);
    if (m.t === 'ask') {
      this._publishDigest(m.from?.origin);
      return;
    }
    if (m.t === 'note') {
      this.log?.info(`[${m.who || m.from?.name || 'peer'}] ${m.text}`);
      this.emit('note', m);
      return;
    }
  }

  async _onRemoteAction(m) {
    if (this.seen.has(m.id)) {
      this.counters.droppedDupe++;
      return;
    }
    this._remember(m.id);
    if (m.origin === this.link.origin) return;
    if (this.cfg.sync.role === 'driver') {
      this.log?.debug(`driver role: ignoring peer action ${m.intent?.why}`);
      return;
    }
    this.counters.rxActions++;
    const intent = m.intent || {};
    this._note(intent, `remote:${m.who || ''}`);
    this._suppressFor(intent);
    const ok = await this._apply(intent).catch((e) => {
      this.log?.error(`could not apply peer action (${intent.why || intent.kind}): ${e.message}`);
      return false;
    });
    if (ok) {
      this.counters.appliedRemote++;
      this.log?.info(`<- peer ${m.who || ''} applied: ${intent.why || intent.kind}`);
    } else {
      this.counters.failed++;
      this.log?.warn(`<- peer ${m.who || ''} could NOT be applied here: ${intent.why || intent.kind} (see 'status')`);
    }
    this.emit('applied', { intent, ok, from: m.who });
  }

  async _apply(intent) {
    if (intent.kind === 'service') {
      const name = intent.name;
      if (name && hasVerb(name) && intent.replay === 'service') {
        const res = await this.gsx.triggerService(name);
        if (res.ok) return true;
        this.log?.debug(`service.trigger(${name}) rejected in every shape: ${JSON.stringify(res.tried)}`);
      }

      return this._viaMenu(intent);
    }
    if (intent.kind === 'menu') return this._viaMenu(intent);
    return false;
  }

  _viaMenu(intent) {
    const label = intent.picked || intent.label || intent.name;
    if (!label) return Promise.resolve(false);
    if (!this.cfg.sync.mirrorMenuPicks) return Promise.resolve(false);
    const idx = this._indexOfLabel(label);
    if (idx >= 0) {
      this.gsx
        .pickMenu(idx)
        .then((r) => r?.ok === false && this.log?.debug('menu.pick rejected', JSON.stringify(r.error)))
        .catch(() => {});
      return Promise.resolve(true);
    }
    if (!this.pendingMenu.some((p) => p.label === label)) {
      this.pendingMenu.push({ label, intent, expires: Date.now() + 12000 });
      this.log?.info(`waiting for menu page with "${label}" (peer wanted: ${intent.why || label})`);
    }
    return Promise.resolve(false);
  }

  _indexOfLabel(label) {
    const want = norm(label);
    const entries = this.gsx.state?.menu?.entries || [];
    const disabled = this.gsx.state?.menu?.disabled || [];
    for (let i = 0; i < entries.length; i++) {
      if (disabled[i]) continue;
      if (norm(String(entries[i] ?? '')) === want) return i;
    }

    for (let i = 0; i < entries.length; i++) {
      const e = norm(String(entries[i] ?? ''));
      if (e && want && (e.includes(want) || want.includes(e))) return i;
    }
    return -1;
  }

  _publishDigest(onlyOrigin = null) {
    const digest = {
      t: 'digest',
      origin: this.link.origin,
      who: this.name,
      ts: Date.now(),
      stateHash: this.gsx.stateHash,
      phases: this.gsx.phases,
      menu: {
        shown: !!this.gsx.state?.menuShown,
        title: this.gsx.state?.menu?.title || null,
        labels: this.gsx.menuLabels.slice(0, 24),
      },
      simReady: this.gsx.simReady && this.gsx.gsxRunning,
    };
    if (onlyOrigin) {
      for (const [conn, peer] of this.link.peers) if (peer.origin === onlyOrigin) conn.send(digest);
      return;
    }
    this.link.broadcast(digest);
    this._compare(digest);
  }

  _askAndPublish() {
    this._publishDigest();
    this.link.broadcast({ t: 'ask', origin: this.link.origin });
  }

  _onDigest(m) {
    if (!m || m.origin === this.link.origin) return;
    this.peerDigest = m;
    this.emit('digest', m);
    this._compare(m, true);
  }

  _compare(digest, isPeer = false) {
    if (!isPeer) return;
    const mine = this.gsx.phases;
    const theirs = digest.phases || {};
    for (const key of new Set([...Object.keys(mine), ...Object.keys(theirs)])) {
      const a = mine[key];
      const b = theirs[key];
      if (!a || !b) continue;
      if (a.state === b.state && a.phaseHash === b.phaseHash) {
        this.drift.delete(key);
        this.replayCount.delete(key);
        continue;
      }

      const wantStart = b.state === 'active' && (a.state === 'idle' || a.state === 'absent');
      const wantStop = b.state === 'idle' && a.state === 'active';
      if (!wantStart && !wantStop) {
        this.log?.debug(`phase detail differs on ${key} (here=${a.state} there=${b.state}) - not actionable`);
        continue;
      }

      const n = (this.drift.get(key) || 0) + 1;
      this.drift.set(key, n);
      if (n < Math.max(1, this.cfg.sync.confirmDigests)) continue;
      const graceMs = Math.max(0, this.cfg.sync.graceSeconds) * 1000;
      if (Date.now() - (this.lastTouched.get(key) || 0) < graceMs) {
        this.counters.reconcileSkipped++;
        continue;
      }
      if (wantStop && !this.cfg.sync.autoStop) {
        this.log?.warn(`DRIFT ${key}: we are RUNNING, peer is idle - not cancelling a live service (sync.autoStop=false)`);
        continue;
      }
      this._reconcile(key, a, b, wantStart ? 'peer started it' : 'peer stopped it', digest);
    }
  }

  _reconcile(key, a, b, why, digest) {
    if (!this.cfg.sync.autoReconcile) {
      this.log?.warn(`DRIFT ${key}: here=${a.state} there=${b.state} (${why}) - auto-reconcile off`);
      return;
    }
    const n = (this.replayCount.get(key) || 0) + 1;
    if (n > this.cfg.sync.maxReplaysPerService) {
      this.log?.warn(`DRIFT ${key}: here=${a.state} there=${b.state} - giving up after ${this.cfg.sync.maxReplaysPerService} replays, do it by hand`);
      return;
    }
    this.replayCount.set(key, n);
    this.lastTouched.set(key, Date.now());
    const intent = {
      kind: 'service',
      name: a.canonical || b.canonical || null,
      key,
      label: a.label || b.label,
      from: a.state,
      to: b.state,
      replay: 'service',
      why: `reconcile ${key} (${why})`,
    };
    this._suppressFor(intent);
    this._apply(intent)
      .then((ok) => {
        this.counters.reconcileFixes++;
        this.log?.info(`reconcile ${key} (${why}) -> ${ok ? 'replayed' : 'queued/pending'}`);
        if (!ok) this.log?.warn(`reconcile ${key} needs a human: no verb and no matching menu label`);
      })
      .catch((e) => this.log?.error(`reconcile ${key} threw: ${e.message}`));
  }

  _signature(intent) {
    if (intent.kind === 'service') return `svc:${intent.name || intent.key}:${intent.to || intent.state}`;
    if (intent.kind === 'menu') return `menu:${norm(intent.to || '')}:${norm((intent.entries || []).slice(0, 3).join('|'))}`;
    return `${intent.kind}:${intent.key || ''}`;
  }

  _isSuppressed(sig) {
    const until = this.suppress.get(sig);
    if (!until) return false;
    if (Date.now() > until) {
      this.suppress.delete(sig);
      return false;
    }
    return true;
  }

  _suppressFor(intent) {
    this.suppress.set(this._signature(intent), Date.now() + this.cfg.sync.echoMs);

    if (intent.kind === 'service') {
      const base = `svc:${intent.name || intent.key}:`;
      for (const s of ['idle', 'active', 'done', 'absent']) this.suppress.set(base + s, Date.now() + this.cfg.sync.echoMs / 2);
    }
  }

  _remember(id) {
    if (!id) return;
    this.seen.add(id);
    this.seenOrder.push(id);
    while (this.seenOrder.length > MAX_SEEN) this.seen.delete(this.seenOrder.shift());
  }

  _note(intent, src) {
    this.recent.push({ ts: Date.now(), src, why: intent.why || intent.kind, kind: intent.kind });
    if (this.recent.length > 60) this.recent.shift();
    this.emit('intent', { intent, src });
  }

  _sweep() {
    const now = Date.now();
    for (const [k, v] of this.suppress) if (v < now) this.suppress.delete(k);
    const keep = [];
    for (const p of this.pendingMenu) {
      const idx = this._indexOfLabel(p.label);
      if (idx >= 0) {
        this.gsx.pickMenu(idx).catch(() => {});
        this.log?.info(`menu page matched - replayed peer pick "${p.label}"`);
        this.counters.appliedRemote++;
        continue;
      }
      if (p.expires > now) keep.push(p);
      else this.log?.warn(`peer pick "${p.label}" never appeared locally - skipped`)
    }
    this.pendingMenu = keep;
  }

  _writeState() {
    try {
      fs.mkdirSync(RUN_DIR, { recursive: true });
      fs.writeFileSync(
        path.join(RUN_DIR, `state-${slug(this.name)}.json`),
        JSON.stringify(this.status(), null, 2),
      );
    } catch {

    }
  }

  status() {
    return {
      ts: new Date().toISOString(),
      self: { name: this.name, id: this.id, origin: this.link.origin, role: this.cfg.sync.role },
      gsx: {
        url: this.cfg.gsx.url,
        connected: this.gsx.connected,
        simReady: this.gsx.simReady && this.gsx.gsxRunning,
        stateHash: this.gsx.stateHash,
        menu: {
          shown: !!this.gsx.state?.menuShown,
          title: this.gsx.state?.menu?.title || null,
          labels: this.gsx.menuLabels.slice(0, 24),
        },
        phases: this.gsx.phases,
        topKeys: Object.keys(this.gsx.state || {}),
        stats: this.gsx.stats,

        shapes: this.gsx.shape,
        unknownStates: unknownStates(),
      },
      link: {
        mode: this.cfg.net.mode,
        room: this.cfg.net.room,
        listening: this.link.listening,
        peers: this.link.connectedPeers.map((p) => ({ name: p.name, origin: p.origin, lamport: p.lamport })),
        stats: this.link.stats,
      },
      sync: {
        counters: this.counters,
        pendingMenu: this.pendingMenu.map((p) => p.label),
        suppressed: [...this.suppress.keys()],
        recent: this.recent.slice(-20),
      },
      peer: this.peerDigest
        ? { who: this.peerDigest.who, phases: this.peerDigest.phases, stateHash: this.peerDigest.stateHash, menu: this.peerDigest.menu, ts: this.peerDigest.ts }
        : null,
    };
  }
}

function slimIntent(intent) {
  return {
    kind: intent.kind,
    name: intent.name || null,
    key: intent.key || null,
    label: intent.label || null,
    from: intent.from || null,
    to: intent.to || null,
    replay: intent.replay,
    why: intent.why,
    picked: intent.picked || null,

    entries: (intent.entries || []).slice(0, 12).map((e) => String(e ?? '').slice(0, 60)),
  };
}

function norm(s) {
  return String(s || '')
    .toLowerCase()
    .replace(/\(.*?\)/g, ' ')
    .replace(/[^a-z0-9]+/g, ' ')
    .trim();
}
