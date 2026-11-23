package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"moonglass.dev/moonglass/internal/keys"
	"moonglass.dev/moonglass/internal/protocol"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// Connection timing (docs/specs/connection.md).
const (
	defaultHeartbeatInterval = 30 * time.Second
	ackTimeout               = 10 * time.Second
	backoffInitial           = time.Second
	backoffCap               = 30 * time.Second
	agentPath                = "/v1/agent"
)

// dialTimeout bounds TCP connect, the TLS handshake and the WebSocket upgrade.
var dialTimeout = 15 * time.Second

// ConnectOptions tunes Connect. Zero values mean the production behaviour.
type ConnectOptions struct {
	// Rand draws the backoff jitter; nil means a time-seeded source.
	Rand *rand.Rand
	// Sleep waits d or returns early with ctx's error; nil means a real timer.
	Sleep func(ctx context.Context, d time.Duration) error
	// OnError is told why each connection ended; nil ignores them.
	OnError func(error)
	// Executor runs each accepted command; it is required.
	Executor Executor
	// AgentVersion is sent in every Heartbeat.
	AgentVersion string
	// HeartbeatInterval is the time between Heartbeats; zero means 30 seconds.
	HeartbeatInterval time.Duration
}

// Connect dials the control plane at rawURL over outbound TLS 1.3 WSS with the
// keys and pin saved by Enroll, sends Heartbeats, and reconnects with
// exponential backoff and full jitter until ctx is cancelled. It reads the
// data dir and never writes it.
func Connect(ctx context.Context, dataDir, rawURL string, opts ConnectOptions) error {
	if opts.Executor == nil {
		return errors.New("connect: ConnectOptions.Executor is nil: pass the executor that runs the deploy commands")
	}
	priv, err := keys.LoadAgentSigningKey(dataDir)
	if err != nil {
		return connectKeyErr(err, dataDir)
	}
	pin, err := keys.LoadControlPlanePin(dataDir)
	if err != nil {
		return connectKeyErr(err, dataDir)
	}
	endpoint, err := agentURL(rawURL)
	if err != nil {
		return err
	}
	tlsCfg, err := keys.ClientTLSConfig(priv, pin)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = defaultHeartbeatInterval
	}
	if opts.Rand == nil {
		opts.Rand = rand.New(rand.NewSource(time.Now().UnixNano())) //nolint:gosec // G404: jitter needs spread, not secrecy
	}
	if opts.Sleep == nil {
		opts.Sleep = sleep
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	transport := &http.Transport{
		TLSClientConfig:       tlsCfg,
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   dialTimeout,
		ResponseHeaderTimeout: dialTimeout,
	}
	client := &http.Client{Transport: pinnedHost{base: transport, host: u.Host}}
	defer client.CloseIdleConnections()

	// The dispatcher outlives every session, so a command survives a drop.
	d := newDispatcher(ctx, opts.OnError)
	defer d.wait()

	attempt := 0
	for {
		acked, serr := session(ctx, client, endpoint, opts, d)
		if ctx.Err() != nil {
			return nil
		}
		if serr != nil && opts.OnError != nil {
			opts.OnError(serr)
		}
		if acked {
			attempt = 0
		}
		delay := backoffDelay(attempt, opts.Rand)
		attempt++
		if opts.Sleep(ctx, delay) != nil {
			return nil
		}
	}
}

// pinnedHost refuses every request that is not https to the one control-plane
// host, so a redirect can never move the agent to plaintext or another peer.
type pinnedHost struct {
	base *http.Transport
	host string
}

func (p pinnedHost) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || r.URL.Host != p.host {
		return nil, fmt.Errorf("connect: refusing %s://%s: the agent only talks https to %s", r.URL.Scheme, r.URL.Host, p.host)
	}
	return p.base.RoundTrip(r)
}

func (p pinnedHost) CloseIdleConnections() { p.base.CloseIdleConnections() }

type frame struct {
	err  error
	data []byte
	typ  websocket.MessageType
}

// session runs one connection: it sends Heartbeats, reads every frame for the
// connection's life and hands accepted commands to the dispatcher. acked
// reports whether at least one Heartbeat was acknowledged, which resets the
// backoff.
func session(ctx context.Context, client *http.Client, endpoint string, opts ConnectOptions, d *dispatcher) (acked bool, err error) {
	dialCtx, cancelDial := context.WithTimeout(ctx, dialTimeout)
	c, _, err := websocket.Dial(dialCtx, endpoint, &websocket.DialOptions{HTTPClient: client})
	cancelDial()
	if err != nil {
		return false, fmt.Errorf("connect: dial %s: %w", endpoint, err)
	}
	defer func() { _ = c.CloseNow() }()
	c.SetReadLimit(protocol.MaxEnvelopeSize)

	// A reader runs for the whole session so a close or EOF is noticed at once.
	readCtx, stopRead := context.WithCancel(ctx)
	defer stopRead()
	frames := make(chan frame)
	go func() {
		for {
			typ, data, rerr := c.Read(readCtx)
			select {
			case frames <- frame{err: rerr, data: data, typ: typ}:
			case <-readCtx.Done():
				return
			}
			if rerr != nil {
				return
			}
		}
	}()

	// Results are written here until the session ends; a failed write ends it
	// through failed.
	failed := make(chan error, 1)
	d.attach(c, failed)
	defer d.detach(c)

	// waiting is the unacknowledged Heartbeat's request id. The timer means
	// "send the next Heartbeat" while it is empty and "the ack deadline"
	// otherwise.
	timer := time.NewTimer(0)
	defer timer.Stop()
	waiting := ""
	for n := 1; ; {
		select {
		case <-ctx.Done():
			_ = c.Close(websocket.StatusNormalClosure, "shutting down")
			return acked, ctx.Err()
		case ferr := <-failed:
			return acked, ferr
		case f := <-frames:
			if f.err != nil {
				return acked, fmt.Errorf("connect: connection lost: %w", f.err)
			}
			if f.typ != websocket.MessageBinary {
				_ = c.Close(websocket.StatusUnsupportedData, "binary frames only")
				return acked, errors.New("connect: control plane sent a text frame: closing the connection")
			}
			env, err := protocol.Unmarshal(f.data)
			if err != nil {
				_ = c.Close(websocket.StatusPolicyViolation, "bad envelope")
				return acked, fmt.Errorf("connect: control plane sent a bad envelope: %w", err)
			}
			if env.GetType() == agentv1.MessageType_HEARTBEAT_ACK {
				if waiting == "" || env.GetRequestId() != waiting {
					_ = c.Close(websocket.StatusPolicyViolation, "unexpected message")
					return acked, fmt.Errorf("connect: expected HEARTBEAT_ACK for request %q, got one for %q", waiting, env.GetRequestId())
				}
				waiting = ""
				acked = true
				timer.Reset(opts.HeartbeatInterval)
				continue
			}
			cmd, err := openCommand(env, opts.Executor)
			if err != nil {
				_ = c.Close(websocket.StatusPolicyViolation, "unexpected message")
				return acked, err
			}
			d.submit(cmd)
		case <-timer.C:
			if waiting != "" {
				return acked, fmt.Errorf("connect: wait for heartbeat ack: %w", context.DeadlineExceeded)
			}
			id := strconv.Itoa(n)
			if err := sendHeartbeat(ctx, c, id, opts.AgentVersion); err != nil {
				return acked, err
			}
			waiting = id
			n++
			timer.Reset(ackTimeout)
		}
	}
}

// sendHeartbeat writes one Heartbeat with request id id.
func sendHeartbeat(ctx context.Context, c *websocket.Conn, id, version string) error {
	env, err := protocol.Wrap(agentv1.MessageType_HEARTBEAT, id, &agentv1.Heartbeat{AgentVersion: version})
	if err != nil {
		return fmt.Errorf("connect: build heartbeat: %w", err)
	}
	payload, err := protocol.Marshal(env)
	if err != nil {
		return fmt.Errorf("connect: encode heartbeat: %w", err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, ackTimeout)
	defer cancel()
	if err := c.Write(writeCtx, websocket.MessageBinary, payload); err != nil {
		return fmt.Errorf("connect: send heartbeat: %w", err)
	}
	return nil
}

// backoffDelay is full jitter: a random wait in [0, min(30s, 1s*2^attempt)].
func backoffDelay(attempt int, rnd *rand.Rand) time.Duration {
	limit := backoffCap
	if attempt < 5 {
		limit = min(backoffCap, backoffInitial<<attempt)
	}
	return time.Duration(rnd.Int63n(int64(limit) + 1))
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// agentURL turns an https:// or wss:// control-plane URL into the wss:// agent endpoint.
func agentURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("connect: bad --url %q: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "wss" {
		return "", fmt.Errorf("connect: --url %q must start with https:// or wss://: the agent connection is always TLS", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("connect: --url %q has no host", raw)
	}
	u.Scheme = "https"
	u.Path = agentPath
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// connectKeyErr adds the advice for a missing or malformed enrolment file.
func connectKeyErr(err error, dataDir string) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("connect: %w: run `moonglass agent enroll` first", err)
	}
	return fmt.Errorf("connect: %w: remove %s and enrol again", err, dataDir)
}
