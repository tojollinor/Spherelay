package main

import (
	"net"
	"syscall"
)

func enableONVIFMulticastLoopback(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var optionError error
	err = raw.Control(func(socket uintptr) {
		optionError = syscall.SetsockoptInt(syscall.Handle(socket), syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1)
	})
	if err != nil {
		return err
	}
	return optionError
}
