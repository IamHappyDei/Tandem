#!/usr/bin/env node

import process from 'node:process';
import crypto from 'node:crypto';
import { serve } from '../src/util/ws.js';
import { Log } from '../src/log.js';

function parseFlags(list) {
  const out = { _: [] };
  for (let i = 0; i < list.length; i++) {
    const a = list[i];
    if (a.startsWith('--')) {
      const k = a.slice(2);
      const nxt = list[i + 1];
      if (nxt !== undefined && !nxt.startsWith('--')) {
        out[k] = nxt;
        i++;
      } else out[k] = true;
    } else out._.push(a);
  }
  return out;
}

export function startRelay({ port = 8790, bind = '0.0.0.0', log = new Log(), pass = '' } = {}) {

  const rooms = new Map();
  const stats = { rx: 0, tx: 0, dropped: 0, joins: 0 };

  function join(conn, room, name) {
    if (!rooms.has(room)) rooms.set(room, new Map());
    rooms.get(room).set(conn, { name, origin: conn.origin || crypto.randomBytes(4).toString('hex'), joined: Date.now() });
    conn.room = room;
    stats.joins++;
    for (const other of rooms.get(room).keys()) if (other !== conn) other.send({ t: 'note', who: 'relay', text: `${name} joined room ${room}` });
    log.info(`room ${room}: ${name} joined (${rooms.get(room).size} in room)`);
  }
  function leave(conn) {
    const room = conn.room;
    const info = rooms.get(room)?.get(conn);
    rooms.get(room)?.delete(conn);
    if (rooms.get(room)?.size === 0) rooms.delete(room);
    if (info) {
      for (const other of rooms.get(room)?.keys() || []) other.send({ t: 'note', who: 'relay', text: `${info.name} left room ${room}` });
      log.info(`room ${room}: ${info.name} left`);
    }
  }

  const server = serve({
    port,
    host: bind,
    onError: (e) => {
      if (e.code === 'EADDRINUSE') log.error(`relay: port ${port} already in use`);
      else log.error('relay error', e.message);
    },
    onConn: (conn) => {
      conn.origin = null;
      conn.on('message', (m) => {
        if (!m || typeof m !== 'object') return;
        stats.rx++;
        if (m.t === 'hello') {
          if (pass && String(m.pass || '') !== pass) {
            conn.send({ t: 'reject', reason: 'bad relay passphrase' });
            return conn.close();
          }
          conn.origin = String(m.origin || crypto.randomBytes(4).toString('hex'));
          join(conn, String(m.room || 'GSX'), String(m.name || 'peer'));
          conn.send({ t: 'welcome', ver: 1, origin: 'relay', name: `relay:${conn.room}`, peers: [...(rooms.get(conn.room)?.keys() || [])].length });
          return;
        }
        if (!conn.room) {
          conn.send({ t: 'reject', reason: 'say hello first' });
          return conn.close();
        }

        const out = { ...m, via: 'relay' };
        for (const other of rooms.get(conn.room).keys()) {
          if (other === conn) continue;
          if (out.origin && other.origin === out.origin) {
            stats.dropped++;
            continue;
          }
          if (other.send(out)) stats.tx++;
        }
      });
      conn.on('close', () => leave(conn));
    },
  });
  server.on('listening', () => log.info(`relay listening on ${bind}:${port}`));
  process.on('SIGINT', () => {
    log.info('relay down', stats);
    server.close();
    process.exit(0);
  });
  const report = setInterval(() => log.info(`relay up | ${[...rooms.keys()].join(',') || 'no rooms'} | ${JSON.stringify(stats)}`), 60000);
  report.unref?.();
  return { server, rooms, stats };
}

if (import.meta.url === `file://${process.argv[1].replace(/\\/g, '/')}`) {
  const f = parseFlags(process.argv.slice(2));
  startRelay({ port: Number(f.port || 8790), bind: f.bind || '0.0.0.0', pass: f.pass || '', log: new Log({ level: f.level || 'info', tag: '[relay]' }) });
}
