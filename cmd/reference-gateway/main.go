// reference-gateway is a local HTTPS Gateway data-plane fixture. It forwards
// the opaque request contract through the repository A2A adapter and is not a
// production gateway implementation.
package main

import (
	"crypto/subtle"
	"flag"
	"log"
	"net/http"
	"strings"
	"time"

	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/gateway"
	"github.com/TsingFengIceberg/agent-federation-hub/internal/secrets"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:19444", "HTTPS listen address")
	certFile := flag.String("tls-cert-file", "", "PEM server certificate")
	keyFile := flag.String("tls-key-file", "", "PEM server private key")
	token := flag.String("token", "", "Bearer token required from the Hub")
	flag.Parse()
	if *certFile == "" || *keyFile == "" || *token == "" {
		log.Fatal("--tls-cert-file, --tls-key-file, and --token are required")
	}
	profiles, err := a2afederation.ParseBindingProfiles("JSONRPC")
	if err != nil {
		log.Fatal(err)
	}
	adapter, err := a2afederation.NewWithProfiles(30*time.Second, profiles, secrets.NewEnvProvider(nil))
	if err != nil {
		log.Fatal(err)
	}
	proxy := gateway.Handler{Adapter: adapter}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if !authorized(r, *token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="reference-gateway"`)
			http.Error(w, "Bearer credential is required", http.StatusUnauthorized)
			return
		}
		log.Printf("proxy operation=%s request_id=%s", r.Header.Get("X-AFH-Gateway-Operation"), r.Header.Get("X-Request-ID"))
		proxy.ServeHTTP(w, r)
	})
	httpServer := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("reference Gateway listening on https://%s", *listen)
	if err := httpServer.ListenAndServeTLS(*certFile, *keyFile); err != nil {
		log.Fatal(err)
	}
}

func authorized(r *http.Request, want string) bool {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
