package tcpbrutal

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/zap"
)

// Exercise real handshakes and HTTP I/O on both listener arrangements. Only
// socket options are substituted, so no kernel module is needed in CI.
func TestHTTPSListenerOrder(t *testing.T) {
	certificate, roots := testCertificate(t)
	for _, order := range []string{"before TLS", "after TLS"} {
		t.Run(order, func(t *testing.T) {
			base, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { base.Close() })
			ops := &fakeSocketOptions{}
			wrapper := &brutalListener{payload: encodeParams(1_000_000, 20, 0), ops: ops, failureMode: "strict", logger: zap.NewNop()}
			tlsConfig := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
			var listener net.Listener
			if order == "before TLS" {
				wrapper.Listener = base
				listener = tls.NewListener(wrapper, tlsConfig)
			} else {
				wrapper.Listener = tls.NewListener(base, tlsConfig)
				listener = wrapper
			}
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.TLS == nil {
					t.Error("TLS state was lost by listener wrapper")
				}
				io.WriteString(w, "brutal HTTPS works")
			}), ReadHeaderTimeout: time.Second}
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() {
				server.Close()
				select {
				case err := <-done:
					if err != nil && err != http.ErrServerClosed {
						t.Errorf("Serve: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("HTTP server did not stop")
				}
			})
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			response, err := client.Get("https://" + base.Addr().String() + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || string(body) != "brutal HTTPS works" {
				t.Fatalf("response: %d %q", response.StatusCode, body)
			}
			ops.mu.Lock()
			defer ops.mu.Unlock()
			paramsCalls := 0
			for _, call := range ops.calls {
				if call.op == "params" {
					paramsCalls++
				}
			}
			if paramsCalls != 1 {
				t.Fatalf("socket configured %d times for one HTTPS connection", paramsCalls)
			}
		})
	}
}

func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}
