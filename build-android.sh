#!/usr/bin/env bash
# HomeTunnel — Android APK build script
# Copyright (c) 2026 d991d. All rights reserved.
#
# Usage:
#   chmod +x build-android.sh
#   ./build-android.sh
#
# Requirements (all free):
#   1. Go 1.21+         — https://go.dev/dl/
#   2. Android Studio   — https://developer.android.com/studio
#      (installs SDK, NDK, and Java for you)
#
# The script will:
#   • install gomobile if missing
#   • compile the Go VPN core into an Android .aar library
#   • build the debug APK with Gradle
#   • print the path to the finished APK

set -euo pipefail

# ─── Resolve project root (directory this script lives in) ────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$SCRIPT_DIR"
HOMETUNNEL_DIR="$PROJECT_ROOT/hometunnel"
ANDROID_DIR="$HOMETUNNEL_DIR/android"
DIST_DIR="$PROJECT_ROOT/dist"
LIBS_DIR="$ANDROID_DIR/app/libs"
AAR="$DIST_DIR/hometunnel-core-android.aar"
MODULE="github.com/d991d/hometunnel"

echo "────────────────────────────────────────────"
echo "  HomeTunnel Android Build"
echo "────────────────────────────────────────────"

# ─── 1. Check Go ──────────────────────────────────────────────────────────────
if ! command -v go &>/dev/null; then
  echo "✗  Go not found. Install from https://go.dev/dl/ then re-run."
  exit 1
fi
echo "✓  Go $(go version | awk '{print $3}')"

# ─── 2. Locate Android SDK ────────────────────────────────────────────────────
# Try local.properties first, then common default locations
LOCAL_PROPS="$ANDROID_DIR/local.properties"
SDK_DIR=""

if [[ -f "$LOCAL_PROPS" ]]; then
  SDK_DIR=$(grep '^sdk\.dir=' "$LOCAL_PROPS" | sed 's/sdk\.dir=//' | sed 's/\\//g')
fi

if [[ -z "$SDK_DIR" || ! -d "$SDK_DIR" ]]; then
  # Try common Android Studio install locations on macOS
  for candidate in \
    "$HOME/Library/Android/sdk" \
    "/Volumes/MyDrives/Library/Android/sdk" \
    "/Volumes/MyDrives/Android/sdk"; do
    if [[ -d "$candidate" ]]; then
      SDK_DIR="$candidate"
      break
    fi
  done
fi

if [[ -z "$SDK_DIR" || ! -d "$SDK_DIR" ]]; then
  echo ""
  echo "✗  Android SDK not found."
  echo "   Install Android Studio from https://developer.android.com/studio"
  echo "   then re-run this script.  The SDK is usually at:"
  echo "   ~/Library/Android/sdk"
  echo ""
  echo "   Or set sdk.dir manually in:"
  echo "   $LOCAL_PROPS"
  exit 1
fi
echo "✓  Android SDK: $SDK_DIR"

# Export ANDROID_HOME so gomobile can find the SDK regardless of default path
export ANDROID_HOME="$SDK_DIR"
export ANDROID_SDK_ROOT="$SDK_DIR"

# ─── 3. Write local.properties ────────────────────────────────────────────────
echo "sdk.dir=$SDK_DIR" > "$LOCAL_PROPS"
echo "✓  Wrote local.properties"

# ─── 4. Locate NDK ────────────────────────────────────────────────────────────
NDK_DIR=""
if [[ -d "$SDK_DIR/ndk" ]]; then
  # Pick the highest version available
  NDK_DIR=$(ls -d "$SDK_DIR/ndk/"*/ 2>/dev/null | sort -V | tail -1)
fi
if [[ -z "$NDK_DIR" || ! -d "$NDK_DIR" ]]; then
  echo ""
  echo "✗  Android NDK not found inside $SDK_DIR/ndk/"
  echo "   In Android Studio: SDK Manager → SDK Tools → NDK (Side by side) → Install"
  exit 1
fi
NDK_DIR="${NDK_DIR%/}"
echo "✓  NDK: $NDK_DIR"

# ─── 5. Install / update gomobile ─────────────────────────────────────────────
echo "→  Installing/updating gomobile…"
go install golang.org/x/mobile/cmd/gomobile@latest
go install golang.org/x/mobile/cmd/gobind@latest
GOBIN=$(go env GOPATH)/bin
export PATH="$GOBIN:$PATH"

echo "→  Running gomobile init…"
gomobile init

echo "✓  gomobile installed at $(which gomobile)"

# ─── 6. Build the Go AAR ──────────────────────────────────────────────────────
mkdir -p "$DIST_DIR" "$LIBS_DIR"

# gomobile bind needs the golang.org/x/mobile source in the module cache.
# We download it but do NOT run go mod tidy (which would remove it as unused).
echo "→  Fetching golang.org/x/mobile source into module cache…"
cd "$HOMETUNNEL_DIR"
GOFLAGS="-mod=mod" go get golang.org/x/mobile@latest
# Download full source so gobind can find the bind sub-package
go mod download golang.org/x/mobile

echo "→  Compiling Go VPN core → .aar (arm64 + amd64)…"
GOFLAGS="-mod=mod" gomobile bind \
  -target=android/arm64,android/amd64 \
  -androidapi 26 \
  -ldflags="-s -w" \
  -o "$AAR" \
  "$MODULE/core/mobile"

cp "$AAR" "$LIBS_DIR/hometunnel-core-android.aar"
echo "✓  $AAR"
echo "✓  $LIBS_DIR/hometunnel-core-android.aar"

# ─── 7. Locate Java (prefer Android Studio's bundled JDK) ────────────────────
# Use Android Studio's bundled JDK to avoid system Java environment issues
for AS_JDK in \
    "/Applications/Android Studio.app/Contents/jbr/Contents/Home" \
    "/Applications/Android Studio.app/Contents/jre/Contents/Home" \
    "/Applications/Android Studio.app/Contents/jre/jdk/Contents/Home"; do
  if [[ -d "$AS_JDK" ]]; then
    export JAVA_HOME="$AS_JDK"
    export PATH="$JAVA_HOME/bin:$PATH"
    echo "✓  Java: $JAVA_HOME"
    break
  fi
done

if [[ -z "$JAVA_HOME" ]]; then
  echo "⚠  Using system Java: $(java -version 2>&1 | head -1)"
fi

# ─── 8. Build the APK ─────────────────────────────────────────────────────────
cd "$ANDROID_DIR"
echo "→  Building debug APK with Gradle…"
chmod +x gradlew

# Clear any Java env vars that corrupt JVM argument parsing
unset JAVA_TOOL_OPTIONS
unset _JAVA_OPTIONS
unset JDK_JAVA_OPTIONS

./gradlew assembleDebug --no-daemon

APK="$ANDROID_DIR/app/build/outputs/apk/debug/app-debug.apk"
if [[ -f "$APK" ]]; then
  # Copy to dist/ for easy sharing
  cp "$APK" "$DIST_DIR/HomeTunnel.apk"
  echo ""
  echo "────────────────────────────────────────────"
  echo "  ✓  Build complete!"
  echo "  APK → dist/HomeTunnel.apk"
  echo "────────────────────────────────────────────"
  echo ""
  echo "  To install on Android:"
  echo "    adb install dist/HomeTunnel.apk"
  echo "  Or share the APK file directly with friends."
  echo ""
else
  echo "✗  APK not found — check Gradle output above for errors."
  exit 1
fi
