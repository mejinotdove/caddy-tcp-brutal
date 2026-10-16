//go:build linux

package tcpbrutal

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

const (
	tcpBrutalVersion = 23302
)

type platformSocketOptions struct{}

func (platformSocketOptions) congestion(fd int) (string, error) {
	return unix.GetsockoptString(fd, unix.IPPROTO_TCP, unix.TCP_CONGESTION)
}

func (platformSocketOptions) setCongestion(fd int, algorithm string) error {
	return unix.SetsockoptString(fd, unix.IPPROTO_TCP, unix.TCP_CONGESTION, algorithm)
}

func (platformSocketOptions) setParams(fd int, payload []byte) error {
	return unix.SetsockoptString(fd, unix.IPPROTO_TCP, TCP_BRUTAL_PARAMS, string(payload))
}

func (platformSocketOptions) version(fd int) (uint32, error) {
	v, err := unix.GetsockoptInt(fd, unix.IPPROTO_TCP, tcpBrutalVersion)
	return uint32(v), err
}

func platformProbe(rate uint64, cwnd uint32, group uint64) error {
	return platformProbeWithOptions(rate, cwnd, group, platformSocketOptions{})
}

func platformProbeWithOptions(rate uint64, cwnd uint32, group uint64, ops socketOptions) error {
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return fmt.Errorf("open probe socket: %w", err)
	}
	defer ln.Close()
	deadline := time.Now().Add(3 * time.Second)
	if err := ln.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set probe listener deadline: %w", err)
	}
	client, err := net.DialTimeout("tcp", ln.Addr().String(), 3*time.Second)
	if err != nil {
		return fmt.Errorf("connect probe socket: %w", err)
	}
	defer client.Close()
	if err := client.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set probe client deadline: %w", err)
	}
	accepted, err := ln.AcceptTCP()
	if err != nil {
		return fmt.Errorf("accept probe connection: %w", err)
	}
	defer accepted.Close()
	if err := accepted.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set accepted probe deadline: %w", err)
	}
	var probeErr error
	raw, err := accepted.SyscallConn()
	if err != nil {
		return fmt.Errorf("get accepted probe raw connection: %w", err)
	}
	err = raw.Control(func(fd uintptr) {
		probeErr = probeSocket(int(fd), rate, cwnd, group, ops)
	})
	if err != nil {
		return fmt.Errorf("control probe socket: %w", err)
	}
	if probeErr != nil {
		return probeErr
	}
	return nil
}

func probeSocket(fd int, rate uint64, cwnd uint32, group uint64, ops socketOptions) error {
	if _, err := configureSocket(fd, encodeParams(rate, cwnd, 0), ops); err != nil {
		return fmt.Errorf("probe brutal algorithm and parameters: %w", err)
	}
	if group == 0 {
		return nil
	}
	version, err := ops.version(fd)
	if err != nil {
		return fmt.Errorf("read TCP_BRUTAL_VERSION: %w", err)
	}
	major := version >> 16
	if major < 2 {
		return fmt.Errorf("tcp-brutal group IDs require version 2 or newer (reported 0x%x)", version)
	}
	return nil
}
