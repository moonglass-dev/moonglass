// Package agent is the Moonglass agent: enrolment today.
package agent

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// Errors for the typed enrol results. Test them with errors.Is.
var (
	ErrTokenInvalid = errors.New("agent: enrolment token is unknown, expired or malformed")
	ErrTokenUsed    = errors.New("agent: enrolment token was already used")
)

// Enroll registers this machine with the control plane at rawURL using a
// one-time token and returns the agent fingerprint. rawURL must be https:// and
// serverFingerprint is the control-plane fingerprint `moonglass server` printed
// at start: the token is sent only after the TLS 1.3 handshake proved possession
// of that key. It reuses the identity in dataDir or generates one, follows no
// redirects, and writes nothing until the server answers ACCEPTED with the same
// 32-byte control-plane key, which it then pins.
func Enroll(ctx context.Context, dataDir, rawURL, serverFingerprint, token string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("enrol: --url must start with https://: the token is never sent without TLS")
	}
	id, saved, err := keys.LoadAgentIdentity(dataDir)
	if err != nil {
		return "", fmt.Errorf("enrol: %w: remove %s to start over", err, dataDir)
	}
	if !saved {
		if id, err = keys.GenerateAgentIdentity(); err != nil {
			return "", fmt.Errorf("enrol: %w", err)
		}
	}
	fingerprint, err := keys.Fingerprint(id.SigningPublic)
	if err != nil {
		return "", fmt.Errorf("enrol: %w", err)
	}
	// Prove the data dir is writable before the POST spends the token.
	if err = os.MkdirAll(dataDir, 0o700); err != nil {
		return "", fmt.Errorf("enrol: create data dir %s: %w", dataDir, err)
	}
	probe, err := os.CreateTemp(dataDir, "probe-*")
	if err != nil {
		return "", fmt.Errorf("enrol: data dir %s is not writable: %w", dataDir, err)
	}
	_ = probe.Close()
	if err = os.Remove(probe.Name()); err != nil {
		return "", fmt.Errorf("enrol: data dir %s is not writable: %w", dataDir, err)
	}
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:   keys.EnrolClientTLSConfig(serverFingerprint),
			DisableKeepAlives: true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	pin, err := post(ctx, client, rawURL, token, id)
	if err != nil {
		return "", err
	}
	if !saved {
		if err := keys.WriteAgentIdentity(dataDir, id); err != nil {
			return "", fmt.Errorf("enrol: %w", err)
		}
	}
	if err := keys.WriteControlPlanePin(dataDir, ed25519.PublicKey(pin)); err != nil {
		return "", fmt.Errorf("enrol: %w", err)
	}
	if err := keys.WriteFingerprint(dataDir, fingerprint); err != nil {
		return "", fmt.Errorf("enrol: %w", err)
	}
	return fingerprint, nil
}

// post sends the EnrollRequest to base and returns the control-plane public key
// after checking it equals the TLS leaf key the fingerprint check verified.
func post(ctx context.Context, client *http.Client, base, token string, id keys.AgentIdentity) ([]byte, error) {
	sum := sha256.Sum256([]byte(token))
	wrapped, err := protocol.Wrap(agentv1.MessageType_ENROLL_REQUEST, "enroll", &agentv1.EnrollRequest{
		TokenId:               hex.EncodeToString(sum[:]),
		AgentSigningPublicKey: id.SigningPublic,
		AgentAgeRecipient:     id.AgreePublic,
	})
	if err != nil {
		return nil, fmt.Errorf("enrol: build request: %w", err)
	}
	body, err := protocol.Marshal(wrapped)
	if err != nil {
		return nil, fmt.Errorf("enrol: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(base, "/")+"/v1/enroll", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("enrol: bad --url %q: %w", base, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("enrol: reach the control plane at %s (check --url and the network): %w", base, redact(err, token))
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, protocol.MaxEnvelopeSize+1))
	if err != nil {
		return nil, fmt.Errorf("enrol: read response: %w", redact(err, token))
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("enrol: control plane answered HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(raw)))
	}
	env, err := protocol.Unmarshal(raw)
	if err != nil {
		return nil, fmt.Errorf("enrol: malformed response envelope: %w", err)
	}
	var resp agentv1.EnrollResponse
	if err := protocol.Open(env, &resp); err != nil {
		return nil, fmt.Errorf("enrol: response is not an EnrollResponse: %w", err)
	}
	switch resp.GetResult() {
	case agentv1.EnrollResult_ACCEPTED:
		if len(resp.GetControlPlanePublicKey()) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("enrol: control plane sent a %d-byte public key, expected %d: nothing was pinned", len(resp.GetControlPlanePublicKey()), ed25519.PublicKeySize)
		}
		if res.TLS == nil || len(res.TLS.PeerCertificates) == 0 {
			return nil, errors.New("enrol: the response did not arrive over a TLS connection with a verified leaf key: nothing was pinned")
		}
		leaf, err := keys.PeerPublicKey([][]byte{res.TLS.PeerCertificates[0].Raw})
		if err != nil {
			return nil, fmt.Errorf("enrol: read the TLS leaf key: %w", err)
		}
		if !leaf.Equal(ed25519.PublicKey(resp.GetControlPlanePublicKey())) {
			return nil, errors.New("enrol: control plane answered with a key other than the one its TLS certificate proved: nothing was pinned")
		}
		return resp.GetControlPlanePublicKey(), nil
	case agentv1.EnrollResult_TOKEN_USED:
		return nil, fmt.Errorf("enrol: %w: ask for a new token", ErrTokenUsed)
	case agentv1.EnrollResult_TOKEN_INVALID:
		return nil, fmt.Errorf("enrol: %w: ask for a new token", ErrTokenInvalid)
	case agentv1.EnrollResult_ENROLL_RESULT_UNSPECIFIED:
	}
	return nil, fmt.Errorf("enrol: control plane sent unusable result %v", resp.GetResult())
}

// redact keeps the token out of transport errors, which can quote the request.
func redact(err error, token string) error {
	return errors.New(strings.ReplaceAll(err.Error(), token, "[token]"))
}
