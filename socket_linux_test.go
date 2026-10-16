//go:build linux

package tcpbrutal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type establishedProbeOptions struct {
	*fakeSocketOptions
	observedEstablished bool
	paramsErr           error
}

func (o *establishedProbeOptions) congestion(fd int) (string, error) {
	info, err := unix.GetsockoptTCPInfo(fd, unix.IPPROTO_TCP, unix.TCP_INFO)
	if err != nil {
		return "", err
	}
	// Linux tcp_reinit_congestion_control skips initialization for both
	// TCP_CLOSE and TCP_LISTEN. The probe must use TCP_ESTABLISHED (1).
	if info.State != 1 {
		return "", fmt.Errorf("probe TCP state=%d, want ESTABLISHED (1)", info.State)
	}
	o.observedEstablished = true
	return o.fakeSocketOptions.congestion(fd)
}

func (o *establishedProbeOptions) setParams(fd int, payload []byte) error {
	if err := o.fakeSocketOptions.setParams(fd, payload); err != nil {
		return err
	}
	return o.paramsErr
}

func TestPlatformProbeUsesEstablishedTCP(t *testing.T) {
	boom := errors.New("parameter failure")
	for _, tc := range []struct {
		name      string
		paramsErr error
		group     uint64
	}{
		{"success", nil, 0},
		{"failure", boom, 0},
		{"group capability", nil, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &establishedProbeOptions{fakeSocketOptions: &fakeSocketOptions{versionValue: 2 << 16}, paramsErr: tc.paramsErr}
			err := platformProbeWithOptions(1_000_000, 20, tc.group, ops)
			if !errors.Is(err, tc.paramsErr) {
				t.Fatalf("probe error=%v, want %v", err, tc.paramsErr)
			}
			if !ops.observedEstablished {
				t.Fatal("probe did not configure an established TCP socket")
			}
		})
	}
}

type versionAwareOptions struct{ *fakeSocketOptions }

func (o versionAwareOptions) version(fd int) (uint32, error) {
	o.mu.Lock()
	algorithm := o.algorithms[fd]
	o.mu.Unlock()
	if algorithm != "brutal" {
		return 0, errors.New("version option only exists after brutal initialization")
	}
	return o.fakeSocketOptions.version(fd)
}

func TestProbeVersionAndGroupIsolation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		group      uint64
		version    uint32
		versionErr error
		wantErr    bool
	}{
		{"v1 no group does not query version", 0, 0, unix.ENOPROTOOPT, false},
		{"v2 shared group", 99, 2<<16 | 1, nil, false},
		{"v1 group rejected", 99, 1 << 16, nil, true},
		{"unavailable version rejected", 99, 0, unix.ENOPROTOOPT, true},
		{"unencoded major rejected", 99, 2, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeSocketOptions{algorithms: map[int]string{42: "cubic"}, versionValue: tc.version, failures: map[string]error{"version:42": tc.versionErr}}
			err := probeSocket(42, 25_000_000, 20, tc.group, versionAwareOptions{fake})
			if (err != nil) != tc.wantErr {
				t.Fatalf("probeSocket()=%v, want error=%v", err, tc.wantErr)
			}
			if tc.versionErr != nil && tc.wantErr && !errors.Is(err, tc.versionErr) {
				t.Fatalf("lost version error: %v", err)
			}
			versionCalls := 0
			paramsCalls := 0
			for _, call := range fake.calls {
				if call.op == "version" {
					versionCalls++
				}
				if call.op == "params" {
					paramsCalls++
					if len(call.payload) != 12 {
						t.Fatalf("probe could join/update live group: %d-byte payload", len(call.payload))
					}
					if binary.NativeEndian.Uint64(call.payload[:8]) != 25_000_000 || binary.NativeEndian.Uint32(call.payload[8:]) != 20 {
						t.Fatal("probe ignored configured rate/gain")
					}
				}
			}
			if paramsCalls != 1 {
				t.Fatalf("probe params calls=%d, want 1", paramsCalls)
			}
			if (versionCalls != 0) != (tc.group != 0) {
				t.Fatalf("version calls=%d for group=%d", versionCalls, tc.group)
			}
		})
	}
}

// Runs automatically on hosts with tcp-brutal; intentionally skips in ordinary
// CI without installing modules or changing the host's kernel configuration.
func TestKernelBrutalIntegration(t *testing.T) {
	if err := platformProbe(1_000_000, 20, 0); err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOPROTOOPT) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) {
			t.Skipf("tcp-brutal unavailable: %v", err)
		}
		t.Fatalf("unexpected probe failure: %v", err)
	}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	listener.SetDeadline(time.Now().Add(3 * time.Second))
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	conn, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if safe, err := configureConn(conn, encodeParams(1_000_000, 20, 0), platformSocketOptions{}); !safe || err != nil {
		t.Fatalf("configure kernel socket=(%v,%v)", safe, err)
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var algorithm string
	var optionErr error
	if err := raw.Control(func(fd uintptr) { algorithm, optionErr = (platformSocketOptions{}).congestion(int(fd)) }); err != nil {
		t.Fatal(err)
	}
	if optionErr != nil || strings.TrimRight(algorithm, "\x00") != "brutal" {
		t.Fatalf("kernel algorithm=%q, error=%v", algorithm, optionErr)
	}
}
