// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — renderer / dashboard logic

'use strict';

// ─── API helper ───────────────────────────────────────────────────────────────
//
// Primary path: IPC bridge → electron.net (main process).
//   electron.net is Chromium's network stack running in the PRIVILEGED main
//   process.  It bypasses CORS, CSP, and Node.js localhost cross-user issues.
//
// Fallback path: direct fetch() in the renderer.
//   Works when webSecurity:false is set in BrowserWindow and the server sends
//   Access-Control-Allow-Origin: * (now fixed in the Go server).

const API = 'http://127.0.0.1:7777';

// debugLog pushes a line into the Logs panel without replacing existing logs.
function debugLog(msg) {
  const logBox = document.getElementById('log-box');
  if (!logBox) return;
  const div = document.createElement('div');
  div.className = 'log-line warn';
  div.textContent = `[debug] ${msg}`;
  logBox.appendChild(div);
  if (logBox.children.length > 500) logBox.removeChild(logBox.firstChild);
  logBox.scrollTop = logBox.scrollHeight;
}

async function api(method, endpoint, body) {
  // ── Primary: IPC bridge (electron.net, no CORS restrictions) ──────────────
  try {
    if (window.ht && typeof window.ht.api === 'function') {
      const res = await window.ht.api(method, endpoint, body);
      // IPC resolved — return whatever the Go server sent back
      return res;
    }
  } catch (ipcErr) {
    debugLog(`IPC error on ${method} ${endpoint}: ${ipcErr.message}`);
  }

  // ── Fallback: direct fetch() ───────────────────────────────────────────────
  try {
    const opts = { method, signal: AbortSignal.timeout(6000) };
    if (body) {
      opts.headers = { 'Content-Type': 'application/json' };
      opts.body    = JSON.stringify(body);
    }
    // No Content-Type on GET/DELETE — avoids CORS preflight for those methods
    const res  = await fetch(API + endpoint, opts);
    let data;
    try { data = await res.json(); } catch { data = {}; }
    return { ok: res.ok, status: res.status, body: data };
  } catch (fetchErr) {
    const msg = fetchErr.message || 'Could not reach server';
    debugLog(`Fetch error on ${method} ${endpoint}: ${msg}`);
    return { ok: false, body: { error: msg } };
  }
}

// ─── State ────────────────────────────────────────────────────────────────────

const state = {
  serverRunning:  false,
  clients:        [],
  invites:        [],
  pollTimer:      null,
  totalBytesIn:   0,
  totalBytesOut:  0,
  pollCount:      0,   // total polls attempted
  failedPolls:    0,   // consecutive failed polls
};

// ─── DOM refs ─────────────────────────────────────────────────────────────────

const $ = (id) => document.getElementById(id);

const els = {
  statusBadge:    $('status-badge'),
  btnStart:       $('btn-start'),
  btnStop:        $('btn-stop'),
  valIp:          $('val-ip'),
  valPort:        $('val-port'),
  valUptime:      $('val-uptime'),
  statClients:    $('stat-clients'),
  statBytesIn:    $('stat-bytes-in'),
  statBytesOut:   $('stat-bytes-out'),
  clientsBody:    $('clients-body'),
  clientCount:    $('client-count'),
  invitesList:    $('invites-list'),
  logBox:         $('log-box'),
  modalOverlay:   $('modal-overlay'),
  modalStepForm:  $('modal-step-form'),
  modalStepResult:$('modal-step-result'),
  inviteName:     $('invite-name'),
  inviteTtl:      $('invite-ttl'),
  modalLinkText:  $('modal-link-text'),
  qrCanvas:       $('qr-canvas'),
  toast:          $('toast'),
};

// ─── Navigation ───────────────────────────────────────────────────────────────

document.querySelectorAll('.nav-item').forEach((item) => {
  item.addEventListener('click', () => {
    document.querySelectorAll('.nav-item').forEach((n) => n.classList.remove('active'));
    document.querySelectorAll('.panel').forEach((p) => p.classList.remove('active'));
    item.classList.add('active');
    $(`panel-${item.dataset.panel}`).classList.add('active');
    if (item.dataset.panel === 'logs') fetchLogs();
  });
});

// ─── Toast ────────────────────────────────────────────────────────────────────

let toastTimer = null;
function showToast(msg, type = 'success') {
  els.toast.textContent = msg;
  els.toast.className   = `toast ${type} show`;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => els.toast.classList.remove('show'), 3000);
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

function fmtBytes(b) {
  if (b === 0) return '0 B';
  const units = ['B','KB','MB','GB'];
  const i = Math.min(Math.floor(Math.log2(b) / 10), units.length - 1);
  return (b / Math.pow(1024, i)).toFixed(i ? 1 : 0) + ' ' + units[i];
}

function fmtTime(iso) {
  const d = new Date(iso);
  const now = new Date();
  const sec = Math.floor((now - d) / 1000);
  if (sec < 60)   return `${sec}s ago`;
  if (sec < 3600) return `${Math.floor(sec/60)}m ago`;
  return `${Math.floor(sec/3600)}h ago`;
}

function copyToClipboard(text) {
  navigator.clipboard.writeText(text)
    .then(() => showToast('Copied to clipboard'))
    .catch(() => showToast('Copy failed', 'error'));
}

// ─── Status polling ───────────────────────────────────────────────────────────

async function fetchStatus() {
  state.pollCount++;
  const res = await api('GET', '/api/status');

  if (!res.ok) {
    state.failedPolls++;
    const errMsg = res.body?.error || 'No response from server';
    // Show "Connecting..." for the first 6 failures (~15 seconds grace period)
    // so users don't panic while the server is still initialising.
    if (state.failedPolls <= 6) {
      setServerConnecting(errMsg);
    } else {
      setServerStopped(errMsg);
    }
    return;
  }

  // Successful API response
  state.failedPolls = 0;
  setDebugMsg('');                        // clear any previous error
  const d = res.body;
  state.serverRunning = d.running;

  els.valIp.textContent     = d.public_ip  || '—';
  els.valPort.textContent   = d.port       || '—';
  els.valUptime.textContent = d.uptime     || '—';

  if (d.running) {
    setServerRunning();
  } else {
    setServerStopped();
  }
}

async function fetchClients() {
  const res = await api('GET', '/api/clients');
  if (!res.ok) return;

  state.clients = res.body || [];

  let bytesIn = 0, bytesOut = 0;
  state.clients.forEach((c) => { bytesIn += c.bytes_in; bytesOut += c.bytes_out; });
  state.totalBytesIn  = bytesIn;
  state.totalBytesOut = bytesOut;

  els.statClients.textContent  = state.clients.length;
  els.statBytesIn.textContent  = fmtBytes(bytesIn);
  els.statBytesOut.textContent = fmtBytes(bytesOut);
  els.clientCount.textContent  = `${state.clients.length} online`;

  renderClientsTable();
}

function renderClientsTable() {
  if (state.clients.length === 0) {
    els.clientsBody.innerHTML = `<tr><td colspan="6">
      <div class="empty-state">
        <div class="empty-icon">◎</div>No clients connected
      </div></td></tr>`;
    return;
  }

  els.clientsBody.innerHTML = state.clients.map((c) => `
    <tr>
      <td><span class="dot-online"></span>${esc(c.display_name || 'Unknown')}</td>
      <td class="mono">${esc(c.virtual_ip)}</td>
      <td class="mono">${esc(c.peer_addr)}</td>
      <td class="mono">${fmtBytes(c.bytes_in)}</td>
      <td class="mono">${fmtBytes(c.bytes_out)}</td>
      <td>${fmtTime(c.connected_at)}</td>
    </tr>`).join('');
}

async function fetchLogs() {
  const res = await api('GET', '/api/logs');
  if (!res.ok) return;
  const lines = res.body?.lines || [];
  renderLogs(lines);
}

function renderLogs(lines) {
  els.logBox.innerHTML = lines.map((l) => {
    let cls = 'log-line';
    if (/\[ERR|error|fatal/i.test(l))  cls += ' err';
    else if (/\[WARN|warn/i.test(l))   cls += ' warn';
    else if (/connected|started|✓/i.test(l)) cls += ' ok';
    else if (/\[INFO/i.test(l))        cls += ' info';
    return `<div class="${cls}">${esc(l)}</div>`;
  }).join('');
  els.logBox.scrollTop = els.logBox.scrollHeight;
}

function esc(s) {
  return String(s)
    .replace(/&/g,'&amp;')
    .replace(/</g,'&lt;')
    .replace(/>/g,'&gt;')
    .replace(/"/g,'&quot;');
}

// ─── Server state display ─────────────────────────────────────────────────────

function setDebugMsg(msg) {
  const el = document.getElementById('debug-msg');
  if (el) el.textContent = msg;
}

function setServerRunning() {
  els.statusBadge.className   = 'badge badge-running';
  els.statusBadge.textContent = 'Running';
  els.btnStart.disabled       = true;
  els.btnStop.disabled        = false;
  setDebugMsg('');
}

function setServerStopped(errMsg) {
  els.statusBadge.className   = 'badge badge-stopped';
  els.statusBadge.textContent = 'Stopped';
  els.btnStart.disabled       = false;
  els.btnStop.disabled        = true;
  els.valIp.textContent       = '—';
  els.valPort.textContent     = '—';
  els.valUptime.textContent   = '—';
  setDebugMsg(errMsg || '');
}

function setServerConnecting(errMsg) {
  els.statusBadge.className   = 'badge badge-starting';
  els.statusBadge.textContent = 'Connecting…';
  els.btnStart.disabled       = true;
  els.btnStop.disabled        = true;
  setDebugMsg(errMsg || '');
}

function setServerStarting() {
  els.statusBadge.className   = 'badge badge-starting';
  els.statusBadge.textContent = 'Starting…';
  els.btnStart.disabled       = true;
  els.btnStop.disabled        = true;
  setDebugMsg('');
}

els.btnStart.addEventListener('click', async () => {
  // First check if the server is already running via the API
  setServerStarting();
  const statusRes = await api('GET', '/api/status');

  if (statusRes.ok && statusRes.body?.running) {
    // Server is already running — just refresh the display
    showToast('Server is running');
    await fetchStatus();
    return;
  }

  // Server not running — open Terminal to start it with sudo
  showToast('Opening Terminal to start the server…', 'info');
  await window.ht.spawnServer().catch(() => {});

  // Poll until the server comes up (up to ~30 seconds)
  let attempts = 0;
  const poll = setInterval(async () => {
    attempts++;
    const r = await api('GET', '/api/status');
    if (r.ok && r.body?.running) {
      clearInterval(poll);
      showToast('Server started!');
      await fetchStatus();
    } else if (attempts >= 20) {
      clearInterval(poll);
      showToast('Server did not start. Check the Terminal window.', 'error');
      setServerStopped();
    }
  }, 1500);
});

els.btnStop.addEventListener('click', async () => {
  const res = await api('POST', '/api/server/stop');
  if (res.ok) {
    showToast('Server stopped');
    setServerStopped();
  } else {
    showToast(res.body?.error || 'Failed to stop', 'error');
  }
});

// ─── Quick actions ────────────────────────────────────────────────────────────

$('btn-copy-ip').addEventListener('click', () => {
  const ip   = els.valIp.textContent;
  const port = els.valPort.textContent;
  if (ip === '—') { showToast('Server is not running', 'error'); return; }
  copyToClipboard(`${ip}:${port}`);
});

$('author-link').addEventListener('click', (e) => {
  e.preventDefault();
  window.ht.openExternal('https://github.com/d991d');
});

// ─── Invite modal ─────────────────────────────────────────────────────────────

function openModal() {
  els.modalStepForm.style.display   = '';
  els.modalStepResult.style.display = 'none';
  els.inviteName.value = '';
  els.inviteTtl.value  = '72';
  els.modalOverlay.classList.add('open');
  setTimeout(() => els.inviteName.focus(), 200);
}

function closeModal() {
  els.modalOverlay.classList.remove('open');
}

$('btn-new-invite').addEventListener('click',   openModal);
$('btn-new-invite-2').addEventListener('click', openModal);
$('btn-modal-cancel').addEventListener('click', closeModal);
$('btn-modal-done').addEventListener('click',   closeModal);

els.modalOverlay.addEventListener('click', (e) => {
  if (e.target === els.modalOverlay) closeModal();
});

$('btn-modal-generate').addEventListener('click', async () => {
  const name = els.inviteName.value.trim() || 'Friend';
  const ttl  = parseInt(els.inviteTtl.value) || 72;

  $('btn-modal-generate').disabled = true;
  $('btn-modal-generate').textContent = 'Generating…';

  const res = await api('POST', '/api/invite', {
    display_name: name,
    ttl_hours:    ttl,
  });

  $('btn-modal-generate').disabled = false;
  $('btn-modal-generate').textContent = 'Generate';

  if (!res.ok) {
    showToast(res.body?.error || 'Failed to generate invite', 'error');
    return;
  }

  const invite = res.body;
  state.invites.unshift(invite);
  renderInvites();

  // Show result step
  els.modalStepForm.style.display   = 'none';
  els.modalStepResult.style.display = '';
  els.modalLinkText.textContent     = invite.invite_link;

  // Draw QR code using a pure-JS implementation (no external CDN)
  drawQR(invite.invite_link, els.qrCanvas);
});

$('btn-copy-link').addEventListener('click', () => {
  copyToClipboard(els.modalLinkText.textContent);
});

// ─── Invite list ──────────────────────────────────────────────────────────────

function renderInvites() {
  if (state.invites.length === 0) {
    els.invitesList.innerHTML = `<div class="empty-state">
      <div class="empty-icon">⬡</div>No invites generated yet</div>`;
    return;
  }

  els.invitesList.innerHTML = state.invites.map((inv) => {
    const revoked = inv.revoked;
    const exp     = new Date(inv.expires_at);
    const expired = exp < new Date();
    const meta    = revoked ? 'Revoked'
                  : expired ? `Expired ${fmtTime(inv.expires_at)}`
                  : `Expires ${fmtTime(inv.expires_at)}`;
    return `
    <div class="invite-row ${revoked ? 'invite-revoked' : ''}">
      <div class="invite-info">
        <div class="invite-name">${esc(inv.display_name)}</div>
        <div class="invite-meta">${meta}</div>
      </div>
      <div style="display:flex;gap:6px">
        <button class="btn btn-ghost" style="padding:4px 10px;font-size:12px"
          onclick="copyToClipboard('${esc(inv.invite_link)}')">Copy</button>
        ${!revoked ? `<button class="btn btn-red" style="padding:4px 10px;font-size:12px"
          onclick="revokeInvite('${esc(inv.id)}')">Revoke</button>` : ''}
      </div>
    </div>`;
  }).join('');
}

async function revokeInvite(id) {
  const res = await api('DELETE', `/api/invite?id=${id}`);
  if (res.ok) {
    const inv = state.invites.find((i) => i.id === id);
    if (inv) inv.revoked = true;
    renderInvites();
    showToast('Invite revoked');
  } else {
    showToast('Failed to revoke', 'error');
  }
}

// Expose for inline onclick
window.copyToClipboard = copyToClipboard;
window.revokeInvite    = revokeInvite;

// ─── Log panel ────────────────────────────────────────────────────────────────

$('btn-clear-logs').addEventListener('click', () => {
  els.logBox.innerHTML = '';
});

$('btn-refresh-logs').addEventListener('click', fetchLogs);

// Receive live log lines from the Go process via main process IPC
// Guard: if preload failed to load (e.g. missing npm deps), window.ht is undefined
if (window.ht) {
  window.ht.onServerLog((line) => {
    const cls = /error|fatal/i.test(line) ? 'err'
              : /warn/i.test(line)        ? 'warn'
              : /connected|✓/i.test(line) ? 'ok'
              : 'info';
    const div = document.createElement('div');
    div.className = `log-line ${cls}`;
    div.textContent = line;
    els.logBox.appendChild(div);
    if (els.logBox.children.length > 500) els.logBox.removeChild(els.logBox.firstChild);
    els.logBox.scrollTop = els.logBox.scrollHeight;
  });

  window.ht.onServerStopped((code) => {
    setServerStopped();
    showToast(`Server process exited (code ${code})`, 'error');
  });
} else {
  console.error('[app] window.ht is undefined — preload.js failed to load. Run: npm install');
  setDebugMsg('Preload failed. Run: npm install  in ui/electron folder, then restart.');
}

// ─── QR Code ──────────────────────────────────────────────────────────────────

/**
 * Minimal QR code renderer — uses the `qrcode` Node module loaded via
 * Electron's preload. Since we can't use require() in the renderer directly,
 * we call it through the context bridge. For now we use a canvas-based
 * approach: the preload exposes a `qr` helper, OR we use the bundled lib.
 *
 * Fallback: show the raw URL text in the canvas if QR generation fails.
 */
function drawQR(text, canvas) {
  // Use qrcode via the context bridge (preload.js exposes window.ht.drawQR)
  if (window.ht && typeof window.ht.drawQR === 'function') {
    window.ht.drawQR(canvas, text).catch(() => drawQRFallback(text, canvas));
  } else {
    // Preload not available (qrcode npm package not installed) — show text fallback
    drawQRFallback(text, canvas);
  }
}

function drawQRFallback(text, canvas) {
  // Simple placeholder: white background with URL text
  const ctx  = canvas.getContext('2d');
  canvas.width  = 180;
  canvas.height = 180;
  ctx.fillStyle = '#ffffff';
  ctx.fillRect(0, 0, 180, 180);
  ctx.fillStyle = '#000000';
  ctx.font      = '10px monospace';
  ctx.textAlign = 'center';

  // Word-wrap the URL across multiple lines
  const maxW = 160;
  const words = text.match(/.{1,20}/g) || [text];
  let y = 20;
  words.forEach((w) => {
    ctx.fillText(w, 90, y);
    y += 14;
  });

  ctx.font      = '9px sans-serif';
  ctx.fillStyle = '#888';
  ctx.fillText('Install qrcode pkg for QR image', 90, 168);
}

// ─── Polling ──────────────────────────────────────────────────────────────────

async function pollAll() {
  await fetchStatus();
  await fetchClients();
}

function startPolling() {
  pollAll();
  state.pollTimer = setInterval(pollAll, 2500);
}

// ─── Boot ─────────────────────────────────────────────────────────────────────

document.addEventListener('DOMContentLoaded', () => {
  startPolling();
  renderInvites();
});
