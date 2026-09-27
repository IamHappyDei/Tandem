
import { EventEmitter } from 'node:events';
import crypto from 'node:crypto';
import { connect, serve } from '../util/ws.js';

export const PROTO_VERSION = 1;

export class Link extends EventEmitter {
  constructor({ mode = 'host', bind = '0.0.0.0', port = 8790, url = '', room = 'GSX', pass = '', name = 'cockpit', log = null, reconnect = true } = {}) {
    super();
    this.mode = mode;
    this.bind = bind;
    this.port = port;
    this.url = url || (mode === 'join' ? '' : `ws://127.0.0.1:${port}`);
    this.room = room;
    this.pass = pass;
    this.name = name;
    this.log = log;
    this.reconnect = reconnect;
    this.origin = crypto.randomBytes(6).toString('hex');
    this.peers = new Map();
    this.server = null;
    this.client = null;
    this.listening = false;
    this._retry = null;
    this.stats = { rx: 0, tx: 0, peersJoined: 0, rejected: 0 };
  }

  start() {
    if (this.mode === 'host') this._host();
    else this._join();
    return this;
  }

  stop() {
    this.reconnect = false;
    clearTimeout(this._retry);
    if (this.server) {
      this.server.close();
      this.server = null;
    }
    if (this.client) {
      this.client.close();
      this.client = null;
    }
  }

  get connectedPeers() {
    return [...this.peers.values()];
  }

  _host() {
    this.server = serve({
      port: this.port,
      host: this.bind,
      onConn: (conn) => this._onInbound(conn),
      onError: (e) => {
        if (e.code === 'EADDRINUSE') this.log?.error(`peer port ${this.port} is already in use`);
        else this.log?.error('peer listen error', e.message);
      },
    });
    this.server.on('listening', () => {
      this.listening = true;
      this.log?.info(`hosting peer link on ${this.bind === '0.0.0.0' ? '<all interfaces>' : this.bind}:${this.port} (room ${this.room})`);
      this.emit('listening', this.port);
    });
  }

  _onInbound(conn) {
    let helloSeen = false;
    const peer = { name: '?', origin: conn.id, lamport: 0, since: Date.now() };
    conn.on('message', (m) => {
      if (!m || typeof m !== 'object') return;
      this.stats.rx++;
      if (m.t === 'hello') {
        if (m.room !== this.room) return this._reject(conn, `wrong room (host is "${this.room}")`);
        if (this.pass && String(m.pass || '') !== this.pass) return this._reject(conn, 'bad passphrase');
        peer.name = String(m.name || 'peer');
        peer.origin = String(m.origin || conn.id);
        peer.lamport = Number(m.lamport || 0) || 0;
        helloSeen = true;
        this.peers.set(conn, peer);
        this.stats.peersJoined++;
        conn.send({
          t: 'welcome',
          ver: PROTO_VERSION,
          origin: this.origin,
          name: this.name,
          peers: [...this.peers.values()].map((p) => p.name),
        });
        this.log?.info(`peer joined: ${peer.name} (${peer.origin})`);
        this.emit('join', peer);
        return;
      }
      if (!helloSeen) {
        this._reject(conn, 'say hello first');
        return;
      }
      this._handle(conn, m);
    });
    conn.on('close', (why) => {
      const p = this.peers.get(conn);
      this.peers.delete(conn);
      if (p) {
        this.log?.info(`peer left: ${p.name} (${why})`);
        this.emit('leave', p);
      }
    });
  }

  _reject(conn, reason) {
    this.stats.rejected++;
    conn.send({ t: 'reject', reason });
    setTimeout(() => conn.close(), 100);
    this.log?.warn(`rejected peer: ${reason}`);
  }

  _join() {
    if (!this.url) {
      this.log?.error('join mode needs a URL, e.g. join ws://192.168.1.20:8790');
      return;
    }
    const conn = connect(this.url);
    this.client = conn;
    let failures = 0;
    conn.on('open', () => {
      this.listening = true;
      conn.send({
        t: 'hello',
        ver: PROTO_VERSION,
        room: this.room,
        pass: this.pass,
        name: this.name,
        origin: this.origin,
        lamport: this.lamport || 0,
      });
    });
    conn.on('message', (m) => {
      this.stats.rx++;
      if (m.t === 'welcome') {
        this.peers.set(conn, {
          name: String(m.name || 'host'),
          origin: String(m.origin || 'host'),
          lamport: Number(m.lamport || 0) || 0,
          since: Date.now(),
        });
        this.stats.peersJoined++;
        this.log?.info(`joined ${this.url} as room "${this.room}"; host is ${this.peers.get(conn).name}`);
        this.emit('join', this.peers.get(conn));
        return;
      }
      if (m.t === 'reject') {
        this.reconnect = false;
        this.log?.error(`host refused us: ${m.reason}`);
        this.emit('rejected', m.reason);
        conn.close();
        return;
      }
      this._handle(conn, m);
    });
    conn.on('close', (why) => {
      const p = this.peers.get(conn);
      this.peers.delete(conn);
      if (p) this.emit('leave', p);
      this.log?.warn(`peer link down (${why})`);
      if (++failures === 2 && !p) {
        this.log?.warn(`cannot reach ${this.url} - check the IP, that the host box is running 'host', and that its firewall allows TCP ${String(this.url).split(':').pop()}`);
      }
      if (this.reconnect) {
        clearTimeout(this._retry);
        this._retry = setTimeout(() => this._join(), 2000);
      }
    });
    conn.on('error', () => {});
  }

  _handle(conn, m) {
    const peer = this.peers.get(conn);
    if (!peer) return;
    if (typeof m.lamport === 'number' && m.lamport > peer.lamport) peer.lamport = m.lamport;
    if (m.t === 'ping') {
      conn.send({ t: 'pong', origin: this.origin });
      return;
    }
    this.emit('message', { ...m, from: peer });
  }

  broadcast(msg, except = null) {
    let n = 0;
    for (const conn of this.peers.keys()) {
      if (conn === except) continue;
      if (conn.send(msg)) n++;
    }
    this.stats.tx += n;
    return n;
  }

  toString() {
    return `Link(${this.mode} room=${this.room} peers=${this.peers.size})`;
  }
}
