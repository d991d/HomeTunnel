// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package api provides the local HTTP API consumed by the Electron server UI.
//
// All endpoints are bound to 127.0.0.1 and are NOT exposed to the network.
//
// Endpoints:
//
//	GET  /api/status          – server status, uptime, public IP, port
//	GET  /api/clients         – list of active sessions
//	POST /api/invite          – generate a new invite token / link
//	DELETE /api/invite/:id    – revoke an invite token
//	POST /api/server/start    – start the VPN listener
//	POST /api/server/stop     – stop the VPN listener
//	GET  /api/logs            – tail last 100 log lines
package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/d991d/hometunnel/server/client_manager"
	"github.com/d991d/hometunnel/shared/config"
)

// ─── Server state (injected by server/main.go) ───────────────────────────────

// ServerState is the live state the API reads from.
type ServerState struct {
	mu        sync.RWMutex
	Running   bool
	StartedAt time.Time
	PublicIP  string
	Port      int
	Manager   *client_manager.Manager
	Config    *config.ServerConfig

	StartFn func() error
	StopFn  func() error

	// Token store: tokenID → claims (in-memory for now)
	tokens map[string]*TokenRecord

	// Ring buffer of log lines
	logMu  sync.Mutex
	logBuf []string
}

// TokenRecord tracks a generated invite token.
type TokenRecord struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	Token       string    `json:"token"`
	InviteLink  string    `json:"invite_link"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	Revoked     bool      `json:"revoked"`
}

// Lock acquires the state write lock.
func (s *ServerState) Lock() { s.mu.Lock() }

// Unlock releases the state write lock.
func (s *ServerState) Unlock() { s.mu.Unlock() }

// RLock acquires the state read lock.
func (s *ServerState) RLock() { s.mu.RLock() }

// RUnlock releases the state read lock.
func (s *ServerState) RUnlock() { s.mu.RUnlock() }

// NewServerState creates an empty ServerState.
func NewServerState(cfg *config.ServerConfig) *ServerState {
	return &ServerState{
		Config: cfg,
		tokens: make(map[string]*TokenRecord),
		logBuf: make([]string, 0, 200),
	}
}

// AddLog appends a line to the in-memory log ring buffer (max 500 lines).
func (s *ServerState) AddLog(line string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	s.logBuf = append(s.logBuf, line)
	if len(s.logBuf) > 500 {
		s.logBuf = s.logBuf[len(s.logBuf)-500:]
	}
}

// ValidateToken checks whether a token string is valid and not revoked.
// Returns (displayName, nil) on success.
func (s *ServerState) ValidateToken(tokenStr string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rec := range s.tokens {
		if rec.Token == tokenStr {
			if rec.Revoked {
				return "", fmt.Errorf("token revoked")
			}
			if time.Now().After(rec.ExpiresAt) {
				return "", fmt.Errorf("token expired")
			}
			return rec.DisplayName, nil
		}
	}
	return "", fmt.Errorf("token not found")
}

// ─── HTTP server ──────────────────────────────────────────────────────────────

// Server is the local HTTP API server.
type Server struct {
	state  *ServerState
	mux    *http.ServeMux
	server *http.Server
}

// New creates a new API Server.
func New(state *ServerState) *Server {
	s := &Server{state: state, mux: http.NewServeMux()}
	s.registerRoutes()
	return s
}

// Start begins listening on cfg.APIAddr.
func (s *Server) Start(addr string) error {
	s.server = &http.Server{
		Addr:    addr,
		Handler: s.mux,
	}
	log.Printf("[API] listening on http://%s", addr)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[API] error: %v", err)
		}
	}()
	return nil
}

// Stop shuts down the API server.
func (s *Server) Stop() { _ = s.server.Close() }

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/api/status",        withCORS(s.handleStatus))
	s.mux.HandleFunc("/api/clients",       withCORS(s.handleClients))
	s.mux.HandleFunc("/api/invite",        withCORS(s.handleInvite))
	s.mux.HandleFunc("/api/server/start",  withCORS(s.handleServerStart))
	s.mux.HandleFunc("/api/server/stop",   withCORS(s.handleServerStop))
	s.mux.HandleFunc("/api/logs",          withCORS(s.handleLogs))
}

// ─── Handlers ─────────────────────────────────────────────────────────────────

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()

	var uptime string
	if s.state.Running {
		uptime = time.Since(s.state.StartedAt).Round(time.Second).String()
	}
	jsonOK(w, map[string]interface{}{
		"running":    s.state.Running,
		"public_ip":  s.state.PublicIP,
		"port":       s.state.Port,
		"uptime":     uptime,
		"version":    "1.0.0",
	})
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	s.state.mu.RLock()
	mgr := s.state.Manager
	s.state.mu.RUnlock()

	if mgr == nil {
		jsonOK(w, []interface{}{})
		return
	}

	sessions := mgr.List()
	type clientDTO struct {
		SessionID   uint32    `json:"session_id"`
		DisplayName string    `json:"display_name"`
		VirtualIP   string    `json:"virtual_ip"`
		PeerAddr    string    `json:"peer_addr"`
		BytesIn     uint64    `json:"bytes_in"`
		BytesOut    uint64    `json:"bytes_out"`
		ConnectedAt time.Time `json:"connected_at"`
		LastSeen    time.Time `json:"last_seen"`
	}
	out := make([]clientDTO, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, clientDTO{
			SessionID:   sess.SessionID,
			DisplayName: sess.DisplayName,
			VirtualIP:   sess.VirtualIP.String(),
			PeerAddr:    sess.PeerAddr.String(),
			BytesIn:     sess.BytesIn(),
			BytesOut:    sess.BytesOut(),
			ConnectedAt: sess.ConnectedAt,
			LastSeen:    sess.LastSeen,
		})
	}
	jsonOK(w, out)
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		s.handleRevokeInvite(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DisplayName string        `json:"display_name"`
		TTLHours    int           `json:"ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.DisplayName = "Friend"
		req.TTLHours = 72
	}
	if req.DisplayName == "" { req.DisplayName = "Friend" }
	if req.TTLHours <= 0   { req.TTLHours = 72 }

	// Generate token: 32 random bytes, base64url encoded
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		jsonErr(w, "failed to generate token", http.StatusInternalServerError)
		return
	}
	tokenStr := base64.RawURLEncoding.EncodeToString(raw)

	id := randomID()
	now := time.Now()
	rec := &TokenRecord{
		ID:          id,
		DisplayName: req.DisplayName,
		Token:       tokenStr,
		IssuedAt:    now,
		ExpiresAt:   now.Add(time.Duration(req.TTLHours) * time.Hour),
	}

	s.state.mu.RLock()
	publicIP := s.state.PublicIP
	port := s.state.Port
	s.state.mu.RUnlock()

	rec.InviteLink = fmt.Sprintf("hometunnel://%s:%d?token=%s", publicIP, port, tokenStr)

	s.state.mu.Lock()
	s.state.tokens[id] = rec
	s.state.mu.Unlock()

	jsonOK(w, rec)
}

func (s *Server) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if rec, ok := s.state.tokens[id]; ok {
		rec.Revoked = true
		jsonOK(w, map[string]string{"status": "revoked"})
		return
	}
	jsonErr(w, "token not found", http.StatusNotFound)
}

func (s *Server) handleServerStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.state.mu.RLock()
	running := s.state.Running
	startFn := s.state.StartFn
	s.state.mu.RUnlock()

	if running {
		jsonOK(w, map[string]string{"status": "already running"})
		return
	}
	if startFn == nil {
		jsonErr(w, "start function not configured", http.StatusInternalServerError)
		return
	}
	if err := startFn(); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "started"})
}

func (s *Server) handleServerStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.state.mu.RLock()
	stopFn := s.state.StopFn
	s.state.mu.RUnlock()

	if stopFn == nil {
		jsonErr(w, "stop function not configured", http.StatusInternalServerError)
		return
	}
	if err := stopFn(); err != nil {
		jsonErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, map[string]string{"status": "stopped"})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	s.state.logMu.Lock()
	lines := make([]string, len(s.state.logBuf))
	copy(lines, s.state.logBuf)
	s.state.logMu.Unlock()

	// Return last 100
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	jsonOK(w, map[string]interface{}{"lines": lines})
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func jsonOK(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func withCORS(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Allow all origins — the API binds to 127.0.0.1 only so this is safe.
		// Electron loads the renderer from file:// which has a "null" origin;
		// using "*" ensures the browser never rejects the response.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		fn(w, r)
	}
}

func randomID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// DetectPublicIP queries external services to find the server's real public IPv4.
// Falls back to the local interface IP if all services are unreachable.
func DetectPublicIP() string {
	services := []string{
		"https://api.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://checkip.amazonaws.com",
		"https://api4.my-ip.io/ip",
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, svc := range services {
		resp, err := client.Get(svc)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
		resp.Body.Close()
		if err != nil {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			log.Printf("[server] public IP detected via %s: %s", svc, ip)
			return ip
		}
	}
	// Last resort: use the outbound LAN interface address
	log.Printf("[server] WARNING: could not detect public IP — using LAN IP. " +
		"Set \"public_ip\" in server.json to override.")
	conn, err := net.Dial("udp4", "8.8.8.8:53")
	if err != nil {
		return "unknown"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}
