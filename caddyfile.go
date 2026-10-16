package tcpbrutal

import (
	"strconv"
	"strings"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// UnmarshalCaddyfile parses the tcp_brutal listener wrapper configuration.
func (m *Module) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	if !d.Next() {
		return d.Err("expected tcp_brutal directive")
	}
	if d.NextArg() {
		return d.ArgErr()
	}
	m.Enabled = true
	seen := make(map[string]bool)
	for d.NextBlock(0) {
		if d.Nesting() > 1 {
			return d.Err("nested tcp_brutal option blocks are not supported")
		}
		name := d.Val()
		if seen[name] {
			return d.Errf("duplicate tcp_brutal option %q", name)
		}
		seen[name] = true
		if !d.NextArg() {
			return d.ArgErr()
		}
		value := d.Val()
		switch name {
		case "enabled":
			if value != "true" && value != "false" {
				return d.Err("enabled must be true or false")
			}
			m.Enabled = value == "true"
		case "rate_bytes_per_second":
			rate, err := parseCaddyUint(d, value, 64)
			if err != nil {
				return err
			}
			m.RateBytesPerSecond = rate
		case "rate_bps":
			rate, err := parseCaddyUint(d, value, 64)
			if err != nil {
				return err
			}
			m.RateBytesPerS = rate
		case "cwnd_gain":
			gain, err := parseCaddyUint(d, value, 32)
			if err != nil {
				return err
			}
			m.CwndGain = uint32(gain)
		case "group_id":
			group, err := parseCaddyUint(d, value, 64)
			if err != nil {
				return err
			}
			m.GroupID = group
		case "failure_mode":
			if value != "fallback" && value != "strict" {
				return d.Err("failure_mode must be fallback or strict")
			}
			m.FailureMode = value
		default:
			return d.Errf("unrecognized tcp_brutal option %q", name)
		}
		if d.NextArg() {
			return d.ArgErr()
		}
		if err := rejectOptionBlock(d); err != nil {
			return err
		}
	}
	validated := *m
	if err := validated.Validate(); err != nil {
		return d.Errf("invalid tcp_brutal configuration: %v", err)
	}
	return nil
}

func parseCaddyUint(d *caddyfile.Dispenser, value string, bits int) (uint64, error) {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, d.Err("expected an unsigned decimal integer")
	}
	n, err := strconv.ParseUint(value, 10, bits)
	if err != nil {
		return 0, d.Errf("invalid unsigned integer %q: %v", value, err)
	}
	return n, nil
}

func rejectOptionBlock(d *caddyfile.Dispenser) error {
	if !d.Next() {
		return nil
	}
	if d.Val() == "{" {
		return d.Err("nested tcp_brutal option blocks are not supported")
	}
	d.Prev()
	return nil
}

var _ caddyfile.Unmarshaler = (*Module)(nil)
