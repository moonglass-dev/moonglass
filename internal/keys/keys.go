// Package keys generates the agent identity (ADR-0003, agent identity only).
package keys

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// AgentIdentity is an agent's Ed25519 signing key and X25519 agreement key.
// The private halves never leave the agent machine.
type AgentIdentity struct {
	SigningPublic  ed25519.PublicKey
	SigningPrivate ed25519.PrivateKey
	AgreePublic    []byte
	AgreePrivate   []byte
}

// GenerateAgentIdentity creates a fresh identity from crypto/rand.
func GenerateAgentIdentity() (AgentIdentity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return AgentIdentity{}, fmt.Errorf("generate ed25519 key: %w", err)
	}
	agree, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return AgentIdentity{}, fmt.Errorf("generate x25519 key: %w", err)
	}
	return AgentIdentity{
		SigningPublic:  pub,
		SigningPrivate: priv,
		AgreePublic:    agree.PublicKey().Bytes(),
		AgreePrivate:   agree.Bytes(),
	}, nil
}

// Fingerprint returns the lowercase hex SHA-256 of a raw Ed25519 public key.
func Fingerprint(signingPublic ed25519.PublicKey) (string, error) {
	if len(signingPublic) != ed25519.PublicKeySize {
		return "", fmt.Errorf("fingerprint: ed25519 public key is %d bytes, expected %d", len(signingPublic), ed25519.PublicKeySize)
	}
	sum := sha256.Sum256(signingPublic)
	return hex.EncodeToString(sum[:]), nil
}
