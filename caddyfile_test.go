package tcpbrutal

import (
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestUnmarshalCaddyfile(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  Module
	}{
		{"shorthand", "tcp_brutal", Module{Enabled: true}},
		{"empty block", "tcp_brutal {\n}\n", Module{Enabled: true}},
		{"disabled", "tcp_brutal {\n enabled false\n}\n", Module{Enabled: false}},
		{"all options", "tcp_brutal {\n enabled true\n rate_bytes_per_second 1000000\n cwnd_gain 15\n group_id 18446744073709551615\n failure_mode strict\n}\n", Module{Enabled: true, RateBytesPerSecond: 1_000_000, CwndGain: 15, GroupID: ^uint64(0), FailureMode: "strict"}},
		{"legacy rate", "tcp_brutal {\n rate_bps 2000000\n}\n", Module{Enabled: true, RateBytesPerS: 2_000_000}},
		{"matching aliases", "tcp_brutal {\n rate_bps 1000000\n rate_bytes_per_second 1000000\n}\n", Module{Enabled: true, RateBytesPerS: 1_000_000, RateBytesPerSecond: 1_000_000}},
		{"zero values use runtime defaults", "tcp_brutal {\n rate_bytes_per_second 0\n cwnd_gain 0\n group_id 0\n failure_mode fallback\n}\n", Module{Enabled: true, FailureMode: "fallback"}},
		{"lower boundary", "tcp_brutal {\n rate_bytes_per_second 62500\n cwnd_gain 5\n}\n", Module{Enabled: true, RateBytesPerSecond: 62500, CwndGain: 5}},
		{"upper boundary", "tcp_brutal {\n rate_bytes_per_second 125000000000\n cwnd_gain 80\n}\n", Module{Enabled: true, RateBytesPerSecond: 125_000_000_000, CwndGain: 80}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := Module{probe: func(uint64, uint32, uint64) error { t.Fatal("parsing probed the kernel"); return nil }}
			if err := m.UnmarshalCaddyfile(caddyfile.NewTestDispenser(tc.input)); err != nil {
				t.Fatal(err)
			}
			if m.Enabled != tc.want.Enabled || m.RateBytesPerS != tc.want.RateBytesPerS || m.RateBytesPerSecond != tc.want.RateBytesPerSecond || m.CwndGain != tc.want.CwndGain || m.GroupID != tc.want.GroupID || m.FailureMode != tc.want.FailureMode {
				t.Fatalf("parsed enabled=%v rate_bps=%d rate_bytes_per_second=%d gain=%d group=%d mode=%q; want %+v", m.Enabled, m.RateBytesPerS, m.RateBytesPerSecond, m.CwndGain, m.GroupID, m.FailureMode, tc.want)
			}
		})
	}
}

func TestUnmarshalCaddyfileRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"unknown option", "rate 25000000"},
		{"missing value", "enabled"},
		{"extra value", "cwnd_gain 20 30"},
		{"invalid boolean", "enabled yes"},
		{"negative rate", "rate_bytes_per_second -1"},
		{"signed rate", "rate_bytes_per_second +1000000"},
		{"fractional rate", "rate_bytes_per_second 1.5"},
		{"rate with units", "rate_bytes_per_second 200Mbps"},
		{"hex rate", "rate_bytes_per_second 0x100000"},
		{"small rate", "rate_bytes_per_second 62499"},
		{"large rate", "rate_bytes_per_second 125000000001"},
		{"overflow rate", "rate_bytes_per_second 18446744073709551616"},
		{"small gain", "cwnd_gain 4"},
		{"large gain", "cwnd_gain 81"},
		{"overflow gain", "cwnd_gain 4294967296"},
		{"negative group", "group_id -1"},
		{"overflow group", "group_id 18446744073709551616"},
		{"invalid mode", "failure_mode ignore"},
		{"duplicate option", "enabled true\n enabled false"},
		{"duplicate rate", "rate_bps 1000000\n rate_bps 1000000"},
		{"conflicting aliases", "rate_bps 1000000\n rate_bytes_per_second 2000000"},
		{"nested option block", "rate_bytes_per_second 1000000 {\n enabled false\n }"},
		{"empty nested option block", "cwnd_gain 20 {\n }"},
		{"empty same-line nested block", "group_id 1 {}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var m Module
			err := m.UnmarshalCaddyfile(caddyfile.NewTestDispenser("tcp_brutal {\n " + tc.body + "\n}\n"))
			if err == nil {
				t.Fatal("invalid configuration was accepted")
			}
			if !strings.Contains(err.Error(), "Testfile:") {
				t.Fatalf("error is missing source location: %v", err)
			}
		})
	}
	var m Module
	if err := m.UnmarshalCaddyfile(caddyfile.NewTestDispenser("tcp_brutal true")); err == nil {
		t.Fatal("positional arguments were silently accepted")
	}
}
