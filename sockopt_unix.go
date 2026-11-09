//go:build darwin || linux

package main

import (
	"net"
	"syscall"
)

func ttlOpt(v6 bool) (level, opt int) {
	if v6 {
		return syscall.IPPROTO_IPV6, syscall.IPV6_UNICAST_HOPS
	}
	return syscall.IPPROTO_IP, syscall.IP_TTL
}

func getTTL(c *net.TCPConn, v6 bool) (int, error) {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var v int
	var serr error
	if err := rc.Control(func(fd uintptr) {
		level, opt := ttlOpt(v6)
		v, serr = syscall.GetsockoptInt(int(fd), level, opt)
	}); err != nil {
		return 0, err
	}
	return v, serr
}

func setTTL(c *net.TCPConn, v6 bool, ttl int) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) {
		level, opt := ttlOpt(v6)
		serr = syscall.SetsockoptInt(int(fd), level, opt, ttl)
	}); err != nil {
		return err
	}
	return serr
}
