// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package transport manages the UDP socket layer for HomeTunnel.
//
// It provides:
//   - A Conn type wrapping net.UDPConn with send/receive channels
//   - Configurable read/write buffer sizes
//   - Graceful shutdown via context cancellation
//   - Packet-level statistics (bytes in/out, drops)
//   - Optional traffic obfuscation (XOR header + random padding)
package transport

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"errors"
	mathrand "math/rand/v2"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultBufferSize is the OS socket buffer size in bytes.
const DefaultBufferSize = 4 * 1024 * 1024 // 4 MB

// DefaultReadDeadline is applied when reading to allow graceful shutdown checks.
const DefaultReadDeadline = 500 * time.Millisecond

// MaxQueueDepth is the channel depth for inbound and outbound packet queues.
const MaxQueueDepth = 2048

// Packet is the unit of data passed through the transport layer.
type Packet struct {
	Data []byte
	Addr *net.UDPAddr
}

// Stats holds transport-layer counters.
type Stats struct {
	BytesIn    atomic.Uint64
	BytesOut   atomic.Uint64
	PacketsIn  atomic.Uint64
	PacketsOut atomic.Uint64
	Drops      atomic.Uint64
}

// ObfuscationConfig controls per-connection obfuscation.
type ObfuscationConfig struct {
	Enabled         bool
	PaddingMaxBytes int  // 0–255 random bytes prepended to each packet
	HeaderXOR       bool // XOR first 16 bytes of payload with derived key
}

// obfsKey is a 32-byte key derived from a well-known constant.
// It makes HomeTunnel packets look like random noise to passive observers
// without requiring any prior key exchange.  Cryptographic security is
// provided by ChaCha20-Poly1305 — this layer targets protocol fingerprinting.
var obfsKey = func() [32]byte {
	return sha256.Sum256([]byte("hometunnel-traffic-obfuscation-v1"))
}()

// obfuscate wraps pkt in the obfuscated wire format:
//
//	[1 byte: pad_len] [pad_len random bytes] [XOR'd payload]
func obfuscate(pkt []byte, cfg ObfuscationConfig) ([]byte, error) {
	padLen := 0
	if cfg.PaddingMaxBytes > 0 {
		padLen = mathrand.IntN(cfg.PaddingMaxBytes + 1)
	}

	out := make([]byte, 1+padLen+len(pkt))
	out[0] = byte(padLen)
	if padLen > 0 {
		if _, err := cryptorand.Read(out[1 : 1+padLen]); err != nil {
			return nil, err
		}
	}

	payload := out[1+padLen:]
	copy(payload, pkt)

	if cfg.HeaderXOR {
		// XOR every byte with the obfuscation key (cycled)
		for i := range payload {
			payload[i] ^= obfsKey[i%32]
		}
	}

	return out, nil
}

// deobfuscate reverses obfuscate.
func deobfuscate(pkt []byte, cfg ObfuscationConfig) ([]byte, error) {
	if len(pkt) < 1 {
		return nil, errors.New("obfs: packet too short")
	}
	padLen := int(pkt[0])
	if len(pkt) < 1+padLen {
		return nil, errors.New("obfs: truncated padding")
	}

	payload := make([]byte, len(pkt)-1-padLen)
	copy(payload, pkt[1+padLen:])

	if cfg.HeaderXOR {
		for i := range payload {
			payload[i] ^= obfsKey[i%32]
		}
	}

	return payload, nil
}

// Conn is a HomeTunnel UDP transport connection.
type Conn struct {
	conn   *net.UDPConn
	recv   chan *Packet // inbound packets from the network
	send   chan *Packet // outbound packets to the network
	obfs   ObfuscationConfig
	stats  Stats
	once   sync.Once
	cancel context.CancelFunc
	ctx    context.Context
	wg     sync.WaitGroup
}

// ListenOptions configures a server-side listener.
type ListenOptions struct {
	Obfuscation ObfuscationConfig
}

// DialOptions configures a client-side connection.
type DialOptions struct {
	Obfuscation ObfuscationConfig
}

// Listen creates a server-side UDP transport bound to addr (e.g. "0.0.0.0:443").
func Listen(addr string, opts ...ListenOptions) (*Conn, error) {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	uc, err := net.ListenUDP("udp4", ua)
	if err != nil {
		return nil, err
	}
	var cfg ObfuscationConfig
	if len(opts) > 0 {
		cfg = opts[0].Obfuscation
	}
	return newConn(uc, cfg), nil
}

// Dial creates a client-side UDP transport targeting serverAddr.
func Dial(serverAddr string, opts ...DialOptions) (*Conn, error) {
	ua, err := net.ResolveUDPAddr("udp4", serverAddr)
	if err != nil {
		return nil, err
	}
	uc, err := net.DialUDP("udp4", nil, ua)
	if err != nil {
		return nil, err
	}
	var cfg ObfuscationConfig
	if len(opts) > 0 {
		cfg = opts[0].Obfuscation
	}
	return newConn(uc, cfg), nil
}

// newConn wraps a *net.UDPConn and starts its read/write goroutines.
func newConn(uc *net.UDPConn, obfs ObfuscationConfig) *Conn {
	_ = uc.SetReadBuffer(DefaultBufferSize)
	_ = uc.SetWriteBuffer(DefaultBufferSize)

	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		conn:   uc,
		recv:   make(chan *Packet, MaxQueueDepth),
		send:   make(chan *Packet, MaxQueueDepth),
		obfs:   obfs,
		ctx:    ctx,
		cancel: cancel,
	}
	c.wg.Add(2)
	go c.readLoop()
	go c.writeLoop()
	return c
}

// readLoop continuously reads UDP datagrams and pushes them onto recv.
// If obfuscation is enabled, each packet is deobfuscated before queuing.
func (c *Conn) readLoop() {
	defer c.wg.Done()
	buf := make([]byte, 65535)
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}
		_ = c.conn.SetReadDeadline(time.Now().Add(DefaultReadDeadline))
		n, addr, err := c.conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return
		}

		raw := make([]byte, n)
		copy(raw, buf[:n])

		if c.obfs.Enabled {
			plain, err := deobfuscate(raw, c.obfs)
			if err != nil {
				// Malformed or non-HomeTunnel packet — silently drop
				c.stats.Drops.Add(1)
				continue
			}
			raw = plain
		}

		c.stats.BytesIn.Add(uint64(n))
		c.stats.PacketsIn.Add(1)

		pkt := &Packet{Data: raw, Addr: addr}
		select {
		case c.recv <- pkt:
		default:
			c.stats.Drops.Add(1)
		}
	}
}

// writeLoop drains the send channel and writes datagrams to the network.
// If obfuscation is enabled, each packet is obfuscated before writing.
func (c *Conn) writeLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			return
		case pkt := <-c.send:
			data := pkt.Data
			if c.obfs.Enabled {
				obfuscated, err := obfuscate(data, c.obfs)
				if err == nil {
					data = obfuscated
				}
			}

			var err error
			if pkt.Addr != nil {
				_, err = c.conn.WriteToUDP(data, pkt.Addr)
			} else {
				_, err = c.conn.Write(data)
			}
			if err == nil {
				c.stats.BytesOut.Add(uint64(len(data)))
				c.stats.PacketsOut.Add(1)
			}
		}
	}
}

// Recv returns the inbound packet channel. Callers must not close this channel.
func (c *Conn) Recv() <-chan *Packet { return c.recv }

// Send queues a packet for transmission.
// addr may be nil for connected (client-side) sockets.
func (c *Conn) Send(data []byte, addr *net.UDPAddr) {
	pkt := &Packet{Data: data, Addr: addr}
	select {
	case c.send <- pkt:
	default:
		c.stats.Drops.Add(1)
	}
}

// SendPacket queues a pre-built Packet for transmission.
func (c *Conn) SendPacket(pkt *Packet) {
	select {
	case c.send <- pkt:
	default:
		c.stats.Drops.Add(1)
	}
}

// LocalAddr returns the local UDP address.
func (c *Conn) LocalAddr() *net.UDPAddr {
	return c.conn.LocalAddr().(*net.UDPAddr)
}

// Stats returns a snapshot of transport statistics.
func (c *Conn) Stats() (bytesIn, bytesOut, packetsIn, packetsOut, drops uint64) {
	return c.stats.BytesIn.Load(),
		c.stats.BytesOut.Load(),
		c.stats.PacketsIn.Load(),
		c.stats.PacketsOut.Load(),
		c.stats.Drops.Load()
}

// Close gracefully shuts down the transport.
func (c *Conn) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		err = c.conn.Close()
		c.wg.Wait()
	})
	return err
}
