// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — Client preload / context bridge

'use strict';

const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('ht', {
  /**
   * Send a command to the Go client engine.
   * Commands: connect | disconnect | status
   */
  send: (cmd) => ipcRenderer.invoke('client-cmd', cmd),

  /**
   * Listen for events emitted by the Go client engine.
   * Events: status | stats | error | log
   */
  onEvent: (cb) => {
    ipcRenderer.on('client-event', (_e, evt) => cb(evt));
  },

  /** Notify main process of connection state change (updates tray menu). */
  setConnectionState: (connected) => {
    ipcRenderer.send('connection-state', connected);
  },

  /** Open a URL in the system default browser. */
  openExternal: (url) => ipcRenderer.send('open-external', url),

  /** Returns the OS platform string ('win32', 'darwin', 'linux'). */
  getPlatform: () => ipcRenderer.invoke('get-platform'),
});
