/**
 * app.js — Babble browser UI.
 *
 * Connects to the WebSocket event stream, plays sounds via BabbleAudio, and
 * renders the session sidebar, event stream, and category volume controls.
 */

import { BabbleAudio } from './audio.js';

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const MAX_EVENTS = 200;
const RECONNECT_BASE_MS = 500;
const RECONNECT_MAX_MS = 5000;
const ACTIVITY_WINDOW_MS = 10_000;   // dot goes green if event in last 10s
const RATE_WINDOW_MS = 60_000;       // events/min rolling window

const CATEGORY_ICONS = {
  ambient: '💭',
  action:  '💻',
  read:    '📖',
  write:   '✏️',
  network: '🌐',
  success: '✅',
  warn:    '⚠️',
  error:   '🔴',
  meta:    '⚡',
};

const CATEGORIES = ['ambient', 'action', 'read', 'write', 'network', 'success', 'warn', 'error'];

const SESSION_PALETTE = [
  '#60a5fa', // blue-400
  '#f472b6', // pink-400
  '#34d399', // emerald-400
  '#fb923c', // orange-400
  '#a78bfa', // violet-400
  '#facc15', // yellow-400
  '#2dd4bf', // teal-400
  '#f87171', // red-400
];

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

const audio = new BabbleAudio();

/** Currently filtered session name, or null to show all. */
let activeFilter = null;

/** Map<sessionName, { color, lastSeen, eventTimestamps: number[] }> */
const sessions = new Map();

let sessionColorIndex = 0;

/** Whether the event stream div is scrolled to the bottom. */
let autoScroll = true;

let ws = null;
let reconnectDelay = RECONNECT_BASE_MS;

// ---------------------------------------------------------------------------
// DOM refs (populated after DOMContentLoaded)
// ---------------------------------------------------------------------------

let elOverlay, elEventStream, elSessionList, elPackSelect;

// ---------------------------------------------------------------------------
// Initialisation
// ---------------------------------------------------------------------------

document.addEventListener('DOMContentLoaded', () => {
  elOverlay     = document.getElementById('audio-overlay');
  elEventStream = document.getElementById('event-stream');
  elSessionList = document.getElementById('session-list');
  elPackSelect  = document.getElementById('pack-select');

  populatePacks();
  buildVolumeControls();
  setupPackSelector();
  setupScrollTracking();

  elOverlay.addEventListener('click', handleOverlayClick, { once: true });

  // Tick to update session activity dots every second.
  setInterval(updateSessionActivity, 1000);
});

// ---------------------------------------------------------------------------
// Audio overlay
// ---------------------------------------------------------------------------

async function handleOverlayClick() {
  elOverlay.style.display = 'none';
  audio.init();
  const packName = elPackSelect.value || 'default';
  try {
    await audio.loadPack(packName);
  } catch (err) {
    console.warn('BabbleApp: failed to load pack:', err);
  }
  connectWebSocket();
}

// ---------------------------------------------------------------------------
// Pack API
// ---------------------------------------------------------------------------

async function populatePacks() {
  try {
    const res = await fetch('/api/packs');
    if (!res.ok) return;
    const packs = await res.json();

    elPackSelect.innerHTML = '';
    for (const pack of packs) {
      const opt = document.createElement('option');
      opt.value = pack.name.toLowerCase();
      opt.textContent = pack.name;
      elPackSelect.appendChild(opt);
    }

    // Default to first pack if available.
    if (packs.length === 0) {
      const opt = document.createElement('option');
      opt.value = 'default';
      opt.textContent = 'Default';
      elPackSelect.appendChild(opt);
    }
  } catch (err) {
    console.warn('BabbleApp: failed to fetch packs:', err);
  }
}

function setupPackSelector() {
  elPackSelect.addEventListener('change', async () => {
    const packName = elPackSelect.value;
    try {
      await audio.loadPack(packName);
    } catch (err) {
      console.warn('BabbleApp: failed to switch pack:', err);
    }
  });
}

// ---------------------------------------------------------------------------
// WebSocket
// ---------------------------------------------------------------------------

function connectWebSocket() {
  if (ws) {
    ws.onclose = null;
    ws.close();
  }

  ws = new WebSocket(`ws://${location.host}/ws`);

  ws.onopen = () => {
    reconnectDelay = RECONNECT_BASE_MS;
    console.log('BabbleApp: WebSocket connected');
  };

  ws.onmessage = (e) => {
    let event;
    try {
      event = JSON.parse(e.data);
    } catch {
      return;
    }
    handleEvent(event);
  };

  ws.onclose = () => {
    console.log(`BabbleApp: WebSocket closed — reconnecting in ${reconnectDelay}ms`);
    setTimeout(() => {
      reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX_MS);
      connectWebSocket();
    }, reconnectDelay);
  };

  ws.onerror = (err) => {
    console.warn('BabbleApp: WebSocket error', err);
  };
}

// ---------------------------------------------------------------------------
// Event handling
// ---------------------------------------------------------------------------

function handleEvent(event) {
  updateSession(event);
  addEventRow(event);
  audio.play(event);
}

// ---------------------------------------------------------------------------
// Session sidebar
// ---------------------------------------------------------------------------

function getOrCreateSession(name) {
  if (!sessions.has(name)) {
    sessions.set(name, {
      color: SESSION_PALETTE[sessionColorIndex++ % SESSION_PALETTE.length],
      lastSeen: 0,
      eventTimestamps: [],
    });
  }
  return sessions.get(name);
}

function updateSession(event) {
  const sess = getOrCreateSession(event.session);
  const now = Date.now();
  sess.lastSeen = now;
  sess.eventTimestamps.push(now);

  // Prune old timestamps outside the rolling window.
  const cutoff = now - RATE_WINDOW_MS;
  while (sess.eventTimestamps.length && sess.eventTimestamps[0] < cutoff) {
    sess.eventTimestamps.shift();
  }

  renderSessionList();
}

function renderSessionList() {
  const fragment = document.createDocumentFragment();

  for (const [name, sess] of sessions) {
    const existing = elSessionList.querySelector(`[data-session="${CSS.escape(name)}"]`);
    if (existing) {
      // Update dynamic parts only.
      updateSessionEl(existing, name, sess);
      fragment.appendChild(existing.cloneNode(true));
    } else {
      fragment.appendChild(buildSessionEl(name, sess));
    }
  }

  elSessionList.innerHTML = '';
  elSessionList.appendChild(fragment);

  // Re-attach mute toggle listeners after re-render.
  elSessionList.querySelectorAll('.mute-btn').forEach(btn => {
    btn.addEventListener('click', (e) => {
      e.stopPropagation();
      const sessionName = btn.closest('[data-session]').dataset.session;
      const muted = audio.toggleSessionMute(sessionName);
      btn.textContent = muted ? '🔇' : '🔊';
      btn.closest('[data-session]').classList.toggle('muted', muted);
    });
  });

  // Re-attach filter click listeners.
  elSessionList.querySelectorAll('.session-item').forEach(item => {
    item.addEventListener('click', () => {
      const name = item.dataset.session;
      activeFilter = activeFilter === name ? null : name;
      elSessionList.querySelectorAll('.session-item').forEach(el => {
        el.classList.toggle('active', el.dataset.session === activeFilter);
      });
      applyFilter();
    });
  });
}

function buildSessionEl(name, sess) {
  const now = Date.now();
  const isActive = (now - sess.lastSeen) < ACTIVITY_WINDOW_MS;
  const evPerMin = Math.round((sess.eventTimestamps.length / RATE_WINDOW_MS) * 60_000);
  const muted = audio.mutedSessions.has(name);

  const div = document.createElement('div');
  div.className = 'session-item' + (activeFilter === name ? ' active' : '') + (muted ? ' muted' : '');
  div.dataset.session = name;

  div.innerHTML = `
    <div class="session-header">
      <span class="activity-dot ${isActive ? 'active' : 'idle'}"></span>
      <span class="session-name" style="color:${sess.color}">${escapeHtml(name)}</span>
      <button class="mute-btn" title="Toggle mute">${muted ? '🔇' : '🔊'}</button>
    </div>
    <div class="session-stats">${evPerMin}/min</div>
  `;
  return div;
}

function updateSessionEl(el, name, sess) {
  const now = Date.now();
  const isActive = (now - sess.lastSeen) < ACTIVITY_WINDOW_MS;
  const evPerMin = Math.round((sess.eventTimestamps.length / RATE_WINDOW_MS) * 60_000);

  const dot = el.querySelector('.activity-dot');
  if (dot) {
    dot.className = `activity-dot ${isActive ? 'active' : 'idle'}`;
  }
  const stats = el.querySelector('.session-stats');
  if (stats) stats.textContent = `${evPerMin}/min`;
}

function updateSessionActivity() {
  // Update each session's activity dot without full re-render.
  const now = Date.now();
  elSessionList.querySelectorAll('[data-session]').forEach(el => {
    const name = el.dataset.session;
    const sess = sessions.get(name);
    if (!sess) return;
    updateSessionEl(el, name, sess);
  });
}

// ---------------------------------------------------------------------------
// Event stream
// ---------------------------------------------------------------------------

function addEventRow(event) {
  const isFiltered = activeFilter && event.session !== activeFilter;

  const row = document.createElement('div');
  row.className = `event-row cat-${event.category}`;
  if (event.isSubagent) row.classList.add('subagent');
  if (isFiltered) row.classList.add('hidden');
  row.dataset.session = event.session;

  const sess = getOrCreateSession(event.session);
  const time = formatTime(event.timestamp);
  const icon = CATEGORY_ICONS[event.category] ?? '•';
  const detail = event.detail ? escapeHtml(truncate(event.detail, 60)) : '';

  row.innerHTML = `
    <span class="ev-time">${time}</span>
    <span class="ev-session" style="color:${sess.color}">${event.isSubagent ? '↳ ' : ''}${escapeHtml(event.session)}</span>
    <span class="ev-category">${icon} ${escapeHtml(event.category)}</span>
    <span class="ev-event">${escapeHtml(event.event)}</span>
    <span class="ev-detail" title="${escapeHtml(event.detail ?? '')}">${detail}</span>
  `;

  // Prepend so newest is at top.
  elEventStream.insertBefore(row, elEventStream.firstChild);

  // Trim old entries.
  while (elEventStream.children.length > MAX_EVENTS) {
    elEventStream.removeChild(elEventStream.lastChild);
  }
}

function applyFilter() {
  elEventStream.querySelectorAll('.event-row').forEach(row => {
    const match = !activeFilter || row.dataset.session === activeFilter;
    row.classList.toggle('hidden', !match);
  });
}

// ---------------------------------------------------------------------------
// Volume controls (bottom bar)
// ---------------------------------------------------------------------------

function buildVolumeControls() {
  const container = document.getElementById('volume-controls');
  if (!container) return;

  for (const cat of CATEGORIES) {
    const icon = CATEGORY_ICONS[cat] ?? '•';
    const wrapper = document.createElement('div');
    wrapper.className = 'vol-control';

    const label = document.createElement('label');
    label.className = `vol-label cat-label-${cat}`;
    label.textContent = `${icon} ${cat}`;

    const slider = document.createElement('input');
    slider.type = 'range';
    slider.min = '0';
    slider.max = '1';
    slider.step = '0.01';
    slider.value = '0.5';
    slider.className = 'vol-slider';
    slider.setAttribute('aria-label', `${cat} volume`);

    slider.addEventListener('input', () => {
      audio.setCategoryVolume(cat, parseFloat(slider.value));
    });

    wrapper.appendChild(label);
    wrapper.appendChild(slider);
    container.appendChild(wrapper);
  }

  // Master volume slider.
  const masterWrapper = document.createElement('div');
  masterWrapper.className = 'vol-control vol-master';

  const masterLabel = document.createElement('label');
  masterLabel.className = 'vol-label';
  masterLabel.textContent = '🔊 master';

  const masterSlider = document.createElement('input');
  masterSlider.type = 'range';
  masterSlider.min = '0';
  masterSlider.max = '1';
  masterSlider.step = '0.01';
  masterSlider.value = '0.8';
  masterSlider.className = 'vol-slider';
  masterSlider.setAttribute('aria-label', 'master volume');

  masterSlider.addEventListener('input', () => {
    audio.setMasterVolume(parseFloat(masterSlider.value));
  });

  masterWrapper.appendChild(masterLabel);
  masterWrapper.appendChild(masterSlider);
  container.appendChild(masterWrapper);
}

// ---------------------------------------------------------------------------
// Scroll tracking
// ---------------------------------------------------------------------------

function setupScrollTracking() {
  elEventStream.addEventListener('scroll', () => {
    // Consider "at bottom" if within 40px of scroll top (since newest is at top,
    // scrollTop === 0 means we are at the top of the list = newest events).
    autoScroll = elEventStream.scrollTop <= 40;
  });
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

function formatTime(iso) {
  if (!iso) return '--:--:--';
  try {
    const d = new Date(iso);
    return d.toLocaleTimeString('en-US', { hour12: false });
  } catch {
    return iso.slice(11, 19) || '--:--:--';
  }
}

function escapeHtml(str) {
  if (!str) return '';
  return String(str)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function truncate(str, maxLen) {
  if (!str || str.length <= maxLen) return str ?? '';
  return str.slice(0, maxLen) + '…';
}
