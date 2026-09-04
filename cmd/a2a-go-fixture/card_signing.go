package main

import (
	"crypto"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

func loadAgentCardSigner(path string) (crypto.Signer, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read AgentCard signing key: %w", err)
	}
	block, _ := pem.Decode(encoded)
	if block == nil {
		return nil, errors.New("AgentCard signing key is not PEM")
	}
	var privateKey any
	switch block.Type {
	case "EC PRIVATE KEY":
		privateKey, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		privateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		privateKey, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, fmt.Errorf("parse AgentCard signing key: %w", err)
	}
	signer, ok := privateKey.(crypto.Signer)
	if !ok || signer.Public() == nil {
		return nil, errors.New("AgentCard signing key is not a supported private key")
	}
	return signer, nil
}

func validateAgentCardSigningFlags(keyFile, keyID string) error {
	if (strings.TrimSpace(keyFile) == "") != (strings.TrimSpace(keyID) == "") {
		return errors.New("-agent-card-signing-key-file and -agent-card-signing-key-id must be configured together")
	}
	return nil
}
