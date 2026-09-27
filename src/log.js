
const LEVELS = { debug: 10, info: 20, warn: 30, error: 40 };
const COLOR = { debug: '\x1b[90m', info: '\x1b[36m', warn: '\x1b[33m', error: '\x1b[31m' };
const RESET = '\x1b[0m';

export class Log {
  constructor({ level = 'info', quiet = false, tag = '' } = {}) {
    this.min = LEVELS[level] ?? 20;
    this.quiet = quiet;
    this.tag = tag;
    this.ring = [];
  }
  _at(lvl, args) {
    if (LEVELS[lvl] < this.min) return;
    const msg = args
      .map((a) => (typeof a === 'string' ? a : inspect(a)))
      .join(' ');
    const rec = { t: Date.now(), lvl, msg };
    this.ring.push(rec);
    if (this.ring.length > 500) this.ring.shift();
    if (this.quiet) return;
    const stamp = new Date(rec.t).toISOString().slice(11, 19);
    process.stdout.write(
      `${COLOR[lvl] || ''}${stamp} ${lvl.toUpperCase().padEnd(5)}${this.tag ? ' ' + this.tag : ''} ${msg}${RESET}\n`,
    );
  }
  debug(...a) {
    this._at('debug', a);
  }
  info(...a) {
    this._at('info', a);
  }
  warn(...a) {
    this._at('warn', a);
  }
  error(...a) {
    this._at('error', a);
  }
  recent(n = 40) {
    return this.ring.slice(-n);
  }

  child(tag) {
    const l = new Log({ level: levelName(this.min), quiet: this.quiet, tag: [this.tag, tag].filter(Boolean).join('') });
    l.ring = this.ring;
    return l;
  }
}

function levelName(min) {
  return Object.keys(LEVELS).find((k) => LEVELS[k] === min) || 'info';
}

function inspect(x) {
  try {
    return typeof x === 'string' ? x : JSON.stringify(x, null, 0);
  } catch {
    return String(x);
  }
}

export const globalLog = new Log();
