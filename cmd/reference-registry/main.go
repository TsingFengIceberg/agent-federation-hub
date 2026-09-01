// reference-registry is a small HTTPS registry used by local integration
// tests. It is deliberately not a production catalog: state is in memory and
// the implementation exists to exercise the replaceable Registry contract.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TsingFengIceberg/agent-federation-hub/internal/core"
)

type server struct {
	mu     sync.RWMutex
	agents map[string]core.Agent
	token  string
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.token != "" && !authorized(r, s.token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="reference-registry"`)
		http.Error(w, "Bearer credential is required", http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("ok\n"))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/agents":
		s.register(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/agents":
		s.list(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *server) register(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	var agent core.Agent
	if err := decoder.Decode(&agent); err != nil || strings.TrimSpace(agent.ID) == "" || strings.TrimSpace(agent.TenantID) == "" {
		http.Error(w, "agent id and tenantId are required", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if s.agents == nil {
		s.agents = make(map[string]core.Agent)
	}
	s.agents[agent.TenantID+"\x00"+agent.ID] = agent
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(agent)
}

func (s *server) list(w http.ResponseWriter, r *http.Request) {
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant_id"))
	if tenant == "" {
		http.Error(w, "tenant_id is required", http.StatusBadRequest)
		return
	}
	s.mu.RLock()
	result := make([]core.Agent, 0)
	for _, agent := range s.agents {
		if agent.TenantID == tenant {
			result = append(result, agent)
		}
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func authorized(r *http.Request, want string) bool {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(value, "Bearer ") {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func main() {
	listen := flag.String("listen", "127.0.0.1:19443", "HTTPS listen address")
	certFile := flag.String("tls-cert-file", "", "PEM server certificate")
	keyFile := flag.String("tls-key-file", "", "PEM server private key")
	token := flag.String("token", "", "optional Bearer token for local tests")
	flag.Parse()
	if *certFile == "" || *keyFile == "" {
		log.Fatal("--tls-cert-file and --tls-key-file are required")
	}
	httpServer := &http.Server{
		Addr: *listen, Handler: &server{agents: make(map[string]core.Agent), token: *token},
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("reference Registry listening on https://%s", *listen)
	if err := httpServer.ListenAndServeTLS(*certFile, *keyFile); err != nil {
		log.Fatal(err)
	}
}
