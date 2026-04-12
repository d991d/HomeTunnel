#!/bin/bash
# HomeTunnel — one-click launcher for macOS
# Double-click this file in Finder to start the server and dashboard.
# Copyright (c) 2026 d991d

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

clear
echo "╔══════════════════════════════════════╗"
echo "║         HomeTunnel Launcher          ║"
echo "╚══════════════════════════════════════╝"
echo ""

# ── Pick the right server binary ─────────────────────────────────────────────
ARCH="$(uname -m)"
if [ "$ARCH" = "arm64" ]; then
  PREFER="hometunnel-server-darwin-arm64"
else
  PREFER="hometunnel-server-darwin-amd64"
fi

SERVER_BIN=""
for p in \
  "$SCRIPT_DIR/hometunnel/dist/$PREFER" \
  "$SCRIPT_DIR/dist/$PREFER" \
  "$SCRIPT_DIR/hometunnel/dist/hometunnel-server-darwin-universal" \
  "$SCRIPT_DIR/dist/hometunnel-server-darwin-universal"; do
  if [ -f "$p" ]; then SERVER_BIN="$p"; break; fi
done

if [ -z "$SERVER_BIN" ]; then
  echo "✗ Server binary not found. Run: make macos-server"
  read -p "Press Enter to exit…"
  exit 1
fi

echo "→ Binary:  $(basename "$SERVER_BIN")"
echo "→ Arch:    $ARCH"
echo ""

# ── Clear quarantine from the entire project (iCloud synced files get quarantined) ──
echo "→ Clearing quarantine from project files…"
xattr -dr com.apple.quarantine "$SCRIPT_DIR" 2>/dev/null && echo "  Done." || echo "  (no quarantine flags found)"
echo ""

# ── Kill any stale process holding port 7777 (prevents API bind failures) ────
STALE=$(sudo lsof -ti:7777 2>/dev/null)
if [ -n "$STALE" ]; then
  echo "→ Clearing stale process on port 7777 (PID $STALE)…"
  sudo kill -9 $STALE 2>/dev/null
  sleep 1
fi

# ── If server already running AND responding correctly, skip to dashboard ──────
STATUS=$(curl -s --max-time 2 http://127.0.0.1:7777/api/status 2>/dev/null)
if echo "$STATUS" | grep -q '"running":true'; then
  echo "✓ Server already running — opening dashboard."
else
  # ── Write a server-start script and open it in a NEW Terminal window ──────
  # This is the same approach that worked before (Apr 10 21:11:43).
  # Terminal.app handles sudo natively and keeps the server alive.
  START_SCRIPT="$(mktemp /tmp/hometunnel_server_XXXXXX.command)"
  cat > "$START_SCRIPT" << INNER
#!/bin/bash
clear
echo "╔══════════════════════════════════════╗"
echo "║   HomeTunnel — VPN Server Running    ║"
echo "╚══════════════════════════════════════╝"
echo ""
echo "Keep this window open while using the VPN."
echo "Close it to stop the server."
echo ""
cd "$SCRIPT_DIR"
sudo "$SERVER_BIN"
echo ""
echo "Server stopped. You can close this window."
INNER
  chmod +x "$START_SCRIPT"

  echo "→ Opening server window (enter your Mac password there)…"
  open -a Terminal "$START_SCRIPT"

  # ── Wait for server to come up ─────────────────────────────────────────────
  echo "→ Waiting for server to start…"
  printf "   "
  for i in $(seq 1 30); do
    sleep 1
    if curl -s --max-time 1 http://127.0.0.1:7777/api/status &>/dev/null; then
      echo ""
      echo "✓ Server is up!"
      break
    fi
    printf "."
    if [ "$i" -eq 30 ]; then
      echo ""
      echo "✗ Server didn't start in 30 seconds."
      echo "  Check the other Terminal window for errors."
      echo "  Last log:"
      tail -10 "$SCRIPT_DIR/server.log" 2>/dev/null
      read -p "Press Enter to exit…"
      exit 1
    fi
  done
fi

echo ""
echo "→ Opening dashboard…"
echo ""

# ── Install npm deps if needed ────────────────────────────────────────────────
ELECTRON_DIR="$SCRIPT_DIR/ui/electron"
if [ ! -d "$ELECTRON_DIR/node_modules" ]; then
  echo "→ Installing dashboard dependencies (first run, ~30 sec)…"
  cd "$ELECTRON_DIR" && npm install --silent && cd "$SCRIPT_DIR"
fi

# ── Launch dashboard ──────────────────────────────────────────────────────────
cd "$ELECTRON_DIR"
npx electron .

echo ""
echo "Dashboard closed. The server window keeps running until you close it."
