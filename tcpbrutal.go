// tcp_brutal_listener.go
package tcpbrutal

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"

	"github.com/caddyserver/caddy/v2"
	"golang.org/x/sys/unix"
)

// Module implements a Caddy listener wrapper that enables tcp-brutal on accepted TCP connections.
type Module struct {
	Enabled       bool   `json:"enabled,omitempty"`
	RateBytesPerS uint64 `json:"rate_bps,omitempty"`  // bytes per second, default 25_000_000 (200 Mbps)
	CwndGain      uint32 `json:"cwnd_gain,omitempty"` // cwnd gain in tenths (e.g. 20 => 2.0)
}

func init() {
	// register pointer so pointer-receiver methods are available at runtime
	caddy.RegisterModule(&Module{})
}

func (Module) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "caddy.listeners.tcp_brutal",
		New: func() caddy.Module { return &Module{} },
	}
}

// Ensure at compile-time that *Module implements caddy.ListenerWrapper
var _ caddy.ListenerWrapper = (*Module)(nil)

// WrapListener implements caddy.ListenerWrapper
// Note: matches Caddy v2.10.x signature: WrapListener(net.Listener) net.Listener
func (m *Module) WrapListener(next net.Listener) net.Listener {
	if m == nil || !m.Enabled {
		return next
	}
	// defaults
	if m.RateBytesPerS == 0 {
		m.RateBytesPerS = 25000000 // 200 Mbps = 200_000_000 bits/s -> 25_000_000 bytes/s
	}
	if m.CwndGain == 0 {
		m.CwndGain = 20 // 2.0 by default
	}
	return &brutalListener{
		Listener: next,
		rate:     m.RateBytesPerS,
		cwnd:     m.CwndGain,
	}
}

type brutalListener struct {
	net.Listener
	rate uint64
	cwnd uint32
}

const TCP_BRUTAL_PARAMS = 23301 // from tcp-brutal

// Accept overrides Accept to set tcp-brutal on the accepted socket.
func (bl *brutalListener) Accept() (net.Conn, error) {
	conn, err := bl.Listener.Accept()
	if err != nil {
		return nil, err
	}

	// The net.Conn should support SyscallConn (TCP/TLS listeners do).
	type syscallConn interface {
		SyscallConn() (syscall.RawConn, error)
	}

	if sc, ok := conn.(syscallConn); ok {
		raw, err := sc.SyscallConn()
		if err == nil {
			controlErr := raw.Control(func(fd uintptr) {
				// 把拥塞算法设为 brutal
				if err := unix.SetsockoptString(int(fd), unix.IPPROTO_TCP, unix.TCP_CONGESTION, "brutal"); err != nil {
					fmt.Printf("tcp_brutal: Setsockopt TCP_CONGESTION failed fd=%d err=%v\n", fd, err)
				} else {
					// 读取当前拥塞算法并打印（验证设置成功）
					if alg, err := unix.GetsockoptString(int(fd), unix.IPPROTO_TCP, unix.TCP_CONGESTION); err != nil {
						fmt.Printf("tcp_brutal: Getsockopt TCP_CONGESTION failed fd=%d err=%v\n", fd, err)
					} else {
						fmt.Printf("tcp_brutal: fd=%d congestion=%q\n", fd, alg)
					}
				}

				// prepare params struct and set TCP_BRUTAL_PARAMS
				var params struct {
					Rate     uint64
					CwndGain uint32
				}
				params.Rate = bl.rate
				params.CwndGain = bl.cwnd

				_, _, errno := syscall.Syscall6(
					syscall.SYS_SETSOCKOPT,
					fd,
					uintptr(unix.IPPROTO_TCP),
					uintptr(TCP_BRUTAL_PARAMS),
					uintptr(unsafe.Pointer(&params)),
					uintptr(unsafe.Sizeof(params)),
					0,
				)
				if errno != 0 {
					fmt.Printf("tcp_brutal: setsockopt TCP_BRUTAL_PARAMS failed fd=%d err=%v\n", fd, errno)
				} else {
					fmt.Printf("tcp_brutal: fd=%d TCP_BRUTAL_PARAMS set rate=%d cwnd_gain=%d\n", fd, params.Rate, params.CwndGain)
				}
			})
			if controlErr != nil {
				fmt.Printf("tcp_brutal: raw.Control error: %v\n", controlErr)
			}
		} else {
			fmt.Printf("tcp_brutal: SyscallConn error: %v\n", err)
		}
	} else {
		// Not a socket that supports SyscallConn (rare for net.Listener from Caddy)
		fmt.Printf("tcp_brutal: accepted conn does not support SyscallConn: %T\n", conn)
	}

	return conn, nil
}

// Provision is a no-op for now; keep signature to satisfy caddy.Module lifecycle.
func (m *Module) Provision(ctx caddy.Context) error {
	// nothing to do
	return nil
}
