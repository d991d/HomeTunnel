# HomeTunnel

> *Internet freedom is not a privilege — it is a right.*

**HomeTunnel** lets anyone with a computer in a free country become a private VPN gateway for their friends and family living under censorship. No subscriptions, no data centers, no third parties — just a direct, encrypted tunnel between people who trust each other.

```
Friend in censored country ──── encrypted tunnel ────► Your home computer ──► Free internet
```

---

## Why HomeTunnel Exists

Hundreds of millions of people live under governments that block news websites, social media, messaging apps, and other essential online services. Commercial VPN providers are often blocked themselves, expensive, or untrustworthy.

HomeTunnel takes a different approach: **your trusted contact runs the server on their home computer in a free country and sends you a single invite link.** You tap the link on your phone or desktop and you are connected — through their home internet, not through any company's infrastructure.

- No logs stored anywhere
- No company that can be pressured to hand over data
- No central server that can be blocked
- Works even when commercial VPN apps are blocked, because the traffic looks like ordinary UDP (with optional obfuscation to look like HTTPS or DNS)

---

## Features

- **Direct peer connection** — traffic routes through your friend's home internet, not a data center
- **Modern encryption** — ChaCha20-Poly1305 with X25519 ephemeral key exchange and HKDF-SHA256 key derivation
- **Forward secrecy** — fresh session keys on every connection; past sessions stay private even if a token is later compromised
- **Replay protection** — 256-slot sliding window + 30-second timestamp guard
- **Token-based auth** — generate shareable invite links; revoke access instantly from the dashboard
- **Obfuscation layer** — optional header XOR, random padding, and port mimicry (HTTPS/DNS) to resist deep packet inspection
- **Cross-platform server** — runs on Linux, macOS, and Windows
- **Cross-platform clients** — iOS, Android, macOS, Windows, and Linux
- **Auto-reconnect** — client reconnects silently if the connection drops
- **Local dashboard** — Electron UI for the server host; nothing is exposed to the network

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
          ────────────┼────────────────────────
          │           │           │           │
    ┌─────▼────┐ ┌────▼─────┐ ┌──▼───────┐ ┌─▼──────┐
    │   iOS    │ │ Android  │ │  macOS/  │ │ Linux  │
    │  Client  │ │  Client  │ │  Windows │ │ Client │
    └──────────┘ └──────────┘ └──────────┘ └────────┘
```

The handshake is a 4-message exchange:

```
Client ──── HELLO (ephemeral pubkey) ────────────────► Server
Client ◄─── CHALLENGE (server pubkey + nonce) ──────── Server
Client ──── AUTH (HMAC proof + encrypted token) ─────► Server
Client ◄─── SESSION ACCEPT (virtual IP + session ID) ─ Server
```

After the handshake, both sides derive the same 32-byte session key via X25519 + HKDF. All subsequent packets are AEAD-encrypted with ChaCha20-Poly1305.

---

## Project Structure

```
vpn-project/
├── core/
│   ├── encryption/     X25519, HKDF, ChaCha20-Poly1305, replay window
│   ├── handshake/      4-message key exchange state machine
│   ├── transport/      UDP socket with buffered send/recv channels
│   ├── tunnel/         Cross-platform TUN interface (Linux / macOS / Windows)
│   └── mobile/         gomobile bridge — exposes Go core to iOS and Android
│
├── server/
│   ├── main.go         Entry point, NAT setup, packet dispatch loop
│   ├── api/            Local HTTP API consumed by the Electron UI
│   └── client_manager/ Session table, virtual IP pool, rate limiting
│
├── client/
│   └── desktop/        Go networking engine with JSON IPC for Electron
│
├── ios/                iOS client (Swift + Network Extension)
│   ├── HomeTunnel/     Main SwiftUI app
│   │   ├── App/        App entry point
│   │   ├── Model/      VPNManager (NETunnelProviderManager)
│   │   └── Views/      Connect / Connecting / Connected screens
│   ├── PacketTunnel/   NEPacketTunnelProvider extension
│   ├── Frameworks/     HometunnelCore.xcframework (gomobile-generated)
│   └── project.yml     xcodegen project definition
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
│   ├── setup-gomobile.sh   Install gomobile + build mobile frameworks
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
| iOS client | iOS 16.0+, Apple Developer account (Network Extension) |
| Android client | Android 8.0+ (API 26+), VpnService permission |
| iOS build | Xcode 15+, gomobile, `xcodegen` |
| Android build | Go 1.21+, Android SDK + NDK, gomobile |
| Desktop client UI | Node.js 18+, Electron 41+ |

---

## Building

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
make android   # Android .aar via gomobile
make apk       # Full Android APK (requires Android SDK)
make ios       # iOS xcframework + Xcode project
```

**Run tests:**
```bash
make test
```

Built binaries land in `dist/`:
```
dist/hometunnel-server-linux-amd64
dist/hometunnel-server-darwin-universal
dist/hometunnel-server-windows-amd64.exe
dist/hometunnel-client-linux-amd64
dist/hometunnel-client-darwin-universal
dist/hometunnel-client-windows-amd64.exe
```

Every binary embeds version metadata. Verify:
```bash
./dist/hometunnel-server --version
# HomeTunnel Server v1.0.0 · build 20260407 · author d991d
```

---

## iOS Build

The iOS client uses Swift + `NEPacketTunnelProvider` for the VPN tunnel, with the Go networking core compiled into an `.xcframework` via gomobile.

### Prerequisites

- Xcode 15 or later (with iOS 16 SDK)
- Go 1.21+
- `xcodegen`: `brew install xcodegen`
- Apple Developer account (paid, $99/year — required for Network Extension on real devices)

### Build

```bash
# Build Go core xcframework + generate Xcode project
make ios

# Open in Xcode
open ios/HomeTunnel.xcodeproj
```

Select your iPhone as the target device and press ⌘R. The app will prompt for VPN permission on first connect.

### Simulator testing

The app includes a full simulator mock mode (`#if targetEnvironment(simulator)`) so the UI — Connect → Connecting → Connected with live stats — can be tested without a physical device or paid developer account.

### iOS app structure

| File | Purpose |
|------|---------|
| `VPNManager.swift` | State controller — wraps `NETunnelProviderManager`, polls shared stats |
| `ConnectView.swift` | Invite link input with paste button and inline link preview |
| `ConnectingView.swift` | Pulsing shield animation during handshake |
| `ConnectedView.swift` | Virtual IP, live bandwidth stats, disconnect |
| `PacketTunnelProvider.swift` | `NEPacketTunnelProvider` — owns Go engine, handles all packet I/O |

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

```bash
bash scripts/setup-gomobile.sh
```

### Manual steps

```bash
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init
make android    # → dist/hometunnel-core-android.aar
make apk        # → android/app/build/outputs/apk/debug/app-debug.apk
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

The app handles `hometunnel://` deep-links so friends can tap an invite link to connect instantly.

---

## Server Setup (for the person in a free country)

### 1. First run

```bash
sudo ./hometunnel-server
```

On first launch the server:
- Generates a 32-byte random secret key
- Saves it to `server.json`
- Listens on `0.0.0.0:48321` (UDP)
- Starts the local management API on `127.0.0.1:7777`
- Detects your public IP automatically

### 2. Port forwarding

Forward **UDP port 48321** on your home router to the server machine's LAN IP. This is the only network configuration required.

### 3. Generate an invite link for your friend

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

Send the `invite_link` to your friend via any messaging app. They tap it — HomeTunnel opens and connects automatically.

### 4. Configuration (`server.json`)

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
    "enabled":           false,
    "header_xor":        true,
    "padding_max_bytes": 127
  }
}
```

> ⚠️ `server.json` contains your secret key. Never commit it to version control.

---

## Server API Reference

All endpoints bind to `127.0.0.1` — local access only.

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

## Security Model

| Threat | Defence |
|--------|---------|
| Passive eavesdropper | ChaCha20-Poly1305 AEAD — unreadable without session key |
| Man-in-the-middle | X25519 handshake + HMAC-SHA256 defeats MITM |
| Replay attack | 256-slot sliding window + 30-second timestamp guard |
| Brute-force token | Rate limit: 5 attempts / 60 s → 10-minute IP block |
| Token theft | Short TTL (default 72 h) + instant server-side revocation |
| Server compromise | Session keys are ephemeral and never stored on disk |
| VPN fingerprinting | Optional header XOR, packet padding, port mimicry |

---

## Obfuscation (for high-censorship environments)

Enable in `server.json` to make HomeTunnel traffic indistinguishable from HTTPS or DNS:

```json
"obfuscation": {
  "enabled":           true,
  "header_xor":        true,
  "padding_max_bytes": 127,
  "traffic_shaping":   false,
  "port_mimicry":      "https"
}
```

`port_mimicry` options: `none` · `https` (port 443) · `dns` (port 53)

---

## macOS Server — Quick Start

A `Launch HomeTunnel.command` launcher is included for non-technical users hosting the server on macOS. Double-click it in Finder — it clears quarantine, starts the server in a Terminal window, and opens the Electron dashboard automatically.

---

## License

Copyright © 2026 [d991d](https://github.com/d991d). All rights reserved.

---

*HomeTunnel is built for people who believe that access to information is a human right. If you are in a position to run a server for someone who needs it, please consider doing so.*
