//go:build !windows

package main

import (
	"net"
	"syscall"
)

func setSockBroadcast(conn *net.UDPConn) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var sockErr error
	rawConn.Control(func(fd uintptr) { //nolint:errcheck
		sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
	})
	return sockErr
}
