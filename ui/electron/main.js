// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Electron main process

'use strict';

const { app, BrowserWindow, ipcMain, shell, Tray, Menu, nativeImage } = require('electron');
const path  = require('path');
const fs    = require('fs');
const http  = require('http');
const { spawn } = require('child_process');

// ─── Config ──────────────────────────────────────────────────────────────────

const API_BASE    = 'http://127.0.0.1:7777';
const API_TIMEOUT = 3000; // ms

// ─── Go server process ───────────────────────────────────────────────────────

let goServer   = null;   // child_process handle
let mainWindow = null;
let tray       = null;
let isQuitting = false;

/**
 * Resolve the path to the bundled Go server binary.
 * In development: ../../dist/hometunnel-server[.exe]
 * In production:  resources/bin/hometunnel-server[.exe]
 */
function getServerBinPath() {
  const ext      = process.platform === 'win32' ? '.exe' : '';
  const binName  = `hometunnel-server${ext}`;

  // Packaged build
  const packed = path.join(process.resourcesPath || '', 'bin', binName);
  if (fs.existsSync(packed)) return packed;

  // Development: two levels up from ui/electron/
  const dev = path.join(__dirname, '..', '..', 'dist', binName);
  if (fs.existsSync(dev)) return dev;

  return null;
}

function startGoServer() {
  const bin = getServerBinPath();
  if (!bin) {
    console.warn('[main] Go server binary not found — API calls will fail until you run `make server`');
    return;
  }

  console.log('[main] spawning Go server:', bin);
  goServer = spawn(bin, [], {
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: false,
  });

  goServer.stdout.on('data', (d) => {
    const line = d.toString().trim();
    console.log('[go]', line);
    if (mainWindow) mainWindow.webContents.send('server-log', line);
  });

  goServer.stderr.on('data', (d) => {
    const line = d.toString().trim();
    console.error('[go:err]', line);
    if (mainWindow) mainWindow.webContents.send('server-log', '[ERR] ' + line);
  });

  goServer.on('exit', (code) => {
    console.log('[main] Go server exited with code', code);
    goServer = null;
    if (!isQuitting && mainWindow) {
      mainWindow.webContents.send('server-stopped', code);
    }
  });
}

function stopGoServer() {
  if (goServer) {
    goServer.kill('SIGTERM');
    goServer = null;
  }
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
    },
  });

  mainWindow.loadFile(path.join(__dirname, 'renderer', 'index.html'));

  mainWindow.once('ready-to-show', () => {
    mainWindow.show();
  });

  // Keep in tray instead of quitting on close
  mainWindow.on('close', (e) => {
    if (!isQuitting) {
      e.preventDefault();
      mainWindow.hide();
    }
  });

  mainWindow.on('closed', () => { mainWindow = null; });

  // Open external links in default browser
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    shell.openExternal(url);
    return { action: 'deny' };
  });
}

// ─── Tray ─────────────────────────────────────────────────────────────────────

function createTray() {
  // Use a 16×16 blank icon if no assets folder yet
  const iconPath = path.join(__dirname, 'assets', 'tray-icon.png');
  const icon = fs.existsSync(iconPath)
    ? nativeImage.createFromPath(iconPath)
    : nativeImage.createEmpty();

  tray = new Tray(icon);
  tray.setToolTip('HomeTunnel');

  const menu = Menu.buildFromTemplate([
    { label: 'Show Dashboard', click: () => { mainWindow && mainWindow.show(); } },
    { type: 'separator' },
    { label: 'Quit HomeTunnel', click: () => { isQuitting = true; app.quit(); } },
  ]);
  tray.setContextMenu(menu);
  tray.on('double-click', () => { mainWindow && mainWindow.show(); });
}

// ─── IPC handlers ─────────────────────────────────────────────────────────────

// Proxy API call from renderer → Go server
ipcMain.handle('api', async (_event, method, endpoint, body) => {
  return new Promise((resolve) => {
    const url     = new URL(API_BASE + endpoint);
    const options = {
      hostname: url.hostname,
      port:     url.port,
      path:     url.pathname + url.search,
      method:   method.toUpperCase(),
      headers:  { 'Content-Type': 'application/json' },
      timeout:  API_TIMEOUT,
    };

    const req = http.request(options, (res) => {
      let data = '';
      res.on('data', (c) => { data += c; });
      res.on('end', () => {
        try { resolve({ ok: res.statusCode < 400, status: res.statusCode, body: JSON.parse(data) }); }
        catch { resolve({ ok: false, status: res.statusCode, body: data }); }
      });
    });

    req.on('error',   (e) => resolve({ ok: false, error: e.message }));
    req.on('timeout', ()  => { req.destroy(); resolve({ ok: false, error: 'timeout' }); });

    if (body) req.write(JSON.stringify(body));
    req.end();
  });
});

// Start / stop the Go server process from the renderer
ipcMain.handle('spawn-server',  () => { startGoServer(); return true; });
ipcMain.handle('kill-server',   () => { stopGoServer();  return true; });
ipcMain.handle('server-alive',  () => goServer !== null);

// Open a URL in the system browser
ipcMain.on('open-external', (_e, url) => shell.openExternal(url));

// ─── App lifecycle ────────────────────────────────────────────────────────────

app.whenReady().then(() => {
  createWindow();
  createTray();
  startGoServer();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow();
    else mainWindow && mainWindow.show();
  });
});

app.on('window-all-closed', () => {
  // Keep running in tray on all platforms
});

app.on('before-quit', () => {
  isQuitting = true;
  stopGoServer();
});
