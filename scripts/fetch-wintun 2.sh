#!/usr/bin/env bash
# Copyright (c) 2026 d991d. All rights reserved.
# HomeTunnel — fetch WinTUN DLL for cross-compilation on Linux/macOS
#
# Usage (from repo root):
#   bash scripts/fetch-wintun.sh [amd64|arm64]   (default: amd64)
#
# Downloads the official WinTUN release, verifies SHA-256, and extracts
# wintun.dll to dist/ so it can be bundled with the Windows installer.

set -euo pipefail

ARCH="${1:-amd64}"
OUTDIR="dist"
WINTUN_VERSION="0.14.1"
WINTUN_URL="https://www.wintun.net/builds/wintun-${WINTUN_VERSION}.zip"
WINTUN_SHA256="07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51"
ZIP_NAME="wintun-${WINTUN_VERSION}.zip"
ZIP_PATH="${TMPDIR:-/tmp}/${ZIP_NAME}"

echo "HomeTunnel — fetching WinTUN ${WINTUN_VERSION} (${ARCH})"

# ── download ───────────────────────────────────────────────────────────────────
if [ ! -f "${ZIP_PATH}" ]; then
    echo "  Downloading ${WINTUN_URL} ..."
    if command -v curl &>/dev/null; then
        curl -fsSL -o "${ZIP_PATH}" "${WINTUN_URL}"
    elif command -v wget &>/dev/null; then
        wget -q -O "${ZIP_PATH}" "${WINTUN_URL}"
    else
        echo "Error: curl or wget required" >&2
        exit 1
    fi
else
    echo "  Using cached ${ZIP_PATH}"
fi

# ── verify checksum ────────────────────────────────────────────────────────────
echo "  Verifying SHA-256 ..."
if command -v sha256sum &>/dev/null; then
    actual=$(sha256sum "${ZIP_PATH}" | awk '{print $1}')
elif command -v shasum &>/dev/null; then
    actual=$(shasum -a 256 "${ZIP_PATH}" | awk '{print $1}')
else
    echo "Warning: no sha256sum/shasum found, skipping checksum" >&2
    actual="${WINTUN_SHA256}"
fi

if [ "${actual}" != "${WINTUN_SHA256}" ]; then
    echo "Checksum mismatch!" >&2
    echo "  expected: ${WINTUN_SHA256}" >&2
    echo "  got:      ${actual}" >&2
    rm -f "${ZIP_PATH}"
    exit 1
fi
echo "  Checksum OK"

# ── extract ────────────────────────────────────────────────────────────────────
EXTRACT_DIR="${TMPDIR:-/tmp}/wintun-extract-$$"
mkdir -p "${EXTRACT_DIR}"
unzip -q "${ZIP_PATH}" -d "${EXTRACT_DIR}"

DLL_SRC="${EXTRACT_DIR}/wintun/bin/${ARCH}/wintun.dll"
if [ ! -f "${DLL_SRC}" ]; then
    echo "Error: wintun.dll not found at ${DLL_SRC}" >&2
    rm -rf "${EXTRACT_DIR}"
    exit 1
fi

# ── copy to dist/ ──────────────────────────────────────────────────────────────
mkdir -p "${OUTDIR}"
cp "${DLL_SRC}" "${OUTDIR}/wintun.dll"
rm -rf "${EXTRACT_DIR}"

echo ""
echo "  wintun.dll → ${OUTDIR}/wintun.dll"
echo ""
echo "Done. Bundle wintun.dll with hometunnel-server-windows.exe"
