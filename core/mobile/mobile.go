// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — gomobile bridge package
//
// This package exposes a gomobile-compatible API for the Go VPN core.
// Compile into hometunnel-core-android.aar via:
//
//	gomobile bind -target=android/arm64,android/amd64 \
//	    -o dist/hometunnel-core-android.aar \
//	    github.com/d991d/hometunnel/core/mobile
//
// All exported types and functions follow gomobile constraints:
// only primitive types, []byte, string, error, and exported structs
// with primitive fields are used across the language boundary.

package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/d991d/hometunnel/core/encryption"
	"github.com/d991d/hometunnel/core/handshake"
	"github.com/d991d/hometunnel/core/transport"
	"github.com/d991d/hometunnel/shared/protocol"
)

// ─── EventListener ────────────────────────────────────────────────────────────

// EventListener receives async events from the VPN engine.
// Implement this interface in Kotlin/Java and pass to Engine.SetListener.
type EventListener interface {
	// OnEvent is called for status, stats, error, and log events.
	// payload is a JSON-encoded map — see eventPayload for the shape.
	OnEvent(payload string)
}

type eventPayload struct {
	Event     string `json:"event"`
	State     string `json:"state,omitempty"`
	VirtualIP string `json:"virtual_ip,omitempty"`
	MTU       int    `json:"mtu,omitempty"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	BytesIn   int64  `json:"bytes_in,omitempty"`
	BytesOut  int64  `json:"bytes_out,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ─── Engine ───────────────────────────────────────────────────────────────────

// Engine manages a single VPN client connection.
// Create one instance, call SetListener, then Connect.
type Engine struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	conn     *transport.Conn
	result   *handshake.ClientResult
	listener EventListener

	serverAddr  string
	token       string
	displayName string

	statsBytesIn  int64
	statsBytesOut int64

	// inboundPackets carries decrypted IP packets to the Android VpnService.
	inbound chan []byte
}

// NewEngine creates a new Engine instance.
func NewEngine() *Engine {
	return &Engine{
		inbound: make(chan []byte, 256),
	}
}

// SetListener registers the callback that receives JSON event strings.
// Must be called before Connect.
func (e *Engine) SetListener(l EventListener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listener = l
}

// Connect initiates a VPN connection. Non-blocking — events arrive via listener.
// serverAddr: "1.2.3.4:7788"
// token:      opaque invite token string
// displayName: shown to the server operator (can be empty)
func (e *Engine) Connect(serverAddr, token, displayName string) {
	e.mu.Lock()
	if e.cancel != nil {
		e.mu.Unlock()
		e.emit(eventPayload{Event: "error", Message: "already connected"})
		return
	}
	e.serverAddr = serverAddr
	e.token = token
	e.displayName = displayName
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.mu.Unlock()

	go e.connectLoop(ctx)
}

// Disconnect tears down the active connection.
func (e *Engine) Disconnect() {
	e.mu.Lock()
	cancel := e.cancel
	e.cancel = nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// IsConnected returns true when a VPN session is active.
func (e *Engine) IsConnected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.result != nil && e.cancel != nil
}

// VirtualIP returns the assigned VPN address (e.g. "10.8.0.2"), or "".
func (e *Engine) VirtualIP() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.result == nil {
		return ""
	}
	return e.result.VirtualIP.String()
}

// MTU returns the negotiated tunnel MTU, or 0 if not connected.
func (e *Engine) MTU() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.result == nil {
		return 0
	}
	return e.result.MTU
}

// ReadPacket blocks until a decrypted IP packet arrives from the server.
// The Android VpnService calls this in a goroutine and writes the result to
// its TUN file descriptor. Returns nil when the engine shuts down.
func (e *Engine) ReadPacket() []byte {
	pkt, ok := <-e.inbound
	if !ok {
		return nil
	}
	return pkt
}

// WritePacket encrypts an outbound IP packet (read from TUN by VpnService) and
// sends it over the UDP tunnel. Returns "" on success or an error string.
func (e *Engine) WritePacket(ipPkt []byte) string {
	e.mu.Lock()
	conn := e.conn
	result := e.result
	e.mu.Unlock()
	if conn == nil || result == nil {
		return "not connected"
	}

	// Prepend 2-byte length prefix (same framing as desktop client)
	payload := make([]byte, 2+len(ipPkt))
	payload[0] = byte(len(ipPkt) >> 8)
	payload[1] = byte(len(ipPkt))
	copy(payload[2:], ipPkt)

	hdr := protocol.NewHeader(protocol.TypeData, result.SessionID, 0)
	ct, seq := result.Session.Seal(payload, protocol.EncodeHeader(hdr))
	hdr.SeqNum = seq
	pkt := protocol.BuildPacket(hdr, ct)

	conn.Send(pkt, nil) // nil = use connected remote address
	e.mu.Lock()
	e.statsBytesOut += int64(len(ipPkt))
	e.mu.Unlock()
	return ""
}

// ─── internals ────────────────────────────────────────────────────────────────

func (e *Engine) connectLoop(ctx context.Context) {
	const retryDelay = 5 * time.Second
	for {
		e.emit(eventPayload{Event: "status", State: "connecting"})
		err := e.doConnect(ctx)
		if err == nil || ctx.Err() != nil {
			e.emit(eventPayload{Event: "status", State: "disconnected"})
			return
		}
		e.log("connection error: " + err.Error())
		e.emit(eventPayload{Event: "status", State: "reconnecting"})
		select {
		case <-ctx.Done():
			e.emit(eventPayload{Event: "status", State: "disconnected"})
			return
		case <-time.After(retryDelay):
		}
	}
}

func (e *Engine) doConnect(ctx context.Context) error {
	conn, err := transport.Dial(e.serverAddr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	hctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	result, err := handshake.ClientHandshake(hctx, conn, e.token)
	cancel()
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}

	e.mu.Lock()
	e.conn = conn
	e.result = result
	e.statsBytesIn = 0
	e.statsBytesOut = 0
	e.mu.Unlock()

	e.emit(eventPayload{
		Event:     "status",
		State:     "connected",
		VirtualIP: result.VirtualIP.String(),
		MTU:       result.MTU,
	})

	// Receive loop: forward decrypted DATA packets to the inbound channel
	recvDone := make(chan struct{})
	go func() {
		defer close(recvDone)
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
					pt, err := result.Session.Open(
						pkt.Data[protocol.HeaderSize:],
						hdr.SeqNum,
						pkt.Data[:protocol.HeaderSize],
					)
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
					ipPkt := make([]byte, realLen)
					copy(ipPkt, pt[2:2+realLen])
					e.mu.Lock()
					e.statsBytesIn += int64(realLen)
					e.mu.Unlock()
					select {
					case e.inbound <- ipPkt:
					default: // drop if consumer is too slow
					}
				case protocol.TypeDisconnect:
					return
				}
			}
		}
	}()

	keepalive := time.NewTicker(25 * time.Second)
	stats := time.NewTicker(3 * time.Second)
	defer keepalive.Stop()
	defer stats.Stop()

	for {
		select {
		case <-ctx.Done():
			dHdr := protocol.NewHeader(protocol.TypeDisconnect, result.SessionID, 0)
			conn.Send(protocol.BuildPacket(dHdr, nil), nil)
			<-recvDone
			e.mu.Lock()
			e.conn = nil
			e.result = nil
			e.mu.Unlock()
			return nil

		case <-recvDone:
			e.mu.Lock()
			e.conn = nil
			e.result = nil
			e.mu.Unlock()
			return fmt.Errorf("server closed connection")

		case <-keepalive.C:
			kHdr := protocol.NewHeader(protocol.TypeKeepalive, result.SessionID, 0)
			conn.Send(protocol.BuildPacket(kHdr, nil), nil)

		case <-stats.C:
			e.mu.Lock()
			bi := e.statsBytesIn
			bo := e.statsBytesOut
			e.mu.Unlock()
			e.emit(eventPayload{Event: "stats", BytesIn: bi, BytesOut: bo})
		}
	}
}

func (e *Engine) emit(p eventPayload) {
	e.mu.Lock()
	l := e.listener
	e.mu.Unlock()
	if l == nil {
		return
	}
	b, _ := json.Marshal(p)
	l.OnEvent(string(b))
}

func (e *Engine) log(msg string) {
	e.emit(eventPayload{Event: "log", Message: msg})
}

// ─── Utility ──────────────────────────────────────────────────────────────────

// InviteParams holds the result of ParseInviteLink.
type InviteParams struct {
	ServerAddr string
	Token      string
	Error      string
}

// ParseInviteLink extracts serverAddr and token from a HomeTunnel invite URL.
// Accepts "hometunnel://connect?server=...&token=..." and https:// equivalents.
func ParseInviteLink(link string) *InviteParams {
	for _, scheme := range []string{"hometunnel://", "vpn://"} {
		if len(link) > len(scheme) && link[:len(scheme)] == scheme {
			link = "https://" + link[len(scheme):]
			break
		}
	}
	params, err := parseQueryString(link)
	if err != nil {
		return &InviteParams{Error: err.Error()}
	}
	server := params["server"]
	token := params["token"]
	if server == "" || token == "" {
		return &InviteParams{Error: "missing server or token in invite link"}
	}
	return &InviteParams{ServerAddr: server, Token: token}
}

// KeyPairHex holds a hex-encoded X25519 key pair (utility, not required for VPN).
type KeyPairHex struct {
	Public  string
	Private string
	Error   string
}

// GenerateKeyPairHex returns a new X25519 key pair as hex strings.
func GenerateKeyPairHex() *KeyPairHex {
	kp, err := encryption.GenerateKeyPair()
	if err != nil {
		return &KeyPairHex{Error: err.Error()}
	}
	return &KeyPairHex{
		Public:  fmt.Sprintf("%x", kp.Public),
		Private: fmt.Sprintf("%x", kp.Private),
	}
}

// Version returns the HomeTunnel version string.
func Version() string { return "1.0.0" }

// Author returns the project author handle.
func Author() string { return "d991d" }

// ─── minimal URL query string parser ─────────────────────────────────────────

func parseQueryString(raw string) (map[string]string, error) {
	qi := -1
	for i := 0; i < len(raw); i++ {
		if raw[i] == '?' {
			qi = i
			break
		}
	}
	if qi < 0 {
		return nil, fmt.Errorf("no query string in URL")
	}
	out := map[string]string{}
	for _, pair := range splitByte(raw[qi+1:], '&') {
		kv := splitByte(pair, '=')
		if len(kv) == 2 {
			out[pctDecode(kv[0])] = pctDecode(kv[1])
		}
	}
	return out, nil
}

func splitByte(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func pctDecode(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '%' && i+2 < len(s) {
			var b byte
			fmt.Sscanf(s[i+1:i+3], "%02x", &b)
			out = append(out, b)
			i += 3
		} else if s[i] == '+' {
			out = append(out, ' ')
			i++
		} else {
			out = append(out, s[i])
			i++
		}
	}
	return string(out)
}
