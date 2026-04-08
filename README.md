# HomeTunnel

**HomeTunnel** is a lightweight peer-to-peer VPN that lets you run a personal VPN server from your home computer and share access with trusted friends — no third-party provider, no subscriptions, no central server.

```
Friend (client) ──── encrypted tunnel ────► Your home computer (server) ──► Internet
```

> Author: [d991d](https://github.com/d991d) · Version 1.0.0

---

## Features

- **Direct connection** — traffic goes through your home internet, not a data center
- **Modern encryption** — ChaCha20-Poly1305 with X25519 ephemeral key exchange and HKDF-SHA256 key derivation
- **Forward secrecy** — fresh session keys on every connection; past sessions stay private even if a token is compromised
- **Replay protection** — 256-slot sliding window + 30-second timestamp guard
- **Token-based auth** — generate shareable invite links; revoke access instantly
- **Obfuscation layer** — optional header XOR, random padding, and port mimicry to resist DPI
- **Cross-platform server** — runs on Linux, macOS, and Windows
- **Cross-platform clients** — desktop (macOS + Windows + Linux) and Android
- **Auto-reconnect** — client reconnects silently if the connection drops
- **Local dashboard API** — Electron UI talks to a local HTTP API; nothing is exposed to the network

---

## How It Works

```
                       Internet
                          │
              ┌───────────▼────────────┐
              │   HomeTunnel Server     │
              │   • TUN interface       │
              │   • NAT / routing       │
              │   • Session manager     │
              │   • Token auth          │
              └───────┬─────────────────┘
                      │  UDP · ChaCha20-Poly1305
          ────────────┼──────────────────
          │           │           │
    ┌─────▼────┐ ┌────▼─────┐ ┌──▼───────┐
    │ Android  │ │  macOS   │ │ Windows  │
    │  Client  │ │  Client  │ │  Client  │
    └──────────┘ └──────────┘ └──────────┘
```

The handshake is a 4-message exchange:

```
Client ──── HELLO (ephemeral pubkey) ────────────────► Server
Client ◄─── CHALLENGE (server pubkey + nonce) ──────── Server
Client ──── AUTH (HMAC proof + encrypted token) ─────► Server
Client ◄─── SESSION ACCEPT (virtual IP + session ID) ─ Server
```

After the handshake, both sides derive the same 32-byte session key via X25519 + HKDF. All subsequent packets are AEAD-encrypted.

---

## Project Structure

```
vpn-project/
├── core/
│   ├── encryption/     X25519, HKDF, ChaCha20-Poly1305, replay window
│   ├── handshake/      4-message key exchange state machine
│   ├── transport/      UDP socket with buffered send/recv channels
│   ├── tunnel/         Cross-platform TUN interface (Linux / macOS / Windows)
│   └── mobile/         gomobile bridge — exposes Go core to Android (.aar)
│
├── server/
│   ├── main.go         Entry point, NAT setup, packet dispatch loop
│   ├── api/            Local HTTP API consumed by the Electron UI
│   └── client_manager/ Session table, virtual IP pool, rate limiting
│
├── client/
│   └── desktop/        Go networking engine with JSON IPC for Electron
│
├── android/            Android client (Kotlin + VpnService)
│   └── app/src/main/
│       ├── java/…/     MainActivity, ConnectFragment, HomeTunnelVpnService
│       └── res/        Layouts, drawables, icons, strings
│
├── ui/
│   ├── electron/       Server dashboard (Electron + dark UI)
│   └── electron-client/ Desktop connect app (Electron)
│
├── shared/
│   ├── config/         Config structs, JSON load/save, build metadata
│   └── protocol/       Wire format constants, packet encode/decode
│
├── scripts/
│   ├── setup-gomobile.sh   Install gomobile + build Android .aar
│   ├── fetch-wintun.sh     Download wintun.dll for Windows (Linux/macOS)
│   └── fetch-wintun.ps1    Download wintun.dll for Windows (PowerShell)
│
├── Makefile
└── .gitignore
```

---

## Requirements

| Component | Requirement |
|-----------|-------------|
| Go | 1.21 or later |
| Linux server | `iptables`, root or `CAP_NET_ADMIN` |
| macOS server | `pfctl`, admin privileges |
| Windows server | Administrator, [wintun.dll](https://www.wintun.net/) next to the binary |
| Android client | Android 8.0+ (API 26+), VpnService permission |
| Android build | Go 1.21+, Android SDK + NDK, gomobile |
| Desktop client UI | Node.js 18+, Electron 41+ |

---

## Building

Clone the repo and build for your platform:

```bash
git clone https://github.com/d991d/hometunnel.git
cd hometunnel
go mod tidy
```

**Current platform (quick build):**
```bash
make server    # builds dist/hometunnel-server
make client    # builds dist/hometunnel-client
```

**All platforms at once:**
```bash
make all
```

**Per-platform targets:**
```bash
make linux     # Linux amd64
make macos     # macOS universal (Intel + Apple Silicon)
make windows   # Windows amd64
make android   # Android .aar via gomobile (see below)
make apk       # Full Android APK (requires Android SDK)
```

**Run tests:**
```bash
make test
```

Built binaries land in `dist/` and are named:
```
dist/hometunnel-server-linux-amd64
dist/hometunnel-server-darwin-universal
dist/hometunnel-server-windows-amd64.exe
dist/hometunnel-client-linux-amd64
dist/hometunnel-client-darwin-universal
dist/hometunnel-client-windows-amd64.exe
```

Every binary embeds author and version metadata via `-ldflags`. Verify:
```bash
./dist/hometunnel-server --version
# HomeTunnel Server
# Author:  d991d
# Version: 1.0.0
# Build:   20260407
```

---

## Android Build

The Android client compiles the Go networking core into a `.aar` library using [gomobile](https://pkg.go.dev/golang.org/x/mobile/cmd/gomobile), then builds a Kotlin app with Android Studio or Gradle.

### Prerequisites

- Go 1.21+ in your PATH
- Android Studio with **SDK Platform 34** and **NDK** installed
- `ANDROID_HOME` pointing to your SDK directory

```bash
export ANDROID_HOME=~/Library/Android/sdk   # macOS
export ANDROID_HOME=~/Android/Sdk            # Linux
```

### One-command setup

The helper script installs gomobile, initialises it, builds the `.aar`, and copies it into the Android project:

```bash
bash scripts/setup-gomobile.sh
```

### Manual steps

```bash
# 1. Install gomobile
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

# 2. Build the Go core library
make android
# → dist/hometunnel-core-android.aar
# → android/app/libs/hometunnel-core-android.aar  (copied automatically)

# 3. Build the APK
make apk
# → android/app/build/outputs/apk/debug/app-debug.apk
```

### Install on device

```bash
adb install android/app/build/outputs/apk/debug/app-debug.apk
```

### Android app structure

| File | Purpose |
|------|---------|
| `HomeTunnelVpnService.kt` | Foreground VpnService — owns TUN fd, bridges TUN ↔ Go Engine |
| `MainActivity.kt` | VPN permission flow, deep-link handling, fragment host |
| `ConnectFragment.kt` | Invite link input, clipboard paste, advanced fields |
| `ConnectingFragment.kt` | Spinner shown during handshake |
| `ConnectedFragment.kt` | Virtual IP, duration timer, bytes in/out, disconnect |

The app handles `hometunnel://` deep-links so friends can tap the invite link directly to connect.

---

## Server Setup

### 1. First run

```bash
sudo ./hometunnel-server
```

On first launch the server:
- Generates a 32-byte random secret key
- Saves it to `server.json` in the current directory
- Starts the VPN listener on `0.0.0.0:48321` (UDP)
- Starts the local API on `127.0.0.1:7777`
- Detects your public IP automatically

### 2. Port forwarding

Forward **UDP port 48321** on your home router to the server's LAN IP. This is the only network configuration required.

### 3. Generate an invite link

```bash
curl -s -X POST http://127.0.0.1:7777/api/invite \
  -H "Content-Type: application/json" \
  -d '{"display_name":"Alice","ttl_hours":72}'
```

Response:
```json
{
  "id": "aB3xZ9kQ",
  "display_name": "Alice",
  "invite_link": "hometunnel://203.0.113.25:48321?token=eyJhbGci...",
  "expires_at": "2026-04-10T12:00:00Z"
}
```

Share the `invite_link` (or its QR code) with your friend.

### 4. Configuration

`server.json` is created automatically. Key options:

```json
{
  "listen_addr": "0.0.0.0:48321",
  "vpn_subnet":  "10.8.0.0/24",
  "server_vip":  "10.8.0.1",
  "mtu":         1380,
  "dns_servers": ["1.1.1.1", "8.8.8.8"],
  "token_ttl":   "72h",
  "log_level":   "info",
  "api_addr":    "127.0.0.1:7777",
  "obfuscation": {
    "enabled":          false,
    "header_xor":       true,
    "padding_max_bytes": 127
  }
}
```

> ⚠️ `server.json` contains your secret key. Never commit it to version control — it is listed in `.gitignore`.

---

## Client Usage

### Desktop (IPC mode)

The desktop client binary is driven by the Electron UI. You can also run it directly for testing:

```bash
# Send commands as newline-delimited JSON on stdin
echo '{"cmd":"connect","server_addr":"203.0.113.25:48321","token":"eyJhbGci...","display_name":"Alice"}' \
  | ./hometunnel-client
```

Events are emitted on stdout:
```json
{"event":"status","state":"connecting","server":"203.0.113.25:48321"}
{"event":"status","state":"connected","virtual_ip":"10.8.0.2","mtu":1380}
{"event":"stats","latency_ms":42,"bytes_in":1234567,"bytes_out":456789}
```

Disconnect gracefully:
```bash
echo '{"cmd":"disconnect"}' | ./hometunnel-client
```

### Android

Tap the invite link on your phone — HomeTunnel opens automatically and pre-fills the connection details. Grant the VPN permission when Android prompts, then tap **Connect**. The app runs in the background as a foreground service with a persistent notification showing your virtual IP.

---

## Server API Reference

All endpoints are bound to `127.0.0.1` and are only accessible locally.

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/status` | Server status, uptime, public IP, port |
| `GET` | `/api/clients` | Active sessions with virtual IP and bandwidth |
| `POST` | `/api/invite` | Generate invite token (`display_name`, `ttl_hours`) |
| `DELETE` | `/api/invite?id=<id>` | Revoke an invite token |
| `POST` | `/api/server/start` | Start the VPN listener |
| `POST` | `/api/server/stop` | Stop the VPN listener |
| `GET` | `/api/logs` | Last 100 log lines |

---

## Security

| Threat | Defence |
|--------|---------|
| Passive eavesdropper | ChaCha20-Poly1305 AEAD — unreadable without session key |
| Man-in-the-middle | X25519 handshake + HMAC-SHA256 defeats MITM |
| Replay attack | 256-slot sliding window + 30-second timestamp guard |
| Brute-force token | Rate limit: 5 attempts / 60 s, then 10-minute IP block |
| Token theft | Short TTL (default 72 h) + instant server-side revocation |
| Server compromise | Session keys are ephemeral and never stored on disk |
| VPN fingerprinting | Optional header XOR, packet padding, port mimicry |

---

## Obfuscation (optional)

Enable in `server.json` and `client.json`:

```json
"obfuscation": {
  "enabled":          true,
  "header_xor":       true,
  "padding_max_bytes": 127,
  "traffic_shaping":  false,
  "port_mimicry":     "none"
}
```

`port_mimicry` options: `none` · `https` (port 443) · `dns` (port 53)

---

## License

Copyright © 2026 [d991d](https://github.com/d991d). All rights reserved.
