package server

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"moonglass.dev/moonglass/internal/registry"
	"moonglass.dev/moonglass/internal/store"
	"moonglass.dev/moonglass/internal/store/sqlite"
)

const (
	wantOwnerTokenLen      = len("mg_") + 52
	wantOwnerTokenLifetime = 90 * 24 * time.Hour
)

// openServerDB opens dir/server.db for raw queries beside the fixture's store.
func openServerDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("open %s/server.db: unexpected error %v", dir, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// readOrgID returns the org id Run stored in dir/org.
func readOrgID(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "org"))
	if err != nil {
		t.Fatalf("read %s/org: unexpected error %v", dir, err)
	}
	org := strings.TrimSpace(string(raw))
	if org == "" {
		t.Fatalf("org id in %s/org: expected a non-empty value, got %q", dir, raw)
	}
	return org
}

// captureLog sends the standard logger to a buffer for the test, so a token
// written to the log is observable.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(io.Discard) })
	return &logs
}

// wantNoAuditEvents asserts the org has no audit rows: minting a token is not
// a registry Invoke.
func wantNoAuditEvents(t *testing.T, st store.Store, org string) {
	t.Helper()
	evs, err := st.AuditEvents(context.Background(), org)
	if err != nil {
		t.Fatalf("AuditEvents: unexpected error %v", err)
	}
	if len(evs) != 0 {
		t.Errorf("audit events: expected none, got %d (%v)", len(evs), evs)
	}
}

// wantNoLeak asserts text does not appear in logs.
func wantNoLeak(t *testing.T, logs *bytes.Buffer, what, text string) {
	t.Helper()
	if got := logs.String(); strings.Contains(got, text) {
		t.Errorf("log output contains the %s %q: expected it not to reach the logger, got %q", what, text, got)
	}
}

func TestIssueOwnerTokenCreatesLocalOwner(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := captureLog(t)

	text, err := f.srv.IssueOwnerToken(ctx, f.org, "ci")
	if err != nil {
		t.Fatalf("IssueOwnerToken: unexpected error %v", err)
	}
	if len(text) != wantOwnerTokenLen || !strings.HasPrefix(text, "mg_") {
		t.Fatalf("token: expected an mg_ token of %d characters, got %q (%d characters)", wantOwnerTokenLen, text, len(text))
	}

	localOwner, err := f.st.UserForIdentity(ctx, "local:owner")
	if err != nil {
		t.Fatalf("UserForIdentity(local:owner): unexpected error %v", err)
	}
	members, err := f.st.Memberships(ctx, f.org)
	if err != nil {
		t.Fatalf("Memberships: unexpected error %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("memberships: expected exactly 1, got %d (%v)", len(members), members)
	}
	wantMember := store.Membership{OrgID: f.org, UserID: localOwner, Role: store.RoleOwner}
	if members[0] != wantMember {
		t.Errorf("membership: expected %+v, got %+v", wantMember, members[0])
	}

	p, err := f.srv.reg.Authenticate(ctx, text)
	if err != nil {
		t.Fatalf("Authenticate: unexpected error %v", err)
	}
	if p.OrgID != f.org || p.UserID != localOwner || p.Role != store.RoleOwner {
		t.Errorf("principal: expected org %s user %s role %q, got org %s user %s role %q",
			f.org, localOwner, store.RoleOwner, p.OrgID, p.UserID, p.Role)
	}
	wantScope := store.Scope{Project: "*", Environment: "*", Operation: "*"}
	if len(p.Scopes) != 1 || p.Scopes[0] != wantScope {
		t.Errorf("scopes: expected exactly [%+v], got %+v", wantScope, p.Scopes)
	}

	tk, role, err := f.st.TokenByHash(ctx, store.HashToken(text))
	if err != nil {
		t.Fatalf("TokenByHash: unexpected error %v", err)
	}
	if role != store.RoleOwner {
		t.Errorf("token role: expected %q, got %q", store.RoleOwner, role)
	}
	if tk.Name != "ci" {
		t.Errorf("token name: expected %q, got %q", "ci", tk.Name)
	}
	if want := testNow.Add(wantOwnerTokenLifetime).UTC().Format(time.RFC3339); tk.ExpiresAt != want {
		t.Errorf("token expiry: expected %q (the clock plus 90 days), got %q", want, tk.ExpiresAt)
	}
	if want := text[len(text)-4:]; tk.Last4 != want {
		t.Errorf("token last four: expected %q, got %q", want, tk.Last4)
	}

	db := openServerDB(t, f.dir)
	var hash string
	if err := db.QueryRow("SELECT hash FROM token_hashes").Scan(&hash); err != nil {
		t.Fatalf("read token_hashes: unexpected error %v", err)
	}
	if want := store.HashToken(text); hash != want {
		t.Errorf("stored token hash: expected store.HashToken(token) = %q, got %q", want, hash)
	}
	secret := strings.TrimPrefix(text, "mg_")
	for _, table := range []string{"tokens", "token_scopes", "token_hashes"} {
		rows, err := db.Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatalf("dump %s: unexpected error %v", table, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("columns of %s: unexpected error %v", table, err)
		}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: unexpected error %v", table, err)
			}
			if row := fmt.Sprint(vals...); strings.Contains(row, text) || strings.Contains(row, secret) {
				t.Errorf("table %s holds the token plaintext in row %v: expected only its hash and last four", table, vals)
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read %s: unexpected error %v", table, err)
		}
		_ = rows.Close()
	}

	wantNoAuditEvents(t, f.st, f.org)
	wantNoLeak(t, logs, "token", text)
	wantNoLeak(t, logs, "token secret", secret)
}

func TestIssueOwnerTokenUsesExistingOwner(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := captureLog(t)

	existing, err := f.st.UserForIdentity(ctx, "owner@example.com")
	if err != nil {
		t.Fatalf("UserForIdentity(owner@example.com): unexpected error %v", err)
	}
	if err := f.st.AddMembership(ctx, f.org, existing, store.RoleOwner); err != nil {
		t.Fatalf("AddMembership: unexpected error %v", err)
	}

	first, err := f.srv.IssueOwnerToken(ctx, f.org, "ci")
	if err != nil {
		t.Fatalf("IssueOwnerToken(ci): unexpected error %v", err)
	}
	second, err := f.srv.IssueOwnerToken(ctx, f.org, "laptop")
	if err != nil {
		t.Fatalf("IssueOwnerToken(laptop): unexpected error %v", err)
	}
	if first == second {
		t.Errorf("tokens: expected two different tokens, got %q twice", first)
	}
	for name, text := range map[string]string{"ci": first, "laptop": second} {
		p, err := f.srv.reg.Authenticate(ctx, text)
		if err != nil {
			t.Errorf("Authenticate(%s token): unexpected error %v", name, err)
			continue
		}
		if p.OrgID != f.org || p.UserID != existing || p.Role != store.RoleOwner {
			t.Errorf("principal for the %s token: expected org %s user %s role %q, got org %s user %s role %q",
				name, f.org, existing, store.RoleOwner, p.OrgID, p.UserID, p.Role)
		}
	}

	members, err := f.st.Memberships(ctx, f.org)
	if err != nil {
		t.Fatalf("Memberships: unexpected error %v", err)
	}
	want := []store.Membership{{OrgID: f.org, UserID: existing, Role: store.RoleOwner}}
	if len(members) != 1 || members[0] != want[0] {
		t.Errorf("memberships: expected exactly %v, got %v", want, members)
	}

	wantNoAuditEvents(t, f.st, f.org)
	wantNoLeak(t, logs, "first token", first)
	wantNoLeak(t, logs, "second token", second)
}

func TestIssueOwnerTokenRejectsEmptyNameWithoutWrites(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	logs := captureLog(t)

	text, err := f.srv.IssueOwnerToken(ctx, f.org, "")
	if err == nil {
		t.Fatalf("IssueOwnerToken with an empty name: expected an error, got token %q", text)
	}
	if !strings.Contains(err.Error(), "name is empty") {
		t.Errorf("error: expected it to say the name is empty, got %q", err)
	}
	if text != "" {
		t.Errorf("token: expected it empty on error, got %q", text)
	}
	if strings.Contains(err.Error(), "mg_") {
		t.Errorf("error: expected it to carry no token text, got %q", err)
	}
	if got := logs.String(); strings.Contains(got, "mg_") {
		t.Errorf("log output: expected no token on the empty-name path, got %q", got)
	}

	members, err := f.st.Memberships(ctx, f.org)
	if err != nil {
		t.Fatalf("Memberships: unexpected error %v", err)
	}
	if len(members) != 0 {
		t.Errorf("memberships: expected none after the rejection, got %v", members)
	}

	var n int
	if err := openServerDB(t, f.dir).QueryRow("SELECT COUNT(*) FROM tokens").Scan(&n); err != nil {
		t.Fatalf("count tokens: unexpected error %v", err)
	}
	if n != 0 {
		t.Errorf("tokens rows: expected 0 after the rejection, got %d", n)
	}

	wantNoAuditEvents(t, f.st, f.org)
}

// runServerOutput starts Run on dir with loopback listeners, reads the
// control-plane fingerprint line and the token lines, then cancels and asserts
// Run returned nil and printed nothing more.
func runServerOutput(t *testing.T, dir string, printEnrol bool, ownerName string) []string {
	t.Helper()
	wantLines := 1 // the control-plane fingerprint line
	if printEnrol {
		wantLines++
	}
	if ownerName != "" {
		wantLines++
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	pr, pw := io.Pipe()
	runDone := make(chan error, 1)
	go func() {
		err := Run(ctx, dir, "127.0.0.1:0", "127.0.0.1:0", printEnrol, ownerName, pw)
		_ = pw.Close()
		runDone <- err
	}()

	reader := bufio.NewReader(pr)
	lines := make([]string, 0, wantLines)
	for len(lines) < wantLines {
		type readResult struct {
			line string
			err  error
		}
		read := make(chan readResult, 1)
		go func() {
			line, err := reader.ReadString('\n')
			read <- readResult{line: strings.TrimSuffix(line, "\n"), err: err}
		}()
		select {
		case r := <-read:
			if r.err != nil {
				t.Fatalf("read stdout line %d of %d: unexpected error %v (read %q)", len(lines)+1, wantLines, r.err, r.line)
			}
			lines = append(lines, r.line)
		case <-time.After(10 * time.Second):
			t.Fatalf("read stdout line %d of %d: expected it within 10s, got nothing", len(lines)+1, wantLines)
		}
	}

	cancel()
	rest, err := io.ReadAll(reader)
	if err != nil {
		t.Errorf("read the rest of stdout: unexpected error %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("stdout after the %d expected lines: expected nothing, got %q", wantLines, rest)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: unexpected error %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Run: expected it to return after ctx cancel, still running after 10s")
	}
	return lines
}

// runServer returns the token lines of runServerOutput, after the fingerprint.
func runServer(t *testing.T, dir string, printEnrol bool, ownerName string) []string {
	t.Helper()
	return runServerOutput(t, dir, printEnrol, ownerName)[1:]
}

func TestRunPrintsOwnerTokenOnce(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logs := captureLog(t)

	before := time.Now()
	lines := runServer(t, dir, false, "ci")
	after := time.Now()

	if len(lines) != 1 {
		t.Fatalf("stdout lines: expected exactly 1, got %d (%q)", len(lines), lines)
	}
	token := lines[0]
	if len(token) != wantOwnerTokenLen || !strings.HasPrefix(token, "mg_") {
		t.Fatalf("printed line: expected an mg_ token of %d characters, got %q", wantOwnerTokenLen, token)
	}

	st, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: unexpected error %v", err)
	}
	org := readOrgID(t, dir)
	p, err := registry.New(st, time.Now).Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate the printed token: unexpected error %v", err)
	}
	if p.OrgID != org || p.Role != store.RoleOwner {
		t.Errorf("principal: expected org %s role %q, got org %s role %q", org, store.RoleOwner, p.OrgID, p.Role)
	}
	tk, _, err := st.TokenByHash(ctx, store.HashToken(token))
	if err != nil {
		t.Fatalf("TokenByHash: unexpected error %v", err)
	}
	expires, err := time.Parse(time.RFC3339, tk.ExpiresAt)
	if err != nil {
		t.Fatalf("parse expiry %q: unexpected error %v", tk.ExpiresAt, err)
	}
	if lower, upper := before.Truncate(time.Second).Add(wantOwnerTokenLifetime), after.Add(wantOwnerTokenLifetime); expires.Before(lower) || expires.After(upper) {
		t.Errorf("token expiry: expected between %s and %s (the call plus 90 days), got %s", lower, upper, expires)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: unexpected error %v", err)
	}

	second := runServer(t, dir, false, "ci")
	if len(second) != 1 {
		t.Fatalf("second run stdout lines: expected exactly 1, got %d (%q)", len(second), second)
	}
	if second[0] == token {
		t.Errorf("second run token: expected a different token, got the first one again (%q)", second[0])
	}

	st2, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open after the second run: unexpected error %v", err)
	}
	defer func() { _ = st2.Close() }()
	members, err := st2.Memberships(ctx, org)
	if err != nil {
		t.Fatalf("Memberships: unexpected error %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("memberships after the second run: expected exactly 1, got %d (%v)", len(members), members)
	}
	if members[0].Role != store.RoleOwner {
		t.Errorf("membership role after the second run: expected %q, got %q", store.RoleOwner, members[0].Role)
	}

	wantNoAuditEvents(t, st2, org)
	wantNoLeak(t, logs, "first token", token)
	wantNoLeak(t, logs, "second token", second[0])
}

func TestRunPrintsEnrolTokenBeforeOwnerToken(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	logs := captureLog(t)

	lines := runServer(t, dir, true, "ci")
	if len(lines) != 2 {
		t.Fatalf("stdout lines: expected exactly 2, got %d (%q)", len(lines), lines)
	}
	enrol, owner := lines[0], lines[1]

	st, err := sqlite.Open(ctx, filepath.Join(dir, "server.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: unexpected error %v", err)
	}
	defer func() { _ = st.Close() }()
	org := readOrgID(t, dir)
	reg := registry.New(st, time.Now)

	p, err := reg.Authenticate(ctx, owner)
	if err != nil {
		t.Errorf("Authenticate the second line: expected the owner token, got error %v", err)
	} else if p.OrgID != org || p.Role != store.RoleOwner {
		t.Errorf("second line principal: expected org %s role %q, got org %s role %q", org, store.RoleOwner, p.OrgID, p.Role)
	}
	if _, err := reg.Authenticate(ctx, enrol); !errors.Is(err, registry.ErrUnauthenticated) {
		t.Errorf("Authenticate the first line: expected %v (the enrolment token is not an owner token), got %v", registry.ErrUnauthenticated, err)
	}

	infos, err := st.EnrolmentTokens(ctx, org)
	if err != nil {
		t.Fatalf("EnrolmentTokens: unexpected error %v", err)
	}
	if len(infos) != 1 {
		t.Errorf("enrolment tokens: expected exactly 1, got %d (%v)", len(infos), infos)
	}

	wantNoAuditEvents(t, st, org)
	wantNoLeak(t, logs, "enrolment token", enrol)
	wantNoLeak(t, logs, "owner token", owner)
}
