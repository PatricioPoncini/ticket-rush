'use strict';

const NAMES = {
  none: 'No protection',
  ratelimit: 'Rate limiting',
  waitingroom: 'Waiting room',
  loadshedding: 'Load shedding',
};
const ORDER = ['none', 'ratelimit', 'waitingroom', 'loadshedding'];
const fmt = new Intl.NumberFormat('en-US');

const form = document.getElementById('controls');
const startBtn = document.getElementById('start');
const canvas = document.getElementById('chart');
const tooltip = document.getElementById('tooltip');
const clock = document.getElementById('clock');
const panels = document.getElementById('panels');
const legend = document.getElementById('legend');
const summaryCard = document.getElementById('summary-card');

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
  return { scenarios: ORDER.filter((s) => scenarios.includes(s)), q, capacity: Number(fd.get('capacity')), buyers: Number(fd.get('buyers')), queue: fd.get('queue') };
}

form.addEventListener('submit', (ev) => {
  ev.preventDefault();
  const p = params();
  if (p.scenarios.length === 0) return;
  if (source) source.close();

  state = {
    scenarios: p.scenarios,
    capacity: Math.floor(p.capacity / 50) * 50,
    tracked: Math.floor(p.buyers / 2) + 1,
    queue: p.queue,
    frames: [],
    totals: Object.fromEntries(p.scenarios.map((s) => [s, emptyTotals()])),
    boughtAt: {},
    hoverT: null,
  };
  buildLegend();
  buildPanels();
  summaryCard.hidden = true;
  startBtn.disabled = true;
  startBtn.textContent = 'Selling…';
  draw();

  source = new EventSource(`/api/run?${p.q}`);
  source.addEventListener('second', (e) => onSecond(JSON.parse(e.data)));
  source.addEventListener('summary', (e) => {
    showSummary(JSON.parse(e.data));
    finish();
  });
  source.onerror = () => finish();
});

function finish() {
  if (source) source.close();
  source = null;
  startBtn.disabled = false;
  startBtn.textContent = 'Open the sale';
}

function emptyTotals() {
  return { purchases: 0, gave_up: 0, rejected_429: 0, rejected_503: 0, timeouts: 0, wasted: 0, catalog_ok: 0, catalog_failed: 0 };
}

function onSecond(frame) {
  state.frames.push(frame);
  const t = frame.t;
  clock.textContent = `20:${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`;
  for (const s of state.scenarios) {
    const sec = frame.seconds[s];
    const tot = state.totals[s];
    for (const k of Object.keys(tot)) tot[k] += sec[k];
    if (sec.tracked_state === 'bought' && state.boughtAt[s] === undefined) state.boughtAt[s] = t;
    updatePanel(s, sec, tot);
  }
  draw();
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
      <dl>
        <dt>Bought</dt><dd data-k="purchases">0</dd>
        <dt>Gave up</dt><dd data-k="gave_up">0</dd>
        <dt>429 Too Many Requests</dt><dd data-k="rejected_429">0</dd>
        <dt>503 shed</dt><dd data-k="rejected_503">0</dd>
        <dt>Timeouts</dt><dd data-k="timeouts">0</dd>
        <dt>Wasted work</dt><dd data-k="wasted">0</dd>
        <dt>Backend queue</dt><dd data-k="backend_queue">0</dd>
        ${s === 'waitingroom' ? '<dt>In the waiting room</dt><dd data-k="room_size">0</dd>' : ''}
      </dl>
      <div class="buyer" data-k="buyer"><div class="who">Buyer #${fmt.format(state.tracked)}</div><div>Not here yet</div></div>
    </article>`).join('');
}

function updatePanel(s, sec, tot) {
  const el = document.getElementById(`panel-${s}`);
  const set = (k, v) => {
    const d = el.querySelector(`[data-k="${k}"]`);
    if (d) d.textContent = fmt.format(v);
  };
  for (const k of Object.keys(tot)) set(k, tot[k]);
  set('backend_queue', sec.backend_queue);
  set('room_size', sec.room_size);
  el.querySelector('[data-k="buyer"]').innerHTML =
    `<div class="who">Buyer #${fmt.format(state.tracked)}</div>${buyerText(s, sec)}`;
}

function buyerText(s, sec) {
  const plural = (n, word) => `${fmt.format(n)} ${word}${n === 1 ? '' : 's'}`;
  const req = plural(sec.tracked_requests, 'request');
  const err = plural(sec.tracked_errors, 'error');
  switch (sec.tracked_state) {
    case 'not_arrived':
      return '<div>Not here yet</div>';
    case 'in_room':
      if (sec.tracked_position >= 0) {
        return `<div class="big">${fmt.format(sec.tracked_position)} people ahead of you</div>`;
      }
      return '<div>In the waiting room (random order)</div>';
    case 'trying':
      return `<div>Trying to buy: ${req}, ${err}</div>`;
    case 'bought':
      return `<div class="big">Bought at second ${state.boughtAt[s]}</div><div>${req}, ${err}</div>`;
    default:
      return `<div class="big">Gave up</div><div>after ${req} and ${err}</div>`;
  }
}

// ---------- summary ----------

function showSummary(rows) {
  const tbody = document.querySelector('#summary tbody');
  tbody.innerHTML = rows.map((r) => `
    <tr>
      <td><span class="key" data-s="${r.scenario}"></span> ${NAMES[r.scenario]}</td>
      <td>${fmt.format(r.purchased)}</td>
      <td>${fmt.format(r.gave_up)}</td>
      <td>${r.wait_p50.toFixed(1)} s</td>
      <td>${r.wait_p95.toFixed(1)} s</td>
      <td>${r.requests_per_buyer.toFixed(1)}</td>
      <td>${r.errors_per_buyer.toFixed(1)}</td>
      <td>${Math.round(r.catalog_success_rate * 100)}%</td>
      <td>${fmt.format(r.wasted)}</td>
      <td>${r.fairness.toFixed(2)}</td>
    </tr>`).join('');
  summaryCard.hidden = false;
}

// ---------- chart ----------

const PAD = { left: 48, right: 110, top: 12, bottom: 32 };

function geometry() {
  const w = canvas.clientWidth;
  const h = canvas.clientHeight || 320;
  const tMax = Math.max(60, Math.ceil(((state?.frames.length || 0) + 5) / 10) * 10);
  const cap = state ? state.capacity : 500;
  let peak = cap;
  if (state) {
    for (const f of state.frames) for (const s of state.scenarios) peak = Math.max(peak, f.seconds[s].purchases);
  }
  const step = niceStep(peak * 1.1 / 5);
  const yMax = Math.ceil(peak * 1.1 / step) * step;
  const pw = w - PAD.left - PAD.right;
  const ph = h - PAD.top - PAD.bottom;
  return {
    w, h, pw, ph, tMax, yMax, step, cap,
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
  canvas.width = g.w * dpr;
  canvas.height = g.h * dpr;
  const ctx = canvas.getContext('2d');
  ctx.scale(dpr, dpr);
  ctx.clearRect(0, 0, g.w, g.h);
  ctx.font = '12px system-ui, -apple-system, "Segoe UI", sans-serif';

  // grid + y ticks
  ctx.lineWidth = 1;
  ctx.fillStyle = css('--ink-soft');
  ctx.textAlign = 'right';
  for (let v = 0; v <= g.yMax; v += g.step) {
    ctx.strokeStyle = v === 0 ? css('--axis') : css('--grid');
    line(ctx, PAD.left, g.y(v), PAD.left + g.pw, g.y(v));
    ctx.fillText(fmt.format(v), PAD.left - 8, g.y(v) + 4);
  }
  ctx.textAlign = 'center';
  for (let t = 0; t <= g.tMax; t += 10) ctx.fillText(`${t}s`, g.x(t), g.h - 10);

  // capacity reference
  ctx.strokeStyle = css('--ink-soft');
  line(ctx, PAD.left, g.y(g.cap), PAD.left + g.pw, g.y(g.cap));
  ctx.textAlign = 'left';
  ctx.fillText(`capacity ${fmt.format(g.cap)}/s`, PAD.left + g.pw + 8, g.y(g.cap) + 4);

  if (!state) return;

  // series
  ctx.lineWidth = 2;
  ctx.lineJoin = 'round';
  ctx.lineCap = 'round';
  for (const s of state.scenarios) {
    ctx.strokeStyle = color(s);
    ctx.beginPath();
    state.frames.forEach((f, i) => {
      const px = g.x(f.t + 0.5);
      const py = g.y(f.seconds[s].purchases);
      if (i === 0) ctx.moveTo(px, py); else ctx.lineTo(px, py);
    });
    ctx.stroke();
  }

  // crosshair
  if (state.hoverT !== null && state.frames[state.hoverT]) {
    const f = state.frames[state.hoverT];
    ctx.strokeStyle = css('--axis');
    ctx.lineWidth = 1;
    line(ctx, g.x(f.t + 0.5), PAD.top, g.x(f.t + 0.5), PAD.top + g.ph);
    for (const s of state.scenarios) {
      dot(ctx, g.x(f.t + 0.5), g.y(f.seconds[s].purchases), color(s));
    }
  }
}

function line(ctx, x1, y1, x2, y2) {
  ctx.beginPath();
  ctx.moveTo(x1, y1);
  ctx.lineTo(x2, y2);
  ctx.stroke();
}

function dot(ctx, x, y, fill) {
  ctx.beginPath();
  ctx.arc(x, y, 6, 0, Math.PI * 2);
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
  const t = Math.round(((mx - PAD.left) / g.pw) * g.tMax - 0.5);
  if (t < 0 || t >= state.frames.length) {
    hideTooltip();
    return;
  }
  state.hoverT = t;
  const f = state.frames[t];
  tooltip.innerHTML = `<div><strong>Second ${t}</strong> · purchases/s</div>` + state.scenarios
    .map((s) => `<div class="row"><span class="key" data-s="${s}"></span>${NAMES[s]}<b>${fmt.format(f.seconds[s].purchases)}</b></div>`)
    .join('');
  tooltip.hidden = false;
  const tx = g.x(t + 0.5);
  const left = tx + 14 + tooltip.offsetWidth > g.w ? tx - 14 - tooltip.offsetWidth : tx + 14;
  tooltip.style.left = `${left}px`;
  tooltip.style.top = `${PAD.top + 8}px`;
  draw();
});
canvas.addEventListener('mouseleave', hideTooltip);

function hideTooltip() {
  tooltip.hidden = true;
  if (state) state.hoverT = null;
  draw();
}

window.addEventListener('resize', draw);
window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', draw);
draw();

// Opening the page with #run starts the sale right away (handy for sharing a link).
if (location.hash === '#run') form.requestSubmit();
