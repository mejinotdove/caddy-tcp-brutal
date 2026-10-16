package tcpbrutal

import (
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/caddyserver/caddy/v2"
)

func TestConfigurationValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  string
		wantErr bool
	}{
		{"defaults", `{}`, false},
		{"legacy rate", `{"enabled":true,"rate_bps":62500}`, false},
		{"preferred rate", `{"rate_bytes_per_second":125000000000}`, false},
		{"equal aliases", `{"rate_bps":100000,"rate_bytes_per_second":100000}`, false},
		{"conflicting aliases", `{"rate_bps":100000,"rate_bytes_per_second":200000}`, true},
		{"rate too small", `{"rate_bytes_per_second":62499}`, true},
		{"legacy rate too small", `{"rate_bps":1}`, true},
		{"rate too large", `{"rate_bytes_per_second":125000000001}`, true},
		{"gain minimum", `{"cwnd_gain":5}`, false},
		{"gain maximum", `{"cwnd_gain":80}`, false},
		{"gain too small", `{"cwnd_gain":4}`, true},
		{"gain too large", `{"cwnd_gain":81}`, true},
		{"strict", `{"failure_mode":"strict"}`, false},
		{"fallback", `{"failure_mode":"fallback"}`, false},
		{"invalid mode", `{"failure_mode":"ignore"}`, true},
		{"group", `{"group_id":18446744073709551615}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m Module
			if err := json.Unmarshal([]byte(tc.config), &m); err != nil {
				t.Fatal(err)
			}
			if err := m.Validate(); (err != nil) != tc.wantErr {
				t.Fatalf("Validate() = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}

func TestProvisionDefaultsAndAliases(t *testing.T) {
	for _, tc := range []struct {
		name   string
		module Module
		rate   uint64
		cwnd   uint32
		group  uint64
	}{
		{"defaults", Module{Enabled: true}, 25_000_000, 20, 0},
		{"legacy", Module{Enabled: true, RateBytesPerS: 1_000_000}, 1_000_000, 20, 0},
		{"preferred", Module{Enabled: true, RateBytesPerSecond: 2_000_000, CwndGain: 15, GroupID: 99}, 2_000_000, 15, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.module
			calls := 0
			m.probe = func(rate uint64, cwnd uint32, group uint64) error {
				calls++
				if rate != tc.rate || cwnd != tc.cwnd || group != tc.group {
					t.Errorf("probe(%d, %d, %d), want (%d, %d, %d)", rate, cwnd, group, tc.rate, tc.cwnd, tc.group)
				}
				return nil
			}
			if err := m.Provision(caddy.Context{}); err != nil {
				t.Fatal(err)
			}
			if err := m.Validate(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("probe calls = %d, want 1", calls)
			}
			ln := &sequenceListener{}
			wrapped := m.WrapListener(ln)
			if wrapped == ln {
				t.Fatal("available enabled module did not wrap listener")
			}
		})
	}
}

func TestDisabledModuleDoesNotProbe(t *testing.T) {
	m := &Module{probe: func(uint64, uint32, uint64) error { t.Fatal("disabled module probed kernel"); return nil }}
	if err := m.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	ln := &sequenceListener{}
	if got := m.WrapListener(ln); got != ln {
		t.Fatal("disabled module changed listener")
	}
	var nilModule *Module
	if got := nilModule.WrapListener(ln); got != ln {
		t.Fatal("nil module changed listener")
	}
}

func TestProbeFailurePolicy(t *testing.T) {
	unavailable := errors.New("Brutal is unavailable")
	for _, mode := range []string{"", "fallback", "strict"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			m := &Module{Enabled: true, FailureMode: mode, probe: func(uint64, uint32, uint64) error { calls++; return unavailable }}
			err := m.Provision(caddy.Context{})
			if mode == "strict" {
				if !errors.Is(err, unavailable) {
					t.Fatalf("strict provision = %v, want unavailable", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ln := &sequenceListener{}
			for range 5 {
				if m.WrapListener(ln) != ln {
					t.Fatal("unavailable module should bypass wrapper until reload")
				}
			}
			if calls != 1 {
				t.Fatalf("probe retried %d times without reload", calls)
			}
		})
	}
}

func TestInvalidConfigurationDoesNotProbe(t *testing.T) {
	m := &Module{Enabled: true, RateBytesPerS: 1, probe: func(uint64, uint32, uint64) error { t.Fatal("invalid config reached kernel probe"); return nil }}
	if err := m.Provision(caddy.Context{}); err == nil {
		t.Fatal("invalid rate was provisioned")
	}
}

func TestConcurrentWrapListener(t *testing.T) {
	m := &Module{Enabled: true, probe: func(uint64, uint32, uint64) error { return nil }}
	if err := m.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ln net.Listener = &sequenceListener{}
			if m.WrapListener(ln) == ln {
				t.Error("module did not wrap listener")
			}
		}()
	}
	wg.Wait()
}
