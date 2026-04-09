# Copyright (c) 2026 d991d. All rights reserved.
# HomeTunnel — fetch WinTUN driver for Windows builds
#
# Usage (run from repo root in PowerShell as Admin):
#   .\scripts\fetch-wintun.ps1
#
# Downloads the official WireGuard WinTUN release, verifies its SHA-256
# checksum, and extracts wintun.dll (amd64) to the dist\ folder.
# The DLL must sit alongside hometunnel-server-windows.exe at runtime.

param(
    [string]$Arch = "amd64",
    [string]$OutDir = "dist"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

# ── constants ──────────────────────────────────────────────────────────────────
$WINTUN_VERSION = "0.14.1"
$WINTUN_URL     = "https://www.wintun.net/builds/wintun-$WINTUN_VERSION.zip"
$WINTUN_SHA256  = "c7f5d8c46e38bd63e4b65e9a30baaed82218dd35ec5a4c9dea7df89ffe0b01ba"
$ZIP_NAME       = "wintun-$WINTUN_VERSION.zip"
$TMP            = [System.IO.Path]::GetTempPath()
$ZIP_PATH       = Join-Path $TMP $ZIP_NAME

Write-Host "HomeTunnel — fetching WinTUN $WINTUN_VERSION ($Arch)" -ForegroundColor Cyan

# ── download ───────────────────────────────────────────────────────────────────
if (-Not (Test-Path $ZIP_PATH)) {
    Write-Host "  Downloading $WINTUN_URL ..."
    Invoke-WebRequest -Uri $WINTUN_URL -OutFile $ZIP_PATH -UseBasicParsing
} else {
    Write-Host "  Using cached $ZIP_PATH"
}

# ── verify checksum ────────────────────────────────────────────────────────────
Write-Host "  Verifying SHA-256 ..."
$actual = (Get-FileHash -Path $ZIP_PATH -Algorithm SHA256).Hash.ToLower()
if ($actual -ne $WINTUN_SHA256) {
    Write-Error "Checksum mismatch!`n  expected: $WINTUN_SHA256`n  got:      $actual"
    Remove-Item $ZIP_PATH -Force
    exit 1
}
Write-Host "  Checksum OK" -ForegroundColor Green

# ── extract ────────────────────────────────────────────────────────────────────
$EXTRACT_DIR = Join-Path $TMP "wintun-extract"
if (Test-Path $EXTRACT_DIR) { Remove-Item $EXTRACT_DIR -Recurse -Force }
Expand-Archive -Path $ZIP_PATH -DestinationPath $EXTRACT_DIR

# WinTUN zip layout: wintun/bin/{amd64,arm64,x86}/wintun.dll
$DLL_SRC = Join-Path $EXTRACT_DIR "wintun\bin\$Arch\wintun.dll"
if (-Not (Test-Path $DLL_SRC)) {
    Write-Error "wintun.dll not found at expected path: $DLL_SRC"
    exit 1
}

# ── copy to dist\ ──────────────────────────────────────────────────────────────
if (-Not (Test-Path $OutDir)) { New-Item -ItemType Directory -Path $OutDir | Out-Null }
$DLL_DST = Join-Path $OutDir "wintun.dll"
Copy-Item $DLL_SRC $DLL_DST -Force

# Clean up extract dir (keep the zip as local cache)
Remove-Item $EXTRACT_DIR -Recurse -Force

Write-Host ""
Write-Host "  wintun.dll → $DLL_DST" -ForegroundColor Green
Write-Host ""
Write-Host "Done. Place wintun.dll in the same directory as hometunnel-server-windows.exe" -ForegroundColor Cyan
