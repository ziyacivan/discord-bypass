package main

import "syscall"

func bindControl(iface string) (func(network, address string, c syscall.RawConn) error, error) {
	if iface == "" {
		return nil, nil
	}
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = syscall.BindToDevice(int(fd), iface)
		}); err != nil {
			return err
		}
		return serr
	}, nil
}
