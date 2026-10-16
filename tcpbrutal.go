package tcpbrutal

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	defaultRate = uint64(25_000_000)
	defaultGain = uint32(20)
	minRate     = uint64(62_500)
	maxRate     = uint64(125_000_000_000)
)

// Module configures tcp-brutal for accepted TCP connections.
type Module struct {
	// Enabled activates tcp-brutal after the startup availability probe succeeds.
	Enabled bool `json:"enabled,omitempty"`
	// RateBytesPerS is the legacy rate field, measured in bytes per second.
	RateBytesPerS uint64 `json:"rate_bps,omitempty"`
	// CwndGain is the congestion-window gain in tenths; its default is 20.
	CwndGain uint32 `json:"cwnd_gain,omitempty"`
	// RateBytesPerSecond is the preferred per-connection rate, in bytes per second.
	RateBytesPerSecond uint64 `json:"rate_bytes_per_second,omitempty"`
	// GroupID selects a tcp-brutal v2 shared group; zero disables grouping.
	GroupID uint64 `json:"group_id,omitempty"`
	// FailureMode is fallback (default) or strict when probing or configuring sockets fails.
	FailureMode string `json:"failure_mode,omitempty"`
	logger      *zap.Logger
	probe       func(rate uint64, cwnd uint32, group uint64) error
	probeFailed bool
}

func init() { caddy.RegisterModule(&Module{}) }

func (Module) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "caddy.listeners.tcp_brutal", New: func() caddy.Module { return &Module{} }}
}

var (
	_ caddy.ListenerWrapper = (*Module)(nil)
	_ caddy.Provisioner     = (*Module)(nil)
	_ caddy.Validator       = (*Module)(nil)
)

func (m *Module) normalize() error {
	if m.RateBytesPerSecond != 0 && m.RateBytesPerS != 0 && m.RateBytesPerSecond != m.RateBytesPerS {
		return fmt.Errorf("rate_bytes_per_second and rate_bps differ")
	}
	rate := m.RateBytesPerSecond
	if rate == 0 {
		rate = m.RateBytesPerS
	}
	if rate == 0 {
		rate = defaultRate
	}
	if rate < minRate || rate > maxRate {
		return fmt.Errorf("rate must be between %d and %d bytes per second", minRate, maxRate)
	}
	if m.CwndGain == 0 {
		m.CwndGain = defaultGain
	}
	if m.CwndGain < 5 || m.CwndGain > 80 {
		return fmt.Errorf("cwnd_gain must be between 5 and 80")
	}
	if m.FailureMode == "" {
		m.FailureMode = "fallback"
	}
	if m.FailureMode != "fallback" && m.FailureMode != "strict" {
		return fmt.Errorf("failure_mode must be fallback or strict")
	}
	m.RateBytesPerSecond = rate
	m.RateBytesPerS = rate
	return nil
}

func (m *Module) Validate() error { return m.normalize() }

func (m *Module) Provision(ctx caddy.Context) error {
	if err := m.normalize(); err != nil {
		return err
	}
	m.probeFailed = false
	m.logger = sampledLogger(ctx.Logger())
	if !m.Enabled {
		return nil
	}
	if m.logger != nil {
		m.logger.Info("tcp_brutal configuration", zap.Uint64("rate_bytes_per_second", m.RateBytesPerSecond), zap.Uint32("cwnd_gain", m.CwndGain), zap.Uint64("group_id", m.GroupID), zap.String("failure_mode", m.FailureMode))
	}
	probe := m.probe
	if probe == nil {
		probe = platformProbe
	}
	if err := probe(m.RateBytesPerSecond, m.CwndGain, m.GroupID); err != nil {
		if m.FailureMode == "strict" {
			return fmt.Errorf("tcp_brutal availability probe failed: %w", err)
		}
		m.probeFailed = true
		if m.logger != nil {
			m.logger.Warn("tcp_brutal unavailable; bypassing this listener wrapper until configuration reload", zap.Error(err))
		}
		return nil
	}
	return nil
}

func sampledLogger(logger *zap.Logger) *zap.Logger {
	if logger == nil {
		return nil
	}
	return logger.WithOptions(zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewSamplerWithOptions(core, time.Second, 1, 0)
	}))
}

func (m *Module) WrapListener(next net.Listener) net.Listener {
	if m == nil || !m.Enabled {
		return next
	}
	if m.probeFailed {
		return next
	}
	return &brutalListener{Listener: next, payload: encodeParams(m.RateBytesPerSecond, m.CwndGain, m.GroupID), ops: platformSocketOptions{}, failureMode: m.FailureMode, logger: m.logger}
}

type brutalListener struct {
	net.Listener
	payload     []byte
	ops         socketOptions
	failureMode string
	logger      *zap.Logger
}

func (bl *brutalListener) Accept() (net.Conn, error) {
	for {
		conn, err := bl.Listener.Accept()
		if err != nil {
			return nil, err
		}
		safe, setupErr := configureConn(conn, bl.payload, bl.ops)
		if setupErr == nil {
			return conn, nil
		}
		if bl.logger != nil {
			bl.logger.Warn("tcp_brutal setup failed", zap.Error(setupErr), zap.String("failure_mode", bl.failureMode))
		}
		if bl.failureMode == "fallback" && safe {
			return conn, nil
		}
		_ = conn.Close()
	}
}

type socketOptions interface {
	congestion(fd int) (string, error)
	setCongestion(fd int, algorithm string) error
	setParams(fd int, payload []byte) error
	version(fd int) (uint32, error)
}

func configureSocket(fd int, payload []byte, ops socketOptions) (fallbackSafe bool, err error) {
	previous, err := ops.congestion(fd)
	if err != nil {
		return true, fmt.Errorf("read current TCP congestion algorithm: %w", err)
	}
	changed := previous != "brutal"
	if changed {
		if err = ops.setCongestion(fd, "brutal"); err != nil {
			return true, fmt.Errorf("switch TCP congestion algorithm to brutal: %w", err)
		}
	}
	if err = ops.setParams(fd, payload); err != nil {
		setErr := fmt.Errorf("set TCP_BRUTAL_PARAMS: %w", err)
		if !changed {
			return true, setErr
		}
		if rollbackErr := ops.setCongestion(fd, previous); rollbackErr != nil {
			return false, errors.Join(setErr, fmt.Errorf("restore TCP congestion algorithm %q: %w", previous, rollbackErr))
		}
		return true, setErr
	}
	return true, nil
}

func configureConn(conn net.Conn, payload []byte, ops socketOptions) (fallbackSafe bool, err error) {
	underlying, err := unwrapConn(conn)
	if err != nil {
		return true, err
	}
	type syscallConn interface {
		SyscallConn() (syscall.RawConn, error)
	}
	sc, ok := underlying.(syscallConn)
	if !ok {
		return true, fmt.Errorf("connection %T does not support SyscallConn", underlying)
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return true, fmt.Errorf("get raw connection: %w", err)
	}
	var safe = true
	var configErr error
	controlErr := raw.Control(func(fd uintptr) { safe, configErr = configureSocket(int(fd), payload, ops) })
	if controlErr != nil {
		return safe, fmt.Errorf("raw connection control: %w", errors.Join(configErr, controlErr))
	}
	if configErr != nil {
		return safe, configErr
	}
	return true, nil
}

func unwrapConn(conn net.Conn) (net.Conn, error) {
	current := conn
	for depth := 0; depth < 16; depth++ {
		if current == nil {
			return nil, fmt.Errorf("connection unwrap returned nil")
		}
		if _, ok := current.(interface {
			SyscallConn() (syscall.RawConn, error)
		}); ok {
			return current, nil
		}
		if n, ok := current.(interface{ NetConn() net.Conn }); ok {
			current = n.NetConn()
			continue
		}
		if n, ok := current.(interface{ Unwrap() net.Conn }); ok {
			current = n.Unwrap()
			continue
		}
		return current, nil
	}
	return nil, fmt.Errorf("connection unwrap exceeded depth limit (possible cycle)")
}
