package deploy

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"
)

const (
	defaultHost   = "unix:///var/run/docker.sock"
	minAPIVersion = "1.44"
	pullTimeout   = 15 * time.Minute
	callTimeout   = 60 * time.Second
)

type Engine struct{ cli *client.Client }

func checkHost(host string) error {
	if path, ok := strings.CutPrefix(host, "unix://"); ok && path != "" {
		return nil
	}
	return fmt.Errorf("DOCKER_HOST %q is not a unix:// socket; Moonglass reaches the Docker Engine only through its Unix socket (ADR-0014)", host)
}

func checkAPIVersion(v string) error {
	if versions.LessThan(v, minAPIVersion) {
		return fmt.Errorf("Docker Engine API %s is older than 1.44 (Docker Engine 25); upgrade Docker Engine", v) //nolint:staticcheck // ADR-0014 specifies this message exactly
	}
	return nil
}

func Connect(ctx context.Context) (*Engine, error) {
	host := cmp.Or(os.Getenv("DOCKER_HOST"), defaultHost)
	if err := checkHost(host); err != nil {
		return nil, err
	}
	cli, err := client.New(client.WithHost(host))
	if err != nil {
		return nil, fmt.Errorf("connect to Docker Engine at %s: %w", host, err)
	}
	pingCtx, cancel := call(ctx)
	defer cancel()
	_, pingErr := cli.Ping(pingCtx, client.PingOptions{NegotiateAPIVersion: true})
	if pingErr == nil {
		pingErr = checkAPIVersion(cli.ClientVersion())
	}
	if pingErr != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("connect to Docker Engine at %s: %w", host, pingErr)
	}
	return &Engine{cli: cli}, nil
}
func (e *Engine) Close() error { return e.cli.Close() }
func call(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, callTimeout)
}

func pullCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, pullTimeout)
}

func isNotFound(err error) bool {
	var nf interface{ NotFound() }
	return errors.As(err, &nf) || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "No such ")
}
