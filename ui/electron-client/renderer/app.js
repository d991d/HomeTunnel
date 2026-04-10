// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel Client — renderer logic

'use strict';

// ─── State ────────────────────────────────────────────────────────────────────

const state = {
  screen:       'connect',   // 'connect' | 'reconnecting' | 'connected'
  serverAddr:   '',
  displayName:  '',
  virtualIP:    '',
  mtu:          0,
  bytesIn:      0,
  bytesOut:     0,
  connectedAt:  null,        // Date object
  durationTimer: null,
  logLines:     0,
  logOpen:      false,
};

// ─── DOM helpers ──────────────────────────────────────────────────────────────

const $  = (id) => document.getElementById(id);
const el = {
  // screens
  screenConnect:     $('screen-connect'),
  screenReconnect:   $('screen-reconnecting'),
  screenConnected:   $('screen-connected'),
  // connect screen
  inviteInput:       $('invite-input'),
  nameInput:         $('name-input'),
  btnConnect:        $('btn-connect'),
  errorBanner:       $('error-banner'),
  errorText:         $('error-text'),
  dismissError:      $('dismiss-error'),
  toggleAdvanced:    $('toggle-advanced'),
  advancedFields:    $('advanced-fields'),
  serverAddr:        $('server-addr'),
  tokenInput:        $('token-input'),
  // reconnect screen
  reconnectDetail:   $('reconnect-detail'),
  btnCancelReconnect:$('btn-cancel-reconnect'),
  // connected screen
  ringFill:          $('ring-fill'),
  ringStatusText:    $('ring-status-text'),
  connectedName:     $('connected-name'),
  connectedAddr:     $('connected-addr'),
  valVip:            $('val-vip'),
  valLatency:        $('val-latency'),
  valDuration:       $('val-duration'),
  valBytesIn:        $('val-bytes-in'),
  valBytesOut:       $('val-bytes-out'),
  btnCopyVip:        $('btn-copy-vip'),
  btnDisconnect:     $('btn-disconnect'),
  // log drawer
  logToggle:         $('log-toggle'),
  logBody:           $('log-body'),
  logCount:          $('log-count'),
  // toast
  toast:             $('toast'),
};

// ─── Screen navigation ────────────────────────────────────────────────────────

function showScreen(name) {
  state.screen = name;
  el.screenConnect.classList.toggle('active',   name === 'connect');
  el.screenReconnect.classList.toggle('active', name === 'reconnecting');
  el.screenConnected.classList.toggle('active', name === 'connected');
  window.ht.setConnectionState(name === 'connected');
}

// ─── Toast ────────────────────────────────────────────────────────────────────

let toastTimer = null;
function toast(msg, type = 'success') {
  el.toast.textContent = msg;
  el.toast.className   = `toast ${type} show`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.toast.classList.remove('show'), 3000);
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function fmtBytes(b) {
  if (!b || b === 0) return '0 B';
  const u = ['B','KB','MB','GB'];
  const i = Math.min(Math.floor(Math.log2(b) / 10), u.length - 1);
  return (b / Math.pow(1024, i)).toFixed(i ? 1 : 0) + ' ' + u[i];
}

function fmtDuration(startDate) {
  const s = Math.floor((Date.now() - startDate) / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h > 0) return `${h}:${String(m).padStart(2,'0')}:${String(sec).padStart(2,'0')}`;
  return `${m}:${String(sec).padStart(2,'0')}`;
}

function copyToClipboard(text) {
  navigator.clipboard.writeText(text)
    .then(() => toast('Copied!'))
    .catch(() => toast('Copy failed', 'error'));
}

// Parse invite link: hometunnel://HOST:PORT?token=TOKEN
function parseInviteLink(link) {
  link = link.trim();
  try {
    // Support both hometunnel:// and vpn://
    const normalized = link.replace(/^(hometunnel|vpn):\/\//, 'https://');
    const url = new URL(normalized);
    const addr  = url.host;                   // "203.0.113.25:48321"
    const token = url.searchParams.get('token');
    if (!addr || !token) return null;
    return { addr, token };
  } catch {
    return null;
  }
}

// ─── Error banner ─────────────────────────────────────────────────────────────

function showError(msg) {
  el.errorText.textContent = msg;
  el.errorBanner.classList.add('show');
  el.inviteInput.classList.add('error');
}

function hideError() {
  el.errorBanner.classList.remove('show');
  el.inviteInput.classList.remove('error');
}

el.dismissError.addEventListener('click', hideError);

// ─── Advanced toggle ──────────────────────────────────────────────────────────

let advancedOpen = false;
el.toggleAdvanced.addEventListener('click', () => {
  advancedOpen = !advancedOpen;
  el.advancedFields.classList.toggle('hidden', !advancedOpen);
  el.toggleAdvanced.textContent = advancedOpen ? 'Advanced ▴' : 'Advanced ▾';
});

// ─── Connect ──────────────────────────────────────────────────────────────────

el.btnConnect.addEventListener('click', doConnect);
el.inviteInput.addEventListener('keydown', (e) => {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); doConnect(); }
});

function doConnect() {
  hideError();
  const displayName = el.nameInput.value.trim() || 'Friend';
  let serverAddr, token;

  // Try invite link first
  const linkRaw = el.inviteInput.value.trim();
  if (linkRaw) {
    const parsed = parseInviteLink(linkRaw);
    if (!parsed) {
      showError('Invalid invite link. It should start with hometunnel://…');
      return;
    }
    serverAddr = parsed.addr;
    token      = parsed.token;
  } else {
    // Fall back to advanced fields
    serverAddr = el.serverAddr.value.trim();
    token      = el.tokenInput.value.trim();
    if (!serverAddr || !token) {
      showError('Enter an invite link or fill in the advanced fields.');
      return;
    }
  }

  state.serverAddr  = serverAddr;
  state.displayName = displayName;

  el.btnConnect.disabled = true;
  el.btnConnect.textContent = 'Connecting…';

  window.ht.send({
    cmd:          'connect',
    server_addr:  serverAddr,
    token:        token,
    display_name: displayName,
  });
}

// ─── Disconnect ───────────────────────────────────────────────────────────────

el.btnDisconnect.addEventListener('click', doDisconnect);
el.btnCancelReconnect.addEventListener('click', doDisconnect);

function doDisconnect() {
  window.ht.send({ cmd: 'disconnect' });
  stopDurationTimer();
  showScreen('connect');
  el.btnConnect.disabled = false;
  el.btnConnect.textContent = 'Connect';
}

// ─── Duration timer ───────────────────────────────────────────────────────────

function startDurationTimer() {
  state.connectedAt = Date.now();
  stopDurationTimer();
  state.durationTimer = setInterval(() => {
    el.valDuration.textContent = fmtDuration(state.connectedAt);
  }, 1000);
}

function stopDurationTimer() {
  clearInterval(state.durationTimer);
  state.durationTimer = null;
}

// ─── Connected UI update ──────────────────────────────────────────────────────

function showConnected(virtualIP, mtu) {
  state.virtualIP = virtualIP;
  state.mtu       = mtu;
  state.bytesIn   = 0;
  state.bytesOut  = 0;

  el.connectedName.textContent = state.displayName || 'Connected';
  el.connectedAddr.textContent = state.serverAddr;
  el.valVip.textContent        = virtualIP || '—';
  el.valLatency.textContent    = '— ms';
  el.valBytesIn.textContent    = '0 B';
  el.valBytesOut.textContent   = '0 B';
  el.valDuration.textContent   = '0:00';

  startDurationTimer();
  showScreen('connected');
}

function updateStats(evt) {
  if (evt.latency_ms > 0) el.valLatency.textContent = `${evt.latency_ms} ms`;
  if (evt.bytes_in  !== undefined) el.valBytesIn.textContent  = fmtBytes(evt.bytes_in);
  if (evt.bytes_out !== undefined) el.valBytesOut.textContent = fmtBytes(evt.bytes_out);

  // Animate the ring based on latency quality
  const lat = evt.latency_ms;
  const ring = el.ringFill;
  if (lat <= 0 || lat > 500) {
    ring.style.stroke = 'var(--amber)';
  } else if (lat < 80) {
    ring.style.stroke = 'var(--green)';
  } else if (lat < 200) {
    ring.style.stroke = 'var(--blue)';
  } else {
    ring.style.stroke = 'var(--amber)';
  }
}

// ─── Go client events ─────────────────────────────────────────────────────────

window.ht.onEvent((evt) => {
  switch (evt.event) {

    case 'status':
      switch (evt.state) {
        case 'connecting':
          el.btnConnect.disabled    = true;
          el.btnConnect.textContent = 'Connecting…';
          addLog('info', `Connecting to ${evt.server || state.serverAddr}…`);
          break;

        case 'connected':
          el.btnConnect.disabled    = false;
          el.btnConnect.textContent = 'Connect';
          addLog('ok', `Connected! Virtual IP: ${evt.virtual_ip}`);
          showConnected(evt.virtual_ip, evt.mtu);
          toast('Connected to HomeTunnel');
          break;

        case 'reconnecting':
          stopDurationTimer();
          showScreen('reconnecting');
          el.reconnectDetail.textContent = evt.message || 'Retrying…';
          addLog('warn', evt.message || 'Reconnecting…');
          break;

        case 'disconnected':
          stopDurationTimer();
          el.btnConnect.disabled    = false;
          el.btnConnect.textContent = 'Connect';
          if (state.screen !== 'connect') {
            showScreen('connect');
            if (evt.reason && evt.reason !== 'user request') {
              showError(`Disconnected: ${evt.reason}`);
              toast(`Disconnected: ${evt.reason}`, 'error');
            }
          }
          addLog('warn', `Disconnected: ${evt.reason || 'unknown'}`);
          break;
      }
      break;

    case 'stats':
      if (state.screen === 'connected') updateStats(evt);
      break;

    case 'error':
      addLog('err', evt.message || 'Unknown error');
      if (state.screen === 'connect') {
        showError(evt.message || 'Connection error');
        el.btnConnect.disabled    = false;
        el.btnConnect.textContent = 'Connect';
      }
      break;

    case 'log':
      addLog(evt.level === 'error' ? 'err' : evt.level || 'info', evt.message);
      break;
  }
});

// ─── Log drawer ───────────────────────────────────────────────────────────────

el.logToggle.addEventListener('click', () => {
  state.logOpen = !state.logOpen;
  el.logBody.classList.toggle('open', state.logOpen);
  el.logToggle.querySelector('span').textContent =
    state.logOpen ? '▾ Logs' : '▸ Logs';
});

function addLog(level, msg) {
  if (!msg) return;
  const cls  = { err: 'err', warn: 'warn', ok: 'ok', info: 'info' }[level] || '';
  const line = document.createElement('div');
  const ts   = new Date().toLocaleTimeString('en', { hour12: false });
  line.className   = `log-line ${cls}`;
  line.textContent = `[${ts}] ${msg}`;
  el.logBody.appendChild(line);

  // Keep max 200 lines
  while (el.logBody.children.length > 200) el.logBody.removeChild(el.logBody.firstChild);
  if (state.logOpen) el.logBody.scrollTop = el.logBody.scrollHeight;

  state.logLines++;
  el.logCount.textContent = `${state.logLines} line${state.logLines !== 1 ? 's' : ''}`;
}

// ─── Misc button wiring ───────────────────────────────────────────────────────

el.btnCopyVip.addEventListener('click', () => copyToClipboard(state.virtualIP));

$('author-link').addEventListener('click', (e) => {
  e.preventDefault();
  window.ht.openExternal('https://github.com/d991d');
});

// Auto-focus invite input on load
document.addEventListener('DOMContentLoaded', async () => {
  el.inviteInput.focus();
  // Ask Go engine for its current state (in case it was already connected)
  window.ht.send({ cmd: 'status' });

  // Show Windows admin notice if running on Windows
  try {
    const platform = await window.ht.getPlatform();
    if (platform === 'win32') {
      const notice = document.getElementById('admin-notice');
      if (notice) notice.style.display = '';
    }
  } catch (_) {}
});
