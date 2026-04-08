// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// HomeTunnel Server — entry point.
//
// Usage:
//
//	./server --config /path/to/server.json
//
// The server:
//  1. Loads config (or generates defaults + secret key on first run)
//  2. Starts the local HTTP API (consumed by the Electron UI)
//  3. Starts the VPN UDP listener
//  4. Runs the packet dispatch loop
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/d991d/hometunnel/core/handshake"
	"github.com/d991d/hometunnel/core/transport"
	"github.com/d991d/hometunnel/core/tunnel"
	"github.com/d991d/hometunnel/server/api"
	"github.com/d991d/hometunnel/server/client_manager"
	"github.com/d991d/hometunnel/shared/config"
	"github.com/d991d/hometunnel/shared/protocol"
)

func main() {
	cfgPath := flag.String("config", "server.json", "path to server config file")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVer {
		fmt.Printf("HomeTunnel Server\nAuthor:  %s\nVersion: %s\nBuild:   %s\n",
			config.Author, config.Version, config.BuildDate)
		os.Exit(0)
	}

	// ── Load / initialise config ──────────────────────────────────────────────
	cfg, err := config.LoadServerConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Generate a secret key on first run
	if cfg.SecretKey == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			log.Fatalf("keygen: %v", err)
		}
		cfg.SecretKey = hex.EncodeToString(raw)
		if err := config.SaveServerConfig(cfg, *cfgPath); err != nil {
			log.Printf("warn: could not save config: %v", err)
		}
		log.Printf("[server] generated new secret key and saved to %s", *cfgPath)
	}

	// ── Set up manager and API state ──────────────────────────────────────────
	mgr, err := client_manager.New(cfg.VPNSubnet, cfg.ServerVIP, cfg.MaxAuthFails, cfg.BlockTimeout)
	if err != nil {
		log.Fatalf("manager: %v", err)
	}

	state := api.NewServerState(cfg)
	state.Manager = mgr
	state.PublicIP = api.DetectPublicIP()
	if cfg.PublicIP != "" {
		state.PublicIP = cfg.PublicIP
	}

	// Parse port from ListenAddr
	_, portStr, _ := net.SplitHostPort(cfg.ListenAddr)
	fmt.Sscanf(portStr, "%d", &state.Port)

	// ── Start API server ──────────────────────────────────────────────────────
	apiSrv := api.New(state)
	if err := apiSrv.Start(cfg.APIAddr); err != nil {
		log.Fatalf("api: %v", err)
	}

	// ── VPN start/stop functions ──────────────────────────────────────────────
	vpnCtx, vpnCancel := context.WithCancel(context.Background())
	var udpConn *transport.Conn
	var tun tunnel.Tunnel

	startVPN := func() error {
		log.Printf("[server] starting VPN on %s", cfg.ListenAddr)

		// Open TUN
		_, network, _ := net.ParseCIDR(cfg.VPNSubnet)
		tunCfg := &tunnel.Config{
			Name:    "hometunnel",
			Address: net.ParseIP(cfg.ServerVIP),
			Network: network,
			MTU:     cfg.MTU,
		}
		var tunErr error
		tun, tunErr = tunnel.Open(tunCfg)
		if tunErr != nil {
			return fmt.Errorf("tun: %w", tunErr)
		}
		log.Printf("[server] TUN interface %s up at %s", tun.Name(), cfg.ServerVIP)

		// Enable IP forwarding and NAT
		if err := setupNAT(tun.Name(), cfg); err != nil {
			log.Printf("[server] warn: NAT setup: %v", err)
		}

		// Open UDP listener
		udpConn, err = transport.Listen(cfg.ListenAddr)
		if err != nil {
			tun.Close()
			return fmt.Errorf("transport: %w", err)
		}
		log.Printf("[server] UDP listener on %s", cfg.ListenAddr)

		// Start packet dispatch
		go dispatchLoop(vpnCtx, udpConn, tun, mgr, state, cfg)

		state.Lock()
		state.Running = true
		state.StartedAt = time.Now()
		state.Unlock()

		log.Printf("[server] ✓ VPN running — public address %s:%d",
			state.PublicIP, state.Port)
		return nil
	}

	stopVPN := func() error {
		vpnCancel()
		if udpConn != nil {
			udpConn.Close()
		}
		if tun != nil {
			teardownNAT(tun.Name(), cfg)
			tun.Close()
		}
		state.Lock()
		state.Running = false
		state.Unlock()
		log.Printf("[server] VPN stopped")
		return nil
	}

	state.StartFn = startVPN
	state.StopFn  = stopVPN

	// Auto-start
	if err := startVPN(); err != nil {
		log.Fatalf("vpn start: %v", err)
	}

	// ── Wait for signal ───────────────────────────────────────────────────────
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("[server] shutting down…")
	_ = stopVPN()
	apiSrv.Stop()
}

// ─── Packet dispatch loop ─────────────────────────────────────────────────────

// dispatchLoop is the heart of the server. It reads UDP packets and either:
//   - Initiates a handshake (HELLO packets from new peers)
//   - Decrypts and forwards DATA packets to the TUN interface
//   - Handles KEEPALIVE / DISCONNECT
func dispatchLoop(
	ctx context.Context,
	conn *transport.Conn,
	tun tunnel.Tunnel,
	mgr *client_manager.Manager,
	state *api.ServerState,
	cfg *config.ServerConfig,
) {
	// TUN → UDP: forward outbound server-originated traffic to the right client
	go tunReadLoop(ctx, tun, conn, mgr)

	for {
		select {
		case <-ctx.Done():
			return
		case pkt, ok := <-conn.Recv():
			if !ok {
				return
			}
			handleInbound(ctx, pkt.Data, pkt.Addr, conn, tun, mgr, state, cfg)
		}
	}
}

// handleInbound processes one inbound UDP packet.
func handleInbound(
	ctx context.Context,
	data []byte, addr *net.UDPAddr,
	conn *transport.Conn,
	tun tunnel.Tunnel,
	mgr *client_manager.Manager,
	state *api.ServerState,
	cfg *config.ServerConfig,
) {
	hdr, err := protocol.DecodeHeader(data)
	if err != nil {
		return
	}

	switch hdr.Type {
	case protocol.TypeHello:
		// Rate-limit check
		if mgr.IsBlocked(addr.IP.String()) {
			log.Printf("[server] blocked auth attempt from %s", addr)
			return
		}

		go func() {
			res, err := handshake.ServerHandshake(
				ctx, conn, addr,
				data[protocol.HeaderSize:],
				state.ValidateToken,
				mgr.AssignIP,
			)
			if err != nil {
				if mgr.RecordFailure(addr.IP.String()) {
					log.Printf("[server] blocked %s after repeated auth failures", addr.IP)
				}
				log.Printf("[server] handshake failed from %s: %v", addr, err)
				state.AddLog(fmt.Sprintf("[WARN] handshake failed from %s: %v", addr, err))
				return
			}
			sess := &client_manager.Session{
				SessionID:   res.SessionID,
				VirtualIP:   net.IP([]byte{0, 0, 0, 0}), // set below
				PeerAddr:    res.PeerAddr,
				DisplayName: res.DisplayName,
				Token:       res.Token,
				CryptoSess:  res.Session,
				ConnectedAt: time.Now(),
				LastSeen:    time.Now(),
			}
			// Extract virtual IP from the accept payload that was sent
			// (AssignIP already embedded it in the handshake)
			mgr.Add(sess)
			log.Printf("[server] ✓ %s connected from %s (session %d)",
				res.DisplayName, addr, res.SessionID)
			state.AddLog(fmt.Sprintf("[INFO] %s connected from %s", res.DisplayName, addr))
		}()

	case protocol.TypeData:
		sess, ok := mgr.GetByAddr(addr)
		if !ok {
			return
		}
		sess.Touch()

		// Validate timestamp (replay guard)
		if time.Duration(abs(time.Now().UnixNano()-hdr.Timestamp)) > protocol.ReplayTimestampTolerance {
			return
		}

		plaintext, err := sess.CryptoSess.Open(
			data[protocol.HeaderSize:], hdr.SeqNum, data[:protocol.HeaderSize])
		if err != nil {
			return
		}
		sess.AddBytesIn(uint64(len(data)))

		// Strip optional obfuscation padding (first 2 bytes = real length)
		if len(plaintext) < 2 {
			return
		}
		realLen := int(plaintext[0])<<8 | int(plaintext[1])
		if realLen+2 > len(plaintext) {
			return
		}
		ipPkt := plaintext[2 : 2+realLen]

		// Write IP packet to TUN
		if _, err := tun.WritePacket(ipPkt); err != nil {
			log.Printf("[server] tun write: %v", err)
		}

	case protocol.TypeKeepalive:
		sess, ok := mgr.GetByAddr(addr)
		if !ok {
			return
		}
		sess.Touch()
		// Echo keepalive back
		kHdr := protocol.NewHeader(protocol.TypeKeepalive, sess.SessionID, 0)
		conn.Send(protocol.BuildPacket(kHdr, nil), addr)

	case protocol.TypeDisconnect:
		sess, ok := mgr.GetByAddr(addr)
		if !ok {
			return
		}
		log.Printf("[server] %s disconnected (session %d)", sess.DisplayName, sess.SessionID)
		state.AddLog(fmt.Sprintf("[INFO] %s disconnected", sess.DisplayName))
		mgr.Remove(sess.SessionID)
	}
}

// tunReadLoop reads IP packets from the TUN interface and routes them to clients.
func tunReadLoop(ctx context.Context, tun tunnel.Tunnel, conn *transport.Conn, mgr *client_manager.Manager) {
	buf := make([]byte, 65535)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		n, err := tun.ReadPacket(buf)
		if err != nil || n < 20 {
			continue
		}
		ipPkt := make([]byte, n)
		copy(ipPkt, buf[:n])

		// Extract destination IP from IPv4 header (bytes 16-20)
		if buf[0]>>4 != 4 {
			continue // skip non-IPv4
		}
		dstIP := net.IP(ipPkt[16:20])

		sess, ok := mgr.GetByVirtualIP(dstIP)
		if !ok {
			continue
		}

		// Prepend length, then encrypt
		payload := make([]byte, 2+len(ipPkt))
		payload[0] = byte(len(ipPkt) >> 8)
		payload[1] = byte(len(ipPkt))
		copy(payload[2:], ipPkt)

		hdr := protocol.NewHeader(protocol.TypeData, sess.SessionID, 0)
		ct, seq := sess.CryptoSess.Seal(payload, protocol.EncodeHeader(hdr))
		hdr.SeqNum = seq
		outPkt := protocol.BuildPacket(hdr, ct)

		conn.Send(outPkt, sess.PeerAddr)
		sess.AddBytesOut(uint64(len(outPkt)))
	}
}

// ─── NAT / IP forwarding setup ────────────────────────────────────────────────

func setupNAT(tunName string, cfg *config.ServerConfig) error {
	switch runtime.GOOS {
	case "linux":
		exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
		exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
			"-s", cfg.VPNSubnet, "-j", "MASQUERADE").Run()
		exec.Command("iptables", "-A", "FORWARD", "-i", tunName, "-j", "ACCEPT").Run()
		exec.Command("iptables", "-A", "FORWARD", "-o", tunName, "-j", "ACCEPT").Run()
	case "darwin":
		exec.Command("sysctl", "-w", "net.inet.ip.forwarding=1").Run()
		// pf NAT rule — basic; production should use a proper pf.conf
		pfRule := fmt.Sprintf("nat on en0 from %s to any -> (en0)\n", cfg.VPNSubnet)
		os.WriteFile("/tmp/hometunnel_pf.conf", []byte(pfRule), 0o600)
		exec.Command("pfctl", "-f", "/tmp/hometunnel_pf.conf", "-e").Run()
	case "windows":
		// Enable IP routing via registry / netsh
		exec.Command("netsh", "routing", "ip", "nat", "install").Run()
	}
	return nil
}

func teardownNAT(tunName string, cfg *config.ServerConfig) {
	switch runtime.GOOS {
	case "linux":
		exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
			"-s", cfg.VPNSubnet, "-j", "MASQUERADE").Run()
		exec.Command("iptables", "-D", "FORWARD", "-i", tunName, "-j", "ACCEPT").Run()
		exec.Command("iptables", "-D", "FORWARD", "-o", tunName, "-j", "ACCEPT").Run()
	case "darwin":
		exec.Command("pfctl", "-d").Run()
	}
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}
