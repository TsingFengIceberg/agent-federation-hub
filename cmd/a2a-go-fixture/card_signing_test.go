package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	a2afederation "github.com/TsingFengIceberg/agent-federation-hub/internal/federation/a2a"
)

func TestLoadAgentCardSignerAndSignFixtureCard(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "card-key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := loadAgentCardSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	card := fixtureCard("https://agent.example", "fixture", "test", nil)
	if err := a2afederation.SignAgentCard(card, signer, "fixture-key"); err != nil {
		t.Fatal(err)
	}
	if err := (a2afederation.CardVerifier{Required: true, Resolver: a2afederation.StaticCardSignatureResolver{"fixture-key": &key.PublicKey}}).Verify(t.Context(), card); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAgentCardSigningFlagsRequiresPair(t *testing.T) {
	if err := validateAgentCardSigningFlags("key.pem", ""); err == nil {
		t.Fatal("key file without ID was accepted")
	}
	if err := validateAgentCardSigningFlags("", "fixture-key"); err == nil {
		t.Fatal("key ID without key file was accepted")
	}
	if err := validateAgentCardSigningFlags("key.pem", "fixture-key"); err != nil {
		t.Fatal(err)
	}
}
