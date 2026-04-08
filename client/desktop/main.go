// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// HomeTunnel Desktop Client — Go networking engine.
//
// This binary is launched as a child process by the Electron UI. All
// communication with Electron uses newline-delimited JSON over stdin/stdout.
//
// Electron → Client (commands on stdin):
//
//	{"cmd":"connect","server_addr":"203.0.113.25:48321","token":"...","display_name":"Alice"}
//	{"cmd":"disconnect"}
//	{"cmd":"status"}
//
// Client → Electron (events on stdout):
//
//	{"event":"status","state":"connecting","server":"203.0.113.25:48321"}
//	{"event":"status","state":"connected","virtual_ip":"10.8.0.2","mtu":1380}
//	{"event":"status","state":"disconnected","reason":"user request"}
//	{"event":"stats","latency_ms":42,"bytes_in":1234,"bytes_out":567}
//	{"event":"error","message":"handshake timed out"}
//	{"event":"log","level":"info","message":"..."}
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/d991d/hometunnel/core/handshake"
	"github.com/d991d/hometunnel/core/transport"
	"github.com/d991d/hometunnel/core/tunnel"
	"github.com/d991d/hometunnel/shared/protocol"
)

// ─── IPC message types ────────────────────────────────────────────────────────

type Command struct {
	Cmd         string `json:"cmd"`
	ServerAddr  string `json:"server_addr,omitempty"`
	Token       string `json:"token,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type Event struct {
	Event     string `json:"event"`
	State     string `json:"state,omitempty"`
	Server    string `json:"server,omitempty"`
	VirtualIP string `json:"virtual_ip,omitempty"`
	MTU       int    `json:"mtu,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Message   string `json:"message,omitempty"`
	Level     string `json:"level,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	BytesIn   uint64 `json:"bytes_in,omitempty"`
	BytesOut  uint64 `json:"bytes_out,omitempty"`
}

// ─── Client state ─────────────────────────────────────────────────────────────

type Client struct {
	mu         sync.Mutex
	state      string // "idle" | "connecting" | "connected" | "reconnecting"
	conn       *transport.Conn
	tun        tunnel.Tunnel
	ctx        context.Context
	cancel     context.CancelFunc
	serverAddr string
	token      string
	virtualIP  net.IP
	mtu        int
	connectedAt time.Time

	bytesIn  uint64
	bytesOut uint64

	autoReconnect bool
	reconnectDelay time.Duration
}

func newClient() *Client {
	return &Client{
		state:          "idle",
		autoReconnect:  true,
		reconnectDelay: 5 * time.Second,
	}
}

// ─── Stdout event emitter ─────────────────────────────────────────────────────

var emitMu sync.Mutex

func emit(e Event) {
	emitMu.Lock()
	defer emitMu.Unlock()
	if err := json.NewEncoder(os.Stdout).Encode(e); err != nil {
		log.Printf("emit error: %v", err)
	}
}

func emitLog(level, msg string) {
	emit(Event{Event: "log", Level: level, Message: msg})
}

func emitErr(msg string) {
	emit(Event{Event: "error", Message: msg})
}

// ─── Connect / disconnect ─────────────────────────────────────────────────────

func (c *Client) connect(serverAddr, token, displayName string) {
	c.mu.Lock()
	if c.state == "connected" || c.state == "connecting" {
		c.mu.Unlock()
		emitErr("already connecting or connected")
		return
	}
	c.serverAddr = serverAddr
	c.token = token
	c.state = "connecting"
	c.mu.Unlock()

	emit(Event{Event: "status", State: "connecting", Server: serverAddr})
	emitLog("info", fmt.Sprintf("connecting to %s", serverAddr))

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.ctx = ctx
	c.cancel = cancel
	c.mu.Unlock()

	go c.connectLoop(ctx, serverAddr, token, displayName)
}

func (c *Client) connectLoop(ctx context.Context, serverAddr, token, displayName string) {
	for {
		err := c.doConnect(ctx, serverAddr, token, displayName)
		if err == nil {
			return // successfully connected (loop runs inside doConnect)
		}
		emitErr(err.Error())

		c.mu.Lock()
		auto := c.autoReconnect
		delay := c.reconnectDelay
		c.state = "reconnecting"
		c.mu.Unlock()

		if !auto {
			emit(Event{Event: "status", State: "disconnected", Reason: err.Error()})
			c.mu.Lock()
			c.state = "idle"
			c.mu.Unlock()
			return
		}

		emit(Event{Event: "status", State: "reconnecting",
			Message: fmt.Sprintf("retrying in %s", delay)})
		emitLog("warn", fmt.Sprintf("reconnecting in %s: %v", delay, err))

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (c *Client) doConnect(ctx context.Context, serverAddr, token, displayName string) error {
	// Dial UDP
	conn, err := transport.Dial(serverAddr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer func() {
		conn.Close()
		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
	}()

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	// Run handshake
	hsCtx, hsCancel := context.WithTimeout(ctx, 15*time.Second)
	defer hsCancel()

	result, err := handshake.ClientHandshake(hsCtx, conn, token)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	// Create TUN interface
	_, network, _ := net.ParseCIDR(fmt.Sprintf("%s/24", result.VirtualIP.String()))
	if network == nil {
		// Fallback: assume /24 based on assigned IP
		network = &net.IPNet{
			IP:   result.VirtualIP.Mask(net.CIDRMask(24, 32)),
			Mask: net.CIDRMask(24, 32),
		}
	}

	tunCfg := &tunnel.Config{
		Name:    "hometunnel",
		Address: result.VirtualIP,
		Network: network,
		MTU:     result.MTU,
	}
	tun, err := tunnel.Open(tunCfg)
	if err != nil {
		return fmt.Errorf("tun: %w", err)
	}
	defer func() {
		// Remove default route before closing TUN
		tunnel.RemoveRoute(tun.Name(), tunnel.DefaultRoute())
		tun.Close()
		c.mu.Lock()
		c.tun = nil
		c.mu.Unlock()
	}()

	// Inject default route through TUN
	if err := tunnel.AddRoute(tun.Name(), tunnel.DefaultRoute(), nil); err != nil {
		emitLog("warn", fmt.Sprintf("route add: %v (manual route may be needed)", err))
	}

	c.mu.Lock()
	c.tun = tun
	c.virtualIP = result.VirtualIP
	c.mtu = result.MTU
	c.state = "connected"
	c.connectedAt = time.Now()
	c.mu.Unlock()

	emit(Event{
		Event:     "status",
		State:     "connected",
		VirtualIP: result.VirtualIP.String(),
		MTU:       result.MTU,
	})
	emitLog("info", fmt.Sprintf("connected! virtual IP: %s", result.VirtualIP))

	// Run the data loops until disconnected
	runErr := c.runDataLoop(ctx, conn, tun, result)
	if runErr != nil {
		emitLog("warn", fmt.Sprintf("data loop ended: %v", runErr))
	}
	return runErr
}

// runDataLoop forwards packets between TUN and the VPN tunnel until ctx is cancelled.
func (c *Client) runDataLoop(
	ctx context.Context,
	conn *transport.Conn,
	tun tunnel.Tunnel,
	res *handshake.ClientResult,
) error {
	// TUN → UDP
	go func() {
		buf := make([]byte, 65535)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, err := tun.ReadPacket(buf)
			if err != nil || n == 0 {
				continue
			}
			ipPkt := make([]byte, n)
			copy(ipPkt, buf[:n])

			// Prepend 2-byte length
			payload := make([]byte, 2+len(ipPkt))
			payload[0] = byte(len(ipPkt) >> 8)
			payload[1] = byte(len(ipPkt))
			copy(payload[2:], ipPkt)

			hdr := protocol.NewHeader(protocol.TypeData, res.SessionID, 0)
			ct, seq := res.Session.Seal(payload, protocol.EncodeHeader(hdr))
			hdr.SeqNum = seq
			pkt := protocol.BuildPacket(hdr, ct)
			conn.Send(pkt, nil)

			c.mu.Lock()
			c.bytesOut += uint64(len(pkt))
			c.mu.Unlock()
		}
	}()

	// UDP → TUN
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case pkt, ok := <-conn.Recv():
				if !ok {
					return
				}
				hdr, err := protocol.DecodeHeader(pkt.Data)
				if err != nil {
					continue
				}
				switch hdr.Type {
				case protocol.TypeData:
					pt, err := res.Session.Open(pkt.Data[protocol.HeaderSize:],
						hdr.SeqNum, pkt.Data[:protocol.HeaderSize])
					if err != nil {
						continue
					}
					if len(pt) < 2 {
						continue
					}
					realLen := int(pt[0])<<8 | int(pt[1])
					if realLen+2 > len(pt) {
						continue
					}
					tun.WritePacket(pt[2 : 2+realLen])
					c.mu.Lock()
					c.bytesIn += uint64(len(pkt.Data))
					c.mu.Unlock()

				case protocol.TypeDisconnect:
					return
				}
			}
		}
	}()

	// Stats ticker
	statsTicker := time.NewTicker(3 * time.Second)
	defer statsTicker.Stop()

	// Keepalive ticker
	keepaliveTicker := time.NewTicker(25 * time.Second)
	defer keepaliveTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Send graceful disconnect
			dHdr := protocol.NewHeader(protocol.TypeDisconnect, res.SessionID, 0)
			conn.Send(protocol.BuildPacket(dHdr, nil), nil)
			c.mu.Lock()
			c.state = "idle"
			c.mu.Unlock()
			emit(Event{Event: "status", State: "disconnected", Reason: "user request"})
			return nil

		case <-keepaliveTicker.C:
			kHdr := protocol.NewHeader(protocol.TypeKeepalive, res.SessionID, 0)
			conn.Send(protocol.BuildPacket(kHdr, nil), nil)

		case <-statsTicker.C:
			c.mu.Lock()
			latency := measureLatency(conn, res.SessionID)
			emit(Event{
				Event:     "stats",
				LatencyMs: latency,
				BytesIn:   c.bytesIn,
				BytesOut:  c.bytesOut,
			})
			c.mu.Unlock()
		}
	}
}

func (c *Client) disconnect() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *Client) status() {
	c.mu.Lock()
	defer c.mu.Unlock()
	emit(Event{
		Event:     "status",
		State:     c.state,
		VirtualIP: func() string {
			if c.virtualIP != nil { return c.virtualIP.String() }
			return ""
		}(),
		MTU: c.mtu,
	})
}

// measureLatency sends a keepalive and times the response (rough estimate).
func measureLatency(conn *transport.Conn, sessionID uint32) int64 {
	start := time.Now()
	kHdr := protocol.NewHeader(protocol.TypeKeepalive, sessionID, 0)
	conn.Send(protocol.BuildPacket(kHdr, nil), nil)

	// Simple: wait up to 2 s for any packet back
	timeout := time.After(2 * time.Second)
	for {
		select {
		case <-timeout:
			return -1
		case pkt, ok := <-conn.Recv():
			if !ok {
				return -1
			}
			hdr, err := protocol.DecodeHeader(pkt.Data)
			if err != nil {
				continue
			}
			if hdr.Type == protocol.TypeKeepalive {
				return time.Since(start).Milliseconds()
			}
		}
	}
}

// ─── Main / IPC loop ──────────────────────────────────────────────────────────

func main() {
	// Emit build identity as first event so Electron can display version info
	emit(Event{
		Event:   "log",
		Level:   "info",
		Message: fmt.Sprintf("HomeTunnel Client | Author: d991d | Version: 1.0.0"),
	})

	client := newClient()

	// Handle OS signals for graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		client.disconnect()
		os.Exit(0)
	}()

	// Read commands from stdin (newline-delimited JSON)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var cmd Command
		if err := json.Unmarshal([]byte(line), &cmd); err != nil {
			emitErr(fmt.Sprintf("invalid command JSON: %v", err))
			continue
		}
		switch cmd.Cmd {
		case "connect":
			if cmd.ServerAddr == "" || cmd.Token == "" {
				emitErr("connect: server_addr and token are required")
				continue
			}
			go client.connect(cmd.ServerAddr, cmd.Token, cmd.DisplayName)

		case "disconnect":
			go client.disconnect()

		case "status":
			client.status()

		default:
			emitErr(fmt.Sprintf("unknown command: %s", cmd.Cmd))
		}
	}
}
