// Copyright (c) 2026 d991d. All rights reserved.
// HomeTunnel — lightweight peer-to-peer VPN
// Author: d991d <https://github.com/d991d>

//go:build darwin

package tunnel

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"unsafe"
)

// utun socket constants (macOS)
const (
	afSystem          = 32
	sysprotoControl   = 2
	ctlIOCGINFO       = 0xc0644e03
	utunOptIfName     = 2
	utunControl       = "com.apple.net.utun_control"
)

type sockaddrCtl struct {
	scLen      uint8
	scFamily   uint8
	ssSysaddr  uint16
	scID       uint32
	scUnit     uint32
	scReserved [5]uint32
}

type ctlInfo struct {
	ctlID   uint32
	ctlName [96]byte
}

type darwinTun struct {
	file *os.File
	name string
}

func openTUN(cfg *Config) (Tunnel, error) {
	// Open a PF_SYSTEM / SYSPROTO_CONTROL socket
	fd, err := syscall.Socket(afSystem, syscall.SOCK_DGRAM, sysprotoControl)
	if err != nil {
		return nil, fmt.Errorf("tunnel: socket: %w", err)
	}

	// Resolve utun control ID
	var info ctlInfo
	copy(info.ctlName[:], utunControl)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), ctlIOCGINFO, uintptr(unsafe.Pointer(&info))); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("tunnel: ioctl CTLIOCGINFO: %w", errno)
	}

	// Connect to the utun control
	sa := sockaddrCtl{
		scLen:     uint8(unsafe.Sizeof(sockaddrCtl{})),
		scFamily:  afSystem,
		ssSysaddr: sysprotoControl,
		scID:      info.ctlID,
		scUnit:    0, // 0 = let OS choose interface number
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_CONNECT,
		uintptr(fd), uintptr(unsafe.Pointer(&sa)),
		uintptr(unsafe.Sizeof(sa))); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("tunnel: connect utun: %w", errno)
	}

	// Get the assigned interface name
	nameBuf := make([]byte, 20)
	nameLen := uint32(len(nameBuf))
	if _, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT,
		uintptr(fd), sysprotoControl, utunOptIfName,
		uintptr(unsafe.Pointer(&nameBuf[0])),
		uintptr(unsafe.Pointer(&nameLen)), 0); errno != 0 {
		syscall.Close(fd)
		return nil, fmt.Errorf("tunnel: getsockopt utun name: %w", errno)
	}
	name := string(nameBuf[:nameLen-1]) // strip null terminator

	file := os.NewFile(uintptr(fd), name)

	// Configure the interface
	mask, _ := cfg.Network.Mask.Size()
	cidr := fmt.Sprintf("%s/%d", cfg.Address.String(), mask)

	if err := exec.Command("ifconfig", name, cfg.Address.String(),
		cfg.Address.String(), "up").Run(); err != nil {
		file.Close()
		return nil, fmt.Errorf("tunnel: ifconfig up: %w", err)
	}
	if err := exec.Command("ifconfig", name, "inet", cidr).Run(); err != nil {
		// Non-fatal if already set
		_ = err
	}
	if err := exec.Command("ifconfig", name, "mtu",
		strconv.Itoa(cfg.MTU)).Run(); err != nil {
		file.Close()
		return nil, fmt.Errorf("tunnel: ifconfig mtu: %w", err)
	}

	return &darwinTun{file: file, name: name}, nil
}

func (t *darwinTun) Name() string { return t.name }

// macOS utun prepends a 4-byte protocol family header to each packet.
func (t *darwinTun) ReadPacket(buf []byte) (int, error) {
	tmp := make([]byte, len(buf)+4)
	n, err := t.file.Read(tmp)
	if err != nil || n < 4 {
		return 0, err
	}
	copy(buf, tmp[4:n])
	return n - 4, nil
}

func (t *darwinTun) WritePacket(buf []byte) (int, error) {
	// Prepend AF_INET (0x00000002) header
	hdr := []byte{0x00, 0x00, 0x00, 0x02}
	data := append(hdr, buf...)
	n, err := t.file.Write(data)
	if n > 4 {
		n -= 4
	}
	return n, err
}

func (t *darwinTun) Close() error {
	exec.Command("ifconfig", t.name, "down").Run()
	return t.file.Close()
}

func addRoute(iface string, dst *net.IPNet, via net.IP) error {
	args := []string{"-n", "add", "-net", dst.String()}
	if via != nil {
		args = append(args, via.String())
	}
	args = append(args, "-interface", iface)
	return exec.Command("route", args...).Run()
}

func removeRoute(iface string, dst *net.IPNet) error {
	return exec.Command("route", "-n", "delete", "-net", dst.String(),
		"-interface", iface).Run()
}
