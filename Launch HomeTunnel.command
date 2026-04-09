#!/bin/bash
# HomeTunnel — one-click launcher for macOS
# Double-click this file in Finder to start the server and dashboard.
# Copyright (c) 2026 d991d

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

echo "╔══════════════════════════════════════╗"
echo "║         HomeTunnel Launcher          ║"
echo "╚══════════════════════════════════════╝"
echo ""

# ── 1. Build server binary if missing ────────────────────────────────────────
SERVER_BIN="dist/hometunnel-server-darwin-universal"
if [ ! -f "$SERVER_BIN" ]; then
  echo "→ Server binary not found — building now (this takes ~30 seconds)…"
  if ! command -v go &>/dev/null; then
    echo "✗ Go is not installed. Install it from https://go.dev/dl/ and try again."
    read -p "Press Enter to exit…"
    exit 1
  fi
  make macos-server
  echo "✓ Server built."
fi

# ── 2. Install Electron dependencies if missing ───────────────────────────────
if [ ! -d "ui/electron/node_modules" ]; then
  echo "→ Installing dashboard dependencies (first run only)…"
  if ! command -v npm &>/dev/null; then
    echo "✗ Node.js is not installed. Install it from https://nodejs.org/ and try again."
    read -p "Press Enter to exit…"
    exit 1
  fi
  cd ui/electron && npm install --silent && cd "$SCRIPT_DIR"
  echo "✓ Dependencies installed."
fi

# ── 3. Start server with sudo (asks for Mac password once) ───────────────────
if curl -s --max-time 1 http://127.0.0.1:7777/api/status &>/dev/null; then
  echo "✓ Server already running — skipping start."
else
  echo "→ Starting VPN server (you will be asked for your Mac password)…"
  sudo -v  # prompt for password upfront
  sudo bash -c "cd '$SCRIPT_DIR' && nohup '$SCRIPT_DIR/$SERVER_BIN' >> '$SCRIPT_DIR/server.log' 2>&1 & echo \$! > '$SCRIPT_DIR/.server.pid'"

  echo "→ Waiting for server to start…"
  for i in $(seq 1 15); do
    sleep 1
    if curl -s --max-time 1 http://127.0.0.1:7777/api/status &>/dev/null; then
      echo "✓ Server is running."
      break
    fi
    if [ "$i" -eq 15 ]; then
      echo "✗ Server did not start. Check server.log for details."
      cat server.log 2>/dev/null | tail -20
      read -p "Press Enter to exit…"
      exit 1
    fi
  done
fi

# ── 4. Open the dashboard ────────────────────────────────────────────────────
echo "→ Opening HomeTunnel dashboard…"
echo ""
cd ui/electron
npx electron .

# ── 5. Cleanup on exit ───────────────────────────────────────────────────────
echo ""
echo "Dashboard closed."

PID_FILE="$SCRIPT_DIR/.server.pid"
if [ -f "$PID_FILE" ]; then
  SERVER_PID=$(cat "$PID_FILE")
  if [ -n "$SERVER_PID" ]; then
    echo "→ Stopping server (PID $SERVER_PID)…"
    sudo kill -TERM "$SERVER_PID" 2>/dev/null && echo "✓ Server stopped."
  fi
  rm -f "$PID_FILE"
fi
