package server

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/store"
)

// agentPath is the WSS endpoint agents dial. It joins the fixed route list
// when the operation registry lands (W2-1).
const agentPath = "/v1/agent"

// authorizeTimeout bounds the store lookup during the TLS handshake.
const authorizeTimeout = 5 * time.Second

// AgentTLSConfig is the TLS 1.3 config for the agent endpoint: the server
// presents the control-plane key and accepts only client keys in the store.
func (s *Server) AgentTLSConfig() (*tls.Config, error) {
	cfg, err := keys.ServerTLSConfig(s.key, s.authorizeAgent)
	if err != nil {
		return nil, fmt.Errorf("agent tls config: %w", err)
	}
	return cfg, nil
}

// authorizeAgent accepts a client key only if an enrolled server has it.
func (s *Server) authorizeAgent(pub ed25519.PublicKey) error {
	ctx, cancel := context.WithTimeout(context.Background(), authorizeTimeout)
	defer cancel()
	if _, err := s.st.ServerByPublicKey(ctx, hex.EncodeToString(pub)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errors.New("agent key is not enrolled")
		}
		return fmt.Errorf("look up agent key: %w", err)
	}
	return nil
}

// AgentHandler serves the WSS endpoint. It must sit behind AgentTLSConfig,
// which authenticates the agent. Each binary frame is one envelope: a
// Heartbeat is answered with a HeartbeatAck carrying the same request id, and
// anything else closes the connection.
func (s *Server) AgentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(agentPath, s.handleAgent)
	return mux
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(protocol.MaxEnvelopeSize)
	answerHeartbeats(r.Context(), c)
}

// answerHeartbeats acks Heartbeats until the peer leaves or sends a bad frame.
func answerHeartbeats(ctx context.Context, c *websocket.Conn) {
	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageBinary {
			_ = c.Close(websocket.StatusUnsupportedData, "binary frames only")
			return
		}
		env, err := protocol.Unmarshal(data)
		if err != nil {
			_ = c.Close(websocket.StatusPolicyViolation, "bad envelope")
			return
		}
		if env.GetType() != agentv1.MessageType_HEARTBEAT {
			_ = c.Close(websocket.StatusPolicyViolation, "unexpected message")
			return
		}
		var hb agentv1.Heartbeat
		if err = protocol.Open(env, &hb); err != nil {
			_ = c.Close(websocket.StatusPolicyViolation, "bad heartbeat")
			return
		}
		ack, err := protocol.Wrap(agentv1.MessageType_HEARTBEAT_ACK, env.GetRequestId(), &agentv1.HeartbeatAck{})
		if err != nil {
			return
		}
		frame, err := protocol.Marshal(ack)
		if err != nil {
			return
		}
		if err = c.Write(ctx, websocket.MessageBinary, frame); err != nil {
			return
		}
	}
}

// ServeAgents serves the agent endpoint on ln over TLS 1.3 until ctx ends.
func (s *Server) ServeAgents(ctx context.Context, ln net.Listener) error {
	cfg, err := s.AgentTLSConfig()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           s.AgentHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Agent connections outlive their HTTP request: ending ctx ends them.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		case <-done:
		}
	}()
	err = srv.Serve(tls.NewListener(ln, cfg))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve agents on %s: %w", ln.Addr(), err)
}
