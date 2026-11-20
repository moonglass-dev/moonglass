// Package server is the control-plane server: enrolment and the registry REST
// API, served over TLS 1.3 on one listener.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
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

	"moonglass.dev/moonglass/internal/api"
	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

const (
	controlPlaneKeyFile = "control-plane.key"
	tokenPrefix         = "mg_"
	tokenSecretLen      = 52
	tokenLifetime       = time.Hour

	ownerTokenLifetime = 90 * 24 * time.Hour
	localOwnerIdentity = "local:owner"
)

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// Server is the control plane: a store and the control-plane Ed25519 identity.
type Server struct {
	st          store.Store
	reg         *registry.Registry
	fingerprint string
	now         func() time.Time
	key         ed25519.PrivateKey
}

// New loads the control-plane key from dataDir/control-plane.key, creating it
// (mode 0600) on first use.
func New(st store.Store, dataDir string, now func() time.Time) (*Server, error) {
	key, err := loadOrCreateKey(dataDir)
	if err != nil {
		return nil, err
	}
	fingerprint, err := keys.Fingerprint(key.Public().(ed25519.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("fingerprint the control-plane key: %w", err)
	}
	return &Server{st: st, reg: registry.New(st, now), key: key, now: now, fingerprint: fingerprint}, nil
}

// Fingerprint is the lowercase hex SHA-256 of the control-plane public key. Run
// prints it as its first stdout line so the operator can pass it to
// `moonglass agent enroll --server-fingerprint`.
func (s *Server) Fingerprint() string { return s.fingerprint }

// TLSConfig is the TLS 1.3 config the API listener presents: the control-plane
// key in a self-signed certificate, with no client certificate requested.
func (s *Server) TLSConfig() (*tls.Config, error) { return keys.ListenerTLSConfig(s.key) }

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

// mount builds the mux and the "METHOD path" patterns on it: the enrolment
// route and every registry operation, each mounted through one helper.
func (s *Server) mount() (*http.ServeMux, []string) {
	mux := http.NewServeMux()
	var routes []string
	add := func(pattern string, h http.Handler) {
		mux.Handle(pattern, h)
		routes = append(routes, pattern)
	}
	add("POST /v1/enroll", http.HandlerFunc(s.handleEnroll))
	apiHandler := api.New(s.reg)
	for _, spec := range registry.Operations() {
		add(spec.Method+" "+spec.Path, apiHandler)
	}
	return mux, routes
}

// Routes lists the "METHOD path" patterns Handler mounts.
func (s *Server) Routes() []string {
	_, routes := s.mount()
	return routes
}

// Handler serves the HTTP API: enrolment and the registry operations, each
// behind http.CrossOriginProtection, which refuses a cross-site browser request
// with an unsafe method with 403 before any handler runs. It has no trusted
// origins and no bypass patterns. The CLI sends no Origin or Sec-Fetch-Site, so
// it is unaffected.
func (s *Server) Handler() http.Handler {
	mux, _ := s.mount()
	return http.NewCrossOriginProtection().Handler(mux)
}

// MintOwnerToken creates an owner token (scope "* * *") for userID and returns
// its text, which is shown only now.
func (s *Server) MintOwnerToken(ctx context.Context, orgID, userID, name string, expires time.Time) (string, error) {
	return registry.MintToken(ctx, s.st, orgID, userID, name, []registry.Scope{{Project: "*", Environment: "*", Operation: "*"}}, expires, s.now())
}

// IssueOwnerToken creates an owner token named name, expiring 90 days after
// issue, for the owner of orgID, and returns its text, which is shown only now.
// When the org has no owner yet it first mints the user behind
// localOwnerIdentity and gives it the owner role.
func (s *Server) IssueOwnerToken(ctx context.Context, orgID, name string) (string, error) {
	if name == "" {
		return "", errors.New("issue owner token: name is empty: pass --print-owner-token NAME, named after where the token will be used")
	}
	members, err := s.st.Memberships(ctx, orgID)
	if err != nil {
		return "", fmt.Errorf("issue owner token: list memberships of org %s: %w", orgID, err)
	}
	userID := ""
	for _, m := range members {
		if m.Role == store.RoleOwner {
			userID = m.UserID
			break
		}
	}
	if userID == "" {
		userID, err = s.st.UserForIdentity(ctx, localOwnerIdentity)
		if err != nil {
			return "", fmt.Errorf("issue owner token: create local owner: %w", err)
		}
		if err := s.st.AddMembership(ctx, orgID, userID, store.RoleOwner); err != nil {
			return "", fmt.Errorf("issue owner token: make local owner the owner of org %s: %w", orgID, err)
		}
	}
	return s.MintOwnerToken(ctx, orgID, userID, name, s.now().Add(ownerTokenLifetime))
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

// Run listens on listen and agentListen and then calls serve. The API listener
// speaks TLS 1.3 only. A bind failure is an error naming the address, and no
// token is minted before both listeners are open.
func Run(ctx context.Context, dataDir, listen, agentListen string, printEnrolToken bool, ownerTokenName string, stdout io.Writer) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	agentLn, err := net.Listen("tcp", agentListen)
	if err != nil {
		_ = ln.Close()
		return fmt.Errorf("listen for agents on %s: %w", agentListen, err)
	}
	return serve(ctx, dataDir, ln, agentLn, printEnrolToken, ownerTokenName, stdout)
}

// serve opens dataDir/server.db, creates an org if there is none, prints the
// control-plane fingerprint as the first stdout line, then serves until ctx
// ends: the API over TLS 1.3 on ln and agents over TLS 1.3 WSS on agentLn. With
// printEnrolToken it issues one token and prints it after the fingerprint; with
// a non-empty ownerTokenName it also issues one owner token and prints it
// there. An empty ownerTokenName means no owner token is requested.
func serve(ctx context.Context, dataDir string, ln, agentLn net.Listener, printEnrolToken bool, ownerTokenName string, stdout io.Writer) error {
	defer func() { _ = ln.Close() }()
	defer func() { _ = agentLn.Close() }()
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
	if _, err = fmt.Fprintln(stdout, s.Fingerprint()); err != nil {
		return fmt.Errorf("print control-plane fingerprint: %w", err)
	}
	if printEnrolToken || ownerTokenName != "" {
		var orgID, token string
		if orgID, err = firstOrg(ctx, st, dataDir); err != nil {
			return err
		}
		if printEnrolToken {
			if token, err = s.IssueEnrolToken(ctx, orgID); err != nil {
				return err
			}
			if _, err = fmt.Fprintln(stdout, token); err != nil {
				return fmt.Errorf("print enrolment token: %w", err)
			}
		}
		if ownerTokenName != "" {
			if token, err = s.IssueOwnerToken(ctx, orgID, ownerTokenName); err != nil {
				return err
			}
			if _, err = fmt.Fprintln(stdout, token); err != nil {
				return fmt.Errorf("print owner token: %w", err)
			}
		}
	}
	cfg, err := s.TLSConfig()
	if err != nil {
		return fmt.Errorf("configure TLS on %s: %w", ln.Addr(), err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, 2)
	go func() { errs <- s.ServeAgents(ctx, agentLn) }()
	go func() { errs <- serveAPI(ctx, s.Handler(), tlsListener{Listener: ln, cfg: cfg}) }()
	first := <-errs
	cancel()
	return errors.Join(first, <-errs)
}

// tlsListener wraps every accepted connection in TLS without exposing the
// *tls.Conn type to net/http. net/http answers a plain-HTTP request on a
// *tls.Conn with an unencrypted "400 Client sent an HTTP request to an HTTPS
// server" (net/http/server.go, conn.serve). Hiding the type means net/http
// treats the connection as plain HTTP, its write fails on the TLS handshake,
// and a plain-HTTP client gets no HTTP response at all.
type tlsListener struct {
	net.Listener
	cfg *tls.Config
}

// Accept returns the connection wrapped in TLS. The handshake runs on the first
// Read, under the http.Server's ReadHeaderTimeout.
func (l tlsListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return tlsConn{tls.Server(c, l.cfg)}, nil
}

// tlsConn reports only net.Conn, so net/http cannot see a *tls.Conn.
type tlsConn struct {
	net.Conn
}

// serveAPI serves the API over TLS 1.3 on ln until ctx ends.
func serveAPI(ctx context.Context, handler http.Handler, ln net.Listener) error {
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
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
	err := srv.Serve(ln)
	close(done)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve %s: %w", ln.Addr(), err)
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
