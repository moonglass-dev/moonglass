// Package api serves the operation registry over REST with Huma (ADR-0013).
// It is the only package that imports Huma. Each operation is one generic
// registration, built from its Spec and the json and doc tags of its In; it
// authenticates the bearer token and calls registry.Invoke, so authorisation
// and audit stay in the registry.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"moonglass.dev/moonglass/internal/registry"
)

const (
	// maxBody is the request body limit, Huma's own 1 MiB default.
	maxBody = 1 << 20

	authDoc = "Bearer token: Authorization: Bearer mg_..."

	// sessionCookie carries a sign-in session. Its __Host- prefix makes the
	// browser refuse it from a subdomain, over plain HTTP and with any Path but /.
	sessionCookie = "__Host-mg_session"
	// orgHeader names the org a session request acts in.
	orgHeader = "Moonglass-Org"

	unauthenticatedDetail = "the token is missing, unknown, expired or revoked: mint a new one"
	bothDetail            = "send a bearer token or a session cookie, not both"
	sessionDetail         = "the session is missing, expired or revoked, or Moonglass-Org is missing or names an org you are not a member of: sign in again and send Moonglass-Org"
	orgMismatchDetail     = "Moonglass-Org names a different org than the token's: drop the header or use a token of that org"
	pendingApprovalDetail = "this destructive call is pending approval"
	deniedDetail          = "your role or the token's scope does not allow this operation"
	internalDetail        = "the control plane could not complete the call; see its logs and retry"
)

// Refusals the principal check makes before it looks a credential up. Each
// wraps registry.ErrUnauthenticated so a caller can test either.
var (
	errBothCredentials = fmt.Errorf("%w: %s", registry.ErrUnauthenticated, bothDetail)
	errSession         = fmt.Errorf("%w: %s", registry.ErrUnauthenticated, sessionDetail)
	errOrgMismatch     = fmt.Errorf("%w: %s", registry.ErrUnauthenticated, orgMismatchDetail)
)

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
	mount(api, reg, reg.ServersList)
	mount(api, reg, reg.ProjectsCreate)
	mount(api, reg, reg.ProjectsList)
	mount(api, reg, reg.EnvironmentsCreate)
	mount(api, reg, reg.EnvironmentsList)
	mount(api, reg, reg.ServicesCreate)
	mount(api, reg, reg.ServicesList)
}

// input is one field of an operation's In: a query parameter for GET, or a
// property of the one JSON body for POST and PUT.
type input struct {
	name     string
	doc      string
	kind     reflect.Kind
	index    int
	required bool
}

// inputs reads the exported fields of an In struct. Every field needs a json
// tag naming the input and is required unless the tag has omitempty. A GET can
// serve only string, boolean, int64 and list-of-strings inputs; a POST or PUT
// body is validated by its schema instead.
func inputs(t reflect.Type, method string) ([]input, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("input type %s is not a struct", t)
	}
	ins := make([]input, 0, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag, ok := f.Tag.Lookup("json")
		name, opts, _ := strings.Cut(tag, ",")
		if !ok || name == "" || name == "-" {
			return nil, fmt.Errorf("field %s has no json tag naming its input: add one, such as `json:\"%s\"`", f.Name, f.Name)
		}
		in := input{
			name:     name,
			doc:      f.Tag.Get("doc"),
			kind:     f.Type.Kind(),
			index:    i,
			required: !slices.Contains(strings.Split(opts, ","), "omitempty"),
		}
		if method == http.MethodGet {
			switch {
			case in.kind == reflect.Slice && f.Type.Elem().Kind() != reflect.String:
				return nil, fmt.Errorf("field %s is %s, which a GET cannot send as a query parameter: use string, boolean, int64 or a list of strings", f.Name, f.Type)
			case in.kind != reflect.String && in.kind != reflect.Bool && in.kind != reflect.Int64 && in.kind != reflect.Slice:
				return nil, fmt.Errorf("field %s is %s, which a GET cannot send as a query parameter: use string, boolean, int64 or a list of strings", f.Name, f.Type)
			}
		}
		ins = append(ins, in)
	}
	return ins, nil
}

// mount registers op on api by one generic path. It panics on an In whose
// fields cannot be served, which is a programming error.
func mount[In, Out any](api huma.API, reg *registry.Registry, op *registry.Operation[In, Out]) {
	spec := op.Spec
	inType := reflect.TypeFor[In]()
	ins, err := inputs(inType, spec.Method)
	if err != nil {
		panic("internal/api: operation " + spec.Name + ": " + err.Error())
	}
	schemas := api.OpenAPI().Components.Schemas
	hop := huma.Operation{
		OperationID: spec.Name,
		Method:      spec.Method,
		Path:        spec.Path,
		Summary:     spec.Summary,
		Parameters: []*huma.Param{{
			Name:        "Authorization",
			In:          "header",
			Description: authDoc,
			Schema:      &huma.Schema{Type: huma.TypeString, Description: authDoc},
		}},
		Responses: map[string]*huma.Response{
			"200": {
				Description: http.StatusText(http.StatusOK),
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: schemas.Schema(reflect.TypeFor[Out](), true, spec.Name+"Out")},
				},
			},
			"default": {
				Description: "Error",
				Content: map[string]*huma.MediaType{
					"application/problem+json": {Schema: schemas.Schema(reflect.TypeFor[huma.ErrorModel](), true, "ErrorModel")},
				},
			},
		},
	}
	if spec.Method == http.MethodGet {
		for _, in := range ins {
			p := &huma.Param{
				Name:        in.name,
				In:          "query",
				Description: in.doc,
				Required:    in.required,
				Schema:      schemas.Schema(inType.Field(in.index).Type, false, ""),
			}
			if in.kind == reflect.Slice {
				explode := true
				p.Explode = &explode
			}
			hop.Parameters = append(hop.Parameters, p)
		}
	} else {
		hop.RequestBody = &huma.RequestBody{
			Required: true,
			Content: map[string]*huma.MediaType{
				"application/json": {Schema: schemas.Schema(inType, true, spec.Name)},
			},
		}
	}
	projectIdx, environmentIdx := -1, -1
	for _, in := range ins {
		if in.kind != reflect.String {
			continue
		}
		switch in.name {
		case "project":
			projectIdx = in.index
		case "environment":
			environmentIdx = in.index
		}
	}

	api.OpenAPI().AddOperation(&hop)
	api.Adapter().Handle(&hop, func(ctx huma.Context) {
		req, _ := humago.Unwrap(ctx)
		in, errs, err := decode[In](ctx, spec.Method, ins, hop.RequestBody, schemas)
		if len(errs) > 0 {
			_ = huma.WriteErr(api, ctx, http.StatusBadRequest, "invalid input for "+spec.Name, errs...)
			return
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				detail := fmt.Sprintf("request body is too large limit=%d bytes", maxBody)
				_ = huma.WriteErr(api, ctx, http.StatusRequestEntityTooLarge, detail)
				return
			}
			writeError(api, ctx, spec.Name, err)
			return
		}
		p, err := principal(ctx.Context(), reg, req)
		if err != nil {
			writeError(api, ctx, spec.Name, err)
			return
		}
		t := registry.Target{
			Project:     fieldString(in, projectIdx),
			Environment: fieldString(in, environmentIdx),
		}
		out, err := registry.Invoke(ctx.Context(), reg, op, p, t, in)
		if err != nil {
			writeError(api, ctx, spec.Name, err)
			return
		}
		var buf bytes.Buffer
		if err := api.Marshal(&buf, "application/json", out); err != nil {
			writeError(api, ctx, spec.Name, err)
			return
		}
		ctx.SetHeader("Content-Type", "application/json")
		ctx.SetStatus(http.StatusOK)
		_, _ = ctx.BodyWriter().Write(buf.Bytes())
	})
}

// fieldString returns the In field at index, or "" when the In has no such
// field.
func fieldString[In any](in In, index int) string {
	if index < 0 {
		return ""
	}
	return reflect.ValueOf(&in).Elem().Field(index).String()
}

// decode reads In from the request: every input as a query parameter for GET,
// or every input from one JSON object body for POST and PUT. It returns the
// schema failures as errors, and a body read failure as err.
func decode[In any](ctx huma.Context, method string, ins []input, body *huma.RequestBody, schemas huma.Registry) (In, []error, error) {
	if method == http.MethodGet {
		in, errs := decodeQuery[In](ctx, ins)
		return in, errs, nil
	}
	in, errs, err := decodeBody[In](ctx, body, schemas)
	return in, errs, err
}

func decodeQuery[In any](ctx huma.Context, ins []input) (In, []error) {
	var in In
	u := ctx.URL()
	q := u.Query()
	known := make(map[string]bool, len(ins))
	for _, f := range ins {
		known[f.name] = true
	}
	var errs []error
	for key := range q {
		if !known[key] {
			errs = append(errs, &huma.ErrorDetail{Location: "query." + key, Message: "unknown query parameter"})
		}
	}
	v := reflect.ValueOf(&in).Elem()
	for _, f := range ins {
		values := q[f.name]
		switch {
		case len(values) == 0:
			if f.required {
				errs = append(errs, &huma.ErrorDetail{Location: "query." + f.name, Message: "required query parameter is missing"})
			}
		case f.kind == reflect.Slice:
			v.Field(f.index).Set(reflect.ValueOf(values))
		case len(values) > 1:
			errs = append(errs, &huma.ErrorDetail{Location: "query." + f.name, Message: "expected one value"})
		default:
			if err := setQuery(v.Field(f.index), f.kind, values[0]); err != nil {
				errs = append(errs, &huma.ErrorDetail{Location: "query." + f.name, Message: err.Error()})
			}
		}
	}
	return in, errs
}

// setQuery sets one scalar query value, or returns why it cannot.
func setQuery(field reflect.Value, kind reflect.Kind, value string) error {
	if kind == reflect.String {
		field.SetString(value)
		return nil
	}
	if kind == reflect.Bool {
		switch value {
		case "true":
			field.SetBool(true)
			return nil
		case "false":
			field.SetBool(false)
			return nil
		default:
			return errors.New("expected true or false")
		}
	}
	if kind == reflect.Int64 {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return errors.New("expected a decimal integer")
		}
		field.SetInt(n)
		return nil
	}
	return fmt.Errorf("cannot decode a %s query parameter", kind)
}

func decodeBody[In any](ctx huma.Context, body *huma.RequestBody, schemas huma.Registry) (In, []error, error) {
	var in In
	var queryErrs []error
	u := ctx.URL()
	for key := range u.Query() {
		queryErrs = append(queryErrs, &huma.ErrorDetail{Location: "query." + key, Message: "unknown query parameter"})
	}
	if len(queryErrs) > 0 {
		return in, queryErrs, nil
	}
	b, err := io.ReadAll(http.MaxBytesReader(nil, io.NopCloser(ctx.BodyReader()), maxBody))
	if err != nil {
		return in, nil, fmt.Errorf("read the request body: %w", err)
	}
	var raw any
	if err := json.Unmarshal(b, &raw); err != nil {
		return in, []error{&huma.ErrorDetail{Location: "body", Message: "the body is not a JSON object"}}, nil
	}
	var res huma.ValidateResult
	var pb huma.PathBuffer
	pb.Push("body")
	huma.Validate(schemas, body.Content["application/json"].Schema, &pb, huma.ModeWriteToServer, raw, &res)
	if len(res.Errors) > 0 {
		errs := make([]error, 0, len(res.Errors))
		for _, e := range res.Errors {
			var d *huma.ErrorDetail
			if !errors.As(e, &d) {
				return in, nil, fmt.Errorf("validate the request body: huma.Validate returned a %T, not a *huma.ErrorDetail", e)
			}
			errs = append(errs, &huma.ErrorDetail{Location: d.Location, Message: d.Message})
		}
		return in, errs, nil
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return in, []error{&huma.ErrorDetail{Location: "body", Message: "the body does not match the input"}}, nil
	}
	return in, nil, nil
}

// principal resolves the authenticated caller of r. A request sends a bearer
// token or the session cookie, never both: both is refused before either is
// looked up, so one credential can never authorise a request the other is
// audited for. A session needs Moonglass-Org, an org its user is a member of. A
// bearer's org comes from the token, and a Moonglass-Org naming another org is
// refused.
func principal(ctx context.Context, reg *registry.Registry, r *http.Request) (registry.Principal, error) {
	auth := r.Header.Get("Authorization")
	cookie, cerr := r.Cookie(sessionCookie)
	hasSession := cerr == nil
	if auth != "" && hasSession {
		return registry.Principal{}, errBothCredentials
	}
	if hasSession {
		org := r.Header.Get(orgHeader)
		if org == "" {
			return registry.Principal{}, errSession
		}
		p, err := reg.AuthenticateSession(ctx, cookie.Value, org)
		if errors.Is(err, registry.ErrUnauthenticated) {
			return registry.Principal{}, errSession
		}
		return p, err
	}
	token, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok {
		return registry.Principal{}, fmt.Errorf("read the Authorization header: %w", registry.ErrUnauthenticated)
	}
	p, err := reg.Authenticate(ctx, token)
	if err != nil {
		return registry.Principal{}, err
	}
	if org := r.Header.Get(orgHeader); org != "" && org != p.OrgID {
		return registry.Principal{}, errOrgMismatch
	}
	return p, nil
}

// writeError answers the caller with the status and detail httpError chooses.
func writeError(api huma.API, ctx huma.Context, operation string, err error) {
	status, detail := httpError(operation, err)
	_ = huma.WriteErr(api, ctx, status, detail)
}

// httpError maps err to its status and detail. The detail is fixed or, for a
// refusal, the handler's own message; another error's text is only logged.
func httpError(operation string, err error) (int, string) {
	switch {
	case errors.Is(err, errBothCredentials):
		return http.StatusUnauthorized, bothDetail
	case errors.Is(err, errSession):
		return http.StatusUnauthorized, sessionDetail
	case errors.Is(err, errOrgMismatch):
		return http.StatusUnauthorized, orgMismatchDetail
	case errors.Is(err, registry.ErrUnauthenticated):
		return http.StatusUnauthorized, unauthenticatedDetail
	case errors.Is(err, registry.ErrPendingApproval):
		return http.StatusForbidden, pendingApprovalDetail
	case errors.Is(err, registry.ErrDenied):
		return http.StatusForbidden, deniedDetail
	}
	if refusal(err) {
		switch {
		case errors.Is(err, registry.ErrInvalidInput):
			return http.StatusBadRequest, err.Error()
		case errors.Is(err, registry.ErrNotFound):
			return http.StatusNotFound, err.Error()
		case errors.Is(err, registry.ErrConflict):
			return http.StatusConflict, err.Error()
		}
	}
	slog.Error("api call failed", "operation", operation, "error", err)
	return http.StatusInternalServerError, internalDetail
}

// refusal reports whether err is a handler refusal. registry.Refuse wraps
// nothing, so a refusal wrapped by another error is not one.
func refusal(err error) bool {
	if _, ok := err.(interface{ Unwrap() error }); ok {
		return false
	}
	if _, ok := err.(interface{ Unwrap() []error }); ok {
		return false
	}
	return true
}
