// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

//go:build linux

package tunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	tunDevice  = "/dev/net/tun"
	ifnamsiz   = 16
	ioctlTUNSETIFF = 0x400454ca
	iffTun     = 0x0001
	iffNoPi    = 0x1000
)

type linuxTun struct {
	file *os.File
	name string
}

type ifreqFlags struct {
	Name  [ifnamsiz]byte
	Flags uint16
	_     [22]byte
}

func openTUN(cfg *Config) (Tunnel, error) {
	fd, err := os.OpenFile(tunDevice, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("tunnel: open %s: %w", tunDevice, err)
	}

	var ifr ifreqFlags
	ifr.Flags = iffTun | iffNoPi
	if cfg.Name != "" {
		copy(ifr.Name[:], cfg.Name)
	}

	_, _, errno := unix.Syscall(unix.SYS_IOCTL,
		fd.Fd(), ioctlTUNSETIFF, uintptr(unsafe.Pointer(&ifr)))
	if errno != 0 {
		fd.Close()
		return nil, fmt.Errorf("tunnel: TUNSETIFF: %w", errno)
	}

	name := nullTerminatedString(ifr.Name[:])

	// Bring up the interface and assign IP
	if err := exec.Command("ip", "link", "set", name, "up").Run(); err != nil {
		fd.Close()
		return nil, fmt.Errorf("tunnel: ip link set up: %w", err)
	}
	mask, _ := cfg.Network.Mask.Size()
	cidr := fmt.Sprintf("%s/%d", cfg.Address.String(), mask)
	if err := exec.Command("ip", "addr", "add", cidr, "dev", name).Run(); err != nil {
		fd.Close()
		return nil, fmt.Errorf("tunnel: ip addr add: %w", err)
	}
	if err := exec.Command("ip", "link", "set", "dev", name, "mtu",
		fmt.Sprintf("%d", cfg.MTU)).Run(); err != nil {
		fd.Close()
		return nil, fmt.Errorf("tunnel: ip link set mtu: %w", err)
	}

	return &linuxTun{file: fd, name: name}, nil
}

func (t *linuxTun) Name() string { return t.name }

func (t *linuxTun) ReadPacket(buf []byte) (int, error) {
	return t.file.Read(buf)
}

func (t *linuxTun) WritePacket(buf []byte) (int, error) {
	return t.file.Write(buf)
}

func (t *linuxTun) Close() error {
	exec.Command("ip", "link", "set", t.name, "down").Run()
	return t.file.Close()
}

func addRoute(iface string, dst *net.IPNet, via net.IP) error {
	args := []string{"route", "add", dst.String()}
	if via != nil {
		args = append(args, "via", via.String())
	}
	args = append(args, "dev", iface)
	return exec.Command("ip", args...).Run()
}

func removeRoute(iface string, dst *net.IPNet) error {
	return exec.Command("ip", "route", "del", dst.String(), "dev", iface).Run()
}

func nullTerminatedString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
