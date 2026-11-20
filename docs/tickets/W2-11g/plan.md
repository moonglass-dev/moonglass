---
verdict: ready
reason: ""
---

# Plan for W2-11g

The spec ("Input fields", "Generated CLI" in `docs/specs/registry.md`) is settled by the owner decision of 2026-11-18. No contract, ADR, dependency or open security question is involved. `registry.Operations()` gains the `In` type. This changes only the Go declaration; no operation is added or changed. The generator derives flags from `In`, and the runner parses, checks and sends them. Only the standard library is used (`reflect`, `flag`, `net/url`, `encoding/json`).

## Files to change

- `internal/registry/operations.go`:
  - Add `type Declaration struct { Spec; In reflect.Type }`, with a doc comment: "an operation's Spec and the type of its input; REST and CLI generation read it".
  - `func Operations() []Declaration` returns `[]Declaration{{Spec: serversListSpec, In: reflect.TypeFor[ServersListIn]()}}`.
  - Because `Spec` is embedded, existing reads of `.Name`, `.Method`, `.Path`, `.Destructive` and `.MinRole` still compile unchanged. They are in `internal/server/server.go`, the `internal/api` tests, `internal/registry/registry_test.go` and `internal/cli/cli_test.go`.
- `internal/cli/cli.go`:
  - Add `type flagKind uint8` with `kindString`, `kindBool`, `kindInt` and `kindList`, starting at 1. Its `String()` returns `string`, `bool`, `integer` or `list`, for the usage text.
  - Add `type input struct { Flag, JSON string; Kind flagKind; Required bool; Doc string }`. `Flag` is the name without the leading `--`.
  - `command` gains `Inputs []input`.
  - `runGenerated(c, args, env, stdout, stderr) int` becomes these steps:
    1. Create `fs := flag.NewFlagSet(c.Name, flag.ContinueOnError)` and call `fs.SetOutput(io.Discard)`. Set up `values := map[string]any{}`, keyed by JSON name, and `badInt := ""`.
    2. Register one flag per input, by kind:
       - string: `fs.Func`, storing the string.
       - list: `fs.Func`, appending to a `[]string`.
       - integer: `fs.Func` using `strconv.ParseInt(s, 10, 64)`. On failure it sets `badInt = in.Flag` and returns an error.
       - bool: `fs.BoolFunc` using `strconv.ParseBool`, storing the bool.
    3. If `fs.Parse(args)` fails: when `badInt != ""`, print `moonglass: <name>: --<flag> must be an integer` to stderr; otherwise print `commandUsage(c)`. Exit 2 in both cases.
    4. If `fs.NArg() > 0`, print `commandUsage(c)` and exit 2.
    5. Find the first required input, in field order, that is missing from `values`. Print `moonglass: <name>: --<flag> is required` and exit 2.
    6. Keep the existing `MOONGLASS_URL`/`MOONGLASS_TOKEN` check and its exit 2 unchanged.
    7. Build the request with `encode(c command, values map[string]any) (query string, body io.Reader, err error)`:
       - `GET`: build `url.Values` in input order. A list uses `Add` once per element, a bool uses `strconv.FormatBool`, an integer uses `strconv.FormatInt(v, 10)`. The body is `http.NoBody`. A non-empty query is appended as `?` plus `q.Encode()`.
       - Any other method: one object from `json.Marshal(values)`, which is `{}` when no flags are given. Set `Content-Type: application/json` and send no query string.
       - The bearer header is unchanged.
    8. On `200 <= status < 300`, copy the body to stdout with `io.Copy` and exit 0. Otherwise call `printProblem(stderr, c.Name, resp)` and exit 1.
  - `printProblem(w io.Writer, name string, resp *http.Response)`:
    - Read at most 1 MiB through `io.LimitReader`.
    - Unmarshal into a struct with `Detail string` (`json:"detail"`) and `Errors []struct{ Location, Message string }` (`json:"errors"`).
    - If decoding fails or `Detail` is empty, use `fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))` as the detail.
    - Print `moonglass: <name>: <detail>`, then one line `  <location>: <message>` per entry.
    - Never print a flag value or the token.
  - `commandUsage(c command) string`:
    - Print `Usage: moonglass <name> [flags]`, then the summary, then `Flags:`.
    - List required inputs first, then optional ones, each group in field order.
    - Each line is `  --<flag> <kind> (required)` or `  --<flag> <kind>`. A list adds `, repeatable`. The doc goes on the next line, indented six spaces.
    - With no inputs, the text ends after the summary.
- `internal/cli/gen.go`:
  - `Generate(ops []registry.Declaration, local []registry.LocalCommand) ([]byte, error)` calls `commands(ops, local)` and renders each command as a literal: `{Name: …, Method: …, Path: …, Summary: …, Inputs: []input{{Flag: …, JSON: …, Kind: kindString, Required: true, Doc: …}}}`.
    - `Inputs` is left out when empty, so `servers list` stays one line.
    - Kind constants are written by name from a `map[flagKind]string`.
    - On error it returns `nil, err`.
  - `commands(ops []registry.Declaration, local []registry.LocalCommand) ([]command, error)`: the subcommand is the operation name with `.` turned into a space. It skips any operation whose subcommand equals a `local[i].Name` and calls `inputs(op)` for the rest.
  - `inputs(d registry.Declaration) ([]input, error)`:
    - If `d.In` is nil or not a struct, return `operation %s: In must be a struct type`.
    - Walk the fields in order, skipping unexported ones.
    - The json name is the part of the tag before the first comma (`strings.Cut(tag, ",")`). An empty name or `-` is an error: `operation %s: input field %s has no json name: give it a snake_case json tag`.
    - A field is required unless `omitempty` is among the tag options.
    - The kind is chosen with `if` chains, not a `switch` on `reflect.Kind`, because the `exhaustive` linter would demand every Kind:
      - `String` → string
      - `Bool` → bool
      - `Int64` → integer
      - `Slice` whose `Elem().Kind()` is `String` → list
    - Any other kind is an error: `operation %s: input field %s (json %q) is a %s, which has no flag: use string, bool, int64 or []string, or add the subcommand %q to registry.LocalCommands`.
    - The flag is `env` for `environment`, otherwise the json name with `_` turned into `-`. The doc is the `doc` tag.
- `internal/cli/generated.go`: regenerated only by `make generate`. The output is byte-identical, because `ServersListIn` has no fields.
- `docs/specs/registry.md`:
  - The `registry.Operations()` sentence now says it lists the shipped operations as `registry.Declaration` values, each with its embedded `Spec` and `In`, the `reflect.Type` of its input, which generation reads.
  - The W2-11g test bullet gains `internal/registry`: `TestOperationsListInTypes`.
  - Nothing else in the spec changes.
- `testdata/tests.txt`: rewritten by `make test-list`.

## Tests to write

Test helper, in the test-only file `internal/cli/export_test.go` (package `cli`):

- `RunDeclared(ops []registry.Declaration, args []string, env func(string) string, stdout, stderr io.Writer) (int, error)`:
  - It builds `commands(ops, nil)`, picks the command whose subcommand words begin `args`, and returns what `runGenerated` returns.
  - It returns an error when generation fails or no command matches.
  - The runner tests therefore use the same flag derivation that `Generate` writes to `generated.go`.

Test operations, in `internal/cli/generated_test.go` (the owner allowed edits to this file for W2-11g):

- A helper `testOps(t)` registers three operations with `registry.Register` on `registry.New(nil, time.Now)`: `test.get` (`GET /v1/test`), `test.post` (`POST`) and `test.put` (`PUT`).
- All three take `testIn`, with these fields:
  - `Project string json:"project" doc:"Project name"`
  - `Environment string json:"environment" doc:"Environment name"`
  - `HealthPath string json:"health_path,omitempty" doc:"…"`
  - `PreDeployReversible bool json:"pre_deploy_reversible,omitempty"`
  - `Domain []string json:"domain,omitempty"`
  - `Port int64 json:"port,omitempty"`
  - `Name string json:"name,omitempty"`
- Each operation is paired with `reflect.TypeFor[testIn]()` into a `[]registry.Declaration`.
- An `httptest.Server` records the method, path, raw query, content type, body and number of requests.

The tests:

- `./internal/registry TestOperationsListInTypes`, in a new file `internal/registry/operations_test.go`:
  - Every entry has a non-nil struct `In`.
  - `servers.list` has `In == reflect.TypeFor[registry.ServersListIn]()`.
  - Its `Spec` is unchanged: `servers.list`, "List servers", `GET`, `/v1/servers`, viewer, not destructive.
- `./internal/cli TestGeneratedFlagNames`:
  - `--project p --env e --health-path /h --pre-deploy-reversible` exits 0 and sends `project`, `environment`, `health_path` and `pre_deploy_reversible`.
  - A table of `--environment e`, `--health_path /h` and `-p p`: each exits 2, puts `Usage: moonglass test get` on stderr, and sends nothing.
- `./internal/cli TestGeneratedFlagsSentAsQueryOrBody`:
  - `GET`: the query holds exactly `project`, `environment` and `name`, and the body is empty.
  - `POST` and `PUT`: the body is exactly `{project, environment, name}`, with `Content-Type: application/json` and an empty `RawQuery`.
  - In all three, `health_path` is absent.
- `./internal/cli TestGeneratedRepeatedBooleanAndIntegerFlags`:
  - `--domain a.example --domain b.example` keeps its order: `query["domain"]` is `[a.example b.example]` for GET, and a JSON array for POST.
  - `--pre-deploy-reversible` sends true and `--pre-deploy-reversible=false` sends false (POST body).
  - `--port 8080` sends the JSON number 8080, and `port=8080` in the query.
  - `--port 8x` exits 2 with `--port must be an integer` and sends nothing.
  - `--name=-v` sends `name` as `-v`.
- `./internal/cli TestGeneratedMissingRequiredFlagExits2`: with no flags, the exit is 2, stderr contains `moonglass: test get: --project is required`, and nothing is sent.
- `./internal/cli TestGeneratedRefusesPositionalAndUnknownFlags`:
  - A table of a positional `extra` and an unknown `--nope`: each exits 2 and sends nothing.
  - Stderr lists every flag, with the required `--project` and `--env` before `--health-path`, and every `doc` text present.
- `./internal/cli TestGeneratedPrintsErrorDetail`, a table of server replies:
  - 400 `application/problem+json` with `detail` and two `errors` entries: stderr is exactly `moonglass: test get: <detail>\n  body.name: <m1>\n  query.project: <m2>\n`, stdout is empty, exit 1.
  - 404 with only `detail`: exactly that one line, exit 1.
  - 200: the body on stdout, exit 0.
  - 500 with an empty body: `moonglass: test get: 500 Internal Server Error`, exit 1.
- `./internal/cli TestGenerateRefusesInputWithoutFlag`:
  - A table of two `In` types for an operation `test.bad`: one with `Items []struct{A string} json:"items"`, one with `Count int json:"count"`.
  - `cli.Generate(ops, nil)` returns nil output and an error that contains `test.bad` and the field name (`Items` or `Count`).
- `./internal/cli TestGenerateSkipsLocalCommands`: the same `test.bad`, with `[]registry.LocalCommand{{Name: "test bad", …}}` passed in, gives no error and output with no `"test bad"`.
- `TestGeneratedCLIIsCurrent`: only one line changes, to `cli.Generate(registry.Operations(), registry.LocalCommands)`. Its assertions stay the same.

## Edge cases

- An integer that overflows or is not decimal (`--port 0x10`, `--port 99999999999999999999`) is refused by `ParseInt` in base 10 with the same message. The `8x` case covers it.
- A bad boolean (`--pre-deploy-reversible=maybe`) is a parse error, so the usage is printed and the exit is 2. The flag package handles it, and the unknown-flag test covers that path.
- `-h` or `--help` returns `flag.ErrHelp`, which takes the same usage-and-exit-2 path as an unknown flag.
- Arguments after a positional, or after `--`, make `NArg() > 0`, so the usage is printed. The positional case covers it.
- A repeated string flag keeps its last value (flag semantics). A list appends in order, which is tested.
- A non-JSON or empty error body falls back to the status line, which the 500 case tests. The body read is capped at 1 MiB.
- A 3xx reply is no longer printed as success. `http.Client` follows redirects, so only a redirect it does not follow reaches the error path.
- In the generator, a nil `In`, a non-struct `In`, a field tagged `json:"-"` or untagged, and an `int` or list-of-objects field all fail loudly, naming the operation and the field. The last two are tested. Unexported fields are skipped, as `encoding/json` does.
- Security (§9.3): error output holds only the server's `detail`, `location` and `message`, never a flag value. Usage prints only flag names, kinds and docs. Nothing is logged, and the token is never printed.

## Risks

- **Diff cap of 360 lines.** Only `operations.go` (~12), `gen.go` (~70) and `cli.go` (~120) count, about 200 lines. Tests, `generated.go` and docs do not count.
- **`tests-kept` gate.** The only existing test file edited is `generated_test.go`, which the owner allowed. `cli_test.go` and `registry_test.go` are not touched. New tests go in new files (`operations_test.go`, `export_test.go`) or are appended to `generated_test.go`.
- **`exhaustive` lint.** There is no `switch` over `reflect.Kind`, and any `switch` on `flagKind` lists all four values.
- **`forbidigo` lint.** All output uses `fmt.Fprintf` to the writers passed in, never `fmt.Print*`.
- **Stale `generated.go`.** Run `make generate`. The output for `servers list` should be unchanged, and `TestGeneratedCLIIsCurrent` confirms it.
- **`TestGeneratedRunnerSendsBearerToken`.** It still passes: `servers list` with no arguments parses an empty FlagSet, and a GET with an empty query goes to `/v1/servers`.
- **Test-locked gate.** The named tests must fail before any implementation. They use `registry.Declaration`, `cli.RunDeclared` and the two-argument `Generate`, so they fail to compile first.

## Verification

Run, in order:

1. `make test-one PKG=./internal/cli NAME='TestGenerated|TestGenerate'`
2. `make test-one PKG=./internal/registry NAME=TestOperationsListInTypes`
3. `make generate`, which should leave `generated.go` unchanged
4. `make test-list`
5. `make ci`
6. `scripts/verify check W2-11g`
