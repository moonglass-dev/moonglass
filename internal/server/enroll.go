package server

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/store"
)

const keyLen = 32

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, protocol.MaxEnvelopeSize))
	if err != nil {
		http.Error(w, "enroll: cannot read request body", http.StatusBadRequest)
		return
	}
	env, err := protocol.Unmarshal(body)
	if err != nil {
		http.Error(w, "enroll: malformed envelope", http.StatusBadRequest)
		return
	}
	var req agentv1.EnrollRequest
	if err = protocol.Open(env, &req); err != nil {
		http.Error(w, "enroll: request is not an EnrollRequest", http.StatusBadRequest)
		return
	}
	if len(req.GetAgentSigningPublicKey()) != keyLen || len(req.GetAgentAgeRecipient()) != keyLen {
		http.Error(w, "enroll: agent public keys must be exactly 32 bytes", http.StatusBadRequest)
		return
	}

	result, err := s.enrol(r, &req)
	if err != nil {
		http.Error(w, "enroll: the control plane could not use its store; see its logs and retry", http.StatusInternalServerError)
		return
	}
	resp := &agentv1.EnrollResponse{Result: result}
	if result == agentv1.EnrollResult_ACCEPTED {
		resp.ControlPlanePublicKey = s.key.Public().(ed25519.PublicKey)
	}
	out, err := protocol.Wrap(agentv1.MessageType_ENROLL_RESPONSE, env.GetRequestId(), resp)
	if err != nil {
		http.Error(w, "enroll: internal error", http.StatusInternalServerError)
		return
	}
	data, err := protocol.Marshal(out)
	if err != nil {
		http.Error(w, "enroll: internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

// enrol checks the bearer token and registers the agent. A non-nil error is a
// store failure and becomes HTTP 500.
func (s *Server) enrol(r *http.Request, req *agentv1.EnrollRequest) (agentv1.EnrollResult, error) {
	token, ok := bearer(r)
	if !ok || req.GetTokenId() != store.HashToken(token) {
		return agentv1.EnrollResult_TOKEN_INVALID, nil
	}
	orgID, err := s.st.UseEnrolmentToken(r.Context(), store.HashToken(token), s.now())
	switch {
	case err == nil:
	case errors.Is(err, store.ErrTokenUsed):
		return agentv1.EnrollResult_TOKEN_USED, nil
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrTokenExpired):
		return agentv1.EnrollResult_TOKEN_INVALID, nil
	default:
		return agentv1.EnrollResult_TOKEN_INVALID, err
	}
	// The token is already spent: a duplicate key is reported as TOKEN_INVALID
	// and the operator issues a fresh token.
	_, err = s.st.AddServer(r.Context(), orgID, hex.EncodeToString(req.GetAgentSigningPublicKey()))
	switch {
	case err == nil:
		return agentv1.EnrollResult_ACCEPTED, nil
	case errors.Is(err, store.ErrDuplicate):
		return agentv1.EnrollResult_TOKEN_INVALID, nil
	default:
		return agentv1.EnrollResult_TOKEN_INVALID, err
	}
}

// bearer extracts a well-formed enrolment token from the Authorization header.
func bearer(r *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(token) != len(tokenPrefix)+tokenSecretLen || !strings.HasPrefix(token, tokenPrefix) {
		return "", false
	}
	return token, true
}
