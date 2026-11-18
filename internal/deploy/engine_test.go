package deploy

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestEngineCallDeadlines checks the two bounds every Engine call in the
// package takes from its context: 15 minutes around a pull and 60 seconds
// around every other call. The deadline is read off the context the call
// would use, so an unbounded pull or any other limit fails here without
// sleeping.
func TestEngineCallDeadlines(t *testing.T) {
	const slack = 5 * time.Second

	cases := []struct {
		newContext func(context.Context) (context.Context, context.CancelFunc)
		name       string
		want       time.Duration
	}{
		{name: "engine call", newContext: call, want: 60 * time.Second},
		{name: "image pull", newContext: pullCtx, want: 15 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			ctx, cancel := tc.newContext(context.Background())
			defer cancel()

			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatalf("%s context Deadline() ok = false, want a deadline %v after the call", tc.name, tc.want)
			}
			got := deadline.Sub(start)
			if got < tc.want || got > tc.want+slack {
				t.Errorf("%s context deadline = %v after the call, want %v", tc.name, got, tc.want)
			}
		})
	}

	t.Run("the caller's earlier deadline wins", func(t *testing.T) {
		parent, cancelParent := context.WithTimeout(context.Background(), time.Second)
		defer cancelParent()
		parentDeadline, ok := parent.Deadline()
		if !ok {
			t.Fatalf("parent context Deadline() ok = false, want its one-second deadline")
		}

		for name, newContext := range map[string]func(context.Context) (context.Context, context.CancelFunc){
			"engine call": call,
			"image pull":  pullCtx,
		} {
			ctx, cancel := newContext(parent)
			deadline, ok := ctx.Deadline()
			if !ok {
				cancel()
				t.Fatalf("%s context Deadline() ok = false, want the caller's deadline", name)
			}
			if !deadline.Equal(parentDeadline) {
				t.Errorf("%s context deadline = %v, want the caller's earlier deadline %v",
					name, deadline, parentDeadline)
			}
			cancel()
			if err := ctx.Err(); err == nil {
				t.Errorf("%s context Err() = nil after cancel, want %v", name, context.Canceled)
			}
		}
	})
}

func TestEngineAPIVersionFloor(t *testing.T) {
	cases := []struct {
		version string
		want    string
	}{
		{
			version: "1.40",
			want:    "Docker Engine API 1.40 is older than 1.44 (Docker Engine 25); upgrade Docker Engine",
		},
		{
			version: "1.43",
			want:    "Docker Engine API 1.43 is older than 1.44 (Docker Engine 25); upgrade Docker Engine",
		},
		{version: "1.44"},
		{version: "1.47"},
		{version: "1.57"},
	}

	for _, tc := range cases {
		t.Run(tc.version, func(t *testing.T) {
			err := checkAPIVersion(tc.version)
			if tc.want == "" {
				if err != nil {
					t.Errorf("checkAPIVersion(%q) error = %v, want nil", tc.version, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkAPIVersion(%q) error = nil, want %q", tc.version, tc.want)
			}
			requireEqual(t, "checkAPIVersion("+tc.version+") error", err.Error(), tc.want)
		})
	}
}

func TestConnectRequiresUnixSocket(t *testing.T) {
	hosts := []string{
		"tcp://127.0.0.1:2375",
		"ssh://host",
		"npipe:////./pipe/docker_engine",
		"http://localhost",
	}
	for _, host := range hosts {
		t.Run(host, func(t *testing.T) {
			t.Setenv("DOCKER_HOST", host)
			want := fmt.Sprintf("DOCKER_HOST %q is not a unix:// socket; "+
				"Moonglass reaches the Docker Engine only through its Unix socket (ADR-0014)", host)

			engine, err := Connect(context.Background())

			if err == nil {
				t.Fatalf("Connect() error = nil with DOCKER_HOST=%q, want the unix-socket refusal", host)
			}
			if engine != nil {
				t.Errorf("Connect() engine = %v with DOCKER_HOST=%q, want nil on error", engine, host)
			}
			requireEqual(t, "Connect error", err.Error(), want)
		})
	}

	t.Run("unix path accepted by the host check", func(t *testing.T) {
		if err := checkHost("unix:///tmp/docker.sock"); err != nil {
			t.Errorf("checkHost(unix:///tmp/docker.sock) error = %v, want nil", err)
		}
	})

	t.Run("empty unix path refused", func(t *testing.T) {
		err := checkHost("unix://")
		if err == nil {
			t.Fatalf("checkHost(%q) error = nil, want a refusal: the path is empty", "unix://")
		}
		if !strings.Contains(err.Error(), `"unix://"`) {
			t.Errorf("checkHost(%q) error = %q, want it to name the value", "unix://", err.Error())
		}
	})
}
