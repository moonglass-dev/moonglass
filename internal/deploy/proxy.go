package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
)

const (
	// maxProxyConfigBytes bounds a config read from or pushed to the proxy.
	maxProxyConfigBytes = 16 << 20
	// proxyServer is the one HTTP server the pushed config names. It listens on
	// the ports the proxy unit is allowed to bind; Caddy adds the :80 redirect.
	proxyServer = "moonglass"
	// acmeStagingDirectory is the Let's Encrypt staging endpoint for acme-staging.
	acmeStagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"
)

// ProxyConfig is how the agent reaches the proxy and what it tells it.
type ProxyConfig struct {
	// OnError receives a push that failed during Run. It is never called for a
	// failed probe or after ctx ends.
	OnError func(error)
	// Socket is the proxy's agent-facing admin socket.
	Socket string
	// Issuer is acme, acme-staging or internal.
	Issuer string
}

// Proxy builds the complete proxy config from agent state and pushes it to the
// proxy's admin socket. One Proxy serves one agent; all its pushes run one at a
// time.
type Proxy struct {
	engine *Engine
	db     *state.DB
	client *http.Client
	last   atomic.Pointer[[]byte]
	cfg    ProxyConfig
	mu     sync.Mutex
}

// NewProxy returns the agent's proxy pusher. It never dials TCP: every request
// goes to the Unix socket at cfg.Socket.
func NewProxy(engine *Engine, db *state.DB, cfg ProxyConfig) *Proxy {
	return &Proxy{
		engine: engine,
		db:     db,
		cfg:    cfg,
		client: &http.Client{Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", cfg.Socket)
			},
		}},
	}
}

// Push builds the complete Caddy JSON from every app's current release that has
// domains and posts it to the proxy. The admin and storage values are read from
// the running proxy and repeated verbatim, so the proxy's guard accepts the
// document. Only a 2xx answer records the document as the last pushed.
func (p *Proxy) Push(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tlsApps, err := issuerApps(p.cfg.Issuer)
	if err != nil {
		return err
	}
	admin, err := p.do(ctx, http.MethodGet, "/config/admin", nil)
	if err != nil {
		return err
	}
	storage, err := p.do(ctx, http.MethodGet, "/config/storage", nil)
	if err != nil {
		return err
	}
	current, err := p.db.CurrentReleases(ctx)
	if err != nil {
		return fmt.Errorf("read the apps to route: %w", err)
	}

	routes := make([]any, 0, len(current))
	for _, cr := range current {
		if len(cr.Record.Domains) == 0 {
			continue
		}
		route, routeErr := p.route(ctx, cr)
		if routeErr != nil {
			return routeErr
		}
		routes = append(routes, route)
	}

	httpApp := map[string]any{"grace_period": "30s"}
	if len(routes) > 0 {
		httpApp["servers"] = map[string]any{
			proxyServer: map[string]any{"listen": []string{":443"}, "routes": routes},
		}
	}
	apps := map[string]any{"http": httpApp}
	for name, value := range tlsApps {
		apps[name] = value
	}

	doc, err := json.Marshal(map[string]any{
		"admin":   json.RawMessage(admin),
		"storage": json.RawMessage(storage),
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{"level": "WARN"}}},
		"apps":    apps,
	})
	if err != nil {
		return fmt.Errorf("encode the proxy config: %w", err)
	}
	if _, err := p.do(ctx, http.MethodPost, "/load", doc); err != nil {
		return err
	}
	p.last.Store(&doc)
	return nil
}

// route builds one route: the release's domains, the counter, and the reverse
// proxy to the release container's address on its project network.
func (p *Proxy) route(ctx context.Context, cr state.CurrentRelease) (any, error) {
	app, release := cr.Record.AppID, cr.Record.ReleaseID
	if cr.Record.ContainerID == "" {
		return nil, fmt.Errorf("app %s release %s has no container in agent state; deploy the app again", app, release)
	}
	addr, err := p.engine.containerIP(ctx, cr.Record.ContainerID, "mg-"+cr.Binding.Project)
	if err != nil {
		return nil, fmt.Errorf("app %s release %s: container %s: %w", app, release, cr.Record.ContainerID, err)
	}
	dial := net.JoinHostPort(addr.String(), strconv.FormatUint(uint64(cr.Record.Port), 10))
	return map[string]any{
		"match": []any{map[string]any{"host": cr.Record.Domains}},
		"handle": []any{
			map[string]any{"handler": "moonglass_counter", "release": cr.Record.ReleaseID},
			map[string]any{"handler": "reverse_proxy", "upstreams": []any{map[string]any{"dial": dial}}},
		},
		"terminal": true,
	}, nil
}

// Run pushes once at start and then pushes again whenever the running config is
// not the last one pushed. A failed probe is not an error: the proxy may be
// restarting, and the next probe tries again. A failed push goes to OnError and
// is retried at the next probe. Run returns nil only when ctx ends.
func (p *Proxy) Run(ctx context.Context) error {
	p.report(ctx, p.Push(ctx))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if !p.differs(ctx) {
				continue
			}
			p.report(ctx, p.Push(ctx))
		}
	}
}

// differs reports whether the running config is not the last one pushed.
func (p *Proxy) differs(ctx context.Context) bool {
	running, err := p.do(ctx, http.MethodGet, "/config/", nil)
	if err != nil {
		return false
	}
	last := p.last.Load()
	if last == nil {
		return true
	}
	var runningDoc, lastDoc any
	if err := json.Unmarshal(running, &runningDoc); err != nil {
		return true
	}
	if err := json.Unmarshal(*last, &lastDoc); err != nil {
		return true
	}
	return !reflect.DeepEqual(runningDoc, lastDoc)
}

// report passes a failed push to OnError, unless ctx has ended: a push that
// stopped because of cancellation is not a proxy failure.
func (p *Proxy) report(ctx context.Context, err error) {
	if err == nil || p.cfg.OnError == nil || ctx.Err() != nil {
		return
	}
	p.cfg.OnError(err)
}

// do is the only HTTP path to the proxy. Each call holds the 60-second Engine
// call limit and reads at most maxProxyConfigBytes. A non-2xx answer carries the
// method, path, status and the bounded body.
func (p *Proxy) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	reqCtx, cancel := call(ctx)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, "http://localhost"+path, reader)
	if err != nil {
		return nil, fmt.Errorf("build %s %s for proxy admin socket %s: %w", method, path, p.cfg.Socket, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s on proxy admin socket %s: %w", method, path, p.cfg.Socket, err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(res.Body, maxProxyConfigBytes))
	if err != nil {
		return nil, fmt.Errorf("read the answer to %s %s from proxy admin socket %s: %w", method, path, p.cfg.Socket, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s on proxy admin socket %s: status %d: %s",
			method, path, p.cfg.Socket, res.StatusCode, truncate(string(raw)))
	}
	return raw, nil
}

// containerIP returns the container's address on network.
func (e *Engine) containerIP(ctx context.Context, id, networkName string) (netip.Addr, error) {
	ctx, cancel := call(ctx)
	defer cancel()
	res, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return netip.Addr{}, fmt.Errorf("inspect it: %w; deploy the app again", err)
	}
	var endpoint *network.EndpointSettings
	if res.Container.NetworkSettings != nil {
		endpoint = res.Container.NetworkSettings.Networks[networkName]
	}
	if endpoint == nil || !endpoint.IPAddress.IsValid() {
		return netip.Addr{}, fmt.Errorf("has no address on network %s; deploy the app again", networkName)
	}
	return endpoint.IPAddress, nil
}

// issuerApps returns the apps.tls entry for issuer, plus apps.pki for the
// internal issuer. Anything else is refused before any request is sent.
func issuerApps(issuer string) (map[string]any, error) {
	var value map[string]any
	apps := map[string]any{}
	switch issuer {
	case "acme":
		value = map[string]any{"module": "acme"}
	case "acme-staging":
		value = map[string]any{"module": "acme", "ca": acmeStagingDirectory}
	case "internal":
		value = map[string]any{"module": "internal"}
		apps["pki"] = map[string]any{"certificate_authorities": map[string]any{
			"local": map[string]any{"install_trust": false},
		}}
	default:
		return nil, fmt.Errorf("TLS issuer %q is not one of acme, acme-staging or internal; pass one of those", issuer)
	}
	apps["tls"] = map[string]any{"automation": map[string]any{"policies": []any{
		map[string]any{"issuers": []any{value}},
	}}}
	return apps, nil
}
