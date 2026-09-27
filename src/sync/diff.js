
import crypto from 'node:crypto';

export function isObj(v) {
  return v && typeof v === 'object';
}

const VOLATILE_KEY =
  /^(progress|percent|pct|completion|elapsed|remaining|time|timer|secs?|seconds|minutes|count|qty|quantity|amount|liters?|litres?|gallons?|kg|lb|weight|fuel|pax|passengers|bags?|suitcases|lat|lon|alt|agl|speed|value$x|cur|x|y|z|pos|position|heading|rpm|volts?|hz)$/i;

const STABLE_KEY =
  /^(state|status|phase|stage|step|index|gen|id|mode|type|doors?|armed|enabled|active|connected|on|off|ready|done|finished|available|busy|stateraw|statecode|statuscode|stateid)$/i;

const NOISE_KEY =
  /^(statetext|progresstext|statustext|text|caption|description|desc|label|displayname|title|message|html|statushtml|tooltip|hint|timestamp|time|datetime|duration|eta|icon|iconsvg|svg|url|path|note|detail|substate)$/i;

export function project(value, keyHint = '', dropNoise = false) {
  if (value === null || value === undefined) return null;
  const t = typeof value;
  if (t === 'boolean') return value;
  if (t === 'number') {
    if (VOLATILE_KEY.test(keyHint) && !STABLE_KEY.test(keyHint)) return null;
    if (!Number.isFinite(value)) return null;
    if (STABLE_KEY.test(keyHint)) return Math.round(value);
    return value === 0 ? 0 : 1;
  }
  if (t === 'string') {
    if (dropNoise && NOISE_KEY.test(keyHint)) return null;
    const s = value.trim();
    if (!s) return null;

    if (s.length > 400 || s.startsWith('data:')) return s.length > 400 ? null : 'data';
    if (/^\d{4}-\d{2}-\d{2}T/.test(s)) return null;
    return s.length > 160 ? s.slice(0, 160) : s;
  }
  if (Array.isArray(value)) {

    if (value.every((v) => typeof v === 'number')) return value.length;
    return value.map((v, i) => project(v, String(i), dropNoise));
  }
  if (t === 'object') {
    const out = {};
    for (const k of Object.keys(value).sort()) {
      const p = project(value[k], k, dropNoise);
      if (p !== null && p !== undefined) out[k] = p;
    }
    return out;
  }
  return null;
}

export function hash(value) {
  return crypto.createHash('sha256').update(stableStringify(value)).digest('hex').slice(0, 16);
}

export function stableStringify(value) {
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'null';
  if (Array.isArray(value)) return '[' + value.map(stableStringify).join(',') + ']';
  return (
    '{' +
    Object.keys(value)
      .sort()
      .map((k) => JSON.stringify(k) + ':' + stableStringify(value[k]))
      .join(',') +
    '}'
  );
}

export function diff(a, b, prefix = '', out = []) {
  if (a === b) return out;
  if (isObj(a) && isObj(b) && Array.isArray(a) === Array.isArray(b)) {
    if (Array.isArray(a)) {
      const n = Math.max(a.length, b.length);
      for (let i = 0; i < n; i++) diff(a[i], b[i], `${prefix}${prefix ? '.' : ''}${i}`, out);
      return out;
    }
    for (const k of new Set([...Object.keys(a), ...Object.keys(b)])) {
      const p = `${prefix}${prefix ? '.' : ''}${k}`;
      diff(a[k], b[k], p, out);
    }
    return out;
  }
  const kind = a === undefined ? 'add' : b === undefined ? 'del' : 'chg';
  out.push({ path: prefix || '(root)', before: a, after: b, kind });
  return out;
}

export function getIn(obj, dotted) {
  let node = obj;
  for (const seg of String(dotted).split('.')) {
    if (!isObj(node)) return undefined;
    node = node[seg];
  }
  return node;
}

export function summarize(obj, max = 24) {
  if (!isObj(obj)) return String(obj);
  return Object.keys(obj)
    .slice(0, max)
    .map((k) => {
      const v = obj[k];
      const t = Array.isArray(v) ? `array(${v.length})` : v === null ? 'null' : typeof v;
      return `${k}:${t}`;
    })
    .join(' ');
}

export function findKey(obj, re, depth = 0) {
  if (depth > 6 || !isObj(obj)) return undefined;
  for (const k of Object.keys(obj)) {
    if (re.test(k)) return obj[k];
  }
  for (const k of Object.keys(obj)) {
    const v = obj[k];
    if (isObj(v)) {
      const hit = findKey(v, re, depth + 1);
      if (hit !== undefined) return hit;
    }
  }
  return undefined;
}

export function countDeep(obj) {
  if (!isObj(obj)) return 1;
  let n = 0;
  for (const k of Object.keys(obj)) n += countDeep(obj[k]);
  return n + 1;
}
