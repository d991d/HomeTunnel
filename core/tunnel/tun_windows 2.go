// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

//go:build windows

// Package tunnel - Windows implementation using WinTUN.
//
// WinTUN is a free, open-source TUN driver for Windows maintained by the
// WireGuard project. The wintun.dll must ship alongside the binary.
// Download: https://www.wintun.net/
package tunnel

import (
	"fmt"
	"net"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wintunDLL           *windows.LazyDLL
	wintunCreateAdapter *windows.LazyProc
	wintunOpenAdapter   *windows.LazyProc
	wintunCloseAdapter  *windows.LazyProc
	wintunStartSession  *windows.LazyProc
	wintunEndSession    *windows.LazyProc
	wintunAllocSendPkt  *windows.LazyProc
	wintunSendPkt       *windows.LazyProc
	wintunRecvPkt       *windows.LazyProc
	wintunReleasePkt    *windows.LazyProc
)

func init() {
	wintunDLL = windows.NewLazyDLL("wintun.dll")
	wintunCreateAdapter = wintunDLL.NewProc("WintunCreateAdapter")
	wintunOpenAdapter   = wintunDLL.NewProc("WintunOpenAdapter")
	wintunCloseAdapter  = wintunDLL.NewProc("WintunCloseAdapter")
	wintunStartSession  = wintunDLL.NewProc("WintunStartSession")
	wintunEndSession    = wintunDLL.NewProc("WintunEndSession")
	wintunAllocSendPkt  = wintunDLL.NewProc("WintunAllocateSendPacket")
	wintunSendPkt       = wintunDLL.NewProc("WintunSendPacket")
	wintunRecvPkt       = wintunDLL.NewProc("WintunReceivePacket")
	wintunReleasePkt    = wintunDLL.NewProc("WintunReleaseReceivePacket")
}

// GUID for the HomeTunnel adapter type
var p2pHVPNGUID = windows.GUID{
	Data1: 0xdeadbeef,
	Data2: 0x1234,
	Data3: 0x5678,
	Data4: [8]byte{0x90, 0xab, 0xcd, 0xef, 0x12, 0x34, 0x56, 0x78},
}

type windowsTun struct {
	adapter uintptr
	session uintptr
	name    string
}

func openTUN(cfg *Config) (Tunnel, error) {
	if err := wintunDLL.Load(); err != nil {
		return nil, fmt.Errorf("tunnel: wintun.dll not found: %w\n"+
			"Download wintun.dll from https://www.wintun.net/ and place it next to the executable.", err)
	}

	nameUTF16, _ := syscall.UTF16PtrFromString("HomeTunnel")
	typeUTF16, _ := syscall.UTF16PtrFromString("HomeTunnel")

	adapter, _, err := wintunCreateAdapter.Call(
		uintptr(unsafe.Pointer(nameUTF16)),
		uintptr(unsafe.Pointer(typeUTF16)),
		uintptr(unsafe.Pointer(&p2pHVPNGUID)),
	)
	if adapter == 0 {
		return nil, fmt.Errorf("tunnel: WintunCreateAdapter: %w", err)
	}

	// Assign IP address via netsh
	mask, _ := cfg.Network.Mask.Size()
	if err := exec.Command("netsh", "interface", "ip", "set", "address",
		"HomeTunnel", "static",
		cfg.Address.String(),
		net.IP(cfg.Network.Mask).String()).Run(); err != nil {
		wintunCloseAdapter.Call(adapter)
		return nil, fmt.Errorf("tunnel: netsh set address: %w", err)
	}

	// Set MTU
	exec.Command("netsh", "interface", "ipv4", "set", "subinterface",
		"HomeTunnel", fmt.Sprintf("mtu=%d", cfg.MTU), "store=persistent").Run()

	_ = mask

	// Start WinTUN session with 0x400000 (4 MB) ring buffer
	session, _, err2 := wintunStartSession.Call(adapter, 0x400000)
	if session == 0 {
		wintunCloseAdapter.Call(adapter)
		return nil, fmt.Errorf("tunnel: WintunStartSession: %w", err2)
	}

	return &windowsTun{adapter: adapter, session: session, name: "HomeTunnel"}, nil
}

func (t *windowsTun) Name() string { return t.name }

func (t *windowsTun) ReadPacket(buf []byte) (int, error) {
	var pktSize uint32
	pkt, _, err := wintunRecvPkt.Call(t.session, uintptr(unsafe.Pointer(&pktSize)))
	if pkt == 0 {
		// ERROR_NO_MORE_ITEMS — no packet yet (non-blocking)
		if err.(syscall.Errno) == 259 {
			return 0, nil
		}
		return 0, fmt.Errorf("tunnel: WintunReceivePacket: %w", err)
	}
	defer wintunReleasePkt.Call(t.session, pkt)

	if int(pktSize) > len(buf) {
		pktSize = uint32(len(buf))
	}
	// Copy from the WinTUN ring buffer into buf
	src := unsafe.Slice((*byte)(unsafe.Pointer(pkt)), pktSize)
	copy(buf[:pktSize], src)
	return int(pktSize), nil
}

func (t *windowsTun) WritePacket(buf []byte) (int, error) {
	pktSize := uint32(len(buf))
	pkt, _, err := wintunAllocSendPkt.Call(t.session, uintptr(pktSize))
	if pkt == 0 {
		return 0, fmt.Errorf("tunnel: WintunAllocateSendPacket: %w", err)
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(pkt)), pktSize)
	copy(dst, buf)
	wintunSendPkt.Call(t.session, pkt)
	return len(buf), nil
}

func (t *windowsTun) Close() error {
	wintunEndSession.Call(t.session)
	wintunCloseAdapter.Call(t.adapter)
	return nil
}

func addRoute(iface string, dst *net.IPNet, via net.IP) error {
	mask := net.IP(dst.Mask).String()
	args := []string{"route", "add", dst.IP.String(), "mask", mask}
	if via != nil {
		args = append(args, via.String())
	}
	args = append(args, "if", iface)
	return exec.Command("route", args...).Run()
}

func removeRoute(iface string, dst *net.IPNet) error {
	mask := net.IP(dst.Mask).String()
	return exec.Command("route", "delete", dst.IP.String(), "mask", mask).Run()
}
