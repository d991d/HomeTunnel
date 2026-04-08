// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package config defines server and client configuration structs and
// JSON-file loading/saving helpers.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Build metadata — injected at compile time via -ldflags.
// These variables identify the author and version of every HomeTunnel binary.
var (
	Author    = "d991d"   // overridden by ldflags: -X .../config.Author=d991d
	Version   = "dev"     // overridden by ldflags: -X .../config.Version=1.0.0
	BuildDate = "unknown" // overridden by ldflags: -X .../config.BuildDate=YYYYMMDD
)

// ─── Server configuration ─────────────────────────────────────────────────────

// ServerConfig holds all server-side settings.
type ServerConfig struct {
	// Network
	ListenAddr string `json:"listen_addr"` // UDP address to bind, e.g. "0.0.0.0:48321"
	PublicIP   string `json:"public_ip"`   // Detected or manually set public IP

	// VPN subnet
	VPNSubnet  string `json:"vpn_subnet"`   // CIDR, e.g. "10.8.0.0/24"
	ServerVIP  string `json:"server_vip"`   // Server's own virtual IP, e.g. "10.8.0.1"
	MTU        int    `json:"mtu"`          // Tunnel MTU (default 1380)
	DNSServers []string `json:"dns_servers"` // Pushed to clients, e.g. ["1.1.1.1","8.8.8.8"]

	// Authentication
	SecretKey    string        `json:"secret_key"`    // 32-byte hex master secret for token HMAC
	TokenTTL     time.Duration `json:"token_ttl"`     // Default invite token lifetime
	MaxAuthFails int           `json:"max_auth_fails"` // Attempts before IP block (default 5)
	BlockTimeout time.Duration `json:"block_timeout"` // Block duration after max fails (default 10m)

	// Obfuscation
	Obfuscation ObfuscationConfig `json:"obfuscation"`

	// Logging
	LogLevel  string `json:"log_level"`  // debug | info | warn | error
	LogFile   string `json:"log_file"`   // empty = stderr

	// API
	APIAddr string `json:"api_addr"` // Local HTTP API address, e.g. "127.0.0.1:7777"
}

// ObfuscationConfig controls optional traffic obfuscation.
type ObfuscationConfig struct {
	Enabled        bool   `json:"enabled"`
	HeaderXOR      bool   `json:"header_xor"`
	PaddingMaxBytes int   `json:"padding_max_bytes"`
	TrafficShaping bool   `json:"traffic_shaping"`
	PortMimicry    string `json:"port_mimicry"` // none | https | dns
}

// DefaultServerConfig returns a sensible default configuration.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		ListenAddr:   "0.0.0.0:48321",
		VPNSubnet:    "10.8.0.0/24",
		ServerVIP:    "10.8.0.1",
		MTU:          1380,
		DNSServers:   []string{"1.1.1.1", "8.8.8.8"},
		TokenTTL:     72 * time.Hour,
		MaxAuthFails: 5,
		BlockTimeout: 10 * time.Minute,
		Obfuscation: ObfuscationConfig{
			Enabled:         false,
			HeaderXOR:       true,
			PaddingMaxBytes: 127,
			TrafficShaping:  false,
			PortMimicry:     "none",
		},
		LogLevel: "info",
		APIAddr:  "127.0.0.1:7777",
	}
}

// ─── Client configuration ─────────────────────────────────────────────────────

// ClientConfig holds all client-side settings.
type ClientConfig struct {
	// Connection
	ServerAddr  string `json:"server_addr"`  // e.g. "203.0.113.25:48321"
	Token       string `json:"token"`        // Invite token (base64url)
	DisplayName string `json:"display_name"` // Friendly name shown on server dashboard

	// Behaviour
	AutoReconnect    bool          `json:"auto_reconnect"`
	ReconnectDelay   time.Duration `json:"reconnect_delay"`
	KeepaliveInterval time.Duration `json:"keepalive_interval"`
	MTU              int           `json:"mtu"`

	// Obfuscation (must match server)
	Obfuscation ObfuscationConfig `json:"obfuscation"`

	// Logging
	LogLevel string `json:"log_level"`
	LogFile  string `json:"log_file"`
}

// DefaultClientConfig returns a sensible default client configuration.
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		AutoReconnect:     true,
		ReconnectDelay:    5 * time.Second,
		KeepaliveInterval: 25 * time.Second,
		MTU:               1380,
		LogLevel:          "info",
		Obfuscation: ObfuscationConfig{
			Enabled:         false,
			HeaderXOR:       true,
			PaddingMaxBytes: 127,
		},
	}
}

// ─── Invite token ─────────────────────────────────────────────────────────────

// TokenClaims holds the fields embedded in an invite token.
type TokenClaims struct {
	PeerID      string    `json:"peer_id"`
	DisplayName string    `json:"display_name"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	// Optional per-token limits
	MaxBytesDown int64 `json:"max_bytes_down,omitempty"` // 0 = unlimited
	MaxBytesUp   int64 `json:"max_bytes_up,omitempty"`
}

// ─── Persistence helpers ──────────────────────────────────────────────────────

// LoadServerConfig reads a JSON config file from path.
// Missing fields fall back to DefaultServerConfig values.
func LoadServerConfig(path string) (*ServerConfig, error) {
	cfg := DefaultServerConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil // use defaults
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SaveServerConfig writes cfg to path as pretty-printed JSON.
func SaveServerConfig(cfg *ServerConfig, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadClientConfig reads a JSON config file from path.
func LoadClientConfig(path string) (*ClientConfig, error) {
	cfg := DefaultClientConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SaveClientConfig writes cfg to path as pretty-printed JSON.
func SaveClientConfig(cfg *ClientConfig, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
