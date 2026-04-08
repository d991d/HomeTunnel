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
package transport

import (
	"context"
	"errors"
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
	BytesIn   atomic.Uint64
	BytesOut  atomic.Uint64
	PacketsIn atomic.Uint64
	PacketsOut atomic.Uint64
	Drops     atomic.Uint64
}

// Conn is a HomeTunnel UDP transport connection.
type Conn struct {
	conn    *net.UDPConn
	recv    chan *Packet // inbound packets from the network
	send    chan *Packet // outbound packets to the network
	stats   Stats
	once    sync.Once
	cancel  context.CancelFunc
	ctx     context.Context
	wg      sync.WaitGroup
}

// Listen creates a server-side UDP transport bound to addr (e.g. "0.0.0.0:48321").
func Listen(addr string) (*Conn, error) {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", ua)
	if err != nil {
		return nil, err
	}
	return newConn(conn), nil
}

// Dial creates a client-side UDP transport targeting serverAddr.
// The local port is chosen by the OS.
func Dial(serverAddr string) (*Conn, error) {
	ua, err := net.ResolveUDPAddr("udp4", serverAddr)
	if err != nil {
		return nil, err
	}
	// Use "connected" UDP so the kernel filters by remote address.
	conn, err := net.DialUDP("udp4", nil, ua)
	if err != nil {
		return nil, err
	}
	return newConn(conn), nil
}

// newConn wraps a *net.UDPConn and starts its read/write goroutines.
func newConn(uc *net.UDPConn) *Conn {
	// Tune socket buffers
	_ = uc.SetReadBuffer(DefaultBufferSize)
	_ = uc.SetWriteBuffer(DefaultBufferSize)

	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		conn:   uc,
		recv:   make(chan *Packet, MaxQueueDepth),
		send:   make(chan *Packet, MaxQueueDepth),
		ctx:    ctx,
		cancel: cancel,
	}
	c.wg.Add(2)
	go c.readLoop()
	go c.writeLoop()
	return c
}

// readLoop continuously reads UDP datagrams and pushes them onto recv.
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
			// socket closed — exit loop
			return
		}
		pkt := &Packet{
			Data: make([]byte, n),
			Addr: addr,
		}
		copy(pkt.Data, buf[:n])
		c.stats.BytesIn.Add(uint64(n))
		c.stats.PacketsIn.Add(1)

		select {
		case c.recv <- pkt:
		default:
			c.stats.Drops.Add(1)
		}
	}
}

// writeLoop drains the send channel and writes datagrams to the network.
func (c *Conn) writeLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			return
		case pkt := <-c.send:
			var err error
			if pkt.Addr != nil {
				_, err = c.conn.WriteToUDP(pkt.Data, pkt.Addr)
			} else {
				// Connected UDP (client side)
				_, err = c.conn.Write(pkt.Data)
			}
			if err == nil {
				c.stats.BytesOut.Add(uint64(len(pkt.Data)))
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
