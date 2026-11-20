//go:build !windows

package main

import "net"

func enableONVIFMulticastLoopback(_ *net.UDPConn) error { return nil }
