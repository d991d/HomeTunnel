// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Electron main process

'use strict';

const { app, BrowserWindow, ipcMain, shell, Tray, Menu, nativeImage, dialog, net } = require('electron');
const path       = require('path');
const fs         = require('fs');
const http       = require('http');
const os         = require('os');
const { exec, spawn } = require('child_process');

// ─── Config ──────────────────────────────────────────────────────────────────

const API_BASE    = 'http://127.0.0.1:7777';
const API_TIMEOUT = 5000; // ms — per-request socket timeout
const IPC_TIMEOUT = 6000; // ms — hard cap on any IPC api call

// ─── State ───────────────────────────────────────────────────────────────────

let mainWindow = null;
let tray       = null;
let isQuitting = false;

// ─── Binary resolution ───────────────────────────────────────────────────────

function getServerBinPath() {
  const isWin  = process.platform === 'win32';
  const isMac  = process.platform === 'darwin';
  const ext    = isWin ? '.exe' : '';
  const suffix = isMac ? '-darwin-universal' : (isWin ? '' : '-linux-amd64');

  // Packaged build
  const packed = path.join(process.resourcesPath || '', 'bin', `hometunnel-server${ext}`);
  if (fs.existsSync(packed)) return packed;

  // Development — two levels up from ui/electron/
  const dev = path.join(__dirname, '..', '..', 'dist', `hometunnel-server${suffix}${ext}`);
  if (fs.existsSync(dev)) return dev;

  // Generic fallback
  const generic = path.join(__dirname, '..', '..', 'dist', `hometunnel-server${ext}`);
  if (fs.existsSync(generic)) return generic;

  return null;
}

function getServerCwd() {
  return path.join(__dirname, '..', '..');
}

// ─── Server availability ─────────────────────────────────────────────────────

function isServerRunning() {
  return new Promise((resolve) => {
    let req;
    try { req = net.request({ method: 'GET', url: API_BASE + '/api/status' }); }
    catch { resolve(false); return; }

    const timer = setTimeout(() => { try { req.abort(); } catch {} resolve(false); }, 2000);

    req.on('response', (res) => {
      clearTimeout(timer);
      res.resume();
      resolve(res.statusCode < 500);
    });
    req.on('error', () => { clearTimeout(timer); resolve(false); });
    req.end();
  });
}

function pollUntilUp(maxAttempts = 30, intervalMs = 1000) {
  let attempts = 0;
  return new Promise((resolve) => {
    const timer = setInterval(async () => {
      attempts++;
      const up = await isServerRunning();
      if (up) { clearInterval(timer); resolve(true); return; }
      if (attempts >= maxAttempts) { clearInterval(timer); resolve(false); }
    }, intervalMs);
  });
}

// ─── Server startup ───────────────────────────────────────────────────────────

/**
 * Opens a Terminal window to start the server with sudo.
 * This is the most reliable cross-version macOS approach — Terminal handles
 * the password prompt natively and the server process persists.
 */
function openTerminalToStartServer() {
  const bin = getServerBinPath();
  if (!bin) {
    dialog.showErrorBox('HomeTunnel',
      'Server binary not found.\nRun: make macos-server\nin the project folder, then try again.'
    );
    return;
  }

  const cwd = getServerCwd();

  // Write a self-contained .command script (double-clickable on macOS)
  const tmpCmd = path.join(os.tmpdir(), 'hometunnel_start.command');
  fs.writeFileSync(tmpCmd, [
    '#!/bin/bash',
    'clear',
    'echo "╔══════════════════════════════════════╗"',
    'echo "║   HomeTunnel — Starting VPN Server   ║"',
    'echo "╚══════════════════════════════════════╝"',
    'echo ""',
    `cd "${cwd}"`,
    'echo "You will be asked for your Mac password to start the VPN server."',
    'echo ""',
    `sudo "${bin}"`,
    '',
  ].join('\n'), { mode: 0o755 });

  // Open it in Terminal.app — macOS handles sudo natively
  exec(`open -a Terminal "${tmpCmd}"`, (err) => {
    if (err) {
      console.error('[main] Could not open Terminal:', err.message);
      // Fallback: show instructions in a dialog
      dialog.showMessageBox(mainWindow, {
        type: 'info',
        title: 'Start Server Manually',
        message: 'Open Terminal and run:',
        detail: `cd '${cwd}'\nsudo '${bin}'`,
        buttons: ['OK'],
      });
    }
  });

  // Start polling — dashboard will connect automatically once server is up
  if (mainWindow) {
    mainWindow.webContents.send('server-log',
      'Terminal opened — enter your password there to start the server.');
    mainWindow.webContents.send('server-log',
      'Dashboard will connect automatically once the server is running…');
  }

  pollUntilUp(60, 1500).then((up) => {
    if (up) {
      console.log('[main] Server is up.');
      if (mainWindow) mainWindow.webContents.send('server-log', '✓ Server started — connected!');
    }
  });
}

function stopGoServer() {
  // Stop via PID file (privileged server started via Terminal)
  const pidFile = path.join(getServerCwd(), '.server.pid');
  if (fs.existsSync(pidFile)) {
    try {
      const pid = parseInt(fs.readFileSync(pidFile, 'utf8').trim(), 10);
      if (pid > 0) process.kill(pid, 'SIGTERM');
      fs.unlinkSync(pidFile);
    } catch (e) {
      console.warn('[main] Could not stop server via pid file:', e.message);
    }
  }

  // Also ask the API to stop gracefully (works when server is running)
  http.request({ hostname: '127.0.0.1', port: 7777, path: '/api/server/stop',
    method: 'POST', timeout: 2000 }, () => {}).on('error', () => {}).end();
}

// ─── Init ────────────────────────────────────────────────────────────────────

async function initServer() {
  const up = await isServerRunning();
  if (up) {
    console.log('[main] Server already running.');
    if (mainWindow) mainWindow.webContents.send('server-log', '✓ Connected to running server.');
    return;
  }

  // Server not running — open Terminal so user can start it with sudo
  openTerminalToStartServer();
}

// ─── Window ───────────────────────────────────────────────────────────────────

function createWindow() {
  mainWindow = new BrowserWindow({
    width:           900,
    height:          640,
    minWidth:        760,
    minHeight:       520,
    title:           'HomeTunnel',
    backgroundColor: '#0f1117',
    show:            false,
    webPreferences: {
      preload:          path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration:  false,
      webSecurity:      false, // allows renderer to fetch http://127.0.0.1:7777 directly
      sandbox:          false, // allow preload to use require() for npm packages (e.g. qrcode)
    },
  });

  mainWindow.loadFile(path.join(__dirname, 'renderer', 'index.html'));

  mainWindow.once('ready-to-show', () => {
    mainWindow.show();
    // Open DevTools for debugging — remove once dashboard connectivity is confirmed
    mainWindow.webContents.openDevTools({ mode: 'detach' });
    initServer();
  });

  mainWindow.on('close', (e) => {
    if (!isQuitting) { e.preventDefault(); mainWindow.hide(); }
  });
  mainWindow.on('closed', () => { mainWindow = null; });

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
  });
}

// ─── Tray ─────────────────────────────────────────────────────────────────────

function createTray() {
  const iconPath = path.join(__dirname, 'assets', 'icon.png');
  const icon = fs.existsSync(iconPath)
    ? nativeImage.createFromPath(iconPath).resize({ width: 16, height: 16 })
    : nativeImage.createEmpty();

  tray = new Tray(icon);
  tray.setToolTip('HomeTunnel');
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: 'Show Dashboard', click: () => mainWindow && mainWindow.show() },
    { type: 'separator' },
    { label: 'Quit HomeTunnel', click: () => { isQuitting = true; app.quit(); } },
  ]));
  tray.on('double-click', () => mainWindow && mainWindow.show());
}

// ─── IPC handlers ─────────────────────────────────────────────────────────────

/**
 * Proxy API calls from renderer → Go server using electron.net (Chromium stack).
 * electron.net reliably reaches localhost servers regardless of which user
 * started them — avoids the Node.js http module's cross-user localhost issue.
 * Always resolves within IPC_TIMEOUT ms.
 */
ipcMain.handle('api', async (_event, method, endpoint, body) => {
  const apiCall = new Promise((resolve) => {
    let req;
    try {
      req = net.request({
        method:  method.toUpperCase(),
        url:     API_BASE + endpoint,
        timeout: API_TIMEOUT,
      });
    } catch (e) {
      resolve({ ok: false, body: { error: e.message } });
      return;
    }

    req.on('response', (res) => {
      let data = '';
      res.on('data',  (c) => { data += c; });
      res.on('end',   () => {
        try { resolve({ ok: res.statusCode < 400, status: res.statusCode, body: JSON.parse(data) }); }
        catch { resolve({ ok: false, status: res.statusCode, body: data }); }
      });
      res.on('error', (e) => resolve({ ok: false, body: { error: e.message } }));
    });

    req.on('error', (e) => {
      const msg = (e.message || '').includes('ECONNREFUSED') || (e.message || '').includes('CONNECTION_REFUSED')
        ? 'Server is not running yet. Please wait for it to start.'
        : e.message;
      resolve({ ok: false, body: { error: msg } });
    });

    if (body) {
      req.setHeader('Content-Type', 'application/json');
      req.write(JSON.stringify(body));
    }
    req.end();
  });

  // Hard cap — the UI will NEVER hang longer than IPC_TIMEOUT
  const hardTimeout = new Promise((resolve) =>
    setTimeout(() => resolve({
      ok: false,
      body: { error: 'Request timed out. Is the server running?' },
    }), IPC_TIMEOUT)
  );

  return Promise.race([apiCall, hardTimeout]);
});

ipcMain.handle('spawn-server',  () => { openTerminalToStartServer(); return true; });
ipcMain.handle('kill-server',   () => { stopGoServer(); return true; });
ipcMain.handle('server-alive',  async () => isServerRunning());

ipcMain.on('open-external', (_e, url) => shell.openExternal(url));

// ─── App lifecycle ────────────────────────────────────────────────────────────

app.whenReady().then(() => {
  createWindow();
  createTray();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
    else mainWindow && mainWindow.show();
  });
});

app.on('window-all-closed', () => { /* keep in tray */ });

app.on('before-quit', () => {
  isQuitting = true;
});
