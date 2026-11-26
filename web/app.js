'use strict';

const NAMES = {
  none: 'No protection',
  ratelimit: 'Rate limiting',
  waitingroom: 'Waiting room',
  loadshedding: 'Load shedding',
};
const ORDER = ['none', 'ratelimit', 'waitingroom', 'loadshedding'];
const fmt = new Intl.NumberFormat('en-US');

const $ = (id) => document.getElementById(id);
const form = $('controls');
const startBtn = $('start');
const canvas = $('chart');
const tooltip = $('tooltip');
const clock = $('clock');
const panels = $('panels');
const legend = $('legend');
const empty = $('empty');
const tailNote = $('tail-note');
const formError = $('form-error');
const summaryCard = $('summary-card');
const fairnessCard = $('fairness-card');
const fairnessEl = $('fairness');

let state = null;
let source = null;

function css(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}
const color = (s) => css(`--s-${s}`);

function params() {
  const fd = new FormData(form);
  const scenarios = fd.getAll('scenario');
  const q = new URLSearchParams();
  q.set('scenarios', scenarios.join(','));
  for (const k of ['buyers', 'capacity', 'admit', 'queue', 'impatient', 'seed', 'speed']) q.set(k, fd.get(k));
  return {
    scenarios: ORDER.filter((s) => scenarios.includes(s)),
    q,
    capacity: Number(fd.get('capacity')),
    buyers: Number(fd.get('buyers')),
  };
}

form.addEventListener('change', () => { formError.hidden = true; });

form.addEventListener('submit', (ev) => {
  ev.preventDefault();
  const p = params();
  if (p.scenarios.length === 0) {
    formError.hidden = false;
    return;
  }
  if (source) source.close();

  state = {
    scenarios: p.scenarios,
    buyers: p.buyers,
    capacity: Math.max(50, Math.floor(p.capacity / 50) * 50),
    seconds: 60,
    tracked: Math.floor(p.buyers / 2) + 1,
    frames: [],
    totals: Object.fromEntries(p.scenarios.map((s) => [s, emptyTotals()])),
    boughtAt: {},
    hoverT: null,
  };
  buildLegend();
  buildPanels();
  summaryCard.hidden = true;
  fairnessCard.hidden = true;
  tailNote.hidden = true;
  empty.hidden = true;
  startBtn.disabled = true;
  startBtn.textContent = 'Selling…';
  clock.textContent = '20:00:00';
  clock.classList.add('live');
  draw();

  source = new EventSource(`/api/run?${p.q}`);
  source.addEventListener('start', (e) => {
    const m = JSON.parse(e.data);
    state.seconds = m.seconds;
    state.capacity = m.capacity;
    draw();
  });
  source.addEventListener('second', (e) => onSecond(JSON.parse(e.data)));
  source.addEventListener('tail', (e) => onTail(JSON.parse(e.data)));
  source.addEventListener('fairness', (e) => { state.points = JSON.parse(e.data); });
  source.addEventListener('summary', (e) => {
    const rows = JSON.parse(e.data);
    showSummary(rows);
    showFairness(rows);
    finish();
  });
  source.onerror = () => finish();
});

function finish() {
  if (source) source.close();
  source = null;
  startBtn.disabled = false;
  startBtn.textContent = 'Open the sale again';
  clock.classList.remove('live');
}

function emptyTotals() {
  return { purchases: 0, gave_up: 0, rejected_429: 0, rejected_503: 0, timeouts: 0, wasted: 0, catalog_ok: 0, catalog_failed: 0 };
}

function addTotals(s, sec) {
  const tot = state.totals[s];
  for (const k of Object.keys(tot)) tot[k] += sec[k];
  return tot;
}

function onSecond(frame) {
  state.frames.push(frame);
  clock.textContent = clockAt(frame.t + 1);
  for (const s of state.scenarios) {
    const sec = frame.seconds[s];
    const tot = addTotals(s, sec);
    if (sec.tracked_state === 'bought' && state.boughtAt[s] === undefined) state.boughtAt[s] = frame.t;
    updatePanel(s, sec, tot);
  }
  draw();
}

// After the chart ends, buyers without protection may keep failing for a
// while. The server folds those seconds into one event so the totals match.
function onTail(tail) {
  let until = -1;
  for (const s of state.scenarios) {
    const t = tail[s];
    if (!t || t.seconds === 0) continue;
    until = Math.max(until, t.last_gave_up);
    const tot = addTotals(s, t.totals);
    if (t.bought_at >= 0 && state.boughtAt[s] === undefined) state.boughtAt[s] = t.bought_at;
    updatePanel(s, t.totals, tot);
  }
  if (until >= 0) {
    tailNote.textContent = `The chart stops a few seconds after the last purchase. Buyers who never got in keep failing until they give up, around ${clockAt(until + 1)}; the panels and the summary include that time.`;
    tailNote.hidden = false;
  }
}

function clockAt(t) {
  return `20:${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`;
}

// ---------- panels ----------

function buildLegend() {
  legend.innerHTML = state.scenarios
    .map((s) => `<span><span class="key" data-s="${s}"></span>${NAMES[s]}</span>`)
    .join('');
}

function buildPanels() {
  panels.innerHTML = state.scenarios.map((s) => `
    <article class="card panel" id="panel-${s}">
      <h3><span class="key" data-s="${s}"></span>${NAMES[s]}</h3>
      <div>
        <div class="label">Bought</div>
        <div class="headline"><span class="num" data-k="purchases">0</span><span class="of">/ ${fmt.format(state.buyers)}</span></div>
      </div>
      <div class="bar"><i style="background: var(--s-${s})"></i></div>
      <dl>
        <dt>Gave up</dt><dd data-k="gave_up">0</dd>
        <dt>${errorLabel(s)}</dt><dd data-k="errors">0</dd>
        <dt>Wasted work</dt><dd data-k="wasted">0</dd>
        <dt>Backend queue</dt><dd data-k="backend_queue">0</dd>
        ${s === 'waitingroom' ? '<dt>In the waiting room</dt><dd data-k="room_size">0</dd>' : ''}
      </dl>
      <div class="buyer" data-k="buyer">${buyerHTML(s, { tracked_state: 'not_arrived' })}</div>
    </article>`).join('');
}

function errorLabel(s) {
  switch (s) {
    case 'none': return 'Timeouts';
    case 'ratelimit': return '429 responses';
    case 'loadshedding': return '503 responses';
    default: return 'Errors';
  }
}

function updatePanel(s, sec, tot) {
  const el = $(`panel-${s}`);
  const set = (k, v) => {
    const d = el.querySelector(`[data-k="${k}"]`);
    if (d) d.textContent = fmt.format(v);
  };
  set('purchases', tot.purchases);
  set('gave_up', tot.gave_up);
  set('errors', tot.rejected_429 + tot.rejected_503 + tot.timeouts);
  set('wasted', tot.wasted);
  set('backend_queue', sec.backend_queue);
  set('room_size', sec.room_size);
  el.querySelector('.bar i').style.width = `${Math.min(100, (tot.purchases / state.buyers) * 100)}%`;
  el.querySelector('[data-k="buyer"]').innerHTML = buyerHTML(s, sec);
}

function buyerHTML(s, sec) {
  const plural = (n, word) => `${fmt.format(n)} ${word}${n === 1 ? '' : 's'}`;
  const who = `<div class="who">Buyer #${fmt.format(state.tracked)}</div>`;
  const req = plural(sec.tracked_requests, 'request');
  const err = plural(sec.tracked_errors, 'error');
  switch (sec.tracked_state) {
    case 'not_arrived':
      return `${who}<div class="big">Not here yet</div>`;
    case 'in_room':
      if (sec.tracked_position >= 0) {
        return `${who}<div class="big">${fmt.format(sec.tracked_position)} people ahead</div><div class="sub">in the waiting room</div>`;
      }
      return `${who}<div class="big">In the waiting room</div><div class="sub">random order, no position</div>`;
    case 'trying':
      return `${who}<div class="big">Trying to buy</div><div class="sub">${req}, ${err}</div>`;
    case 'bought':
      return `${who}<div class="big">Bought at ${clockAt(state.boughtAt[s])}</div><div class="sub">${req}, ${err}</div>`;
    default:
      return `${who}<div class="big">Gave up</div><div class="sub">after ${req} and ${err}</div>`;
  }
}

// ---------- summary ----------

function showSummary(rows) {
  const tbody = document.querySelector('#summary tbody');
  tbody.innerHTML = rows.map((r) => `
    <tr>
      <td><span class="key" data-s="${r.scenario}"></span>${NAMES[r.scenario]}</td>
      <td>${fmt.format(r.purchased)}</td>
      <td>${fmt.format(r.gave_up)}</td>
      <td>${r.purchased ? `${r.wait_p50.toFixed(1)} s` : '—'}</td>
      <td>${r.purchased ? `${r.wait_p95.toFixed(1)} s` : '—'}</td>
      <td>${r.requests_per_buyer.toFixed(1)}</td>
      <td>${r.errors_per_buyer.toFixed(1)}</td>
      <td>${Math.round(r.catalog_success_rate * 100)}%</td>
      <td>${fmt.format(r.wasted)}</td>
      <td>${r.purchased > 1 ? r.fairness.toFixed(2) : '—'}</td>
    </tr>`).join('');
  summaryCard.hidden = false;
}

// ---------- who buys first ----------

function showFairness(rows) {
  if (!state.points) return;
  fairnessEl.innerHTML = rows.map((r) => `
    <figure>
      <figcaption><span class="key" data-s="${r.scenario}"></span>${NAMES[r.scenario]}<span class="rho">${r.purchased > 1 ? `fairness ${r.fairness.toFixed(2)}` : ''}</span></figcaption>
      <canvas data-s="${r.scenario}"></canvas>
    </figure>`).join('');
  fairnessCard.hidden = false;
  drawFairness();
}

function drawFairness() {
  if (!state || !state.points || fairnessCard.hidden) return;
  const all = Object.values(state.points).flat();
  const tTop = Math.max(10, ...all.map((p) => p[1]));
  const yStep = niceStep(tTop / 3);
  const yMax = Math.ceil(tTop / yStep) * yStep;
  const P = { left: 34, right: 8, top: 8, bottom: 22 };
  for (const cv of fairnessEl.querySelectorAll('canvas')) {
    const s = cv.dataset.s;
    const dpr = window.devicePixelRatio || 1;
    const w = cv.clientWidth;
    const h = cv.clientHeight;
    cv.width = Math.round(w * dpr);
    cv.height = Math.round(h * dpr);
    const ctx = cv.getContext('2d');
    ctx.scale(dpr, dpr);
    ctx.font = '11px system-ui, -apple-system, "Segoe UI", sans-serif';
    const pw = w - P.left - P.right;
    const ph = h - P.top - P.bottom;
    const x = (o) => P.left + (o / state.buyers) * pw;
    const y = (t) => P.top + ph - (t / yMax) * ph;

    ctx.fillStyle = css('--ink-soft');
    ctx.textAlign = 'right';
    ctx.textBaseline = 'middle';
    ctx.lineWidth = 1;
    for (let v = 0; v <= yMax; v += yStep) {
      ctx.strokeStyle = v === 0 ? css('--axis') : css('--grid');
      line(ctx, P.left, crisp(y(v)), P.left + pw, crisp(y(v)));
      ctx.fillText(`${v}s`, P.left - 6, y(v));
    }
    ctx.textBaseline = 'alphabetic';
    ctx.textAlign = 'left';
    ctx.fillText('first to arrive', P.left, h - 4);
    ctx.textAlign = 'right';
    ctx.fillText('last', P.left + pw, h - 4);

    const pts = state.points[s] || [];
    ctx.fillStyle = color(s);
    ctx.globalAlpha = 0.6;
    for (const [o, t] of pts) {
      ctx.beginPath();
      ctx.arc(x(o), y(t), 1.6, 0, Math.PI * 2);
      ctx.fill();
    }
    ctx.globalAlpha = 1;
    if (pts.length > 0 && state.totals[s].purchases < state.buyers / 5) {
      ctx.fillStyle = css('--ink-soft');
      ctx.textAlign = 'center';
      ctx.fillText(`only ${fmt.format(state.totals[s].purchases)} bought`, P.left + pw / 2, P.top + ph / 2);
    }
  }
}

// ---------- chart ----------

const PAD = { left: 44, right: 12, top: 16, bottom: 26 };

function geometry() {
  const w = canvas.clientWidth;
  const h = canvas.clientHeight || 280;
  const seconds = state ? state.seconds : 60;
  const xStep = niceStep(seconds / 8);
  const tMax = Math.ceil(seconds / xStep) * xStep;
  const cap = state ? state.capacity : 500;
  let peak = cap;
  if (state) {
    for (const f of state.frames) for (const s of state.scenarios) peak = Math.max(peak, f.seconds[s].purchases);
  }
  const yStep = niceStep((peak * 1.15) / 4);
  const yMax = Math.ceil((peak * 1.15) / yStep) * yStep;
  const pw = w - PAD.left - PAD.right;
  const ph = h - PAD.top - PAD.bottom;
  return {
    w, h, pw, ph, tMax, xStep, yMax, yStep, cap,
    x: (t) => PAD.left + (t / tMax) * pw,
    y: (v) => PAD.top + ph - (v / yMax) * ph,
  };
}

function niceStep(raw) {
  const p = 10 ** Math.floor(Math.log10(raw));
  for (const m of [1, 2, 5, 10]) if (m * p >= raw) return m * p;
  return 10 * p;
}

function draw() {
  const dpr = window.devicePixelRatio || 1;
  const g = geometry();
  canvas.width = Math.round(g.w * dpr);
  canvas.height = Math.round(g.h * dpr);
  const ctx = canvas.getContext('2d');
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, g.w, g.h);
  ctx.font = '11px system-ui, -apple-system, "Segoe UI", sans-serif';
  const soft = css('--ink-soft');

  // grid + y ticks
  ctx.lineWidth = 1;
  ctx.fillStyle = soft;
  ctx.textAlign = 'right';
  ctx.textBaseline = 'middle';
  for (let v = 0; v <= g.yMax; v += g.yStep) {
    ctx.strokeStyle = v === 0 ? css('--axis') : css('--grid');
    line(ctx, PAD.left, crisp(g.y(v)), PAD.left + g.pw, crisp(g.y(v)));
    ctx.fillText(fmt.format(v), PAD.left - 8, g.y(v));
  }
  // x ticks
  ctx.textAlign = 'center';
  ctx.textBaseline = 'alphabetic';
  for (let t = 0; t <= g.tMax; t += g.xStep) ctx.fillText(`${t}s`, g.x(t), g.h - 6);

  // capacity reference: dashed, labelled inside the plot
  ctx.strokeStyle = soft;
  ctx.setLineDash([4, 4]);
  line(ctx, PAD.left, crisp(g.y(g.cap)), PAD.left + g.pw, crisp(g.y(g.cap)));
  ctx.setLineDash([]);
  ctx.textAlign = 'right';
  ctx.textBaseline = 'bottom';
  ctx.fillText(`capacity ${fmt.format(g.cap)}/s`, PAD.left + g.pw, g.y(g.cap) - 4);

  if (!state || state.frames.length === 0) return;

  // series; lines at zero are drawn first so the active ones stay on top
  ctx.lineWidth = 2;
  ctx.lineJoin = 'round';
  ctx.lineCap = 'round';
  const last = state.frames[state.frames.length - 1];
  const order = [...state.scenarios].sort((a, b) => last.seconds[a].purchases - last.seconds[b].purchases);
  for (const s of order) {
    ctx.strokeStyle = color(s);
    ctx.beginPath();
    ctx.moveTo(g.x(0), g.y(0));
    for (const f of state.frames) ctx.lineTo(g.x(f.t + 1), g.y(f.seconds[s].purchases));
    ctx.stroke();
  }

  // crosshair
  if (state.hoverT !== null && state.frames[state.hoverT]) {
    const f = state.frames[state.hoverT];
    const x = g.x(f.t + 1);
    ctx.strokeStyle = css('--axis');
    ctx.lineWidth = 1;
    line(ctx, crisp(x), PAD.top, crisp(x), PAD.top + g.ph);
    for (const s of state.scenarios) dot(ctx, x, g.y(f.seconds[s].purchases), color(s));
  }
}

const crisp = (v) => Math.round(v) + 0.5;

function line(ctx, x1, y1, x2, y2) {
  ctx.beginPath();
  ctx.moveTo(x1, y1);
  ctx.lineTo(x2, y2);
  ctx.stroke();
}

function dot(ctx, x, y, fill) {
  ctx.beginPath();
  ctx.arc(x, y, 5.5, 0, Math.PI * 2);
  ctx.fillStyle = css('--surface');
  ctx.fill();
  ctx.beginPath();
  ctx.arc(x, y, 4, 0, Math.PI * 2);
  ctx.fillStyle = fill;
  ctx.fill();
}

canvas.addEventListener('mousemove', (e) => {
  if (!state || state.frames.length === 0) return;
  const g = geometry();
  const rect = canvas.getBoundingClientRect();
  const mx = e.clientX - rect.left;
  const t = Math.round(((mx - PAD.left) / g.pw) * g.tMax) - 1;
  if (t < 0 || t >= state.frames.length) {
    hideTooltip();
    return;
  }
  state.hoverT = t;
  const f = state.frames[t];
  tooltip.innerHTML = `<div class="title">${clockAt(t + 1)} · purchases in that second</div>` + state.scenarios
    .map((s) => `<div class="row"><span class="key" data-s="${s}"></span>${NAMES[s]}<b>${fmt.format(f.seconds[s].purchases)}</b></div>`)
    .join('');
  tooltip.hidden = false;
  const tx = g.x(t + 1);
  const left = tx + 14 + tooltip.offsetWidth > g.w ? tx - 14 - tooltip.offsetWidth : tx + 14;
  tooltip.style.left = `${left}px`;
  tooltip.style.top = `${PAD.top}px`;
  draw();
});
canvas.addEventListener('mouseleave', hideTooltip);

function hideTooltip() {
  tooltip.hidden = true;
  if (state) state.hoverT = null;
  draw();
}

window.addEventListener('resize', () => { draw(); drawFairness(); });
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => { draw(); drawFairness(); });
draw();

// Opening the page with #run starts the sale right away (handy for sharing a link).
if (location.hash === '#run') form.requestSubmit();
