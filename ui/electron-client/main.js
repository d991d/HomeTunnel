// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Client Electron main process

'use strict';

const { app, BrowserWindow, ipcMain, shell, Tray, Menu, nativeImage } = require('electron');
const path   = require('path');
const fs     = require('fs');
const { spawn } = require('child_process');

// ─── State ────────────────────────────────────────────────────────────────────

let mainWindow = null;
let tray       = null;
let goClient   = null;   // Go client child process
let isQuitting = false;

// ─── Go client binary path ────────────────────────────────────────────────────

function getClientBinPath() {
  const isWin  = process.platform === 'win32';
  const isMac  = process.platform === 'darwin';
  const ext    = isWin ? '.exe' : '';

  // Platform-specific suffix used by the Makefile
  const suffix = isWin  ? '-windows-amd64'
               : isMac  ? '-darwin-universal'
               :           '-linux-amd64';

  // Search order: packaged (suffixed) → packaged (plain) → dev (suffixed) → dev (plain)
  const candidates = [
    path.join(process.resourcesPath || '', 'bin', `hometunnel-client${suffix}${ext}`),
    path.join(process.resourcesPath || '', 'bin', `hometunnel-client${ext}`),
    path.join(__dirname, '..', '..', 'dist', `hometunnel-client${suffix}${ext}`),
    path.join(__dirname, '..', '..', 'dist', `hometunnel-client${ext}`),
  ];

  for (const p of candidates) {
    if (fs.existsSync(p)) return p;
  }
  return null;
}

// ─── Go client process management ────────────────────────────────────────────

function spawnGoClient() {
  if (goClient) return; // already running

  const bin = getClientBinPath();
  if (!bin) {
    console.warn('[main] Go client binary not found — run `make client` first');
    return false;
  }

  console.log('[main] spawning Go client:', bin);
  goClient = spawn(bin, [], {
    stdio: ['pipe', 'pipe', 'pipe'],
    // Run in the binary's own directory so wintun.dll is found on Windows
    cwd: path.dirname(bin),
  });

  // Parse newline-delimited JSON events from stdout
  let buf = '';
  goClient.stdout.on('data', (data) => {
    buf += data.toString();
    const lines = buf.split('\n');
    buf = lines.pop(); // keep incomplete line in buffer
    for (const line of lines) {
      if (!line.trim()) continue;
      try {
        const evt = JSON.parse(line);
        if (mainWindow) mainWindow.webContents.send('client-event', evt);
      } catch {
        if (mainWindow) mainWindow.webContents.send('client-event', {
          event: 'log', level: 'warn', message: line,
        });
      }
    }
  });

  goClient.stderr.on('data', (data) => {
    const msg = data.toString().trim();
    console.error('[go:err]', msg);
    if (mainWindow) mainWindow.webContents.send('client-event', {
      event: 'log', level: 'error', message: msg,
    });
  });

  goClient.on('exit', (code) => {
    console.log('[main] Go client exited, code:', code);
    goClient = null;
    if (!isQuitting && mainWindow) {
      mainWindow.webContents.send('client-event', {
        event: 'status', state: 'disconnected',
        reason: `process exited (code ${code})`,
      });
    }
  });

  return true;
}

// Send a JSON command to the Go client via stdin
function sendToGoClient(cmd) {
  if (!goClient || !goClient.stdin.writable) {
    // Re-spawn if needed then retry
    if (spawnGoClient()) {
      setTimeout(() => sendToGoClient(cmd), 300);
    }
    return;
  }
  goClient.stdin.write(JSON.stringify(cmd) + '\n');
}

function killGoClient() {
  if (goClient) {
    try { goClient.stdin.write(JSON.stringify({ cmd: 'disconnect' }) + '\n'); } catch {}
    setTimeout(() => { if (goClient) { goClient.kill('SIGTERM'); goClient = null; } }, 800);
  }
}

// ─── IPC handlers ─────────────────────────────────────────────────────────────

// Renderer sends a connect/disconnect/status command → forward to Go binary
ipcMain.handle('client-cmd', (_event, cmd) => {
  if (!goClient) spawnGoClient();
  sendToGoClient(cmd);
  return true;
});

// Provide platform info to renderer
ipcMain.handle('get-platform', () => process.platform);

// Open a URL in the system browser
ipcMain.on('open-external', (_e, url) => shell.openExternal(url));

// ─── Window ───────────────────────────────────────────────────────────────────

function createWindow() {
  mainWindow = new BrowserWindow({
    width:           420,
    height:          580,
    minWidth:        380,
    minHeight:       480,
    maxWidth:        520,
    resizable:       true,
    title:           'HomeTunnel',
    backgroundColor: '#0f1117',
    show:            false,
    webPreferences: {
      preload:          path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration:  false,
      sandbox:          false, // allow preload to use require() for npm packages
    },
  });

  mainWindow.loadFile(path.join(__dirname, 'renderer', 'index.html'));
  mainWindow.setMenuBarVisibility(false);

  mainWindow.once('ready-to-show', () => mainWindow.show());

  mainWindow.on('close', (e) => {
    if (!isQuitting) {
      e.preventDefault();
      mainWindow.hide();
    }
  });

  mainWindow.on('closed', () => { mainWindow = null; });

  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
  });
}

// ─── Tray ─────────────────────────────────────────────────────────────────────

function createTray() {
  const iconPath = path.join(__dirname, 'assets', 'tray-icon.png');
  const icon = fs.existsSync(iconPath)
    ? nativeImage.createFromPath(iconPath)
    : nativeImage.createEmpty();

  tray = new Tray(icon);
  tray.setToolTip('HomeTunnel');

  const updateMenu = (connected = false) => {
    const menu = Menu.buildFromTemplate([
      { label: connected ? '● Connected' : '○ Disconnected', enabled: false },
      { type: 'separator' },
      { label: 'Show Window', click: () => mainWindow && mainWindow.show() },
      { type: 'separator' },
      { label: 'Quit', click: () => { isQuitting = true; app.quit(); } },
    ]);
    tray.setContextMenu(menu);
  };

  updateMenu(false);
  tray.on('double-click', () => mainWindow && mainWindow.show());

  // Update tray menu when connection state changes
  ipcMain.on('connection-state', (_e, connected) => updateMenu(connected));
}

// ─── App lifecycle ────────────────────────────────────────────────────────────

app.whenReady().then(() => {
  createWindow();
  createTray();
  spawnGoClient();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
    else mainWindow && mainWindow.show();
  });
});

app.on('window-all-closed', () => {
  // Stay in tray
});

app.on('before-quit', () => {
  isQuitting = true;
  killGoClient();
});
