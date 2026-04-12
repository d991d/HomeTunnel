// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package client_manager manages connected VPN sessions on the server side.
//
// Responsibilities:
//   - Virtual IP pool allocation and release
//   - Session table (sessionID → Session)
//   - Per-session bandwidth accounting
//   - Keepalive / idle timeout enforcement
//   - Rate-limiting failed authentication attempts
package client_manager

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/d991d/hometunnel/core/encryption"
)

// DefaultIdleTimeout is how long a session may be silent before eviction.
const DefaultIdleTimeout = 2 * time.Minute

// KeepaliveInterval is how often the server sends keepalive probes.
const KeepaliveInterval = 25 * time.Second

// Errors
var (
	ErrPoolExhausted   = errors.New("manager: virtual IP pool exhausted")
	ErrSessionNotFound = errors.New("manager: session not found")
	ErrIPNotInPool     = errors.New("manager: IP not in pool")
)

// ─── Session ──────────────────────────────────────────────────────────────────

// Session represents a connected VPN peer.
type Session struct {
	SessionID   uint32
	VirtualIP   net.IP
	PeerAddr    *net.UDPAddr
	DisplayName string
	Token       string
	CryptoSess  *encryption.Session
	ConnectedAt time.Time
	LastSeen    time.Time

	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64
}

// AddBytesIn increments the inbound byte counter.
func (s *Session) AddBytesIn(n uint64)  { s.bytesIn.Add(n) }

// AddBytesOut increments the outbound byte counter.
func (s *Session) AddBytesOut(n uint64) { s.bytesOut.Add(n) }

// BytesIn returns total inbound bytes.
func (s *Session) BytesIn() uint64 { return s.bytesIn.Load() }

// BytesOut returns total outbound bytes.
func (s *Session) BytesOut() uint64 { return s.bytesOut.Load() }

// Touch updates LastSeen to now.
func (s *Session) Touch() { s.LastSeen = time.Now() }

// ─── Manager ──────────────────────────────────────────────────────────────────

// Manager tracks all active sessions and the virtual IP address pool.
type Manager struct {
	mu       sync.RWMutex
	sessions map[uint32]*Session        // sessionID → session
	byIP     map[string]*Session        // virtualIP → session
	byAddr   map[string]*Session        // peerAddr.String() → session
	ipPool   *IPPool

	// Auth rate limiting
	rateMu   sync.Mutex
	failures map[string]*failRecord     // sourceIP → failure record
	blocked  map[string]time.Time       // sourceIP → unblock time

	MaxAuthFails int
	BlockTimeout time.Duration
	IdleTimeout  time.Duration
}

type failRecord struct {
	count   int
	lastAt  time.Time
	window  time.Duration
}

// New creates a new Manager for the given VPN subnet.
// subnet is e.g. "10.8.0.0/24", serverIP is e.g. "10.8.0.1".
func New(subnet, serverIP string, maxAuthFails int, blockTimeout time.Duration) (*Manager, error) {
	pool, err := NewIPPool(subnet, serverIP)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		sessions:     make(map[uint32]*Session),
		byIP:         make(map[string]*Session),
		byAddr:       make(map[string]*Session),
		ipPool:       pool,
		failures:     make(map[string]*failRecord),
		blocked:      make(map[string]time.Time),
		MaxAuthFails: maxAuthFails,
		BlockTimeout: blockTimeout,
		IdleTimeout:  DefaultIdleTimeout,
	}
	go m.sweepLoop()
	return m, nil
}

// Add registers a new session.
func (m *Manager) Add(sess *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sess.SessionID] = sess
	m.byIP[sess.VirtualIP.String()] = sess
	m.byAddr[sess.PeerAddr.String()] = sess
}

// Remove evicts a session and releases its virtual IP.
func (m *Manager) Remove(sessionID uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok {
		return
	}
	delete(m.sessions, sessionID)
	delete(m.byIP, sess.VirtualIP.String())
	delete(m.byAddr, sess.PeerAddr.String())
	m.ipPool.Release(sess.VirtualIP)
}

// GetBySessionID looks up a session by its ID.
func (m *Manager) GetBySessionID(id uint32) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

// GetByAddr looks up a session by the peer's UDP address.
func (m *Manager) GetByAddr(addr *net.UDPAddr) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byAddr[addr.String()]
	return s, ok
}

// GetByVirtualIP looks up a session by its virtual IP.
func (m *Manager) GetByVirtualIP(ip net.IP) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byIP[ip.String()]
	return s, ok
}

// List returns a snapshot of all active sessions.
func (m *Manager) List() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Count returns the number of active sessions.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// AssignIP allocates the next available virtual IP and a random session ID.
// Returns (virtualIP [4]byte, sessionID uint32, error).
func (m *Manager) AssignIP() ([4]byte, uint32, error) {
	ip, err := m.ipPool.Allocate()
	if err != nil {
		return [4]byte{}, 0, err
	}
	// Generate session ID (collision extremely unlikely with 32-bit space)
	var sessID [4]byte
	for {
		b, e := encryption.RandomBytes(4)
		if e != nil {
			m.ipPool.Release(ip)
			return [4]byte{}, 0, e
		}
		copy(sessID[:], b)
		id := uint32(sessID[0])<<24 | uint32(sessID[1])<<16 | uint32(sessID[2])<<8 | uint32(sessID[3])
		m.mu.RLock()
		_, exists := m.sessions[id]
		m.mu.RUnlock()
		if !exists && id != 0 {
			var ip4 [4]byte
			copy(ip4[:], ip.To4())
			return ip4, id, nil
		}
	}
}

// ─── Rate limiting ────────────────────────────────────────────────────────────

// IsBlocked returns true if sourceIP is currently rate-limited.
func (m *Manager) IsBlocked(sourceIP string) bool {
	m.rateMu.Lock()
	defer m.rateMu.Unlock()
	if until, ok := m.blocked[sourceIP]; ok {
		if time.Now().Before(until) {
			return true
		}
		delete(m.blocked, sourceIP)
	}
	return false
}

// RecordFailure records a failed auth attempt from sourceIP.
// Returns true if the IP should now be blocked.
func (m *Manager) RecordFailure(sourceIP string) bool {
	m.rateMu.Lock()
	defer m.rateMu.Unlock()
	rec := m.failures[sourceIP]
	if rec == nil {
		rec = &failRecord{window: 60 * time.Second}
		m.failures[sourceIP] = rec
	}
	// Reset count if window expired
	if time.Since(rec.lastAt) > rec.window {
		rec.count = 0
	}
	rec.count++
	rec.lastAt = time.Now()
	if rec.count >= m.MaxAuthFails {
		m.blocked[sourceIP] = time.Now().Add(m.BlockTimeout)
		delete(m.failures, sourceIP)
		return true
	}
	return false
}

// ─── Sweep / idle timeout ─────────────────────────────────────────────────────

func (m *Manager) sweepLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.sweepIdle()
	}
}

func (m *Manager) sweepIdle() {
	now := time.Now()
	m.mu.RLock()
	var stale []uint32
	for id, s := range m.sessions {
		if now.Sub(s.LastSeen) > m.IdleTimeout {
			stale = append(stale, id)
		}
	}
	m.mu.RUnlock()
	for _, id := range stale {
		m.Remove(id)
	}
}

// ─── IP Pool ──────────────────────────────────────────────────────────────────

// IPPool manages available virtual IP addresses within a subnet.
type IPPool struct {
	mu        sync.Mutex
	available []net.IP
	allocated map[string]struct{}
}

// NewIPPool creates an IPPool for the given subnet, excluding serverIP.
func NewIPPool(subnet, serverIP string) (*IPPool, error) {
	_, network, err := net.ParseCIDR(subnet)
	if err != nil {
		return nil, fmt.Errorf("manager: invalid subnet %q: %w", subnet, err)
	}
	sip := net.ParseIP(serverIP).To4()
	if sip == nil {
		return nil, fmt.Errorf("manager: invalid server IP %q", serverIP)
	}

	pool := &IPPool{allocated: make(map[string]struct{})}
	// Enumerate every host address in the subnet except network, broadcast, and serverIP
	for ip := cloneIP(network.IP); network.Contains(ip); incrementIP(ip) {
		// Skip network address (all zeros in host part)
		if ip.Equal(network.IP) {
			continue
		}
		// Skip broadcast (all ones in host part)
		broadcast := broadcastIP(network)
		if ip.Equal(broadcast) {
			continue
		}
		// Skip server IP
		if ip.Equal(sip) {
			continue
		}
		c := cloneIP(ip)
		pool.available = append(pool.available, c)
	}
	return pool, nil
}

// Allocate returns the next available IP.
func (p *IPPool) Allocate() (net.IP, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ip := range p.available {
		if _, used := p.allocated[ip.String()]; !used {
			p.allocated[ip.String()] = struct{}{}
			return ip, nil
		}
	}
	return nil, ErrPoolExhausted
}

// Release returns ip to the pool.
func (p *IPPool) Release(ip net.IP) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.allocated, ip.String())
}

// Available returns the number of unallocated addresses.
func (p *IPPool) Available() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.available) - len(p.allocated)
}

// ─── IP helpers ───────────────────────────────────────────────────────────────

func cloneIP(ip net.IP) net.IP {
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}

func broadcastIP(n *net.IPNet) net.IP {
	b := cloneIP(n.IP)
	for i := range b {
		b[i] |= ^n.Mask[i]
	}
	return b
}
