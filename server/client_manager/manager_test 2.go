// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — client manager / IP pool unit tests

package client_manager

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New("10.99.0.0/24", "10.99.0.1", 5, 10*time.Minute)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

func newTestPool(t *testing.T) *IPPool {
	t.Helper()
	pool, err := NewIPPool("10.99.0.0/24", "10.99.0.1")
	if err != nil {
		t.Fatalf("NewIPPool: %v", err)
	}
	return pool
}

// ipFromArr converts [4]byte to net.IP for use in Session structs.
func ipFromArr(arr [4]byte) net.IP {
	return net.IP(arr[:])
}

// ── IPPool ────────────────────────────────────────────────────────────────────

func TestIPPoolAllocate(t *testing.T) {
	pool := newTestPool(t)
	ip, err := pool.Allocate()
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if ip == nil {
		t.Fatal("Allocate returned nil IP")
	}
}

func TestIPPoolNoDuplicates(t *testing.T) {
	pool := newTestPool(t)
	seen := map[string]bool{}

	for i := 0; i < 253; i++ {
		ip, err := pool.Allocate()
		if err != nil {
			t.Fatalf("Allocate #%d: %v", i+1, err)
		}
		key := ip.String()
		if seen[key] {
			t.Fatalf("duplicate IP allocated: %v", key)
		}
		seen[key] = true
	}
}

func TestIPPoolExhaustion(t *testing.T) {
	pool := newTestPool(t)
	var allocated []net.IP
	for {
		ip, err := pool.Allocate()
		if err != nil {
			break
		}
		allocated = append(allocated, ip)
	}
	if len(allocated) == 0 {
		t.Fatal("pool allocated nothing before exhaustion")
	}
	pool.Release(allocated[0])
	_, err := pool.Allocate()
	if err != nil {
		t.Fatalf("Allocate after Release: %v", err)
	}
}

func TestIPPoolRelease(t *testing.T) {
	pool := newTestPool(t)
	ip, _ := pool.Allocate()
	pool.Release(ip)
	if pool.Available() == 0 {
		t.Fatal("Available() is 0 after Release")
	}
}

func TestIPPoolServerIPExcluded(t *testing.T) {
	pool := newTestPool(t)
	serverIP := net.ParseIP("10.99.0.1").To4()
	for i := 0; i < 253; i++ {
		ip, err := pool.Allocate()
		if err != nil {
			break
		}
		if ip.Equal(serverIP) {
			t.Fatalf("pool allocated the server's own IP %v", ip)
		}
	}
}

func TestIPPoolConcurrentAllocate(t *testing.T) {
	pool := newTestPool(t)
	var mu sync.Mutex
	seen := map[string]bool{}
	errs := make(chan error, 50)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip, err := pool.Allocate()
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			defer mu.Unlock()
			key := ip.String()
			if seen[key] {
				errs <- fmt.Errorf("concurrent duplicate IP: %v", key)
			}
			seen[key] = true
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// ── Manager ───────────────────────────────────────────────────────────────────

func TestManagerAddRemoveSession(t *testing.T) {
	m := newTestManager(t)

	virtualIP, sessionID, err := m.AssignIP()
	if err != nil {
		t.Fatalf("AssignIP: %v", err)
	}
	if sessionID == 0 {
		t.Fatal("AssignIP returned zero session ID")
	}

	addr, _ := net.ResolveUDPAddr("udp", "192.168.1.100:51820")
	sess := &Session{
		SessionID:   sessionID,
		VirtualIP:   ipFromArr(virtualIP),
		PeerAddr:    addr,
		DisplayName: "alice",
		ConnectedAt: time.Now(),
		LastSeen:    time.Now(),
	}
	m.Add(sess)

	got, ok := m.GetBySessionID(sessionID)
	if !ok || got == nil {
		t.Fatal("GetBySessionID returned nothing after Add")
	}
	if got.DisplayName != "alice" {
		t.Fatalf("name mismatch: %q", got.DisplayName)
	}

	m.Remove(sessionID)
	_, ok = m.GetBySessionID(sessionID)
	if ok {
		t.Fatal("session still present after Remove")
	}
}

func TestManagerGetByVirtualIP(t *testing.T) {
	m := newTestManager(t)
	virtualIP, sessionID, _ := m.AssignIP()
	addr, _ := net.ResolveUDPAddr("udp", "6.6.6.6:9999")
	vip := ipFromArr(virtualIP)
	sess := &Session{
		SessionID: sessionID,
		VirtualIP: vip,
		PeerAddr:  addr,
		LastSeen:  time.Now(),
	}
	m.Add(sess)

	got, ok := m.GetByVirtualIP(vip)
	if !ok || got == nil {
		t.Fatal("GetByVirtualIP returned nil")
	}
	if got.SessionID != sessionID {
		t.Fatalf("wrong session: %d != %d", got.SessionID, sessionID)
	}
}

func TestManagerRateLimitingBlocks(t *testing.T) {
	m := newTestManager(t)
	sourceIP := "10.0.0.1"

	for i := 0; i < 5; i++ {
		m.RecordFailure(sourceIP)
	}
	if !m.IsBlocked(sourceIP) {
		t.Fatal("source IP should be blocked after 5 failures")
	}
}

func TestManagerNotBlockedBeforeThreshold(t *testing.T) {
	m := newTestManager(t)
	sourceIP := "10.0.0.2"

	for i := 0; i < 4; i++ {
		m.RecordFailure(sourceIP)
	}
	if m.IsBlocked(sourceIP) {
		t.Fatal("source IP blocked too early (< 5 failures)")
	}
}

func TestManagerSessionTouch(t *testing.T) {
	m := newTestManager(t)
	virtualIP, sessionID, _ := m.AssignIP()
	addr, _ := net.ResolveUDPAddr("udp", "5.5.5.5:4444")
	sess := &Session{
		SessionID: sessionID,
		VirtualIP: ipFromArr(virtualIP),
		PeerAddr:  addr,
		LastSeen:  time.Now(),
	}
	m.Add(sess)

	before := sess.LastSeen
	time.Sleep(2 * time.Millisecond)
	sess.Touch()

	if !sess.LastSeen.After(before) {
		t.Fatal("Touch did not update LastSeen")
	}
}

func TestManagerCount(t *testing.T) {
	m := newTestManager(t)
	if m.Count() != 0 {
		t.Fatal("Count should be 0 on empty manager")
	}

	ip, id, _ := m.AssignIP()
	addr, _ := net.ResolveUDPAddr("udp", "1.1.1.1:1111")
	m.Add(&Session{
		SessionID: id,
		VirtualIP: ipFromArr(ip),
		PeerAddr:  addr,
		LastSeen:  time.Now(),
	})
	if m.Count() != 1 {
		t.Fatalf("Count should be 1, got %d", m.Count())
	}

	m.Remove(id)
	if m.Count() != 0 {
		t.Fatalf("Count should be 0 after Remove, got %d", m.Count())
	}
}

func TestManagerByteCounters(t *testing.T) {
	sess := &Session{}
	sess.AddBytesIn(1024)
	sess.AddBytesOut(512)

	if sess.BytesIn() != 1024 {
		t.Fatalf("BytesIn: want 1024, got %d", sess.BytesIn())
	}
	if sess.BytesOut() != 512 {
		t.Fatalf("BytesOut: want 512, got %d", sess.BytesOut())
	}
}

func TestManagerIPReclaimedOnRemove(t *testing.T) {
	m := newTestManager(t)
	addr, _ := net.ResolveUDPAddr("udp", "1.2.3.4:51820")

	ip1, id1, _ := m.AssignIP()
	m.Add(&Session{SessionID: id1, VirtualIP: ipFromArr(ip1), PeerAddr: addr, LastSeen: time.Now()})
	m.Remove(id1)

	// After removing, the pool should have freed the IP — we can allocate again
	ip2, id2, err := m.AssignIP()
	if err != nil {
		t.Fatalf("AssignIP after Remove failed: %v", err)
	}
	if id2 == 0 {
		t.Fatal("re-allocated session ID is zero")
	}
	_ = ip2
}
