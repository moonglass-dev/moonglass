---
verdict: ready
reason: ""
---

# Plan for FR1-1

Context: R-1 finding 1. The enrolment token and owner tokens cross `--listen` in clear, and the agent pins whatever key answers the URL. The owner decision of 2026-11-18 (`docs/specs/enrolment.md` "Transport security", `docs/specs/connection.md` "Transport") is already in the specs. This ticket builds it. No contract, ADR or module change is needed: it uses only `crypto/tls`, `net`, `net/url` and `net/http`, the registry change is one synopsis string, and the schemas stay as they are.

Size: non-test, non-doc lines come to about 150, under the 250 cap. Every existing test file this plan edits is on `ownerTestEdits["FR1-1"]` in `tools/pipeline/verify.go`: `internal/agent/enroll_test.go`, `internal/agent/connect_test.go`, `internal/server/enroll_test.go`, `internal/server/owner_token_test.go`, `cmd/moonglass/enroll_test.go` and `internal/proxy/memory_test.go`. No other existing test file may lose a line. New tests go in new files, or are only added to files outside that list (`internal/keys/tls_test.go`, `internal/cli/*_test.go`, `cmd/moonglass/server_test.go`).

## Files to change

- `internal/keys/tls.go`
  - `func ListenerTLSConfig(priv ed25519.PrivateKey) (*tls.Config, error)`: `SelfSignedCert(priv)` as the only certificate, `MinVersion: tls.VersionTLS13`, `SessionTicketsDisabled: true`, `ClientAuth` left at `tls.NoClientCert`, no `NextProtos`.
  - `func EnrolClientTLSConfig(fingerprint string) *tls.Config`: `MinVersion: tls.VersionTLS13`, `InsecureSkipVerify: true` with the same `//nolint:gosec // G402` justification as `ClientTLSConfig`, no `ClientSessionCache`. Its `VerifyPeerCertificate` (with `//nolint:gosec // G123`, as in the existing code) does `PeerPublicKey(rawCerts)` and then `Fingerprint(key)`, and requires an exact `==` match with `fingerprint`, so an empty string or an uppercase copy fails. On a mismatch it fails with `errors.New("control plane key does not match the server fingerprint: copy the fingerprint moonglass server printed at start")`.
- `internal/server/server.go`
  - `Server` gains a `fingerprint string` field. `New` computes it once with `keys.Fingerprint(key.Public().(ed25519.PublicKey))` and returns the error wrapped.
  - `func (s *Server) Fingerprint() string`, and `func (s *Server) TLSConfig() (*tls.Config, error) { return keys.ListenerTLSConfig(s.key) }`. Tests in other packages use these to serve `Handler()` the way production does, without reading `control-plane.key`.
  - `Run(ctx, dataDir, listen, agentListen string, printEnrolToken bool, ownerTokenName string, stdout io.Writer) error` keeps its signature. It now only does `net.Listen` for both addresses (closing the first if the second fails) and returns `serve(ctx, dataDir, ln, agentLn, printEnrolToken, ownerTokenName, stdout)`.
  - New unexported `serve(ctx context.Context, dataDir string, ln, agentLn net.Listener, printEnrolToken bool, ownerTokenName string, stdout io.Writer) error` holds today's body of `Run`. It defers closing both listeners. Its order is: create the data dir, open the store, `New`, print `s.Fingerprint()` with `fmt.Fprintln` as the first stdout line on every start, then the existing token block unchanged, then `cfg, err := s.TLSConfig()`, then serve the API on `tlsListener{Listener: ln, cfg: cfg}` and the agents on `agentLn`.
  - `serveEnrol` is renamed `serveAPI(ctx, handler, ln net.Listener)` with the comment "serves the API over TLS 1.3". The plain-HTTP comment and path go, and the error says `serve %s` with `ln.Addr()`.
  - New `tlsListener struct{ net.Listener; cfg *tls.Config }`, whose `Accept` returns `tlsConn{tls.Server(c, l.cfg)}`, and `tlsConn struct{ net.Conn }`. The comment says why: `net/http` answers a plain-HTTP request on a `*tls.Conn` with an unencrypted `400 Client sent an HTTP request to an HTTPS server` (`net/http/server.go`, `conn.serve`). Hiding the type means a failed handshake just closes the connection, so a plain-HTTP client gets no HTTP response. The handshake runs on the first Read, under `ReadHeaderTimeout`. `r.TLS` is nil, and no handler reads it.
  - The package comment and the `Run` doc say TLS 1.3 on `listen`.
- `internal/agent/enroll.go`
  - The signature becomes `func Enroll(ctx context.Context, dataDir, rawURL, serverFingerprint, token string) (string, error)`, and the `*http.Client` parameter is gone.
  - The first statement, before `LoadAgentIdentity` and `MkdirAll`, is `u, err := url.Parse(rawURL)`. If `err != nil || u.Scheme != "https" || u.Host == ""`, it returns `errors.New("enrol: --url must start with https://: the token is never sent without TLS")`. Neither the URL nor the token appears in that error.
  - Enroll builds its own client: `&http.Client{Transport: &http.Transport{TLSClientConfig: keys.EnrolClientTLSConfig(serverFingerprint), DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}`. The proxy is nil, so the connection is direct. A 3xx then hits the existing "answered HTTP %d" error.
  - `post`'s `url` parameter is renamed `base`. In the `ACCEPTED` branch, after the length check, it gets the verified leaf key with `keys.PeerPublicKey([][]byte{res.TLS.PeerCertificates[0].Raw})`, guarding `res.TLS == nil || len(PeerCertificates) == 0`. If `!leaf.Equal(ed25519.PublicKey(resp.GetControlPlanePublicKey()))`, it returns `enrol: control plane answered with a key other than the one its TLS certificate proved: nothing was pinned`. Nothing is written, because `post` returns before any write.
- `internal/cli/cli.go`
  - In `runGenerated`, right after the empty `MOONGLASS_URL`/`MOONGLASS_TOKEN` check, `if err := checkURL(base); err != nil { fmt.Fprintf(stderr, "moonglass: %s: %v\n", c.Name, err); return 2 }`, before `encode` and before any request.
  - `func checkURL(raw string) error`: parses with `url.Parse`. It allows `https` when the host is non-empty, and `http` only when `u.Hostname() == "localhost"` or `net.ParseIP(host)` is a loopback address. Everything else gets `errors.New("MOONGLASS_URL must start with https://; http:// is allowed only for localhost or a loopback IP address")`. The URL and the token are never echoed. `https` keeps `http.DefaultClient`, so standard verification applies.
- `cmd/moonglass/main.go`
  - `runAgentEnroll` gains `serverFP := fs.String("server-fingerprint", "", "control-plane key fingerprint printed by moonglass server")`. A missing one (`*serverFP == ""`) joins the usage/exit-2 condition. The call becomes `agent.Enroll(context.Background(), *dataDir, *url, *serverFP, fs.Arg(0))`, and the `net/http` import is dropped.
  - The `--listen` help becomes `"API and enrolment listen address (TLS 1.3)"`.
- `internal/registry/routes.go`: the `agent enroll` synopsis becomes `agent enroll [--data-dir DIR] --url URL --server-fingerprint FP TOKEN`.
- `docs/specs/enrolment.md`
  - Intro: transport is TLS 1.3 on `--listen` (FR1-1).
  - "HTTP endpoint": delete "TLS is not required on loopback tests; production TLS and `wss` are W1-4." and the sentence "The token travels in an `Authorization` header over plain HTTP… W1-4." Replace them with: the token travels only inside TLS 1.3, and the agent follows no redirects.
  - "Commands": `moonglass server` prints the control-plane fingerprint as its first stdout line on every start, before any token line, and the line order is updated. The `agent enroll` synopsis gets `--server-fingerprint FP`. The agent refuses a URL that is not `https://` before generating keys or sending anything, and a missing `--server-fingerprint` prints the usage and exits 2.
  - "Transport security": the pinned key is the TLS leaf key verified against FP, and it must equal `control_plane_public_key` or nothing is written. "Localhost" means the host `localhost` or a loopback IP literal. Plain HTTP to `--listen` is closed without an HTTP response. Out of scope, stated plainly: a wrong FP copied by the operator, and a compromised control plane holding the real key (threat model §4).
- `docs/specs/registry.md`, "Generation": the runner accepts `https://` `MOONGLASS_URL` with standard certificate verification, and `http://` only for `localhost` or a loopback IP literal. Anything else exits 2 with a message naming `MOONGLASS_URL`, and nothing is sent.
- `docs/specs/connection.md`: the FR1-1 paragraph gets the same "leaf key equals `control_plane_public_key`" wording, and nothing else.
- `testdata/tests.txt`: `make test-list`.

## Tests to write

New test files, so no existing lines are lost:

- `./internal/keys TestListenerTLSConfigNeedsNoClientCert` (`internal/keys/listener_tls_test.go`). It uses a `handshake(t, serverCfg, clientCfg) (tls.ConnectionState, error)` helper over a `127.0.0.1:0` TCP listener, with 5 s deadlines and both sides closed afterwards. A client built from `EnrolClientTLSConfig(fp)` with no certificate completes the handshake. The test checks `Version == tls.VersionTLS13`, that `PeerCertificates[0]`'s key equals `priv.Public()`, and that the server side saw no peer certificates. A clone with `MinVersion = MaxVersion = tls.VersionTLS12` fails.
- `./internal/keys TestEnrolClientTLSConfigChecksFingerprint` (same file). Table-driven against a server presenting K: `Fingerprint(K)` succeeds. These fail: the fingerprint of another key, `""`, `strings.ToUpper(Fingerprint(K))`, and a server config cloned with `MinVersion = MaxVersion = TLS12` paired with the correct fingerprint. Messages show the case name, the fingerprint and the error.
- `./internal/server TestRunServesListenOnlyOverTLS` (`internal/server/listen_tls_test.go`). It opens two `127.0.0.1:0` listeners and runs `serve(ctx, dir, ln, agentLn, false, "", io.Discard)` in a goroutine.
  - A plain `http.Client` (5 s timeout) `POST http://<addr>/v1/enroll` returns an error and a nil response.
  - A client built from `keys.EnrolClientTLSConfig(fp)`, where `fp` is `keys.Fingerprint` of the public half of `dir/control-plane.key` read after start, gets HTTP 400 on `POST https://<addr>/v1/enroll` with an empty body, and HTTP 401 on `GET https://<addr>/v1/servers`.
  - Both responses have `res.TLS.Version == tls.VersionTLS13` and a leaf key equal to the key from the file.
  - Cancelling ctx makes `serve` return nil.
- `./internal/server TestRunPrintsControlPlaneFingerprintFirst` (same file). It uses the new `runServerOutput` helper (below). For `(false, "")`, `(true, "")`, `(false, "ci")` and `(true, "ci")`, `lines[0]` is exactly the fingerprint from the file (64 lowercase hex characters, nothing else), and the remaining lines are the token count expected. With both flags, `lines[1]` is the enrolment token (`EnrolmentTokens` has it, and `Authenticate` rejects it) and `lines[2]` is the owner token. A second run on the same dir prints the same `lines[0]`.
- `./internal/agent TestEnrollPinsFingerprintVerifiedKey` (`internal/agent/enroll_tls_test.go`). `Enroll(ctx, dir, e.ts.URL, e.fp, tok)` succeeds. `control-plane.pub` equals the leaf key from a `tls.Dial` with `EnrolClientTLSConfig(e.fp)` to `e.ts.Listener.Addr()`, and equals `e.srv`'s key.
- `./internal/agent TestEnrollFingerprintMismatchSendsNoToken` (same file). The env's handler is wrapped in a counter. With the fingerprint of a fresh key, the enrol fails, the counter is 0, `snapshot(dir)` is empty, and the error and the formatted messages don't contain the token. A later `Enroll` with `e.fp` and the same token, into a new dir, succeeds, which shows the token was unused.
- `./internal/agent TestEnrollRefusesPlainHTTP` (same file). A live `httptest.NewServer` handler counts requests. Table cases: `http://localhost:PORT`, `http://127.0.0.1:PORT`, `http://example.invalid`, `HTTP://127.0.0.1:PORT`, `127.0.0.1:PORT` with no scheme, and `wss://…`. Each enrol fails with an error containing `https://` and not the token, the counter stays 0, and `snapshot(dir)` is empty.
- `./internal/agent TestEnrollRejectsResponseKeyOtherThanTLSKey` (same file). An `httptest.NewUnstartedServer` has `TLS = keys.ListenerTLSConfig(privA)` and is started with `StartTLS`. Its handler decodes the envelope and answers `ACCEPTED` with key B. `Enroll` with `Fingerprint(A)` fails, and neither `control-plane.pub` nor `fingerprint` exists in the dir (`os.Stat` gives `fs.ErrNotExist`).
- `./internal/cli TestGeneratedRunnerRefusesPlainHTTPExceptLocalhost` (`internal/cli/url_test.go`). The runner is called via `Run` with an injected env and the `servers list` subcommand.
  - `http://localhost:PORT`, `http://127.0.0.1:PORT` and `http://[::1]:PORT` reach the loopback server, exit 0 and send the bearer. The `[::1]` case uses its own `net.Listen("tcp", "[::1]:0")` server and is skipped only if `::1` cannot be bound.
  - `http://example.invalid` and `http://10.0.0.1:PORT` exit 2, stderr contains `MOONGLASS_URL` and not the token, and the server counter stays 0.
  - `httptest.NewTLSServer`, which is untrusted by the system roots, exits 1, and the handler is never called.
- `./cmd/moonglass TestAgentEnrollNeedsServerFingerprint` (`cmd/moonglass/enroll_fingerprint_test.go`). `run([]string{"agent","enroll","--data-dir",dir,"--url","https://127.0.0.1:1",token})`, where `dir := filepath.Join(t.TempDir(),"agent")`, exits 2. Stderr equals `usage`, stdout is empty, `dir` does not exist, and the `agent enroll` line of `usage` contains `--server-fingerprint FP`.

Edits to existing tests, all on the FR1-1 allow-list, change only the transport and the line count:

- `internal/agent/enroll_test.go`
  - `newEnv` serves `httptest.NewUnstartedServer(s.Handler())` with `ts.TLS, _ = s.TLSConfig()` and `StartTLS`, and adds `fp: s.Fingerprint()` to `env`.
  - Every `Enroll(ctx, e.ts.Client(), dir, e.ts.URL, tok)` becomes `Enroll(ctx, dir, e.ts.URL, e.fp, tok)`.
  - `rawEnroll` uses a client with `keys.EnrolClientTLSConfig(fp)`. It takes `fp` as a parameter, or becomes a method on `env`.
- `internal/agent/connect_test.go:491-498`: same TLS server, and `Enroll(ctx, agentDir, enrolSrv.URL, s.Fingerprint(), token)`.
- `internal/proxy/memory_test.go:122-129`: same change.
- `cmd/moonglass/enroll_test.go`: `newTestServer` returns `(url, fingerprint, token)` from a TLS server. The args gain `--server-fingerprint`, and the "missing token" case keeps `--server-fingerprint` so usage is still for the missing token.
- `internal/server/enroll_test.go`: `newFixture` and the reopen case's `ts2` use the TLS server. `post` uses a client from `keys.EnrolClientTLSConfig(srv.Fingerprint())`.
- `internal/server/owner_token_test.go`: the current `runServer` body becomes `runServerOutput(t, dir, printEnrol, ownerName) []string`, which reads `wantLines+1` lines, fingerprint first. `runServer` becomes `return runServerOutput(...)[1:]`, so the existing callers and their assertions stay as they are.

## Edge cases

- `http://`, an uppercase scheme, a missing scheme, `wss://`, an empty host, or a URL that does not parse at enrol: refused before keys, the data dir or the network are touched (`TestEnrollRefusesPlainHTTP`).
- A wrong, empty or uppercase fingerprint: the handshake fails before any HTTP bytes, so the token is never written to the socket (`TestEnrolClientTLSConfigChecksFingerprint`, `TestEnrollFingerprintMismatchSendsNoToken`). An empty `--server-fingerprint` at the CLI is caught earlier, with usage and exit 2 (`TestAgentEnrollNeedsServerFingerprint`).
- A server offering only TLS 1.2, or a client offering only TLS 1.2: the handshake fails (both keys tests).
- A hostile server that proves key A and sends key B in `ACCEPTED`: refused, nothing pinned (`TestEnrollRejectsResponseKeyOtherThanTLSKey`). A nil `res.TLS` or empty `PeerCertificates` (impossible over https, but guarded) is an error, not a panic.
- A 3xx from the enrol URL: not followed, and reported as an HTTP status error (`CheckRedirect` returns `ErrUseLastResponse`). Not separately named. It is covered by the existing non-200 path, so no new test is needed beyond review.
- Session resumption bypassing `VerifyPeerCertificate`: no client session cache, and server tickets are disabled (`ListenerTLSConfig`). `TestListenerTLSConfigNeedsNoClientCert` also asserts `SessionTicketsDisabled`.
- A plain-HTTP request to `--listen`: no HTTP response, thanks to the `tlsConn` wrapper (`TestRunServesListenOnlyOverTLS`).
- `MOONGLASS_URL` set to `http://` with a non-loopback host, or to a hostname that merely resolves to loopback (for example `http://127.0.0.1.nip.io`): refused, because only the literal `localhost` or a loopback IP literal is allowed. An untrusted `https` certificate gives exit 1 with no request (`TestGeneratedRunnerRefusesPlainHTTPExceptLocalhost`).
- Token in output: none of the new error strings interpolates the token or the URL's userinfo. Each refusal test asserts that the token is absent from the error, stdout and stderr.
- If a listener fails to bind, no token is minted (listen now happens before `serve`), and the error names the address.

## Risks

- **The tests-kept gate.** Removing a single line from an existing test file outside the FR1-1 allow-list fails `scripts/verify`. All new tests go in new files, and the `runServer` change keeps old callers byte-identical. `cmd/moonglass/server_test.go` and `internal/cli/*_test.go` existing files are not edited.
- **Plain-HTTP 400 from net/http.** Plain `srv.Serve(tls.NewListener(...))` or `ServeTLS` would answer a plain-HTTP request with a cleartext 400, which fails the "no HTTP response" criterion. The `tlsConn` wrapper is the deliberate fix, documented in a comment, so a reviewer does not "simplify" it back.
- **Locating Run's bound address.** `Run` with `:0` cannot report its port without adding a stdout line, and the spec fixes stdout to the fingerprint and tokens. Splitting it into `Run` (listen) and `serve` (everything else) lets the test pass its own `:0` listeners without a test hook or a global.
- **The 24 h carrier certificate on a long-running listener.** The enrol client ignores expiry, so enrolment keeps working. Browsers and the CLI cannot use this certificate anyway, and rotation and a CA front are out of scope. The spec text says so rather than claiming more.
- **The CLI against `moonglass server` directly.** `--listen` no longer speaks HTTP, and its certificate is not CA-trusted, so the generated CLI reaches it only through a trusted TLS front. That is the owner decision, and existing CLI tests use their own loopback `httptest` servers and stay green.
- **Lint.** `InsecureSkipVerify` and `VerifyPeerCertificate` carry the same `nolint:gosec` G402 and G123 justifications already accepted in `ClientTLSConfig`. Test-side TLS 1.2 configs clone the package configs instead of setting `InsecureSkipVerify` in tests.
- **Diff budget.** About 150 counted lines, against a cap of 250. No shared test-helper production code is added, and each test package carries its own small TLS-server helper.
- **Trusted core (`internal/keys`).** The two new functions mirror `ClientTLSConfig` and `ServerTLSConfig` line for line, so the owner reviews a small, familiar shape.
