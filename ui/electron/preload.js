// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — preload / context bridge

'use strict';

const { contextBridge, ipcRenderer } = require('electron');
const QRCode = require('qrcode');

/**
 * Expose a safe, minimal API surface to the renderer.
 * The renderer never has direct access to Node.js or Electron internals.
 */
contextBridge.exposeInMainWorld('ht', {
  /**
   * Make an HTTP API call to the Go server.
   * @param {string} method   GET | POST | DELETE
   * @param {string} endpoint e.g. '/api/status'
   * @param {object} [body]   optional JSON body
   * @returns {Promise<{ok:boolean, status:number, body:any}>}
   */
  api: (method, endpoint, body) =>
    ipcRenderer.invoke('api', method, endpoint, body),

  /** Spawn the Go server binary (called automatically on launch). */
  spawnServer: () => ipcRenderer.invoke('spawn-server'),

  /** Send SIGTERM to the Go server process. */
  killServer: () => ipcRenderer.invoke('kill-server'),

  /** Returns true if the Go child process is currently running. */
  serverAlive: () => ipcRenderer.invoke('server-alive'),

  /** Open a URL in the system default browser. */
  openExternal: (url) => ipcRenderer.send('open-external', url),

  /** Listen for log lines emitted by the Go server process. */
  onServerLog: (cb) => {
    ipcRenderer.on('server-log', (_e, line) => cb(line));
  },

  /** Listen for the Go server process exiting unexpectedly. */
  onServerStopped: (cb) => {
    ipcRenderer.on('server-stopped', (_e, code) => cb(code));
  },

  /**
   * Render a QR code onto a canvas element.
   * @param {HTMLCanvasElement} canvas
   * @param {string} text
   */
  drawQR: (canvas, text) =>
    QRCode.toCanvas(canvas, text, { width: 180, margin: 1 }),
});
