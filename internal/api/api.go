// Package api serves the operation registry over REST with Huma (ADR-0013).
// It is the only package that imports Huma. Each operation is one thin
// registration that authenticates the bearer token and calls registry.Invoke;
// authorisation and audit stay in the registry.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"moonglass.dev/moonglass/internal/registry"
)

// authHeader is the input every operation shares.
type authHeader struct {
	Authorization string `header:"Authorization" doc:"Bearer token: Authorization: Bearer mg_..."`
}

type serversListOutput struct {
	Body registry.ServersListOut
}

// New returns the REST handler for reg's operations. It mounts only the
// operations: no docs, schema or OpenAPI routes.
func New(reg *registry.Registry) http.Handler {
	mux := http.NewServeMux()
	build(humago.New(mux, config()), reg)
	return mux
}

// OpenAPI returns the OpenAPI 3.1 description of the registry, as checked in
// to internal/api/openapi.json.
func OpenAPI() ([]byte, error) {
	api := humago.New(http.NewServeMux(), config())
	build(api, registry.New(nil, time.Now))
	out, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode OpenAPI: %w", err)
	}
	return append(out, '\n'), nil
}

func config() huma.Config {
	cfg := huma.DefaultConfig("Moonglass", "1")
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	cfg.CreateHooks = nil
	return cfg
}

func build(api huma.API, reg *registry.Registry) {
	op := reg.ServersList
	huma.Register(api, huma.Operation{
		OperationID: op.Spec.Name, Method: op.Spec.Method, Path: op.Spec.Path, Summary: op.Spec.Summary,
	}, func(ctx context.Context, in *authHeader) (*serversListOutput, error) {
		p, err := principal(ctx, reg, in.Authorization)
		if err != nil {
			return nil, err
		}
		out, err := registry.Invoke(ctx, reg, op, p, registry.Target{}, registry.ServersListIn{})
		if err != nil {
			return nil, httpError(err)
		}
		return &serversListOutput{Body: out}, nil
	})
}

func principal(ctx context.Context, reg *registry.Registry, header string) (registry.Principal, error) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return registry.Principal{}, huma.Error401Unauthorized("send the token as: Authorization: Bearer mg_...")
	}
	p, err := reg.Authenticate(ctx, token)
	if err != nil {
		return registry.Principal{}, httpError(err)
	}
	return p, nil
}

func httpError(err error) error {
	switch {
	case errors.Is(err, registry.ErrUnauthenticated):
		return huma.Error401Unauthorized("the token is missing, unknown, expired or revoked: mint a new one")
	case errors.Is(err, registry.ErrPendingApproval):
		return huma.Error403Forbidden("this destructive call is pending approval")
	case errors.Is(err, registry.ErrDenied):
		return huma.Error403Forbidden("your role or the token's scope does not allow this operation")
	}
	slog.Error("api call failed", "error", err)
	return huma.Error500InternalServerError("the control plane could not complete the call; see its logs and retry")
}
