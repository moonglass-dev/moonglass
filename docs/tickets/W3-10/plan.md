---
verdict: ready
reason: ""
---

# Plan for W3-10

No contract change: the ticket uses the existing `Store` methods (`Memberships`, `UserForIdentity`, `AddMembership`, `AddToken` through `registry.MintToken`). It adds no operation, route, storage column, migration or module. Every behaviour comes from `docs/specs/enrolment.md` "Commands" (D27) and `docs/specs/registry.md` "Tokens". Test files do not count toward `max_diff_lines`. The non-test diff is about 45 lines in `server.go`, 12 in `main.go` and 1 in `routes.go`.

## Files to change

- `internal/server/server.go`:
  - New constants: `ownerTokenLifetime = 90 * 24 * time.Hour` and `localOwnerIdentity = "local:owner"`.
  - New method `func (s *Server) IssueOwnerToken(ctx context.Context, orgID, name string) (string, error)`:
    1. When `name == ""`, it returns `errors.New("issue owner token: name is empty: pass --print-owner-token NAME, named after where the token will be used")` before any store call.
    2. It calls `s.st.Memberships(ctx, orgID)` (wrapping the error as `"issue owner token: list memberships of org %s: %w"`) and takes the `UserID` of the first membership with `Role == store.RoleOwner`.
    3. When the org has no owner, it calls `s.st.UserForIdentity(ctx, localOwnerIdentity)`, then `s.st.AddMembership(ctx, orgID, userID, store.RoleOwner)`. Each error is wrapped with context: `"issue owner token: create local owner: %w"` and `"issue owner token: make local owner the owner of org %s: %w"`.
    4. It returns `s.MintOwnerToken(ctx, orgID, userID, name, s.now().Add(ownerTokenLifetime))`. `MintOwnerToken` and `registry.MintToken` are reused unchanged. Their errors never contain the token text.
  - `Run` signature becomes `func Run(ctx context.Context, dataDir, listen, agentListen string, printEnrolToken bool, ownerTokenName string, stdout io.Writer) error`. The doc comment names the new parameter: empty means not requested.
  - The org is resolved with the existing `firstOrg` once, when `printEnrolToken || ownerTokenName != ""`.
  - The enrolment block stays as it is and runs first. Then, when `ownerTokenName != ""`, `Run` calls `s.IssueOwnerToken(ctx, orgID, ownerTokenName)` and `fmt.Fprintln(stdout, token)`, wrapping a write failure as `"print owner token: %w"`.
  - Both blocks run before `net.Listen`. Nothing is logged.
- `cmd/moonglass/main.go`, `runServer`:
  - Adds `ownerToken := fs.String("print-owner-token", "", "mint one owner token named NAME and print it")`.
  - After `fs.Parse`, it sets `ownerGiven` with `fs.Visit(func(f *flag.Flag) { if f.Name == "print-owner-token" { ownerGiven = true } })`.
  - Usage plus exit 2 when any of these hold: the parse fails, `fs.NArg() != 0`, or `ownerGiven && *ownerToken == ""`. This check comes before `server.Run`, so nothing touches the data dir.
  - It passes `*ownerToken` to `server.Run`.
- `internal/registry/routes.go`: the `server` synopsis becomes `server [--data-dir DIR] [--listen ADDR] [--agent-listen ADDR] [--print-enroll-token] [--print-owner-token NAME]`. No other change. `cli.Usage` already renders `Synopsis`. The generated files do not include `LocalCommands` synopses, so `make generate` output does not change.
- `docs/specs/enrolment.md`, "Commands", second bullet:
  - Drop "From W3-10" so it reads as current behaviour.
  - Add: an empty `NAME` (`--print-owner-token ""` or `--print-owner-token=`) prints usage to stderr and exits 2. With both flags, the enrolment token line comes first.
  - Add: the token goes to stdout only, so the owner runs the hook from a shell and not under the systemd unit, whose stdout is the journal.
  - Add: an org that already has an owner gets no second owner.
- `docs/specs/registry.md`: "Tokens" already says "from W3-10 `moonglass server --print-owner-token NAME` prints one". Drop "from W3-10" so it reads as current behaviour. "Routes and commands that are not operations" still matches: the `server` command list is unchanged.
- `testdata/tests.txt`: regenerated with `make test-list`.

## Tests to write

New file `internal/server/owner_token_test.go` (package `server`). It reuses `newFixture` and `testNow` from `enroll_test.go`.

- `./internal/server TestIssueOwnerTokenCreatesLocalOwner`: on a fresh fixture org, `IssueOwnerToken(ctx, f.org, "ci")` returns no error and a text with prefix `mg_` and length 55.
  - `f.st.Memberships` is exactly one entry, role `owner`, whose `UserID` equals `f.st.UserForIdentity(ctx, "local:owner")`.
  - `f.srv.reg.Authenticate(ctx, text)` gives `OrgID == f.org`, that user, `Role == store.RoleOwner` and `Scopes` deep-equal `[{* * *}]`.
  - `f.st.TokenByHash(ctx, store.HashToken(text))` gives `Name == "ci"`, `Last4 == text[len(text)-4:]` and `ExpiresAt == testNow.Add(90*24*time.Hour).UTC().Format(time.RFC3339)`.
  - Opening `f.dir/server.db` with `sql.Open("sqlite", …)` (driver registered by the `internal/store/sqlite` import) shows `SELECT hash FROM token_hashes` equal to `store.HashToken(text)`. No row of `tokens`, `token_scopes` or `token_hashes` contains `text` or its 52-character secret.
- `./internal/server TestIssueOwnerTokenUsesExistingOwner`: the setup runs `UserForIdentity(ctx, "owner@example.com")` and `AddMembership(..., RoleOwner)`. Two calls (`"ci"`, `"laptop"`) return two different texts, and both authenticate as that user with role owner. `Memberships` deep-equals exactly `[{f.org, existingUser, owner}]`.
- `./internal/server TestIssueOwnerTokenRejectsEmptyNameWithoutWrites`: `IssueOwnerToken(ctx, f.org, "")` returns a non-nil error whose text contains `name is empty`, and the returned text is `""`. Afterwards `Memberships` is empty and `SELECT COUNT(*) FROM tokens` is 0.
- `./internal/server TestRunPrintsOwnerTokenOnce`: the helper `runServer(t, dir, printEnrol bool, ownerName string) []string` does this:
  - It starts `Run(ctx, dir, "127.0.0.1:0", "127.0.0.1:0", printEnrol, ownerName, pw)` in a goroutine, writing to an `io.Pipe`. The goroutine closes `pw` after `Run` returns.
  - It reads the expected number of lines with `bufio.Reader.ReadString('\n')`, then cancels.
  - It asserts that `Run` returned nil and that the rest of the pipe (`io.ReadAll`) is empty, which proves exactly those lines were printed.

  The test records `before := time.Now()` and `after := time.Now()` around the first call.
  - The single line is a 55-character `mg_` token.
  - It reopens `dir/server.db` with `sqlite.Open` and reads the org from `dir/org` (trimmed). `registry.New(st, time.Now).Authenticate` gives that org with role owner.
  - The parsed `ExpiresAt` lies between `before.Truncate(time.Second).Add(90d)` and `after.Add(90d)`.
  - It closes the store, then makes a second call with `"ci"` on the same dir. That call prints a token that differs from the first, and the org still has exactly one membership, role owner.
- Helper `./internal/server TestRunPrintsEnrolTokenBeforeOwnerToken`: `runServer(t, dir, true, "ci")` reads two lines. On reopening the db, line 2 authenticates as owner and line 1 does not (`registry.ErrUnauthenticated`), because it is the enrolment token. `EnrolmentTokens(org)` has one entry.

New file `cmd/moonglass/server_test.go`:

- `./cmd/moonglass TestServerOwnerTokenFlagNeedsName`: the table holds `{"server", "--data-dir", dir, "--print-owner-token", ""}` and `{"server", "--data-dir", dir, "--print-owner-token="}`, where `dir = filepath.Join(t.TempDir(), "data")`. For each, `run` returns 2, stderr equals `usage`, stdout is empty, and `os.Stat(filepath.Join(dir, "server.db"))` is `fs.ErrNotExist`.
- `./cmd/moonglass TestUsageListsOwnerTokenFlag`: one line of `usage` has a trimmed prefix of `server ` and contains `[--print-owner-token NAME]`. On failure the message shows the full usage.

Existing tests (`TestUsageOnMissingOrUnknownCommand`, the registry list tests, enrol tests) run unchanged.

## Edge cases

- Empty name at the method level: rejected before any store call (`TestIssueOwnerTokenRejectsEmptyNameWithoutWrites`).
- Empty name at the flag level, in both spellings: exit 2 with usage, and no data dir or db is created (`TestServerOwnerTokenFlagNeedsName`).
- `--print-owner-token` with no value as the last argument: `flag` returns "needs an argument", the parse fails, and the existing usage path exits 2. No new test is needed, since it is the same branch.
- Flag not given: `ownerGiven` is false and the name is empty, so `Run` issues no owner token. The existing behaviour is unchanged.
- Org already has an owner: no `local:owner` membership is added (`TestIssueOwnerTokenUsesExistingOwner`).
- A second run on the same data dir reuses the `local:owner` owner made by the first run, through `Memberships`, so no duplicate membership is added (second half of `TestRunPrintsOwnerTokenOnce`).
- An org with members but no owner (only admins, say): `local:owner` is added as the owner. This is the spec's "no owner yet" rule. The store returns `ErrDuplicate` if `local:owner` already holds a non-owner role in this org. That error is wrapped and returned, and the server fails to start with a message, so the user is never silently promoted.
- Store or stdout write failures: wrapped with context, and none of them include the token text. The `MintToken` error paths are already covered in `internal/registry`.
- Token leak: `Run` and `IssueOwnerToken` write no logs. The token goes only to `stdout`. `Run` has no stderr, and `runServer` prints only `err` from `Run`, which never contains the token.

## Risks

- Changing the `Run` signature breaks callers. The only caller is `cmd/moonglass/main.go` (grep confirms), and it changes in the same diff.
- Flaky `Run` tests. The listeners use `127.0.0.1:0`, and the token is printed before `Listen`. Shutdown is by context cancel: `serveEnrol` and `ServeAgents` both return nil on `ErrServerClosed`. The pipe gives a race-free handoff under `-race`. The data dir is a `t.TempDir()`, and the store is closed between runs, so `sqlite.Open` on reopen does not contend.
- The expiry assertion would break on the RFC 3339 seconds truncation. The lower bound is truncated to the second.
- Trusted-core review: `internal/registry` is touched only in the synopsis string, and the token minting path is the existing `MintToken`. No scope or role logic is added: the hook only ever produces a `* * *` token for the org's owner, which is what §9 item 4 requires.
- `scripts/verify` paths gate: every changed path is in `allowed_paths` (`internal/server/`, `cmd/moonglass/`, `internal/registry/routes.go`, the two specs, `testdata/tests.txt`).
- `words` gate: the code has no "fallback" in comments, and messages say "not requested" rather than anything banned.
