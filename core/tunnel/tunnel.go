// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

// Package tunnel provides a cross-platform virtual TUN network interface.
//
// It abstracts the OS-specific mechanics of creating a TUN device and
// exposes a simple ReadPacket / WritePacket interface for the VPN engine.
//
// Platform support:
//
//	Linux   — /dev/net/tun  via ioctl
//	macOS   — utun socket   via syscall
//	Windows — WinTUN driver via wintun.dll
package tunnel

import (
	"fmt"
	"net"
)

// Tunnel is a virtual TUN network interface.
type Tunnel interface {
	// Name returns the OS interface name (e.g. "tun0", "utun3").
	Name() string

	// ReadPacket reads one IP packet from the TUN device.
	// It blocks until a packet is available.
	ReadPacket(buf []byte) (int, error)

	// WritePacket writes one IP packet to the TUN device.
	WritePacket(buf []byte) (int, error)

	// Close tears down the TUN interface.
	Close() error
}

// Config holds parameters for creating a TUN interface.
type Config struct {
	// Name is a hint for the interface name; the OS may modify it.
	Name string

	// Address is the virtual IPv4 address assigned to this interface.
	Address net.IP

	// Network is the VPN subnet (e.g. 10.8.0.0/24).
	Network *net.IPNet

	// MTU is the maximum transmission unit.
	MTU int

	// DNS servers to configure on the interface (client side).
	DNSServers []net.IP
}

// Open creates a new TUN interface using the platform-specific implementation.
// This function is implemented separately per OS in tun_linux.go,
// tun_darwin.go, and tun_windows.go.
func Open(cfg *Config) (Tunnel, error) {
	return openTUN(cfg)
}

// AddRoute adds a host or network route through the TUN interface.
// Called by the client to inject the default route (0.0.0.0/0).
func AddRoute(iface string, dst *net.IPNet, via net.IP) error {
	return addRoute(iface, dst, via)
}

// RemoveRoute removes a route added by AddRoute.
func RemoveRoute(iface string, dst *net.IPNet) error {
	return removeRoute(iface, dst)
}

// DefaultRoute returns the 0.0.0.0/0 subnet.
func DefaultRoute() *net.IPNet {
	_, net, _ := net.ParseCIDR("0.0.0.0/0")
	return net
}

// Description returns a human-readable summary of the tunnel config.
func (c *Config) Description() string {
	return fmt.Sprintf("TUN %s addr=%s net=%s mtu=%d",
		c.Name, c.Address, c.Network, c.MTU)
}
