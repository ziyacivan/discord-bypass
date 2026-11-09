package main

import (
	"net"
	"strings"
	"syscall"
)

const ipv6BoundIf = 125 // IPV6_BOUND_IF, syscall paketinde tanımlı değil

// bindControl, giden soketleri verilen arayüze bağlar (IP_BOUND_IF). Böylece
// başka bir VPN/TUN yazılımının route'ları atlanır.
func bindControl(iface string) (func(network, address string, c syscall.RawConn) error, error) {
	if iface == "" {
		return nil, nil
	}
	ifi, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	idx := ifi.Index
	return func(network, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			if strings.HasSuffix(network, "6") {
				serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, ipv6BoundIf, idx)
			} else {
				serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_BOUND_IF, idx)
			}
		}); err != nil {
			return err
		}
		return serr
	}, nil
}
