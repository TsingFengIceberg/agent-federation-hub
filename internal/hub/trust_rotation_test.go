package hub

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
)

// TestPartnerCARotation exercises an in-process certificate rollover using
// the same trust shape as a partner workload. It is opt-in because it opens a
// loopback TLS listener and is intended for the trust integration job.
func TestPartnerCARotation(t *testing.T) {
	if testing.Short() || getenv("AFH_RUN_TRUST_TESTS") != "1" {
		t.Skip("AFH_RUN_TRUST_TESTS is not enabled")
	}
	caOne, keyOne, _ := makeCertificateAuthority(t)
	caTwo, keyTwo, _ := makeCertificateAuthority(t)
	certOne := makeSignedCertificate(t, caOne, keyOne, x509.ExtKeyUsageServerAuth, nil, []net.IP{net.ParseIP("127.0.0.1")})
	certTwo := makeSignedCertificate(t, caTwo, keyTwo, x509.ExtKeyUsageServerAuth, nil, []net.IP{net.ParseIP("127.0.0.1")})
	var current atomic.Value
	current.Store(certOne)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusOK) }), TLSConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			certificate := current.Load().(tls.Certificate)
			return &certificate, nil
		},
	}}
	tlsListener := tls.NewListener(listener, server.TLSConfig)
	go func() { _ = server.Serve(tlsListener) }()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(caOne)
	roots.AddCert(caTwo)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}}
	endpoint := "https://" + listener.Addr().String()
	requestOnce := func() {
		response, requestErr := client.Get(endpoint)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status=%d", response.StatusCode)
		}
	}
	requestOnce()
	current.Store(certTwo)
	client.CloseIdleConnections()
	requestOnce()
}

func getenv(name string) string {
	return os.Getenv(name)
}
