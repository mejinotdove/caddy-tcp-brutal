package tcpbrutal

import (
	"errors"
	"net"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type sequenceListener struct {
	conns []net.Conn
	err   error
	calls int
}

func (l *sequenceListener) Accept() (net.Conn, error) {
	l.calls++
	if len(l.conns) == 0 {
		if l.err != nil {
			return nil, l.err
		}
		return nil, net.ErrClosed
	}
	conn := l.conns[0]
	l.conns = l.conns[1:]
	return conn, nil
}
func (*sequenceListener) Close() error { return nil }
func (*sequenceListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}

func TestAcceptFailurePolicies(t *testing.T) {
	boom := errors.New("kernel rejected params")
	for _, tc := range []struct {
		name          string
		mode          string
		rollbackFails bool
		wantFirst     bool
	}{
		{"fallback returns restored connection", "fallback", false, true},
		{"strict rejects failed connection and retries", "strict", false, false},
		{"fallback refuses unsafe connection", "fallback", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := &fakeConn{raw: &fakeRawConn{fd: 42}}
			second := &fakeConn{raw: &fakeRawConn{fd: 43}}
			base := &sequenceListener{conns: []net.Conn{first, second}}
			failures := map[string]error{"params:42": boom}
			if tc.rollbackFails {
				failures["set:42:cubic"] = errors.New("rollback failed")
			}
			ops := &fakeSocketOptions{algorithms: map[int]string{42: "cubic", 43: "cubic"}, failures: failures}
			core, logs := observer.New(zap.DebugLevel)
			ln := &brutalListener{Listener: base, payload: encodeParams(1_000_000, 20, 0), ops: ops, failureMode: tc.mode, logger: zap.New(core)}
			conn, err := ln.Accept()
			if err != nil {
				t.Fatalf("per-connection failure escaped into listener: %v", err)
			}
			want := net.Conn(second)
			if tc.wantFirst {
				want = first
			}
			if conn != want {
				t.Fatalf("returned %p, want %p", conn, want)
			}
			if first.closed.Load() == tc.wantFirst {
				t.Fatalf("failed connection closed=%v, want %v", first.closed.Load(), !tc.wantFirst)
			}
			if second.closed.Load() {
				t.Fatal("successful retry connection was closed")
			}
			if logs.Len() == 0 {
				t.Fatal("failure was not logged")
			}
		})
	}
}

func TestAcceptPassesThroughListenerError(t *testing.T) {
	boom := errors.New("listener stopped")
	ln := &brutalListener{Listener: &sequenceListener{err: boom}}
	if conn, err := ln.Accept(); conn != nil || !errors.Is(err, boom) {
		t.Fatalf("Accept=(%v,%v)", conn, err)
	}
}

func TestAcceptSuccessDoesNotLogPerConnection(t *testing.T) {
	conn := &fakeConn{raw: &fakeRawConn{fd: 42}}
	core, logs := observer.New(zap.InfoLevel)
	ln := &brutalListener{Listener: &sequenceListener{conns: []net.Conn{conn}}, payload: encodeParams(1_000_000, 20, 0), ops: &fakeSocketOptions{algorithms: map[int]string{42: "cubic"}}, failureMode: "fallback", logger: zap.New(core)}
	if got, err := ln.Accept(); err != nil || got != conn {
		t.Fatalf("Accept=(%v,%v)", got, err)
	}
	if logs.Len() != 0 {
		t.Fatalf("successful accept emitted %d production logs", logs.Len())
	}
}

func TestFailureLoggingIsBounded(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	logger := sampledLogger(zap.New(core))
	for range 100 {
		logger.Warn("tcp_brutal setup failed", zap.Error(errors.New("unavailable")))
	}
	if logs.Len() < 1 || logs.Len() > 2 {
		t.Fatalf("100 identical failures emitted %d logs, want at most 1 per sampling interval", logs.Len())
	}
}
