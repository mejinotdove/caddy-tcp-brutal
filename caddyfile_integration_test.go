package tcpbrutal_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	_ "github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
)

func TestCaddyfileSample(t *testing.T) {
	content, err := os.ReadFile("Caddyfile")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, caddyfile.Format(content)) {
		t.Fatal("sample needs caddy fmt formatting")
	}
	config := adaptCaddyfile(t, string(content))
	if config["admin"].(map[string]any)["listen"] != "127.0.0.1:8081" {
		t.Fatal("sample admin interface must stay on loopback")
	}
	wrappers := adaptedListenerWrappers(t, config)
	if len(wrappers) != 2 || wrappers[0]["wrapper"] != "tcp_brutal" || wrappers[1]["wrapper"] != "tls" {
		t.Fatalf("wrong listener wrapper order: %+v", wrappers)
	}
	brutal := wrappers[0]
	if brutal["enabled"] != true || brutal["rate_bytes_per_second"] != float64(25_000_000) || brutal["cwnd_gain"] != float64(20) || brutal["failure_mode"] != "fallback" {
		t.Fatalf("wrong adapted parameters: %+v", brutal)
	}
	validateExample(t, config)
}

func TestCaddyfileREADMEExample(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, code, ok := strings.Cut(string(content), "```caddyfile\n")
	if !ok {
		t.Fatal("README is missing a Caddyfile example")
	}
	code, _, ok = strings.Cut(code, "```")
	if !ok {
		t.Fatal("README has an unclosed Caddyfile example")
	}
	config := adaptCaddyfile(t, code)
	wrappers := adaptedListenerWrappers(t, config)
	if len(wrappers) != 2 || wrappers[0]["wrapper"] != "tcp_brutal" || wrappers[1]["wrapper"] != "tls" {
		t.Fatalf("README config has wrong wrapper order: %+v", wrappers)
	}
	validateExample(t, config)
}

func TestCaddyfileAdaptationOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wrapper string
		want    map[string]any
	}{
		{"shorthand enables module", "tcp_brutal", map[string]any{"wrapper": "tcp_brutal", "enabled": true}},
		{"legacy rate alias", "tcp_brutal {\n rate_bps 1000000\n}", map[string]any{"wrapper": "tcp_brutal", "enabled": true, "rate_bps": float64(1_000_000)}},
		{"explicit disabled", "tcp_brutal {\n enabled false\n}", map[string]any{"wrapper": "tcp_brutal"}},
		{"full options", "tcp_brutal {\n enabled true\n rate_bytes_per_second 1000000\n cwnd_gain 15\n group_id 99\n failure_mode strict\n}", map[string]any{"wrapper": "tcp_brutal", "enabled": true, "rate_bytes_per_second": float64(1_000_000), "cwnd_gain": float64(15), "group_id": float64(99), "failure_mode": "strict"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := adaptCaddyfile(t, caddyfileConfig(tc.wrapper))
			wrappers := adaptedListenerWrappers(t, config)
			if len(wrappers) != 2 {
				t.Fatalf("got %d wrappers", len(wrappers))
			}
			got := wrappers[0]
			if len(got) != len(tc.want) {
				t.Fatalf("adapted unexpected fields: %+v; want %+v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Fatalf("%s=%v, want %v", key, got[key], want)
				}
			}
			validateExample(t, config)
		})
	}
}

func TestCaddyfileAdaptationRejectsInvalidOptions(t *testing.T) {
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		t.Fatal("Caddyfile adapter not registered")
	}
	for _, wrapper := range []string{
		"tcp_brutal true",
		"tcp_brutal {\n unknown 1\n}",
		"tcp_brutal {\n rate_bytes_per_second 1\n}",
		"tcp_brutal {\n enabled true\n enabled false\n}",
		"tcp_brutal {\n rate_bps 1000000\n rate_bytes_per_second 2000000\n}",
		"tcp_brutal {\n cwnd_gain 20 {\n }\n}",
	} {
		t.Run(wrapper, func(t *testing.T) {
			_, _, err := adapter.Adapt([]byte(caddyfileConfig(wrapper)), map[string]any{"filename": "InvalidCaddyfile"})
			if err == nil {
				t.Fatal("real Caddyfile adapter accepted invalid configuration")
			}
			if !strings.Contains(err.Error(), "InvalidCaddyfile:") {
				t.Fatalf("error lost source location: %v", err)
			}
		})
	}
}

func caddyfileConfig(wrapper string) string {
	return fmt.Sprintf(`{
	admin 127.0.0.1:8081
	servers {
		listener_wrappers {
			%s
			tls
		}
	}
}

http://:8080 {
	respond "ok"
}
`, wrapper)
}

func adaptCaddyfile(t *testing.T, input string) map[string]any {
	t.Helper()
	adapter := caddyconfig.GetAdapter("caddyfile")
	if adapter == nil {
		t.Fatal("Caddyfile adapter not registered")
	}
	encoded, warnings, err := adapter.Adapt(caddyfile.Format([]byte(input)), map[string]any{"filename": "Caddyfile"})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected Caddyfile adaptation warnings: %+v", warnings)
	}
	var config map[string]any
	if err := json.Unmarshal(encoded, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func adaptedListenerWrappers(t *testing.T, config map[string]any) []map[string]any {
	t.Helper()
	servers := config["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	if len(servers) != 1 {
		t.Fatalf("got %d servers, want one", len(servers))
	}
	var result []map[string]any
	for _, server := range servers {
		for _, value := range server.(map[string]any)["listener_wrappers"].([]any) {
			result = append(result, value.(map[string]any))
		}
	}
	return result
}
