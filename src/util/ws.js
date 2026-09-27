
import http from 'node:http';
import crypto from 'node:crypto';
import { EventEmitter } from 'node:events';

const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11';
const MAX_FRAME = 8 * 1024 * 1024;
const OP = { CONT: 0x0, TEXT: 0x1, BIN: 0x2, CLOSE: 0x8, PING: 0x9, PONG: 0xa };

export class Conn extends EventEmitter {
  constructor(kind) {
    super();
    this.kind = kind;
    this.alive = false;
    this.id = crypto.randomBytes(4).toString('hex');
    this.remote = '';
    this._onMessageObj = this._onMessageObj.bind(this);
  }
  _onMessageObj(obj) {
    if (obj && typeof obj === 'object') this.emit('message', obj);
  }
  _fail(why) {
    if (!this.alive) return;
    this.alive = false;
    this.emit('close', why);
  }
  send() {
    return false;
  }
  close() {
    this.alive = false;
  }

  async ready(ms = 3000) {
    if (this.alive) return true;
    return new Promise((res) => {
      const t = setTimeout(() => res(false), ms);
      this.once('open', () => {
        clearTimeout(t);
        res(true);
      });
      this.once('close', () => {
        clearTimeout(t);
        res(false);
      });
    });
  }
}

function encodeFrame(opcode, payload) {
  const len = payload.length;
  let header;
  if (len < 126) {
    header = Buffer.alloc(2);
    header[1] = len;
  } else if (len < 65536) {
    header = Buffer.alloc(4);
    header[1] = 126;
    header.writeUInt16BE(len, 2);
  } else {
    header = Buffer.alloc(10);
    header[1] = 127;
    header.writeBigUInt64BE(BigInt(len), 2);
  }
  header[0] = 0x80 | opcode;
  return Buffer.concat([header, payload]);
}

function decodeFrame(buf) {
  if (buf.length < 2) return null;
  const b0 = buf[0];
  const b1 = buf[1];
  const fin = (b0 & 0x80) !== 0;
  const opcode = b0 & 0x0f;
  const masked = (b1 & 0x80) !== 0;
  let len = b1 & 0x7f;
  let off = 2;
  if (len === 126) {
    if (buf.length < off + 2) return null;
    len = buf.readUInt16BE(off);
    off += 2;
  } else if (len === 127) {
    if (buf.length < off + 8) return null;
    const big = buf.readBigUInt64BE(off);
    if (big > BigInt(MAX_FRAME)) throw new Error('ws frame too large');
    len = Number(big);
    off += 8;
  }
  let key = null;
  if (masked) {
    if (buf.length < off + 4) return null;
    key = buf.subarray(off, off + 4);
    off += 4;
  }
  if (buf.length < off + len) return null;
  let payload = buf.subarray(off, off + len);
  if (key) {
    const out = Buffer.allocUnsafe(len);
    for (let i = 0; i < len; i++) out[i] = payload[i] ^ key[i & 3];
    payload = out;
  }
  return { fin, opcode, payload, consumed: off + len };
}

class SocketConn extends Conn {
  constructor(socket, { expectMask = true } = {}) {
    super('socket');
    this.sock = socket;
    this.expectMask = expectMask;
    this.buf = Buffer.alloc(0);
    this.frags = [];
    this.fragOp = 0;
    this.lastPong = Date.now();
    this.alive = true;
    socket.setNoDelay(true);
    socket.on('data', (d) => this._data(d));
    socket.on('error', () => this._fail('socket-error'));
    socket.on('close', () => this._fail('socket-close'));

    socket.on('end', () => this._fail('socket-end'));
  }
  _data(chunk) {
    this.buf = this.buf.length ? Buffer.concat([this.buf, chunk]) : chunk;
    for (;;) {
      let f;
      try {
        f = decodeFrame(this.buf);
      } catch (e) {
        this._protocolError(e.message);
        return;
      }
      if (!f) return;
      this.buf = this.buf.subarray(f.consumed);
      if (f.opcode === OP.CLOSE) {
        try {
          this.sock.write(encodeFrame(OP.CLOSE, Buffer.alloc(0)));
        } catch {}
        try {
          this.sock.end();
        } catch {}
        this._fail('peer-close');
        return;
      }
      if (f.opcode === OP.PING) {
        try {
          this.sock.write(encodeFrame(OP.PONG, f.payload));
        } catch {}
        continue;
      }
      if (f.opcode === OP.PONG) {
        this.lastPong = Date.now();
        continue;
      }
      if (f.opcode === OP.CONT) {
        if (!this.frags.length) {
          this._protocolError('unexpected continuation');
          return;
        }
        this.frags.push(f.payload);
        if (!f.fin) continue;
        this._emitPayload(Buffer.concat(this.frags), this.fragOp);
        this.frags = [];
        continue;
      }
      if (f.opcode === OP.TEXT || f.opcode === OP.BIN) {
        if (!f.fin) {
          this.frags = [f.payload];
          this.fragOp = f.opcode;
          continue;
        }
        this._emitPayload(f.payload, f.opcode);
        continue;
      }
      this._protocolError(`unsupported opcode ${f.opcode}`);
      return;
    }
  }
  _emitPayload(payload, opcode) {
    if (opcode !== OP.TEXT) return;
    let obj;
    try {
      obj = JSON.parse(payload.toString('utf8'));
    } catch {
      return;
    }
    this._onMessageObj(obj);
  }
  _protocolError(why) {
    this.emit('error', new Error(why));
    try {
      this.sock.destroy();
    } catch {}
    this._fail('protocol:' + why);
  }
  send(msg) {
    if (!this.alive) return false;
    try {
      return this.sock.write(encodeFrame(OP.TEXT, Buffer.from(JSON.stringify(msg))));
    } catch {
      this._fail('write-failed');
      return false;
    }
  }
  ping() {
    if (!this.alive) return;
    try {
      this.sock.write(encodeFrame(OP.PING, Buffer.alloc(0)));
    } catch {}
  }
  close() {
    if (!this.alive) return;
    try {
      this.sock.write(encodeFrame(OP.CLOSE, Buffer.alloc(0)));
      this.sock.end();
    } catch {
      try {
        this.sock.destroy();
      } catch {}
    }
    this._fail('local-close');
  }
}

export function serve({ port, host = '127.0.0.1', onConn, onError } = {}) {
  const server = http.createServer((req, res) => {
    res.writeHead(200, { 'content-type': 'text/plain' });
    res.end('gsx-sync endpoint\n');
  });
  const conns = new Set();
  server.on('upgrade', (req, socket, head) => {
    const key = req.headers['sec-websocket-key'];
    if (!key || String(req.headers.upgrade || '').toLowerCase() !== 'websocket') {
      socket.destroy();
      return;
    }
    const accept = crypto.createHash('sha1').update(key + GUID).digest('base64');
    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\n' +
        'Upgrade: websocket\r\n' +
        'Connection: Upgrade\r\n' +
        `Sec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
    const conn = new SocketConn(socket);
    conn.remote = `${req.socket.remoteAddress}:${req.socket.remotePort}`;
    conn.on('close', () => conns.delete(conn));
    conns.add(conn);
    if (head && head.length) conn._data(head);
    onConn?.(conn, req);
  });
  server.on('error', (e) => onError?.(e));

  const keeper = setInterval(() => {
    for (const c of conns) {
      if (Date.now() - c.lastPong > 45000) {
        c.close();
        continue;
      }
      c.ping();
    }
  }, 15000);
  keeper.unref?.();
  server.listen(port, host);
  server.conns = conns;
  const close = server.close.bind(server);
  server.close = (cb) => {
    clearInterval(keeper);
    for (const c of conns) c.close();
    close(cb);
  };
  return server;
}

export function connect(url, { headers, timeoutMs = 8000 } = {}) {
  const conn = new Conn('client');
  let ws;
  try {
    ws = new WebSocket(url, headers ? { headers } : undefined);
  } catch (e) {
    queueMicrotask(() => conn.emit('error', e));
    queueMicrotask(() => conn._fail('ctor-failed'));
    return conn;
  }

  let reported = false;
  const finish = (why) => {
    if (reported) return;
    reported = true;
    clearTimeout(timer);
    conn.alive = false;
    conn.emit('close', why);
  };
  const timer = setTimeout(() => {
    if (reported) return;
    try {
      ws.close();
    } catch {}
    conn.emit('error', new Error(`no answer from ${url} within ${timeoutMs}ms`));
    finish('connect-timeout');
  }, timeoutMs);
  timer.unref?.();
  ws.onopen = () => {
    clearTimeout(timer);
    conn.alive = true;
    conn.emit('open');
  };
  ws.onmessage = (e) => {
    let obj;
    try {
      obj = JSON.parse(typeof e.data === 'string' ? e.data : Buffer.from(e.data).toString('utf8'));
    } catch {
      return;
    }
    conn._onMessageObj(obj);
  };
  ws.onclose = (e) => finish('remote-close:' + (e.code || 'n/a'));
  ws.onerror = () => {};
  conn.send = (msg) => {
    if (ws.readyState !== 1) return false;
    ws.send(JSON.stringify(msg));
    return true;
  };
  conn.close = () => {
    try {
      ws.close();
    } catch {}
    finish('local-close');
  };
  return conn;
}

export { SocketConn };
