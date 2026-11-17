// Package server is the control-plane server: enrolment today.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

const (
	controlPlaneKeyFile = "control-plane.key"
	tokenPrefix         = "mg_"
	tokenSecretLen      = 52
	tokenLifetime       = time.Hour
)

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Server is the control plane: a store and the control-plane Ed25519 identity.
type Server struct {
	st  store.Store
	now func() time.Time
	key ed25519.PrivateKey
}

// New loads the control-plane key from dataDir/control-plane.key, creating it
// (mode 0600) on first use.
func New(st store.Store, dataDir string, now func() time.Time) (*Server, error) {
	key, err := loadOrCreateKey(dataDir)
	if err != nil {
		return nil, err
	}
	return &Server{st: st, key: key, now: now}, nil
}

func loadOrCreateKey(dataDir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dataDir, controlPlaneKeyFile)
	raw, err := os.ReadFile(path) //nolint:gosec // G304: dataDir is the operator's own flag, joined with a fixed file name
	if err == nil {
		if len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("control-plane key %s is %d bytes, expected %d: restore it from backup or remove it to create a new identity", path, len(raw), ed25519.PrivateKeySize)
		}
		return ed25519.PrivateKey(raw), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read control-plane key %s: %w", path, err)
	}
	if err = os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir %s: %w", dataDir, err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate control-plane key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: dataDir is the operator's own flag, joined with a fixed file name
	if err != nil {
		return nil, fmt.Errorf("create control-plane key %s: %w", path, err)
	}
	if _, err := f.Write(priv); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write control-plane key %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close control-plane key %s: %w", path, err)
	}
	return priv, nil
}

// Handler serves the HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/enroll", s.handleEnroll)
	return mux
}

// IssueEnrolToken creates a one-hour, one-use enrolment token for orgID and
// returns its text. Only the hash is stored.
func (s *Server) IssueEnrolToken(ctx context.Context, orgID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate enrolment token: %w", err)
	}
	token := tokenPrefix + strings.ToLower(tokenEncoding.EncodeToString(b))
	if err := s.st.AddEnrolmentToken(ctx, orgID, store.HashToken(token), s.now().Add(tokenLifetime)); err != nil {
		return "", fmt.Errorf("store enrolment token: %w", err)
	}
	return token, nil
}

// Run opens dataDir/server.db, creates an org if there is none, and serves
// until ctx ends. With printEnrolToken it issues one token and prints it to stdout.
func Run(ctx context.Context, dataDir, listen string, printEnrolToken bool, stdout io.Writer) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data dir %s: %w", dataDir, err)
	}
	st, err := sqlite.Open(ctx, filepath.Join(dataDir, "server.db"))
	if err != nil {
		return fmt.Errorf("open store in %s: %w", dataDir, err)
	}
	defer func() { _ = st.Close() }()
	s, err := New(st, dataDir, time.Now)
	if err != nil {
		return err
	}
	if printEnrolToken {
		var orgID, token string
		if orgID, err = firstOrg(ctx, st, dataDir); err != nil {
			return err
		}
		if token, err = s.IssueEnrolToken(ctx, orgID); err != nil {
			return err
		}
		if _, err = fmt.Fprintln(stdout, token); err != nil {
			return fmt.Errorf("print enrolment token: %w", err)
		}
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		case <-done:
		}
	}()
	err = srv.Serve(ln)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve %s: %w", listen, err)
}

// firstOrg returns the org id stored in dataDir/org, creating the org the first time.
func firstOrg(ctx context.Context, st store.Store, dataDir string) (string, error) {
	path := filepath.Join(dataDir, "org")
	raw, err := os.ReadFile(path) //nolint:gosec // G304: dataDir is the operator's own flag, joined with a fixed file name
	if err == nil {
		return strings.TrimSpace(string(raw)), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read org id %s: %w", path, err)
	}
	orgID, err := st.CreateOrg(ctx)
	if err != nil {
		return "", fmt.Errorf("create org: %w", err)
	}
	if err := os.WriteFile(path, []byte(orgID+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write org id %s: %w", path, err)
	}
	return orgID, nil
}
