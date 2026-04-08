# HomeTunnel Build System
# Copyright (c) 2026 d991d. All rights reserved.
#
# Usage:
#   make all          — build server + client for all platforms
#   make server       — build server for current platform
#   make client       — build desktop client for current platform
#   make linux        — server + client for Linux amd64
#   make macos        — server + client for macOS (amd64 + arm64 universal)
#   make windows      — server + client for Windows amd64
#   make android      — Go core .aar for Android (requires gomobile)
#   make clean        — remove dist/

GO      := go
OUTDIR  := dist
MODULE  := github.com/d991d/hometunnel

AUTHOR  := d991d
VERSION := 1.0.0
BUILD   := $(shell date -u +%Y%m%d)

# Build metadata embedded into every binary
LDFLAGS := -ldflags="-s -w \
  -X '$(MODULE)/shared/config.Author=$(AUTHOR)' \
  -X '$(MODULE)/shared/config.Version=$(VERSION)' \
  -X '$(MODULE)/shared/config.BuildDate=$(BUILD)'"

.PHONY: all server client linux macos windows android apk apk-release clean deps test test-short wintun wintun-win

all: linux macos windows

deps:
	$(GO) mod tidy

# ─── Current platform ─────────────────────────────────────────────────────────

server:
	@mkdir -p $(OUTDIR)
	$(GO) build $(LDFLAGS) -o $(OUTDIR)/hometunnel-server ./server
	@echo "✓  $(OUTDIR)/hometunnel-server"

client:
	@mkdir -p $(OUTDIR)
	$(GO) build $(LDFLAGS) -o $(OUTDIR)/hometunnel-client ./client/desktop
	@echo "✓  $(OUTDIR)/hometunnel-client"

# ─── Linux ────────────────────────────────────────────────────────────────────

linux: linux-server linux-client

linux-server:
	@mkdir -p $(OUTDIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-server-linux-amd64 ./server
	@echo "✓  $(OUTDIR)/hometunnel-server-linux-amd64"

linux-client:
	@mkdir -p $(OUTDIR)
	GOOS=linux GOARCH=amd64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-client-linux-amd64 ./client/desktop
	@echo "✓  $(OUTDIR)/hometunnel-client-linux-amd64"

# ─── macOS ────────────────────────────────────────────────────────────────────

macos: macos-server macos-client

macos-server:
	@mkdir -p $(OUTDIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-server-darwin-amd64 ./server
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-server-darwin-arm64 ./server
	lipo -create -output $(OUTDIR)/hometunnel-server-darwin-universal \
		$(OUTDIR)/hometunnel-server-darwin-amd64 \
		$(OUTDIR)/hometunnel-server-darwin-arm64 2>/dev/null || \
		cp $(OUTDIR)/hometunnel-server-darwin-arm64 \
		   $(OUTDIR)/hometunnel-server-darwin-universal
	@echo "✓  $(OUTDIR)/hometunnel-server-darwin-universal"

macos-client:
	@mkdir -p $(OUTDIR)
	GOOS=darwin GOARCH=amd64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-client-darwin-amd64 ./client/desktop
	GOOS=darwin GOARCH=arm64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-client-darwin-arm64 ./client/desktop
	lipo -create -output $(OUTDIR)/hometunnel-client-darwin-universal \
		$(OUTDIR)/hometunnel-client-darwin-amd64 \
		$(OUTDIR)/hometunnel-client-darwin-arm64 2>/dev/null || \
		cp $(OUTDIR)/hometunnel-client-darwin-arm64 \
		   $(OUTDIR)/hometunnel-client-darwin-universal
	@echo "✓  $(OUTDIR)/hometunnel-client-darwin-universal"

# ─── Windows ──────────────────────────────────────────────────────────────────

windows: windows-server windows-client

windows-server:
	@mkdir -p $(OUTDIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-server-windows-amd64.exe ./server
	@echo "✓  $(OUTDIR)/hometunnel-server-windows-amd64.exe"

windows-client:
	@mkdir -p $(OUTDIR)
	GOOS=windows GOARCH=amd64 $(GO) build $(LDFLAGS) \
		-o $(OUTDIR)/hometunnel-client-windows-amd64.exe ./client/desktop
	@echo "✓  $(OUTDIR)/hometunnel-client-windows-amd64.exe"

# ─── Android (requires gomobile) ──────────────────────────────────────────────

android:
	@mkdir -p $(OUTDIR)
	gomobile bind \
		-target=android/arm64,android/amd64 \
		-androidapi 26 \
		-ldflags="-s -w" \
		-o $(OUTDIR)/hometunnel-core-android.aar \
		$(MODULE)/core/mobile
	cp $(OUTDIR)/hometunnel-core-android.aar android/app/libs/hometunnel-core-android.aar
	@echo "✓  $(OUTDIR)/hometunnel-core-android.aar"
	@echo "✓  android/app/libs/hometunnel-core-android.aar"

# Build debug APK (requires Android SDK + Gradle)
apk: android
	cd android && ./gradlew assembleDebug
	@echo "✓  android/app/build/outputs/apk/debug/app-debug.apk"

# Build release APK
apk-release: android
	cd android && ./gradlew assembleRelease
	@echo "✓  android/app/build/outputs/apk/release/app-release.apk"

# ─── WinTUN ───────────────────────────────────────────────────────────────────

# Download wintun.dll for Windows builds (Linux/macOS cross-compile helper)
wintun:
	@bash scripts/fetch-wintun.sh

# Download wintun.dll on Windows (PowerShell)
wintun-win:
	powershell -ExecutionPolicy Bypass -File scripts\fetch-wintun.ps1

# ─── Tests ────────────────────────────────────────────────────────────────────

test:
	$(GO) test ./core/encryption/... ./server/client_manager/... -v

test-short:
	$(GO) test ./core/encryption/... ./server/client_manager/... -short

# ─── Utilities ────────────────────────────────────────────────────────────────

clean:
	rm -rf $(OUTDIR)
