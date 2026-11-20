---
verdict: ready
reason: ""
---

# Plan for W2-11f

## Context

`internal/api` today serves `servers.list` through one hand-written `huma.Register` call with a fixed `authHeader` input. W2-11d will add operations whose `In` types carry only `json` and `doc` tags. `huma.Register` builds query parameters only from `query:"…"` struct tags. Go cannot build a generic type parameter at runtime, so `huma.Register` cannot serve an `In` that has no Huma tags as query parameters. The one generic path therefore registers each operation at Huma's lower level: `api.OpenAPI().AddOperation` plus `api.Adapter().Handle`. Huma still supplies the router adapter, the body schema (`Components.Schemas.Schema`), schema validation (`huma.Validate`), problem+json error writing (`huma.WriteErr`) and response marshalling (`api.Marshal`). `internal/api` does the decoding and keeps the error mapping in one place. No new module, no contract change, no ADR change.

## Files to change

- `internal/api/api.go`: rewritten around one generic mount.
  - Delete `authHeader`, `serversListOutput` and the hand-written `huma.Register` in `build`.
  - `build(api huma.API, reg *registry.Registry)` becomes a single line, `mount(api, reg, reg.ServersList)`.
  - `const maxBody = 1 << 20`: Huma's default limit.
  - `type input struct { name string; index int; kind reflect.Kind; required bool; doc string }` holds one field of `In`. The `json` tag gives the name: the part before the first comma, with `omitempty` meaning optional. `doc` is the `doc` tag. `kind` is `reflect.String`, `reflect.Bool`, `reflect.Int64` or `reflect.Slice` (with element string).
  - `func inputs(t reflect.Type) ([]input, error)` reads the exported fields of `In`. It returns an error naming the operation's field when a field has no `json` tag, or a `-` tag, or when its kind is not one of the four. The four-kind check runs only for `GET`, because a `POST` or `PUT` body is validated by its schema, so a local command's list-of-objects input still works over REST (spec "Input fields").
  - `func mount[In, Out any](api huma.API, reg *registry.Registry, op *registry.Operation[In, Out])`. It panics on an `In` it cannot serve, which is a programming error, like `registerShipped`. It builds:
    - `huma.Operation{OperationID: Spec.Name, Method, Path, Summary}`.
    - `Parameters`: the `Authorization` header parameter as today (same description and schema), then for `GET` one `In: "query"` parameter per input, with `Name`, `Description: doc`, `Required`, `Explode: &true` for lists, and `Schema` from `api.OpenAPI().Components.Schemas.Schema(field.Type, false, "")`.
    - `RequestBody` for `POST` and `PUT`: `{Required: true, Content: {"application/json": {Schema: Schemas.Schema(reflect.TypeFor[In](), true, Spec.Name)}}}`. Huma's registry defaults give required fields unless `omitempty`, `additionalProperties: false` and `doc` descriptions.
    - `Responses`: `"200"` with `application/json` and the `Out` schema; `"default"` with `application/problem+json` and the `ErrorModel` schema. Together these keep `openapi.json` in the shape it has today.
    - `targetIdx` holds the `index` of the string inputs named `project` and `environment`, or -1 when absent.
    - Then `api.OpenAPI().AddOperation(&hop)` and `api.Adapter().Handle(&hop, func(ctx huma.Context){ serve(...) })`.
  - The request flow inside the handler closure, in this order:
    1. `in, errs, err := decode[In](ctx, method, inputs, bodySchema, schemas)`. On non-empty `errs`, `huma.WriteErr(api, ctx, 400, "invalid input for "+name, errs...)` and return. On `*http.MaxBytesError`, `huma.WriteErr(api, ctx, 413, fmt.Sprintf("request body is too large limit=%d bytes", maxBody))`, which is Huma's own text. On any other `err` (a body read failure), write the 500 path. This step comes before authentication and before `Invoke`, so it writes no audit event.
    2. `p, err := principal(ctx, reg, ctx.Header("Authorization"))`. A missing or non-`Bearer ` header now gets the spec detail `the token is missing, unknown, expired or revoked: mint a new one`.
    3. `t := registry.Target{Project: in.field(targetIdx.project), Environment: …}`, which is empty when the fields are absent. No name lookup.
    4. `out, err := registry.Invoke(ctx.Context(), reg, op, p, t, in)`.
    5. On success, `api.Marshal(&buf, "application/json", out)` into a `bytes.Buffer`. A marshal error goes to the 500 path. Otherwise set `Content-Type: application/json`, status 200, and write the buffer.
  - `func decode[In any](ctx huma.Context, method string, ins []input, body *huma.Schema, reg huma.Registry) (In, []error, error)`:
    - `GET`: `q := ctx.URL().Query()`. Each key not among the inputs adds `&huma.ErrorDetail{Location: "query."+key, Message: "unknown query parameter"}`. For each input, the values are `q[name]`:
      - none and required: `"required query parameter is missing"`;
      - none and optional: the zero value;
      - more than one for a non-list: `"expected one value"`;
      - bool: exactly `true` or `false`, else `"expected true or false"`;
      - int64: `strconv.ParseInt(v, 10, 64)`, else `"expected a decimal integer"`;
      - list: the values as-is, in order, with commas kept.
      Fields are set by `reflect.Value.Field(index)`.
    - `POST`/`PUT`:
      - Every query key adds an `unknown query parameter` error.
      - `io.ReadAll(http.MaxBytesReader(nil, io.NopCloser(ctx.BodyReader()), maxBody))` reads the body. A read error is returned as `err`.
      - `json.Unmarshal(b, &raw)` failing adds `{Location: "body", Message: "the body is not a JSON object"}`, with fixed text and never the decoder's text, which can quote input bytes.
      - Otherwise `huma.Validate(reg, body, pb /* "body" */, huma.ModeWriteToServer, raw, &res)`. Each `res.Errors` entry is copied to a new `&huma.ErrorDetail{Location, Message}`, so the `Value` (which for an unknown or missing property is the whole body map) is dropped.
      - With no errors, `json.Unmarshal(b, &in)`. Failure there, such as an integer past int64, adds `{Location: "body", Message: "the body does not match the input"}`.
  - `httpError(err error) (int, string)` replaces the `huma.Error*` constructors so every error is written by one `huma.WriteErr` call (`writeError(api, ctx, err)`):
    - `ErrUnauthenticated` 401, `ErrPendingApproval` 403, `ErrDenied` 403, each with its fixed text, checked first.
    - A refusal is 400 for `ErrInvalidInput`, 404 for `ErrNotFound` and 409 for `ErrConflict`, with `err.Error()` as the detail. It counts as a refusal only when `err` has no `Unwrap() error` and no `Unwrap() []error`. `registry.Refuse` wraps nothing, so a wrapped refusal (whose text could carry another error) is not a refusal and goes to the 500 path.
    - Anything else: `slog.Error("api call failed", "operation", name, "error", err)` and 500 with the fixed message.
  - Package doc: replace "one thin registration" with "one generic registration per operation, built from its Spec and the json and doc tags of its In".
- `internal/api/openapi.json`: regenerated with `make generate`, never hand-edited. The expected diff is none or minimal: the same header parameter, 200 and default responses.
- `docs/specs/registry.md` "Inputs and errors": correct two statements about mechanism, with no behaviour change:
  - "REST inputs": "`RejectUnknownQueryParameters` is set on every operation, and bodies keep Huma's default of no additional properties" becomes "`internal/api` checks every query parameter name against the inputs, and the body schema has no additional properties". Huma's flag only works inside `huma.Register`, which cannot serve tag-less `In` types.
  - "Targets, authorisation and audit": "Huma checks the shape before the handler authenticates" becomes "`internal/api` checks the shape before it authenticates".
  - Add one line: a refusal that is wrapped by another error is a 500.

## Tests to write

All are in `internal/api/inputs_test.go`, `package api`. They call `mount` directly on `humago.New(http.NewServeMux(), config())` over a real `sqlite.Open` store in `t.TempDir()`, with an org, an owner user and an owner token from `registry.MintToken`. Test operations come from `registry.Register` on `registry.New(st, now)`. Each handler records the `In` and `Call` it got and whether it ran. Table-driven; failure messages show want and got.

Test `In` types:
- `getIn{Name string "name"; Flag bool "flag,omitempty"; Count int64 "count,omitempty"; Domain []string "domain,omitempty"}`.
- `bodyIn{Name string "name"; Count int64 "count,omitempty"; Ready bool "ready,omitempty"; Domain []string "domain,omitempty"}`.
- `targetIn{Project string "project"; Environment string "environment"}`.

Each type has `doc` tags.

The tests:
- `./internal/api TestQueryInputsDecoded`: `GET ?name=n&flag=true&count=-42&domain=a&domain=b,c` gives `getIn{"n", true, -42, ["a","b,c"]}`, and `flag=false` gives false. `?name=n` alone gives zero values for `flag`, `count` and `domain`.
- `./internal/api TestBodyInputsDecoded`: a `POST` and a `PUT` with `{"name":"n","count":7,"ready":true,"domain":["x","y,z"]}` get that struct, with the list in order. `{"name":"n"}` gives zero values for the rest.
- `./internal/api TestUnknownInputsRefused`: `GET ?name=n&bogus=1` and `POST {"name":"n","bogus":1}` are each 400 with an `errors` entry at `query.bogus` or `body.bogus`. In both cases the handler does not run and `st.AuditEvents` is unchanged. A query parameter on a `POST` is also 400.
- `./internal/api TestSchemaFailuresAre400`: each case uses a unique sentinel value such as `sentinel-7f3a` and runs with the owner token and with no token. Cases:
  - `GET` missing `name` (`query.name`);
  - `GET ?count=sentinel-7f3a` (`query.count`);
  - `GET ?flag=sentinel` (`query.flag`);
  - `GET ?name=a&name=b` (`query.name`, `expected one value`);
  - `POST {}` (location `body`, message names `name`);
  - `POST []` (location `body`);
  - `POST {"name":"n","count":"sentinel-7f3a"}` (`body.count`);
  - `POST` body `not json sentinel-7f3a` (`body`).
  Each is 400, never 422, with `Content-Type: application/problem+json` and detail `invalid input for <op name>`. Every `errors` entry has a non-empty `location` and `message` and no `value` key, which is checked by decoding into `map[string]any`. The raw body does not contain the sentinel. No token still gives 400. `AuditEvents` is unchanged.
- `./internal/api TestRefusalStatuses`: a handler returning `registry.Refuse(kind, "thing \"x\" …")` for each kind gives 400, 404 and 409, with that exact `detail` and problem+json.
- `./internal/api TestUnmappedHandlerErrorIs500`: three handler errors, `fmt.Errorf("query rows: %w", errors.New("secret-text"))`, `fmt.Errorf("create: %w", store.ErrDuplicate)` and `fmt.Errorf("wrap: %w", registry.Refuse(registry.ErrConflict, "inner-text"))`. Each is 500 with detail `the control plane could not complete the call; see its logs and retry`, and the body contains neither `secret-text`, `store: already exists` nor `inner-text`.
- `./internal/api TestTargetFilledFromInputs`: create project `web` with environment `prod`. With the owner token:
  - `GET ?project=nope&environment=prod` is 404 `project "nope" not found`;
  - `?project=web&environment=nope` is 404 `environment "nope" not found in project "web"`;
  - `?project=web&environment=prod` reaches the handler with `Call.ProjectID` and `Call.EnvironmentID` equal to the store's ids.
  The `getIn` operation (no target fields) reaches its handler with `Call.ProjectID == "" && Call.EnvironmentID == ""`, and the audit event for that call is `allowed`, which shows Invoke saw the empty target.
- `./internal/api TestMissingTokenIs401WithSpecDetail`: a well-formed `GET ?name=n` with no header, with `Authorization: Basic abc` and with `Bearer mg_`+52×`a` is 401 each time, with detail `the token is missing, unknown, expired or revoked: mint a new one`, and the handler does not run.
- `./internal/api TestBodyOverLimitIs413`: a `POST` with `{"name":"` + (1 MiB + 1 bytes of `a`) + `"}` and the owner token is 413 with problem+json, and the handler does not run.

Existing tests stay green unchanged: `TestEveryMountedRouteIsOperationOrAllowlisted`, `TestServerErrorIsLoggedWithoutTheToken` (slog still logs the wrapped `write audit event for servers.list`), `TestHumaConfinedToAPI` and `TestGeneratedOpenAPIIsCurrent`. Run `make test-list` after adding the tests.

## Edge cases

- Repeated scalar query parameter (`?name=a&name=b`) gives 400 `expected one value`. Row in `TestSchemaFailuresAre400`.
- `flag=1`, `flag=TRUE` and `flag=` give 400. Only `true` and `false` are accepted, as the spec says. Row `?flag=sentinel` in `TestSchemaFailuresAre400`.
- `count=1.5` and `count=99999999999999999999` give 400 via `ParseInt`. Covered by the `count` row.
- Empty-string value `?name=` is present, so a required string accepts `""`. Name validity is the handler's or the store's job (`ErrInvalidName` becomes a refusal). No test is needed, because no behaviour is added.
- A JSON `null` for a required field fails Huma's type check with 400. An array or scalar body fails with `expected object` 400. Covered by the `[]`, `{}` and `not json` rows.
- An unknown property's or missing property's Huma `Value` is the whole body map, which could hold a ciphertext. The entry is rebuilt without `Value`, so it cannot be sent. The sentinel check in `TestSchemaFailuresAre400` covers this.
- `json` decoder error text can quote input bytes, so fixed messages are used. Covered by the `not json sentinel` row.
- Body exactly 1 MiB is accepted, and 1 MiB + 1 is 413. `TestBodyOverLimitIs413` covers the over-limit case.
- A body read failure that is not over the limit gives 500 and is logged, not sent.
- Malformed request plus bad token gives 400, because shape is checked first and the token is never examined. `TestSchemaFailuresAre400` covers it.
- A wrapped refusal gives 500, so another error's text never reaches `detail`. Covered in `TestUnmappedHandlerErrorIs500`.
- `Bearer ` with an empty token is passed to `Authenticate`, which gives `ErrUnauthenticated` and 401.
- An unknown target outside the token's scope is 403 from `Invoke`. `internal/api` adds no lookup, and its existing registry tests cover this.
- A `GET` `In` with an unsupported kind (for example `[]int`) makes `mount` panic at startup with the operation and field named, so it fails loudly before serving. W2-11g's generator enforces the same rule for the CLI.

## Risks

- Ticket text says "`RejectUnknownQueryParameters`", but that flag only acts inside `huma.Register`, which cannot serve `In` types with no Huma tags. The behaviour is identical (400, same location `query.<key>`, same message as Huma's), and the spec's mechanism sentence is corrected in the same change, which the ticket's `docs/specs/registry.md` scope allows. The reviewer should see this called out in the PR body.
- `openapi.json` drift: it is regenerated only by `make generate`. `TestGeneratedOpenAPIIsCurrent` guards it, and the parameter and response shapes are kept as today.
- Huma's case-insensitive property matching (`ValidateStrictCasing` false) accepts `{"Name":…}` for `name`. `encoding/json` matches it the same way, so the decoded value is consistent. No refusal is weakened.
- Authorisation stays in `registry.Invoke` (threat model §3 B1). The decode step decides no access, and the target holds names only (§8). No log line holds the token: `principal` never logs, and the 500 log holds the error only. That error never contains the token, because `Authenticate` errors do not include it.
- Diff size: tests, `openapi.json` and docs do not count. `api.go` is expected at roughly 230 lines of change, under the 390 cap.
- No new dependency: only `huma/v2` (ADR-0013) and the standard library. `CGO_ENABLED=0` is unchanged.
