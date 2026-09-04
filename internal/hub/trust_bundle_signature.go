package hub

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// TrustBundleSignatureVerifier verifies a detached base64url signature over
// the exact Trust Bundle bytes. Signature and key distribution are deployment
// responsibilities; this package only defines the fail-closed boundary.
type TrustBundleSignatureVerifier struct {
	Key crypto.PublicKey
}

func NewTrustBundleSignatureVerifier(publicKeyPEM []byte) (TrustBundleSignatureVerifier, error) {
	block, _ := pem.Decode(publicKeyPEM)
	if block == nil {
		return TrustBundleSignatureVerifier{}, errors.New("trust bundle signing key is not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return TrustBundleSignatureVerifier{}, fmt.Errorf("parse trust bundle signing key: %w", err)
	}
	switch key.(type) {
	case *ecdsa.PublicKey, *rsa.PublicKey, ed25519.PublicKey:
		return TrustBundleSignatureVerifier{Key: key}, nil
	default:
		return TrustBundleSignatureVerifier{}, errors.New("trust bundle signing key must be ECDSA, RSA, or Ed25519")
	}
}

func loadTrustBundlePublicKey(path string) (TrustBundleSignatureVerifier, error) {
	if strings.TrimSpace(path) == "" {
		return TrustBundleSignatureVerifier{}, errors.New("trust bundle signing public key path is required")
	}
	encodedKey, err := os.ReadFile(path)
	if err != nil {
		return TrustBundleSignatureVerifier{}, fmt.Errorf("read trust bundle signing key: %w", err)
	}
	return NewTrustBundleSignatureVerifier(encodedKey)
}

func (v TrustBundleSignatureVerifier) Verify(payload, encodedSignature []byte) error {
	if v.Key == nil {
		return errors.New("trust bundle signing key is not configured")
	}
	signatureText := strings.TrimSpace(string(encodedSignature))
	if signatureText == "" {
		return errors.New("trust bundle signature is empty")
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil {
		if signature, err = base64.StdEncoding.DecodeString(signatureText); err != nil {
			return errors.New("trust bundle signature is not base64")
		}
	}
	digest := sha256.Sum256(payload)
	switch key := v.Key.(type) {
	case ed25519.PublicKey:
		if !ed25519.Verify(key, payload, signature) {
			return errors.New("trust bundle Ed25519 signature verification failed")
		}
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
			return errors.New("trust bundle RSA signature verification failed")
		}
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(key, digest[:], signature) {
			return errors.New("trust bundle ECDSA signature verification failed")
		}
	default:
		return errors.New("unsupported trust bundle signing key")
	}
	return nil
}

func loadTrustBundleSignature(path, keyPath string) (TrustBundleSignatureVerifier, error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(keyPath) == "" {
		return TrustBundleSignatureVerifier{}, errors.New("trust bundle signature and public key paths are required together")
	}
	return loadTrustBundlePublicKey(keyPath)
}
