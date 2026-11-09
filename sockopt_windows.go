package main

import (
	"math/bits"
	"net"
	"strings"
	"syscall"
	"unsafe"
)

// ws2ipdef.h sabitleri
const (
	ipprotoIP       = 0
	ipprotoIPv6     = 41
	ipTTL           = 4
	ipv6UnicastHops = 4
	ipUnicastIf     = 31
	ipv6UnicastIf   = 31
)

func ttlOpt(v6 bool) (level, opt int32) {
	if v6 {
		return ipprotoIPv6, ipv6UnicastHops
	}
	return ipprotoIP, ipTTL
}

func getTTL(c *net.TCPConn, v6 bool) (int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var v int32
	var serr error
	if err := rc.Control(func(fd uintptr) {
		level, opt := ttlOpt(v6)
		l := int32(unsafe.Sizeof(v))
		serr = syscall.Getsockopt(syscall.Handle(fd), level, opt, (*byte)(unsafe.Pointer(&v)), &l)
	}); err != nil {
		return 0, err
	}
	return int(v), serr
}

func setTTL(c *net.TCPConn, v6 bool, ttl int) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) {
		level, opt := ttlOpt(v6)
		serr = syscall.SetsockoptInt(syscall.Handle(fd), int(level), int(opt), ttl)
	}); err != nil {
		return err
	}
	return serr
}

// bindControl, giden soketleri verilen arayüzden çıkmaya zorlar (IP_UNICAST_IF).
// iface, Windows'taki bağdaştırıcı adıdır (ör. "Ethernet", "Wi-Fi").
func bindControl(iface string) (func(network, address string, c syscall.RawConn) error, error) {
	if iface == "" {
		return nil, nil
	}
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	idx := uint32(ifi.Index)
	return func(network, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			h := syscall.Handle(fd)
			if strings.HasSuffix(network, "6") {
				serr = syscall.SetsockoptInt(h, ipprotoIPv6, ipv6UnicastIf, int(idx))
			} else {
				// IPv4 için indeks ağ bayt sırasında verilmelidir.
				serr = syscall.SetsockoptInt(h, ipprotoIP, ipUnicastIf, int(int32(bits.ReverseBytes32(idx))))
			}
		}); err != nil {
			return err
		}
		return serr
	}, nil
}
