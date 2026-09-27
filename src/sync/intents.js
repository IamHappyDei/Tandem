
import { project, hash, findKey, isObj } from './diff.js';

export const SERVICE_VOCAB = [
  { name: 'Deboarding', tokens: ['deboard', 'offload', 'deplane', 'deplane'] },
  { name: 'Boarding', tokens: ['boarding', 'board', 'pax board'] },
  { name: 'Refueling', tokens: ['refuel', 'refueling', 'fuelling', 'fueling', 'fuel', 'hydrant'] },
  { name: 'Catering', tokens: ['catering', 'truck cater'] },
  { name: 'Departure', tokens: ['departure', 'all complete', 'complete service'] },
  { name: 'OperateJetways', tokens: ['jetway', 'jet bridge', 'jetbridge', 'bridge', 'paxbridge'] },
  { name: 'OperateStairs', tokens: ['stair', 'ramp stairs', 'stairs'] },
  { name: 'GPU', tokens: ['gpu', 'ground power', 'ground_power', 'pdu', 'pre-condition', 'air con', 'pac '] },
  { name: 'DeIce', tokens: ['deice', 'de-ice', 'anti icing', 'antiicing', 'ice protection'] },
  { name: 'Water', tokens: ['potable', 'water service', 'water'] },
  { name: 'Lavatory', tokens: ['lavatory', 'lav service', 'toilet', 'dmin'] },
  { name: 'Cleaning', tokens: ['cleaning', 'clean cab'] },

  { name: 'Chocks', tokens: ['chock'], knownVerb: false },
  { name: 'Pushback', tokens: ['pushback', 'push back', 'push-out', 'tow', 'tractor'], knownVerb: false },
  { name: 'Baggage', tokens: ['baggage', 'cargo', 'container', 'loader', 'belt'], knownVerb: false },
  { name: 'Crew', tokens: ['crew transport', 'crew bus', 'crew'], knownVerb: false },
  { name: 'FuelTruck', tokens: ['fuel truck', 'bowser', 'fueler'], knownVerb: false },
];

const KNOWN = new Map(SERVICE_VOCAB.map((v) => [v.name, v]));

export function canonicalize(raw) {
  if (raw === undefined || raw === null) return null;
  const s = ' ' + String(raw).toLowerCase().replace(/[_\-.]+/g, ' ').replace(/\s+/g, ' ');

  let best = null;
  let bestAt = Infinity;
  let bestLen = 0;
  for (const v of SERVICE_VOCAB) {
    for (const t of v.tokens) {
      const at = s.indexOf(t);
      if (at < 0) continue;

      if (at < bestAt || (at === bestAt && t.length > bestLen)) {
        best = v;
        bestAt = at;
        bestLen = t.length;
      }
    }
  }
  return best ? best.name : null;
}

export function hasVerb(name) {
  const v = name && KNOWN.get(name);
  return !!(v && v.knownVerb !== false);
}

export function cheapMenu(m) {
  if (!isObj(m)) return null;
  return {
    title: m.title ?? null,
    header: m.header ?? null,
    subtitle: m.subtitle ?? null,
    layout: m.layout ?? null,
    entries: Array.isArray(m.entries) ? m.entries.map((e) => (e === undefined ? null : String(e).trim())) : null,
    disabled: Array.isArray(m.disabled) ? m.disabled.map(Boolean) : null,
    stateClass: Array.isArray(m.stateClass) ? m.stateClass.map((x) => String(x ?? '')) : null,
    hasDocument: m.document ? String(m.document).length : 0,
  };
}

export function phaseOf(svc) {
  if (svc === undefined || svc === null) return { state: 'absent', hash: '0', progress: null, label: null, token: null };

  const enumTok = firstString(svc, /^(state|status|phase|stateclass|statecode)$/i);
  const raw = firstNumber(svc, /^(stateraw|statecode|statuscode|stateid)$/i);
  const prose = firstString(svc, /^(statetext|caption|text|description|desc|label|displayname)$/i);
  const progress = readProgress(svc);

  let state = classifyToken(enumTok) || classifyProse(prose) || classifyToken(prose);
  if (!state) {

    const proj = project(svc, '', true);
    if (proj === null || proj === false || proj === 0) state = 'idle';
    else if (typeof proj === 'boolean' || typeof proj === 'number') state = proj ? 'active' : 'idle';
    else {
      state = 'unknown';
      noteUnknown(enumTok || prose || (raw !== null ? `#${raw}` : typeof svc));
    }
  }

  let detail = project(svc, '', state !== 'unknown');
  const signals = Object.keys(detail || {}).filter((k) => !/^(id|icon|displayname|name)$/i.test(k));
  if (!signals.length) detail = project(svc);
  return { state, hash: hash({ state, detail }), progress, label: prose || enumTok || null, token: enumTok || (raw !== null ? String(raw) : null) };
}

const PHASE_TOKENS = [
  ['idle', ['notrequested', 'available', 'unavailable', 'idle', 'none', 'off', 'disabled', 'standby', 'inactive', 'notstarted', 'cancelled', 'canceled', 'stopped', 'unset', 'ready']],
  ['done', ['completed', 'complete', 'done', 'finished', 'invoiced', 'closed', 'deplaned']],
  ['active', ['inprogress', 'inuse', 'performing', 'requested', 'running', 'active', 'engaged', 'operating', 'busy', 'waiting', 'queued', 'underway', 'started', 'loading', 'unloading', 'on', 'true', 'yes', '1']],
];

function norm(tok) {
  return String(tok || '')
    .toLowerCase()
    .replace(/[^a-z0-9]/g, '');
}

function classifyToken(tok) {
  if (tok === null || tok === undefined || tok === '') return null;
  const s = norm(tok);
  for (const [state, list] of PHASE_TOKENS) if (list.includes(s)) return state;
  return null;
}

function classifyProse(t) {
  if (!t) return null;
  const s = String(t).toLowerCase();
  if (/(can be (requested|started|called|toggled)|is available|not requested|no service|not set|not started|awaiting|\bidle\b|\bnone\b|unchecked|disabled|\boff\b|\bfalse\b)/.test(s)) return 'idle';
  if (/(complet|finished|invoiced|\bdone\b)/.test(s)) return 'done';
  if ((/(in progress|underway|running|operating|engaged|in use|requested|waiting|queued|loading|unloading|departing|being performed|performing|\bon\b)/.test(s))) return 'active';
  return classifyToken(s);
}

function readProgress(svc) {
  const n = firstNumber(svc, /^(progress|percent|pct|completion)$/i);
  if (n !== null) return n;
  const t = firstString(svc, /^(progresstext|progress|percenttext)$/i);
  const m = t && /(\d{1,3})\s*%/.exec(t);
  return m ? Number(m[1]) : null;
}

const unknown = new Map();
function noteUnknown(tok) {
  const k = String(tok).slice(0, 60);
  unknown.set(k, (unknown.get(k) || 0) + 1);
}
export function unknownStates() {
  return [...unknown.entries()].map(([token, seen]) => ({ token, seen }));
}

function firstString(node, re, depth = 0) {
  const v = findKey(node, re, depth);
  return typeof v === 'string' ? v : typeof v === 'number' ? String(v) : null;
}
function firstNumber(node, re) {
  const v = findKey(node, re);
  return typeof v === 'number' ? v : null;
}

export function servicePhaseMap(services) {
  const out = {};
  if (!isObj(services)) return out;
  const rows = [];
  if (Array.isArray(services)) {
    for (const item of services) {
      if (!isObj(item)) continue;
      const key = item.name ?? item.label ?? item.id ?? item.service ?? item.title ?? null;
      rows.push([key ? String(key) : JSON.stringify(key ?? null).slice(0, 40), item]);
    }
  } else {
    for (const k of Object.keys(services)) rows.push([k, services[k]]);
  }
  for (const [key, value] of rows) {
    const canonical = canonicalize(key) ?? (isObj(value) ? canonicalize(value.name ?? value.label ?? '') : null);
    const info = isObj(value) && !Array.isArray(value) ? phaseOf(value) : phaseOf({ state: value });

    const verbKnown = canonical ? hasVerb(canonical) : false;
    out[key] = {
      key,
      canonical,
      knownVerb: verbKnown,
      state: info.state,
      phaseHash: info.hash,
      progress: info.progress,
      label: canonical || info.label || key,
      raw: isObj(value) ? undefined : value,
    };
  }
  return out;
}

export function classify(change) {
  const intents = [];
  const { before, after } = change;

  const b = servicePhaseMap(before.services);
  const a = servicePhaseMap(after.services);

  if (!Object.keys(b).length && Object.keys(a).length) {
    intents.push({ kind: 'bootstrap', replay: 'none', why: `services map first seen (${Object.keys(a).length} rows)` });
  } else {
    for (const key of new Set([...Object.keys(b), ...Object.keys(a)])) {
      const bb = b[key];
      const aa = a[key];
      if (bb && aa && bb.phaseHash === aa.phaseHash && bb.state === aa.state) continue;
      const from = bb ? bb.state : 'absent';
      const to = aa ? aa.state : 'absent';
      if (from === to && bb && aa && bb.phaseHash === aa.phaseHash) continue;
      const name = (aa || bb).canonical;
      intents.push({
        kind: 'service',
        name: name || undefined,
        key,
        from,
        to,
        label: (aa || bb).label,
        replay: name && (aa || bb).knownVerb ? 'service' : 'menu',
        why: `${name || key}: ${from} -> ${to}`,
      });
    }
  }

  const bm = cheapMenu(before.menu);
  const am = cheapMenu(after.menu);
  if (am && bm && after.menuShown) {
    const samePage = bm.title === am.title && JSON.stringify(bm.entries) === JSON.stringify(am.entries);
    if (!samePage) {
      intents.push({
        kind: 'menu',
        from: bm.title || null,
        to: am.title || null,
        entries: am.entries,
        picked: guessPickedLabel(bm, am),
        replay: 'menu',
        why: `menu "${bm.title || '?'}" -> "${am.title || '?'}"`,
      });
    }
  }

  const bkeys = new Set(String(before.top || '').split(',').filter(Boolean));
  for (const k of String(after.top || '').split(',').filter(Boolean)) {
    if (!bkeys.has(k) && k !== 'services' && k !== 'menu') {
      intents.push({ kind: 'unknown', key: k, replay: 'none', why: `new state key "${k}"` });
    }
  }
  return intents;
}

function guessPickedLabel(before, after) {
  if (!before.entries || !after.entries) return null;

  const cand = [after.header, after.title].filter(Boolean).map((s) => String(s).trim());
  for (const c of cand) if (before.entries.includes(c)) return c;

  const gone = before.entries.filter((e) => e && !after.entries.includes(e));
  return gone.length === 1 ? gone[0] : null;
}

export function describeIntents(intents) {
  return intents.map((i) => i.why).join(' | ');
}
