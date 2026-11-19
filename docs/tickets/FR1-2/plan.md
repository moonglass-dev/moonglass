---
verdict: ready
reason: ""
---

# Plan for FR1-2

The storage-schema change is a contract change. The owner approved it in `docs/plan/build-plan.md` (FR1-2: "a storage-schema amendment, approved by the owner"), so the verdict is `ready`. No `.proto`, ADR, dependency or `agent.toml` change is needed: `EnrollRequest.agent_age_recipient` already exists, `internal/server` already imports `internal/keys`, and only `internal/keys` imports `filippo.io/age`.

## Files to change

- `internal/store/migrations/sqlite/0007_agent_age_recipient.sql` and `internal/store/migrations/pg/0007_agent_age_recipient.sql` (new, byte-identical): `ALTER TABLE servers ADD COLUMN agent_age_recipient TEXT NOT NULL DEFAULT '';`. `servers` already has RLS and `org_isolation` on Postgres (pg `0001_init.sql`). `pg.Migrate` grants are table-level, so the new column is covered and `internal/store/pg` does not change.
- `internal/store/store.go`:
  - `Server` gains `AgeRecipient string` after `PublicKey`. Update its doc comment: "a registered server, its agent signing public key and its agent age recipient (`age1…`)".
  - The interface line becomes `AddServer(ctx context.Context, orgID, publicKey, ageRecipient string) (Server, error)` with a comment: "ageRecipient is the agent's age recipient (`age1…`), copied and never parsed; an empty one is an error and nothing is written". The two-argument form is gone.
- `internal/store/sqlstore.go`:
  - `func (s *SQLStore) AddServer(ctx context.Context, orgID, publicKey, ageRecipient string) (Server, error)`: first statement `if ageRecipient == "" { return Server{}, errors.New("add server: agent age recipient is empty: pass the age1… recipient the agent sent at enrolment") }`, before `NewID` and any SQL. `srv` sets `AgeRecipient: ageRecipient`. The `servers` insert becomes `INSERT INTO servers (id, org_id, created_at, agent_age_recipient) VALUES ($1, $2, $3, $4)`. The `agent_keys` insert is unchanged and stays in the same `inOrg` transaction, so a duplicate public key rolls back the `servers` row. Doc comment: "The signing public key is stored only in agent_keys; the age recipient only on servers."
  - `ServerByPublicKey`: the `servers` query becomes `SELECT created_at, agent_age_recipient FROM servers WHERE id = $1 AND org_id = $2`, scanned into `&srv.CreatedAt, &srv.AgeRecipient`. The `agent_keys` lookup is unchanged.
  - `Servers`: select `s.agent_age_recipient` after `k.public_key`, scan into `&srv.AgeRecipient`.
  - `lookup.go` and `LookupTables` are not touched.
- `internal/server/enroll.go`:
  - In `handleEnroll`, right after the 32-byte length check and before `s.enrol`:
    ```go
    recipient, err := keys.AgreeRecipient(req.GetAgentAgeRecipient())
    if err != nil {
        http.Error(w, "enroll: agent_age_recipient is not an X25519 public key", http.StatusBadRequest)
        return
    }
    result, err := s.enrol(r, &req, recipient)
    ```
    The message is fixed and holds no key bytes or token.
  - `func (s *Server) enrol(r *http.Request, req *agentv1.EnrollRequest, recipient keys.Recipient) (agentv1.EnrollResult, error)`: the `AddServer` call becomes `s.st.AddServer(r.Context(), orgID, hex.EncodeToString(req.GetAgentSigningPublicKey()), recipient.String())`. The result mapping is unchanged.
  - Import `moonglass.dev/moonglass/internal/keys`.
- `docs/specs/storage.md`:
  - "Row-level tables", `servers` bullet: `org_id`, `id`, `created_at`, `agent_age_recipient` (the agent's age X25519 recipient in Bech32 form, `age1…`; empty for servers enrolled before FR1-2). Change "The public key lives only in `agent_keys`" to "The signing public key lives only in `agent_keys`".
  - New section `### Agent age recipients (FR1-2)` after "Server deploy keys (W3-8)": the column is `TEXT NOT NULL DEFAULT ''` on both backends. Enrolment writes `keys.AgreeRecipient(agent_age_recipient).String()`. `AddServer` refuses an empty recipient before any SQL and writes it in the same transaction as the `agent_keys` row. The store copies the text and does not parse it. `ServerByPublicKey` and `Servers` return it. It is a public key. It lives on `servers` and not `agent_keys`, because a lookup row holds only its key and IDs. Servers enrolled before migration `0007` hold `''` until a later re-enrolment or rotation ticket. Wrapping to the recipient is W4-8 and W3-3, after the CLI fingerprint check in `docs/specs/secrets.md`.
- `docs/specs/enrolment.md`:
  - "HTTP endpoint": after the length check, the server turns `agent_age_recipient` into an age recipient with `keys.AgreeRecipient` (HTTP 400 and the token unused if that fails). On `ACCEPTED`, `store.AddServer` gets the hex signing key and that recipient's `age1…` string, and `servers.agent_age_recipient` stores it (`docs/specs/storage.md`, "Agent age recipients (FR1-2)").
  - "Out of scope": "Changing W1-2 store schema" becomes "Changing W1-2 store schema, except FR1-2's `servers.agent_age_recipient` amendment".

Counted (non-test, non-doc) diff is about 35 lines, under the cap of 90.

## Tests to write

New file `internal/store/agent_age_recipient_test.go` (package `store_test`). It holds a helper `func testRecipient(t *testing.T) string { return "age1" + randHex(t, 16) }` that the updated callers use as well.

- `./internal/store TestServerAgeRecipientConformanceSQLite`: `s, _ := newSQLite(t); runServerAgeRecipientConformance(t, s)`.
- `./internal/store TestServerAgeRecipientConformancePostgres`: `env := newPGEnv(t, true); runServerAgeRecipientConformance(t, env.openApp(t))`. `newPGEnv` skips without `MOONGLASS_TEST_POSTGRES` and fails under `CI`.
- `runServerAgeRecipientConformance(t, s store.Store)`, as table-driven subtests:
  - "round trip": `AddServer(org, pub, rec)` returns `AgeRecipient == rec`. `ServerByPublicKey(pub).AgeRecipient == rec`. `Servers(org)` has one entry with `AgeRecipient == rec`. Failure messages print the got and want values.
  - "two servers keep their own": two `AddServer` calls in one org with distinct recipients. `Servers(org)` maps each ID to its own recipient, and `ServerByPublicKey` returns each one's own recipient.
  - "empty refused": add one server, then `AddServer(org, pub2, "")` returns a non-nil error. `Servers(org)` equals the list from before the call (same IDs and recipients), and `ServerByPublicKey(pub2)` is `errors.Is(err, store.ErrNotFound)`.
  - "org isolation": org A adds a server with recipient `recA`, org B adds its own. `Servers(B)` holds only B's server, no entry has A's ID, and no entry's `AgeRecipient` is `recA`.
- `./internal/store TestServerAgeRecipientMigration` (SQLite only):
  1. `path := sqlitePath(t); db := rawSQLite(t, path)`. Build `fstest.MapFS` from `migrations.SQLite()` with only the names `< "0007"`: read each entry with `fs.ReadFile`. Assert the map holds exactly the six files `0001`–`0006`, so the test fails loudly if numbering changes. `store.Migrate(ctx, db, partial)`.
  2. With raw SQL insert one `orgs` row, one `servers` row (`id, org_id, created_at`) and one `agent_keys` row (`public_key, server_id, org_id`).
  3. `store.Migrate(ctx, db, migrations.SQLite())` returns nil, because the checksums of `0001`–`0006` match.
  4. `SELECT agent_age_recipient FROM servers WHERE id = ?` returns `''`.
  5. `st, err := sqlite.Open(ctx, path)` (a migration no-op), then `st.ServerByPublicKey(ctx, pub)` returns the server with `AgeRecipient == ""` and the same ID.
  6. A raw `INSERT INTO servers (id, org_id, created_at, agent_age_recipient) VALUES (?, ?, ?, NULL)` returns a non-nil error.
- `./internal/server TestEnrollStoresAgeRecipient` (in `internal/server/enroll_test.go`): `f := newFixture(t); tok := f.issue(t); req := newRequest(t, tok)`. The post answers 200 `ACCEPTED`. `want, err := keys.AgreeRecipient(req.AgentAgeRecipient)` and `want.String()` has the prefix `age1`. `f.servers(t)` has length 1 and `AgeRecipient == want.String()`. `f.st.ServerByPublicKey(ctx, hex.EncodeToString(req.AgentSigningPublicKey))` gives the same recipient.
- `./internal/server TestEnrollDuplicateKeyStoresNoRecipient`: tokens A and B, `reqA := newRequest(t, tokA)`, `reqB := newRequest(t, tokB)`, `reqB.AgentSigningPublicKey = reqA.AgentSigningPublicKey`. Precondition: `!bytes.Equal(reqA.AgentAgeRecipient, reqB.AgentAgeRecipient)`. A answers `ACCEPTED` and B answers `TOKEN_INVALID`. `f.servers(t)` has length 1, with `AgeRecipient == AgreeRecipient(reqA.AgentAgeRecipient).String()` and `!= AgreeRecipient(reqB...).String()`.
- Existing tests: only the added argument changes (`testRecipient(t)` in `internal/store`, and in `internal/server/agent_test.go:58` a fixed non-empty `"age1" + hex.EncodeToString(pub)`). The call sites are `conformance_test.go:126,187,189`, `projects_conformance_test.go:29`, `releases_conformance_test.go:370`, `rollbacks_conformance_test.go:144`, `pg_rls_test.go:64` and `internal/server/agent_test.go:58`. No assertion changes.
- Run `make test-list` so `testdata/tests.txt` gains the five new names.

## Edge cases

- Empty recipient: refused in `AddServer` before `NewID` and any SQL. Covered by "empty refused": the list is unchanged and `ServerByPublicKey` returns `ErrNotFound`.
- Duplicate signing key with a different recipient: the `agent_keys` primary key fails inside the same transaction, so the new `servers` row and its recipient roll back. Covered by `TestEnrollDuplicateKeyStoresNoRecipient` and the existing "duplicate agent key with a second token".
- Wrong-length `agent_age_recipient`: the existing 32-byte check still answers 400 before `AgreeRecipient` and before the token is used. The existing tests cover it.
- `keys.AgreeRecipient` failing on a 32-byte input: age's X25519 recipient parse only checks the HRP and the length, so HTTP cannot reach this branch. It still answers 400 with a fixed message before `UseEnrolmentToken`, so the token is not spent. The branch exists because the error must be handled.
- Servers from before migration 0007: `DEFAULT ''`, and reads return `""`. Covered by `TestServerAgeRecipientMigration`.
- NULL: refused by `NOT NULL` on both backends. Raw SQLite insert in the migration test; `TestSchemasMatch` asserts the same nullability on Postgres.
- Cross-org reads: `Servers` runs in `inOrg` with an `org_id` predicate (and RLS on pg). `ServerByPublicKey` reads `servers` only after the org is resolved from `agent_keys`. Covered by "org isolation".
- Hostile recipient text: the store binds it as a parameter and never interpolates it. The only writer is enrolment, which stores `Recipient.String()`, a canonical `age1…` value.

## Risks

- Contract change: approved in `docs/plan/build-plan.md`. The column name, type and default match the ticket exactly. Nothing else in the schema changes.
- `TestSchemasMatch` or `TestLookupTablesMatchSchema`: the two `0007` files are identical. The column is on `servers` (row-level), not on a lookup table. `LookupTables` is untouched.
- `TestMigrateIdempotent` and checksum errors: only new files are added, and no applied migration is edited.
- Postgres grants: `pg.Migrate` grants per table, so the app role can write the new column. The Postgres conformance test runs in CI and proves it.
- Missed callers break the build: `AddServer` has one implementation (`SQLStore`). The eight call sites are listed above; `go build ./...` and `make ci` catch any others.
- depguard: no `filippo.io/age` import outside `internal/keys`. `internal/server` already imports `internal/keys` (`agent.go`).
- Banned words and logging: no TODO or fallback wording, and the new 400 message is fixed. Enrolment still writes no logs.
- Size gate: tests and docs do not count, and the counted diff is about 35 lines out of 90.
- Spec drift: `storage.md` and `enrolment.md` change in the same diff. The docs claim only that the public recipient is stored. Wrapping and the fingerprint check stay W4-8, W3-3 and `secrets.md`.

## Verification

`make test-one PKG=./internal/store NAME=TestServerAgeRecipient` (and each named test), `make test-list`, `make ci`, and `scripts/verify check FR1-2`.
