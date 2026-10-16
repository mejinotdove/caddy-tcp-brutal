package tcpbrutal

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

type socketCall struct {
	op        string
	fd        int
	algorithm string
	payload   []byte
}

type fakeSocketOptions struct {
	mu           sync.Mutex
	algorithms   map[int]string
	failures     map[string]error
	calls        []socketCall
	versionValue uint32
}

func (f *fakeSocketOptions) congestion(fd int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, socketCall{op: "get", fd: fd})
	return f.algorithms[fd], f.failures[fmt.Sprintf("get:%d", fd)]
}
func (f *fakeSocketOptions) setCongestion(fd int, algorithm string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, socketCall{op: "set", fd: fd, algorithm: algorithm})
	if err := f.failures[fmt.Sprintf("set:%d:%s", fd, algorithm)]; err != nil {
		return err
	}
	if f.algorithms == nil {
		f.algorithms = make(map[int]string)
	}
	f.algorithms[fd] = algorithm
	return nil
}
func (f *fakeSocketOptions) setParams(fd int, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, socketCall{op: "params", fd: fd, payload: bytes.Clone(payload)})
	return f.failures[fmt.Sprintf("params:%d", fd)]
}
func (f *fakeSocketOptions) version(fd int) (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, socketCall{op: "version", fd: fd})
	return f.versionValue, f.failures[fmt.Sprintf("version:%d", fd)]
}

func TestParameterWireFormat(t *testing.T) {
	const rate uint64 = 0x0102030405060708
	const gain uint32 = 0x11223344
	const group uint64 = 0x9988776655443322
	for _, id := range []uint64{0, group} {
		payload := encodeParams(rate, gain, id)
		wantSize := 12
		if id != 0 {
			wantSize = 20
		}
		if len(payload) != wantSize {
			t.Fatalf("group=%d payload length=%d, want %d", id, len(payload), wantSize)
		}
		if got := binary.NativeEndian.Uint64(payload[:8]); got != rate {
			t.Fatalf("rate=%x, want %x", got, rate)
		}
		if got := binary.NativeEndian.Uint32(payload[8:12]); got != gain {
			t.Fatalf("gain=%x, want %x", got, gain)
		}
		if id != 0 && binary.NativeEndian.Uint64(payload[12:20]) != group {
			t.Fatal("group ID not packed at offset 12")
		}
	}
}

func TestConfigureSocketRollback(t *testing.T) {
	setupErr := errors.New("set failed")
	rollbackErr := errors.New("restore failed")
	for _, tc := range []struct {
		name          string
		original      string
		failures      map[string]error
		wantSafe      bool
		wantErr       error
		wantAlgorithm string
		wantParams    bool
	}{
		{"success", "cubic", nil, true, nil, "brutal", true},
		{"read failure", "cubic", map[string]error{"get:42": setupErr}, true, setupErr, "cubic", false},
		{"switch failure", "cubic", map[string]error{"set:42:brutal": setupErr}, true, setupErr, "cubic", false},
		{"parameter failure restores algorithm", "bbr", map[string]error{"params:42": setupErr}, true, setupErr, "bbr", true},
		{"restore failure is unsafe", "cubic", map[string]error{"params:42": setupErr, "set:42:cubic": rollbackErr}, false, setupErr, "brutal", true},
		{"already brutal remains safe", "brutal", map[string]error{"params:42": setupErr}, true, setupErr, "brutal", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &fakeSocketOptions{algorithms: map[int]string{42: tc.original}, failures: tc.failures}
			payload := encodeParams(1_000_000, 20, 0)
			safe, err := configureSocket(42, payload, ops)
			if safe != tc.wantSafe || !errors.Is(err, tc.wantErr) {
				t.Fatalf("configureSocket()=(%v,%v), want (%v,%v)", safe, err, tc.wantSafe, tc.wantErr)
			}
			if tc.name == "restore failure is unsafe" && !errors.Is(err, rollbackErr) {
				t.Fatalf("lost rollback error: %v", err)
			}
			if ops.algorithms[42] != tc.wantAlgorithm {
				t.Fatalf("algorithm=%q, want %q", ops.algorithms[42], tc.wantAlgorithm)
			}
			paramsCalls := 0
			for _, call := range ops.calls {
				if call.op == "params" {
					paramsCalls++
					if !bytes.Equal(call.payload, payload) {
						t.Fatal("payload changed")
					}
				}
			}
			if (paramsCalls != 0) != tc.wantParams {
				t.Fatalf("parameter calls=%d, expected called=%v", paramsCalls, tc.wantParams)
			}
			if tc.wantErr == nil {
				reads := 0
				for _, call := range ops.calls {
					if call.op == "get" {
						reads++
					}
				}
				if reads != 1 {
					t.Fatalf("success path did %d reads, want only original algorithm read", reads)
				}
			}
		})
	}
}

type fakeRawConn struct {
	fd           uintptr
	controlErr   error
	controlCalls int
}

func (r *fakeRawConn) Control(fn func(uintptr)) error {
	r.controlCalls++
	if r.controlErr != nil {
		return r.controlErr
	}
	fn(r.fd)
	return nil
}
func (*fakeRawConn) Read(func(uintptr) bool) error  { return errors.New("unexpected raw read") }
func (*fakeRawConn) Write(func(uintptr) bool) error { return errors.New("unexpected raw write") }

type fakeConn struct {
	net.Conn
	raw    syscall.RawConn
	rawErr error
	closed atomic.Bool
}

func (c *fakeConn) SyscallConn() (syscall.RawConn, error) { return c.raw, c.rawErr }
func (c *fakeConn) Close() error                          { c.closed.Store(true); return nil }

type unwrappingConn struct{ net.Conn }

func (c *unwrappingConn) Unwrap() net.Conn { return c.Conn }

type cyclicConn struct{ net.Conn }

func (c *cyclicConn) NetConn() net.Conn { return c }

func TestConfigureConnTLSAndWrappers(t *testing.T) {
	for _, kind := range []string{"tcp", "tls", "nested"} {
		t.Run(kind, func(t *testing.T) {
			raw := &fakeRawConn{fd: 42}
			base := &fakeConn{raw: raw}
			var conn net.Conn = base
			if kind == "tls" || kind == "nested" {
				conn = tls.Server(base, &tls.Config{})
			}
			if kind == "nested" {
				conn = &unwrappingConn{Conn: conn}
			}
			ops := &fakeSocketOptions{algorithms: map[int]string{42: "cubic"}}
			if safe, err := configureConn(conn, encodeParams(1_000_000, 20, 0), ops); !safe || err != nil {
				t.Fatalf("configureConn=(%v,%v)", safe, err)
			}
			if raw.controlCalls != 1 || ops.algorithms[42] != "brutal" {
				t.Fatal("underlying socket not configured exactly once")
			}
			if base.closed.Load() {
				t.Fatal("configuration closed transport")
			}
		})
	}
}

func TestConfigureConnErrors(t *testing.T) {
	boom := errors.New("raw socket failure")
	for _, tc := range []struct {
		name      string
		conn      net.Conn
		wantError error
	}{
		{"syscall connection failure", &fakeConn{rawErr: boom}, boom},
		{"control failure", &fakeConn{raw: &fakeRawConn{fd: 42, controlErr: boom}}, boom},
		{"unsupported connection", &unwrappingConn{}, nil},
		{"unwrap cycle", &cyclicConn{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &fakeSocketOptions{}
			_, err := configureConn(tc.conn, encodeParams(1_000_000, 20, 0), ops)
			if err == nil {
				t.Fatal("configuration silently succeeded")
			}
			if tc.wantError != nil && !errors.Is(err, tc.wantError) {
				t.Fatalf("lost original error: %v", err)
			}
			if len(ops.calls) != 0 {
				t.Fatal("socket options called without a raw descriptor")
			}
		})
	}
}
