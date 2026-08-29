package a2afederation

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// CardSignatureResolver resolves a trusted verification key for a signed
// AgentCard. Key discovery is deliberately injected so deployments can use an
// OIDC JWKS, a registry trust store, or an operator-managed static key.
type CardSignatureResolver interface {
	ResolveCardKey(context.Context, *a2a.AgentCard, string) (crypto.PublicKey, error)
}

// StaticCardSignatureResolver is the deterministic local/CI implementation.
// Production deployments should populate it from a managed JWKS or trust
// service rather than storing private keys in Hub configuration.
type StaticCardSignatureResolver map[string]crypto.PublicKey

func (r StaticCardSignatureResolver) ResolveCardKey(_ context.Context, _ *a2a.AgentCard, keyID string) (crypto.PublicKey, error) {
	key, ok := r[keyID]
	if !ok || key == nil {
		return nil, fmt.Errorf("AgentCard signing key %q is unavailable", keyID)
	}
	return key, nil
}

// CardVerifier validates the optional A2A JWS signatures. Required controls
// whether an unsigned Card is rejected; a signed Card must always have at
// least one valid signature when a verifier is configured.
type CardVerifier struct {
	Resolver CardSignatureResolver
	Required bool
}

func (v CardVerifier) Verify(ctx context.Context, card *a2a.AgentCard) error {
	if card == nil {
		return errors.New("AgentCard is required")
	}
	if len(card.Signatures) == 0 {
		if v.Required {
			return errors.New("AgentCard signature is required")
		}
		return nil
	}
	if v.Resolver == nil {
		if !v.Required {
			return nil
		}
		return errors.New("AgentCard signature resolver is not configured")
	}
	canonical, err := CanonicalAgentCard(card)
	if err != nil {
		return err
	}
	for _, signature := range card.Signatures {
		protected, err := decodeProtectedHeader(signature.Protected)
		if err != nil {
			continue
		}
		keyID, _ := protected["kid"].(string)
		if keyID == "" {
			continue
		}
		key, err := v.Resolver.ResolveCardKey(ctx, card, keyID)
		if err != nil {
			continue
		}
		if err := verifyJWS(protected, signature.Protected, signature.Signature, canonical, key); err == nil {
			return nil
		}
	}
	return errors.New("AgentCard has no valid trusted signature")
}

// CanonicalAgentCard returns the signature-free JSON representation used as a
// JWS payload. encoding/json sorts object keys, which gives stable RFC 8785
// ordering for AgentCard's string/bool/array fields; no floating-point values
// are introduced by the protocol model itself.
func CanonicalAgentCard(card *a2a.AgentCard) ([]byte, error) {
	if card == nil {
		return nil, errors.New("AgentCard is required")
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return nil, fmt.Errorf("marshal AgentCard: %w", err)
	}
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("decode AgentCard: %w", err)
	}
	delete(object, "signatures")
	var canonical strings.Builder
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(object); err != nil {
		return nil, fmt.Errorf("canonicalize AgentCard: %w", err)
	}
	return []byte(strings.TrimSuffix(canonical.String(), "\n")), nil
}

// SignAgentCard appends an ES256/RS256/EdDSA JWS signature to card. It is
// provided for provider fixtures and contract tests; private key custody is
// outside the Hub process in production.
func SignAgentCard(card *a2a.AgentCard, signer crypto.Signer, keyID string) error {
	if card == nil || signer == nil || strings.TrimSpace(keyID) == "" {
		return errors.New("AgentCard, signer, and key ID are required")
	}
	canonical, err := CanonicalAgentCard(card)
	if err != nil {
		return err
	}
	alg, err := signingAlgorithm(signer)
	if err != nil {
		return err
	}
	protectedBytes, err := json.Marshal(map[string]string{"alg": alg, "kid": keyID, "typ": "JOSE"})
	if err != nil {
		return err
	}
	protected := base64.RawURLEncoding.EncodeToString(protectedBytes)
	payload := base64.RawURLEncoding.EncodeToString(canonical)
	input := []byte(protected + "." + payload)
	digest := sha256.Sum256(input)
	var signature []byte
	if key, ok := signer.(*ecdsa.PrivateKey); ok {
		if key.Curve.Params().BitSize != 256 {
			return errors.New("ES256 requires a P-256 key")
		}
		r, s, signErr := ecdsa.Sign(rand.Reader, key, digest[:])
		if signErr != nil {
			return fmt.Errorf("sign AgentCard: %w", signErr)
		}
		signature = make([]byte, 64)
		r.FillBytes(signature[:32])
		s.FillBytes(signature[32:])
	} else {
		var signErr error
		signature, signErr = signer.Sign(rand.Reader, digest[:], cryptoHashForAlgorithm(alg))
		if signErr != nil {
			return fmt.Errorf("sign AgentCard: %w", signErr)
		}
	}
	card.Signatures = append(card.Signatures, a2a.AgentCardSignature{
		Protected: protected, Signature: base64.RawURLEncoding.EncodeToString(signature),
	})
	return nil
}

func signingAlgorithm(signer crypto.Signer) (string, error) {
	switch key := signer.Public().(type) {
	case *ecdsa.PublicKey:
		if key.Curve != nil && key.Curve.Params().BitSize == 256 {
			return "ES256", nil
		}
	case *rsa.PublicKey:
		return "RS256", nil
	case ed25519.PublicKey:
		return "EdDSA", nil
	}
	return "", errors.New("unsupported AgentCard signing key; use P-256 ECDSA, RSA, or Ed25519")
}

func cryptoHashForAlgorithm(algorithm string) crypto.SignerOpts {
	if algorithm == "EdDSA" {
		return crypto.Hash(0)
	}
	return crypto.SHA256
}

func decodeProtectedHeader(encoded string) (map[string]any, error) {
	if encoded == "" {
		return nil, errors.New("protected JWS header is empty")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("protected JWS header is not base64url")
	}
	var header map[string]any
	if err := json.Unmarshal(decoded, &header); err != nil {
		return nil, errors.New("protected JWS header is not JSON")
	}
	return header, nil
}

func verifyJWS(header map[string]any, protected, encodedSignature string, payload []byte, key crypto.PublicKey) error {
	algorithm, _ := header["alg"].(string)
	if algorithm == "" || strings.EqualFold(algorithm, "none") {
		return errors.New("unsupported JWS algorithm")
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil {
		return errors.New("JWS signature is not base64url")
	}
	input := []byte(protected + "." + base64.RawURLEncoding.EncodeToString(payload))
	digest := sha256.Sum256(input)
	switch algorithm {
	case "ES256":
		public, ok := key.(*ecdsa.PublicKey)
		if !ok || public.Curve.Params().BitSize != 256 || len(signature) != 64 {
			return errors.New("ES256 key or signature is invalid")
		}
		r := new(big.Int).SetBytes(signature[:32])
		s := new(big.Int).SetBytes(signature[32:])
		if !ecdsa.Verify(public, digest[:], r, s) {
			return errors.New("ES256 signature verification failed")
		}
	case "RS256":
		public, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("RS256 key is invalid")
		}
		if err := rsa.VerifyPKCS1v15(public, crypto.SHA256, digest[:], signature); err != nil {
			return errors.New("RS256 signature verification failed")
		}
	case "EdDSA":
		public, ok := key.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(public, input, signature) {
			return errors.New("EdDSA signature verification failed")
		}
	default:
		return errors.New("unsupported JWS algorithm")
	}
	return nil
}
