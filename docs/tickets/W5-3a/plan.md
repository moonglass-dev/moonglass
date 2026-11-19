---
verdict: ready
reason: ""
---

# Plan for W5-3a

Context: `docs/specs/local-policy.md` (approved, D43–D53) and ADR-0004 decisions 13–14 define the meaning of `[commands]` and `[sources]`. W1-1 only loads them. This ticket adds:
- the pure decisions in `internal/policy`;
- one image-name normaliser, shared with the allow blocks;
- a check at load that every source pattern is in the normal form;
- the rule that a present, empty list allows nothing.

Nothing calls the new functions yet (W5-3). The decoder gives `Parse` a non-nil slice of length 0 for `x = []`: go-toml v2.4.3 `assignArray` uses `reflect.MakeSlice(t, 0, 0)`. W1-1's current "empty list" error already relies on this.

## Files to change

- `internal/policy/image.go`: rewrite `normalizeRepo(name string) string` to follow the spec's four steps. `name` is the repository after `splitImage` has removed the tag and digest.
  - `host, rest, ok := strings.Cut(name, "/")`. If `!ok`, or the host has no `.` or `:` and is not `localhost`, then host = `docker.io` and rest = name.
  - Lower-case the host, and turn `index.docker.io` into `docker.io`.
  - If the host is `docker.io` and rest has no `/`, prefix rest with `library/`.
  - Return `host + "/" + rest`.

  The `docker.io/` TrimPrefix special case is removed. `parseBlockImage`, `parseSpecImage` and `imagesMatch` stay as they are, so a block still matches only when both sides have a digest. The path keeps its case.
- `internal/policy/source.go` (new, beside `check.go`):
  - `type Class uint8` with `ClassNone`, `ClassDelete` and `ClassExec` (iota).
  - `func (p *Policy) Forbids(c Class) bool` maps `ClassDelete` to `"delete"` and `ClassExec` to `"exec"`, then returns `slices.Contains(p.cfg.ForbidCommands, name)`. `ClassNone` and any other value return false.
  - `type SourceRejection struct{ key, source string }`, where `Error()` returns exactly:
    - for registries: `agent.toml on this server allows images only from [sources] registries; <source> matches none of them. Add a pattern to [sources] registries in /etc/moonglass/agent.toml and restart the agent.`
    - for repositories: the same sentence with `builds` and `repositories`.

    It never holds or prints a pattern.
  - `func imageSource(ref string) string` runs `splitImage` and then `normalizeRepo`, and returns `""` for an empty ref.
  - `func (p *Policy) CheckImageSource(ref string) error`:
    - nil `Registries` returns nil;
    - `src := imageSource(ref)`;
    - a host of exactly `moonglass.local` (already lower-cased) returns nil;
    - any `path.Match(pattern, src)` that is true returns nil, and a match error counts as no match;
    - anything else returns `&SourceRejection{key: "registries", source: src}`.
  - `func (p *Policy) CheckRepositorySource(repo string) error`:
    - nil `Repositories` returns nil;
    - `src := "github.com/" + strings.ToLower(repo)`;
    - it returns nil only when `repo` has exactly two elements that each pass `isPathElement` and some pattern matches;
    - anything else returns `&SourceRejection{key: "repositories", source: src}`.

    The element check fails closed: without it, `github.com/*/*` would match `github.com/../x`.
  - None of these functions use the Engine, a file, the network or the clock.
- `internal/policy/parse.go`:
  - For each `[sources]` key, a non-nil list of length 0 is stored as `[]string{}`. Any other list goes through `validateStringList` with a new extra validator. `validateStringList` itself does not change, so `forbid = []` and empty allow-block lists are still errors.
  - `validRegistryPattern(s)` checks, in order:
    1. bad glob;
    2. `s != strings.ToLower(s)`;
    3. `strings.Cut(s, "/")` finds no `/`, or nothing after it;
    4. the host contains `*`, `?` or `[`;
    5. the host has no `.` or `:` and is not `localhost`;
    6. the host is `index.docker.io`;
    7. the host is `moonglass.local`.

    Every message ends with `; write patterns in the normal form, such as docker.io/library/nginx` and is prefixed with `registries: `.
  - `validRepositoryPattern(s)` checks, in order:
    1. bad glob;
    2. not lower case;
    3. no `github.com/` prefix, or not exactly three non-empty elements.

    The last error is `%q must be github.com/<owner>/<repo>, such as %s`. The suggestion is `s+"/*"` when `s` has the prefix and two elements, and `github.com/acme/*` otherwise.
- `internal/policy/types.go`:
  - `Config.clone` uses `slices.Clone`, which keeps nil and empty apart.
  - The `Config` comment is updated: a list is nil when absent and empty when present and empty, and the decisions are the three new methods. Enforcement belongs to the agent.
- `internal/policy/config_test.go`: delete only the `empty repositories` and `empty registries` rows (D52). The other source fixtures still fail or load for the same reason.
- `docs/specs/container-policy.md`:
  - The `sources.*` row and the `Parse`/`LoadFile` paragraph state the W5-3a rules as current behaviour.
  - "Matching" names the `localhost` and `index.docker.io` handling.
  - Item 9 of "Choices made here" becomes: unknown `[commands]` classes, duplicates, empty entries and an empty `forbid` list are errors, and the `[sources]` rules are in `local-policy.md`.
- `docs/specs/local-policy.md`: one wording fix. The repositories rejection sentence is written out in full with "allows builds only from". Repeating "images" from the registries sentence would be untrue for a build.
- `testdata/tests.txt`: regenerate with `make test-list`.

## Tests to write

New tests go in `internal/policy/source_test.go`, except the allow-block test, which goes in `allow_test.go`. All are table-driven and show input, want and got.

- `TestImageSourceNormalForm`:
  - every row of the spec table;
  - `index.docker.io/acme/web` → `docker.io/acme/web`;
  - `docker.io/nginx` → `docker.io/library/nginx`;
  - `localhost/web` → `localhost/web`;
  - `index.docker.io/nginx` → `docker.io/library/nginx`;
  - `localhost:5000/web:v1@sha256:…` → `localhost:5000/web`.
- `TestImageSourceMatching`:
  - a matching reference returns nil;
  - a reference that matches no pattern returns a `*SourceRejection` (found with `errors.As`) with the exact sentence;
  - `ghcr.io/acme/*` rejects `ghcr.io/acme/team/web`, and `ghcr.io/acme/*/*` admits it;
  - `docker.io/library/*` admits `nginx:1.27`;
  - tag and digest variants give the same result;
  - the detail contains no pattern;
  - `moonglass.local/p/s` and `MoonGlass.Local/p/s:v1` return nil;
  - with `registries` absent, every reference returns nil;
  - an empty ref is rejected when `registries` is present;
  - `ghcr.io/Acme/web` and `moonglass.local:5000/x` are rejected.
- `TestRepositorySourceMatching`:
  - `Acme/Web` passes `github.com/acme/*` and `github.com/*/*`;
  - `github.com/other/*` gives the exact repositories sentence naming `github.com/acme/web`;
  - `acme`, `a/b/c`, `../x` and `""` are rejected under `github.com/*/*`;
  - with `repositories` absent, every repository returns nil.
- `TestSourcePatternsValidatedAtLoad`:
  - each of these `registries` patterns fails, naming the key and showing `docker.io/library/nginx`: `GHCR.io/acme/*`, `*.io/acme/*`, `ghcr?io/x`, `[g]hcr.io/x`, `acme/*`, `ghcr.io`, `index.docker.io/acme/*`, `moonglass.local/*`;
  - `repositories` fails for `github.com/Acme/*`, `gitlab.com/acme/*`, `github.com/acme/web/x` and `github.com//x`;
  - `github.com/acme` suggests `github.com/acme/*`;
  - `docker.io/library/*`, `localhost:5000/*`, `ghcr.io/acme/*/*`, `github.com/acme/*` and `github.com/*/*` load.
- `TestEmptySourceListAllowsNothing`:
  - `registries = []` and `repositories = []` load;
  - every non-`moonglass.local` image is rejected, and `moonglass.local/p/s` returns nil;
  - every repository is rejected;
  - `Config()` reports a present empty list as non-nil with length 0, and an absent one as nil.
- `TestForbidsClasses`:
  - covers `forbid` absent, `["delete"]`, `["exec"]` and `["delete", "exec"]`, each asked about all three classes;
  - `["exec"]` loads and `Forbids(ClassExec)` is true.
- `TestAllowBlockImageUsesSourceNormalForm`:
  - an `index.docker.io/prom/node-exporter@digestA` block allows `prom/node-exporter@digestA`;
  - a `localhost/web@digestA` block allows `localhost/web@digestA` but not `docker.io/localhost/web@digestA`, nor `localhost/web:v1` with no digest.

## Edge cases

- An empty or malformed ref or repository fails closed when its list is present. Covered by the rows above.
- An upper-case path never matches, because patterns are lower case.
- `moonglass.local:5000` is matched against `registries` like any other host.
- Duplicate, empty-entry and bad-glob entries, and `forbid = []`, stay errors through the existing W1-1 rows.
- `Config()` copies keep nil and empty apart. `TestParseFullExample` and `TestLoadFileAbsentIsEmptyConfig` still DeepEqual.
- `FuzzParse` stays total, because the validators only return errors.

## Risks

- **Allow-block matching widens** only through `index.docker.io`, `localhost/` and an upper-case registry host. Upper-case hosts are step 3 of the approved normal form, and a registry host is a DNS name. A digest is still required through the unchanged `imagesMatch`. The existing allow tests and the escape suite stay unchanged and green. The PR description will state the three cases for the owner.
- **Repositories detail wording:** the plan writes it out as "allows builds only from" in the spec. If a reviewer wants the literal "images", it is a one-word change.
- **`docker.io/*` loads but matches nothing**, as the spec says. The plan adds no load rule beyond the spec's list.
- **A second normaliser outside the package:** `internal/deploy/image.go` has its own `normalizeRepo`, outside `allowed_paths`. It is left alone and noted in the PR description as a follow-up.
- **Diff budget of 330 lines:** about 90 for `source.go`, 45 for `parse.go`, 15 for `image.go`, 8 for `types.go`, 15 for the specs and 150 for tests. Tests stay compact by sharing a parse helper and tables.
- No banned words.
- Before handing off, run `make test-list`, `make fmt`, `make ci` and `scripts/verify check W5-3a`.
