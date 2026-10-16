package tcpbrutal_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
	_ "github.com/caddyserver/caddy/v2/modules/filestorage"
	_ "github.com/mejinotdove/caddy-tcp-brutal"
)

func TestCaddyExampleConfiguration(t *testing.T) {
	content, err := os.ReadFile("caddy.json")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	admin := config["admin"].(map[string]any)
	if admin["listen"] != "127.0.0.1:8081" {
		t.Fatal("sample admin interface must stay on loopback")
	}
	servers := config["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	for _, value := range servers {
		server := value.(map[string]any)
		wrappers := server["listener_wrappers"].([]any)
		if len(wrappers) != 2 || wrappers[0].(map[string]any)["wrapper"] != "tcp_brutal" || wrappers[1].(map[string]any)["wrapper"] != "tls" {
			t.Fatal("sample must configure tcp_brutal before explicit TLS wrapper")
		}
	}
	validateExample(t, config)
}

func TestCaddyREADMEHTTPSConfiguration(t *testing.T) {
	content, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, code, ok := strings.Cut(string(content), "```json\n")
	if !ok {
		t.Fatal("README missing HTTPS JSON example")
	}
	code, _, ok = strings.Cut(code, "```")
	if !ok {
		t.Fatal("README has unclosed JSON example")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(code), &config); err != nil {
		t.Fatalf("README JSON invalid: %v", err)
	}
	servers := config["apps"].(map[string]any)["http"].(map[string]any)["servers"].(map[string]any)
	hasHostname := false
	for _, value := range servers {
		server := value.(map[string]any)
		for _, value := range server["routes"].([]any) {
			route := value.(map[string]any)
			if matchers, ok := route["match"].([]any); ok {
				for _, matcher := range matchers {
					if hosts, ok := matcher.(map[string]any)["host"].([]any); ok && len(hosts) > 0 {
						hasHostname = true
					}
				}
			}
		}
	}
	if !hasHostname {
		t.Fatal("HTTPS example needs a hostname so Caddy can obtain a certificate")
	}
	validateExample(t, config)
}

func validateExample(t *testing.T, config map[string]any) {
	t.Helper()
	// Validate the real Caddy module graph without starting listeners, issuing
	// certificates, or requiring the host's optional congestion-control module.
	config["admin"] = map[string]any{"disabled": true}
	config["storage"] = map[string]any{"module": "file_system", "root": t.TempDir()}
	httpApp := config["apps"].(map[string]any)["http"].(map[string]any)
	servers := httpApp["servers"].(map[string]any)
	for _, value := range servers {
		server := value.(map[string]any)
		server["automatic_https"] = map[string]any{"disable": true}
		for _, value := range server["listener_wrappers"].([]any) {
			wrapper := value.(map[string]any)
			if wrapper["wrapper"] == "tcp_brutal" {
				wrapper["enabled"] = false
			}
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var caddyConfig caddy.Config
	if err := json.Unmarshal(encoded, &caddyConfig); err != nil {
		t.Fatal(err)
	}
	if err := caddy.Validate(&caddyConfig); err != nil {
		t.Fatalf("Caddy rejected documented configuration: %v", err)
	}
}
