#!/usr/bin/env bash
# Copyright (c) 2026 d991d. All rights reserved.
# HomeTunnel — gomobile setup script
#
# Installs gomobile, initialises it, then builds the Android .aar.
#
# Prerequisites:
#   • Go 1.21+  (https://go.dev/dl/)
#   • Android SDK + NDK  (install via Android Studio → SDK Manager)
#   • ANDROID_HOME set, e.g.:  export ANDROID_HOME=~/Library/Android/sdk
#   • NDK installed: $ANDROID_HOME/ndk/<version>/
#
# Usage:
#   export ANDROID_HOME=~/Library/Android/sdk
#   bash scripts/setup-gomobile.sh

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

GO="${GOPATH:-$HOME/go}/bin/go"
if ! command -v go &>/dev/null && [ -x "$GO" ]; then
    export PATH="$PATH:$(dirname "$GO")"
fi
if ! command -v go &>/dev/null; then
    echo "Error: 'go' not found. Install Go from https://go.dev/dl/" >&2
    exit 1
fi

echo "Go: $(go version)"

# ── check ANDROID_HOME ────────────────────────────────────────────────────────
if [ -z "${ANDROID_HOME:-}" ]; then
    # Common locations
    for candidate in \
        "$HOME/Library/Android/sdk" \
        "$HOME/Android/Sdk" \
        "/usr/local/lib/android/sdk"; do
        if [ -d "$candidate" ]; then
            export ANDROID_HOME="$candidate"
            break
        fi
    done
fi

if [ -z "${ANDROID_HOME:-}" ]; then
    echo "Error: ANDROID_HOME is not set." >&2
    echo "Install Android Studio and set:" >&2
    echo "  export ANDROID_HOME=~/Library/Android/sdk" >&2
    exit 1
fi
echo "ANDROID_HOME: $ANDROID_HOME"

# ── install gomobile ──────────────────────────────────────────────────────────
echo ""
echo "Installing gomobile…"
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest

GOMOBILE="$(go env GOPATH)/bin/gomobile"

# ── init gomobile (downloads NDK toolchain) ───────────────────────────────────
echo "Initialising gomobile (may take a few minutes)…"
"$GOMOBILE" init

# ── build .aar ───────────────────────────────────────────────────────────────
echo ""
echo "Building hometunnel-core-android.aar…"
mkdir -p dist android/app/libs

"$GOMOBILE" bind \
    -target=android/arm64,android/amd64 \
    -androidapi 26 \
    -ldflags="-s -w" \
    -o dist/hometunnel-core-android.aar \
    github.com/d991d/hometunnel/core/mobile

cp dist/hometunnel-core-android.aar android/app/libs/hometunnel-core-android.aar

echo ""
echo "✓  dist/hometunnel-core-android.aar"
echo "✓  android/app/libs/hometunnel-core-android.aar"
echo ""
echo "Now build the APK:"
echo "  cd android && ./gradlew assembleDebug"
echo "  → app/build/outputs/apk/debug/app-debug.apk"
