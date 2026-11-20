package state

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

const agentDBName = "agent.db"

func dbPath(dataDir string) string {
	return filepath.Join(dataDir, agentDBName)
}

func testTime() time.Time {
	return time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)
}

func testTimeText(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// testCommandSeq numbers the request ids testCommand mints, so every command
// written to one database has its own row.
var testCommandSeq atomic.Int64

// testCommand is the Command an existing test passes to a state write. Its
// result is never read back, so only carrying one matters.
func testCommand(t *testing.T, appID string) Command {
	t.Helper()
	return Command{
		RequestID: fmt.Sprintf("c-test-%d", testCommandSeq.Add(1)),
		Type:      CommandDeploy,
		AppID:     appID,
		Received:  testTime(),
		Result:    &agentv1.CommandResult{Outcome: agentv1.CommandOutcome_SUCCEEDED},
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sampleBinding() Binding {
	return Binding{Project: "stored-proj", Environment: "stored-env", Service: "stored-svc"}
}

func sampleRecord(appID, releaseID string) Record {
	return Record{
		AppID:       appID,
		ReleaseID:   releaseID,
		ImageRef:    "ghcr.io/acme/web:" + releaseID,
		ImageID:     "sha256:image-" + releaseID,
		RepoDigest:  "sha256:digest-" + releaseID,
		Port:        8080,
		HealthPath:  "/healthz",
		Domains:     []string{"a.example", "b.example"},
		ContainerID: "container-" + releaseID,
	}
}

// openDir opens the agent state in dir and closes it when the test ends.
func openDir(t *testing.T, dir string) *DB {
	t.Helper()
	d, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open(%q) error = %v, want nil", dir, err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	return openDir(t, dir), dir
}

func mustDeploy(t *testing.T, s release.State, r Record) release.State {
	t.Helper()
	next, err := s.Deploy(r.ReleaseID, r.ImageRef, r.HasPreDeploy)
	if err != nil {
		t.Fatalf("State.Deploy(%q, %q, %v) error = %v, want nil", r.ReleaseID, r.ImageRef, r.HasPreDeploy, err)
	}
	return next
}

func mustSwitch(t *testing.T, s release.State, releaseID string) release.State {
	t.Helper()
	next, err := s.Switch(releaseID)
	if err != nil {
		t.Fatalf("State.Switch(%q) error = %v, want nil", releaseID, err)
	}
	return next
}

func mustRecord(t *testing.T, d *DB, b Binding, r Record, s release.State, now time.Time) {
	t.Helper()
	if err := d.RecordCandidate(context.Background(), testCommand(t, r.AppID), b, r, s, now); err != nil {
		t.Fatalf("RecordCandidate(app %q, release %q) error = %v, want nil", r.AppID, r.ReleaseID, err)
	}
}

// captureStandardLogs runs fn with the standard log logger and the default slog
// logger both writing into one buffer, and returns everything they logged.
func captureStandardLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer

	logger := log.Default()
	oldWriter := logger.Writer()
	oldFlags := logger.Flags()
	logger.SetOutput(&buf)
	logger.SetFlags(0)
	defer func() {
		logger.SetOutput(oldWriter)
		logger.SetFlags(oldFlags)
	}()

	oldDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(oldDefault)

	fn()
	return buf.String()
}

// tableRows renders every row of table as one string, ordered by primary key.
func tableRows(t *testing.T, d *DB, table string) []string {
	t.Helper()
	query := `SELECT * FROM "` + table + `" ORDER BY 1`
	if table == "releases" {
		query = `SELECT * FROM "` + table + `" ORDER BY 1, 2`
	}
	rows, err := d.db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("select %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	names, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		cells := make([]string, len(vals))
		for i, v := range vals {
			switch x := v.(type) {
			case nil:
				cells[i] = "<null>"
			case []byte:
				cells[i] = fmt.Sprintf("%q", string(x))
			case string:
				cells[i] = fmt.Sprintf("%q", x)
			default:
				cells[i] = fmt.Sprintf("%v", x)
			}
		}
		out = append(out, strings.Join(cells, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %s: %v", table, err)
	}
	return out
}

func snapshot(t *testing.T, d *DB) []string {
	t.Helper()
	out := []string{}
	for _, table := range []string{"apps", "releases"} {
		for _, row := range tableRows(t, d, table) {
			out = append(out, table+"|"+row)
		}
	}
	return out
}

func tableNames(t *testing.T, d *DB) []string {
	t.Helper()
	rows, err := d.db.QueryContext(context.Background(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read tables: %v", err)
	}
	return out
}

func columns(t *testing.T, d *DB, table string) []string {
	t.Helper()
	rows, err := d.db.QueryContext(context.Background(), `PRAGMA table_info("`+table+`")`)
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var (
			cid        int64
			name       string
			columnType string
			notNull    int64
			defaultVal any
			pk         int64
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("scan pragma table_info(%s): %v", table, err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read pragma table_info(%s): %v", table, err)
	}
	return out
}

type releaseRow struct {
	AppID      string
	ReleaseID  string
	ImageRef   string
	ImageID    string
	RepoDigest string
	Port       int64
	HealthPath string
	Domains    string
	// HasPreDeploy is 1 when the release has a pre-deploy step.
	HasPreDeploy        int64
	Status              string
	PreDeployReversible sql.NullString
	ContainerID         string
	Error               string
	CreatedAt           string
	SwitchedAt          sql.NullString
	StopAfter           sql.NullString
}

const selectRelease = `SELECT app_id, release_id, image_ref, image_id, repo_digest, port, health_path, domains,
	has_pre_deploy, pre_deploy_reversible, status, container_id, error, created_at, switched_at, stop_after
	FROM releases WHERE app_id = ? AND release_id = ?`

func readRelease(t *testing.T, d *DB, appID, releaseID string) releaseRow {
	t.Helper()
	var r releaseRow
	err := d.db.QueryRowContext(context.Background(), selectRelease, appID, releaseID).Scan(
		&r.AppID, &r.ReleaseID, &r.ImageRef, &r.ImageID, &r.RepoDigest, &r.Port, &r.HealthPath, &r.Domains,
		&r.HasPreDeploy, &r.PreDeployReversible, &r.Status, &r.ContainerID, &r.Error, &r.CreatedAt,
		&r.SwitchedAt, &r.StopAfter,
	)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("releases row for app %q and release %q is missing, want one", appID, releaseID)
	}
	if err != nil {
		t.Fatalf("read releases row for app %q and release %q: %v", appID, releaseID, err)
	}
	return r
}

type appRow struct {
	binding   Binding
	Current   sql.NullString
	Previous  sql.NullString
	Candidate sql.NullString
	Phase     string
	CreatedAt string
}

func readApp(t *testing.T, d *DB, appID string) appRow {
	t.Helper()
	var row appRow
	err := d.db.QueryRowContext(context.Background(),
		`SELECT project, environment, service, current_release_id, previous_release_id, candidate_release_id, phase, created_at
		FROM apps WHERE app_id = ?`, appID).
		Scan(&row.binding.Project, &row.binding.Environment, &row.binding.Service,
			&row.Current, &row.Previous, &row.Candidate, &row.Phase, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("apps row for %q is missing, want one", appID)
	}
	if err != nil {
		t.Fatalf("read apps row for %q: %v", appID, err)
	}
	return row
}

func assertReleaseRow(t *testing.T, got releaseRow, r Record, status, errText string, now time.Time) {
	t.Helper()
	if got.AppID != r.AppID {
		t.Errorf("releases.app_id = %q, want %q", got.AppID, r.AppID)
	}
	if got.ReleaseID != r.ReleaseID {
		t.Errorf("releases.release_id = %q, want %q", got.ReleaseID, r.ReleaseID)
	}
	if got.ImageRef != r.ImageRef {
		t.Errorf("releases.image_ref = %q, want %q", got.ImageRef, r.ImageRef)
	}
	if got.ImageID != r.ImageID {
		t.Errorf("releases.image_id = %q, want %q", got.ImageID, r.ImageID)
	}
	if got.RepoDigest != r.RepoDigest {
		t.Errorf("releases.repo_digest = %q, want %q", got.RepoDigest, r.RepoDigest)
	}
	if got.Port != int64(r.Port) {
		t.Errorf("releases.port = %d, want %d", got.Port, r.Port)
	}
	if got.HealthPath != r.HealthPath {
		t.Errorf("releases.health_path = %q, want %q", got.HealthPath, r.HealthPath)
	}
	var domains []string
	if err := json.Unmarshal([]byte(got.Domains), &domains); err != nil {
		t.Errorf("releases.domains = %q, want a JSON array: %v", got.Domains, err)
	} else if len(domains) != 0 || len(r.Domains) != 0 {
		if !reflect.DeepEqual(domains, r.Domains) {
			t.Errorf("releases.domains = %v, want %v", domains, r.Domains)
		}
	}
	wantPreDeploy := int64(0)
	if r.HasPreDeploy {
		wantPreDeploy = 1
	}
	if got.HasPreDeploy != wantPreDeploy {
		t.Errorf("releases.has_pre_deploy = %d, want %d", got.HasPreDeploy, wantPreDeploy)
	}
	if got.Status != status {
		t.Errorf("releases.status = %q, want %q", got.Status, status)
	}
	if got.ContainerID != r.ContainerID {
		t.Errorf("releases.container_id = %q, want %q", got.ContainerID, r.ContainerID)
	}
	if got.Error != errText {
		t.Errorf("releases.error = %q, want %q", got.Error, errText)
	}
	if want := testTimeText(now); got.CreatedAt != want {
		t.Errorf("releases.created_at = %q, want %q", got.CreatedAt, want)
	}
	nullColumns := []struct {
		name  string
		value sql.NullString
	}{
		{"pre_deploy_reversible", got.PreDeployReversible},
		{"switched_at", got.SwitchedAt},
		{"stop_after", got.StopAfter},
	}
	for _, column := range nullColumns {
		if column.value.Valid {
			t.Errorf("releases.%s = %q, want NULL", column.name, column.value.String)
		}
	}
}

// TestPackageImportsReleaseAndStoreOnly pins the imports of the package's own
// files: key material and the deploy executor stay out, and the command
// records' own messages are the one protocol import.
func TestPackageImportsReleaseAndStoreOnly(t *testing.T) {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read package directory %q: %v", dir, err)
	}
	fset := token.NewFileSet()
	importers := map[string][]string{}
	var sources []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %q: %v", name, err)
		}
		sources = append(sources, name)
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s has an import path %s that is not a quoted string: %v", name, spec.Path.Value, err)
			}
			importers[importPath] = append(importers[importPath], name)
		}
	}
	if len(sources) == 0 {
		t.Fatalf("non-test Go files in %q = none, want the package's implementation files", dir)
	}
	forbidden := []string{
		"moonglass.dev/moonglass/internal/keys",
		"moonglass.dev/moonglass/internal/protocol",
		"moonglass.dev/moonglass/internal/deploy",
	}
	// The command rows are the CommandResult protobuf, so the state package
	// imports exactly that message package and nothing else under protocol.
	const commandMessages = "moonglass.dev/moonglass/internal/protocol/agentv1"
	for importPath, users := range importers {
		if importPath == commandMessages {
			continue
		}
		for _, prefix := range forbidden {
			if importPath != prefix && !strings.HasPrefix(importPath, prefix+"/") {
				continue
			}
			t.Errorf("%v in package state import %q, want no import of %q or a package under it", users, importPath, prefix)
		}
	}
}

func TestOpenCreatesFileMode0600(t *testing.T) {
	t.Run("fresh file", func(t *testing.T) {
		d, dir := openTemp(t)
		path := dbPath(dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %q after Open: %v, want the file to exist", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %04o, want %04o", agentDBName, got, 0o600)
		}

		r := sampleRecord("app-1", "rel-1")
		s := mustDeploy(t, release.State{}, r)
		mustRecord(t, d, sampleBinding(), r, s, testTime())

		wal := path + "-wal"
		walInfo, err := os.Stat(wal)
		if err != nil {
			t.Fatalf("stat %q after a write: %v, want the file in WAL mode", wal, err)
		}
		if got := walInfo.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %04o, want %04o", filepath.Base(wal), got, 0o600)
		}
		shm := path + "-shm"
		shmInfo, err := os.Stat(shm)
		if err == nil {
			if got := shmInfo.Mode().Perm(); got != 0o600 {
				t.Errorf("%s mode = %04o, want %04o", filepath.Base(shm), got, 0o600)
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stat %q: %v", shm, err)
		}
	})

	t.Run("existing file with a looser mode", func(t *testing.T) {
		dir := t.TempDir()
		path := dbPath(dir)
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("create %q: %v", path, err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod %q to 0644: %v", path, err)
		}
		openDir(t, dir)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %q after Open: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode after Open = %04o, want %04o", agentDBName, got, 0o600)
		}
	})
}

func TestOpenSetsPragmas(t *testing.T) {
	want := []struct {
		name  string
		value string
	}{
		{"journal_mode", "wal"},
		{"synchronous", "2"},
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
		{"cache_size", "-2000"},
	}
	check := func(t *testing.T, d *DB, when string) {
		t.Helper()
		for _, p := range want {
			var got string
			if err := d.db.QueryRowContext(context.Background(), "PRAGMA "+p.name).Scan(&got); err != nil {
				t.Fatalf("%s: PRAGMA %s error = %v, want %q", when, p.name, err, p.value)
			}
			if strings.ToLower(got) != p.value {
				t.Errorf("%s: PRAGMA %s = %q, want %q", when, p.name, got, p.value)
			}
		}
	}

	dir := t.TempDir()
	d := openDir(t, dir)
	check(t, d, "first open")
	if err := d.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}
	reopened := openDir(t, dir)
	check(t, reopened, "second open")
}

func TestOpenMigratesSpecSchema(t *testing.T) {
	ctx := context.Background()
	d, dir := openTemp(t)

	wantTables := []string{"apps", "commands", "releases", "schema_migrations"}
	if got := tableNames(t, d); !reflect.DeepEqual(got, wantTables) {
		t.Fatalf("tables = %v, want %v", got, wantTables)
	}

	wantApps := []string{
		"app_id", "project", "environment", "service",
		"current_release_id", "previous_release_id", "candidate_release_id",
		"phase", "created_at",
	}
	if got := columns(t, d, "apps"); !reflect.DeepEqual(got, wantApps) {
		t.Errorf("apps columns = %v, want %v", got, wantApps)
	}
	wantReleases := []string{
		"app_id", "release_id", "image_ref", "image_id", "repo_digest",
		"port", "health_path", "domains", "has_pre_deploy", "pre_deploy_reversible",
		"status", "container_id", "error", "created_at", "switched_at", "stop_after",
	}
	if got := columns(t, d, "releases"); !reflect.DeepEqual(got, wantReleases) {
		t.Errorf("releases columns = %v, want %v", got, wantReleases)
	}

	rows, err := d.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close schema_migrations rows: %v", err)
	}
	if !contains(versions, "0001_releases.sql") {
		t.Errorf("schema_migrations versions = %v, want them to contain %q", versions, "0001_releases.sql")
	}
	if !contains(versions, "0002_commands.sql") {
		t.Errorf("schema_migrations versions = %v, want them to contain %q", versions, "0002_commands.sql")
	}

	r := sampleRecord("app-1", "rel-1")
	s := mustDeploy(t, release.State{}, r)
	mustRecord(t, d, sampleBinding(), r, s, testTime())
	before := snapshot(t, d)
	if err := d.Close(); err != nil {
		t.Fatalf("Close error = %v, want nil", err)
	}

	reopened := openDir(t, dir)
	if after := snapshot(t, reopened); !reflect.DeepEqual(before, after) {
		t.Errorf("a second Open changed the rows:\n before = %v\n after  = %v", before, after)
	}
	_, gotState, known, err := reopened.App(ctx, r.AppID)
	if err != nil {
		t.Fatalf("App(%q) after a second Open error = %v, want nil", r.AppID, err)
	}
	if !known {
		t.Errorf("App(%q) after a second Open known = false, want true", r.AppID)
	}
	if !reflect.DeepEqual(gotState, s) {
		t.Errorf("App(%q) after a second Open state = %+v, want %+v", r.AppID, gotState, s)
	}
}

func TestOpenErrorNamesPath(t *testing.T) {
	ctx := context.Background()

	t.Run("data directory does not exist", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "missing")
		d, err := Open(ctx, dir)
		if err == nil {
			_ = d.Close()
			t.Fatalf("Open(%q) error = nil, want an error", dir)
		}
		if d != nil {
			_ = d.Close()
			t.Errorf("Open(%q) DB = %v, want nil", dir, d)
		}
		path := dbPath(dir)
		if !strings.Contains(err.Error(), path) {
			t.Errorf("Open error = %q, want it to contain %q", err, path)
		}
		if _, statErr := os.Stat(dir); !errors.Is(statErr, fs.ErrNotExist) {
			t.Errorf("stat %q error = %v, want Open not to create the data directory", dir, statErr)
		}
	})

	t.Run("altered applied migration", func(t *testing.T) {
		d, dir := openTemp(t)
		path := dbPath(dir)
		if err := d.Close(); err != nil {
			t.Fatalf("Close error = %v, want nil", err)
		}
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("sql.Open(sqlite, %q) error = %v, want nil", path, err)
		}
		if _, err := raw.ExecContext(ctx, `UPDATE schema_migrations SET checksum = 'altered'`); err != nil {
			t.Fatalf("alter schema_migrations checksum: %v", err)
		}
		if err := raw.Close(); err != nil {
			t.Fatalf("close raw database handle: %v", err)
		}

		if _, openErr := Open(ctx, dir); openErr == nil {
			t.Fatalf("Open(%q) with an altered migration error = nil, want an error", dir)
		} else if !strings.Contains(openErr.Error(), path) {
			t.Errorf("Open error = %q, want it to contain %q", openErr, path)
		}
	})
}

func TestRecordCandidateFirstDeployWritesBinding(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name         string
		hasPreDeploy bool
		phase        release.Phase
	}{
		{"no pre-deploy", false, release.PhaseReady},
		{"with pre-deploy", true, release.PhasePending},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := openTemp(t)
			b := sampleBinding()
			r := sampleRecord("app-1", "rel-1")
			r.HasPreDeploy = tc.hasPreDeploy
			now := testTime()

			gotBinding, gotState, known, err := d.App(ctx, r.AppID)
			if err != nil {
				t.Fatalf("App(%q) before any record error = %v, want nil", r.AppID, err)
			}
			if known {
				t.Errorf("App(%q) before any record known = true, want false", r.AppID)
			}
			if gotBinding != (Binding{}) {
				t.Errorf("App(%q) before any record binding = %+v, want the zero binding", r.AppID, gotBinding)
			}
			if !reflect.DeepEqual(gotState, release.State{}) {
				t.Errorf("App(%q) before any record state = %+v, want the zero state", r.AppID, gotState)
			}

			s := mustDeploy(t, release.State{}, r)
			if s.Phase != tc.phase {
				t.Fatalf("State.Deploy(hasPreDeploy=%v).Phase = %v, want %v", tc.hasPreDeploy, s.Phase, tc.phase)
			}
			mustRecord(t, d, b, r, s, now)

			assertReleaseRow(t, readRelease(t, d, r.AppID, r.ReleaseID), r, "candidate", "", now)

			gotBinding, gotState, known, err = d.App(ctx, r.AppID)
			if err != nil {
				t.Fatalf("App(%q) after RecordCandidate error = %v, want nil", r.AppID, err)
			}
			if !known {
				t.Errorf("App(%q) after RecordCandidate known = false, want true", r.AppID)
			}
			if gotBinding != b {
				t.Errorf("App(%q) binding = %+v, want %+v", r.AppID, gotBinding, b)
			}
			if !reflect.DeepEqual(gotState, s) {
				t.Errorf("App(%q) state = %+v, want %+v", r.AppID, gotState, s)
			}

			row := readApp(t, d, r.AppID)
			if row.binding != b {
				t.Errorf("apps binding = %+v, want %+v", row.binding, b)
			}
			if row.Phase != tc.phase.String() {
				t.Errorf("apps.phase = %q, want %q", row.Phase, tc.phase.String())
			}
			if row.CreatedAt != testTimeText(now) {
				t.Errorf("apps.created_at = %q, want %q", row.CreatedAt, testTimeText(now))
			}
			if row.Current.Valid {
				t.Errorf("apps.current_release_id = %q on the first deploy, want NULL", row.Current.String)
			}
			if row.Previous.Valid {
				t.Errorf("apps.previous_release_id = %q on the first deploy, want NULL", row.Previous.String)
			}
			if !row.Candidate.Valid || row.Candidate.String != r.ReleaseID {
				t.Errorf("apps.candidate_release_id = %+v, want %q", row.Candidate, r.ReleaseID)
			}
		})
	}
}

func TestRecordCandidateKeepsBinding(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)
	b := sampleBinding()
	t1 := testTime()

	r1 := sampleRecord("app-1", "rel-1")
	s1 := mustDeploy(t, release.State{}, r1)
	mustRecord(t, d, b, r1, s1, t1)
	first := readApp(t, d, r1.AppID)

	t2 := t1.Add(time.Hour)
	r2 := sampleRecord("app-1", "rel-2")
	s2 := mustDeploy(t, mustSwitch(t, s1, r1.ReleaseID), r2)
	mustRecord(t, d, b, r2, s2, t2)

	second := readApp(t, d, r2.AppID)
	if second.binding != first.binding {
		t.Errorf("apps binding after a second candidate = %+v, want %+v", second.binding, first.binding)
	}
	if want := testTimeText(t1); second.CreatedAt != want {
		t.Errorf("apps.created_at after a second candidate = %q, want %q", second.CreatedAt, want)
	}
	if !second.Current.Valid || second.Current.String != r1.ReleaseID {
		t.Errorf("apps.current_release_id = %+v, want %q", second.Current, r1.ReleaseID)
	}
	if second.Previous.Valid {
		t.Errorf("apps.previous_release_id = %q, want NULL", second.Previous.String)
	}
	if !second.Candidate.Valid || second.Candidate.String != r2.ReleaseID {
		t.Errorf("apps.candidate_release_id = %+v, want %q", second.Candidate, r2.ReleaseID)
	}
	if want := release.PhaseReady.String(); second.Phase != want {
		t.Errorf("apps.phase after a second candidate = %q, want %q", second.Phase, want)
	}

	_, gotState, known, err := d.App(ctx, r2.AppID)
	if err != nil {
		t.Fatalf("App(%q) after a second candidate error = %v, want nil", r2.AppID, err)
	}
	if !known {
		t.Errorf("App(%q) after a second candidate known = false, want true", r2.AppID)
	}
	if !reflect.DeepEqual(gotState, s2) {
		t.Errorf("App(%q) state after a second candidate = %+v, want %+v", r2.AppID, gotState, s2)
	}

	t3 := t2.Add(time.Hour)
	r3 := sampleRecord("app-1", "rel-3")
	s3 := mustDeploy(t, mustSwitch(t, s2, r2.ReleaseID), r3)
	mustRecord(t, d, b, r3, s3, t3)

	third := readApp(t, d, r3.AppID)
	if !third.Current.Valid || third.Current.String != r2.ReleaseID {
		t.Errorf("apps.current_release_id = %+v, want %q", third.Current, r2.ReleaseID)
	}
	if !third.Previous.Valid || third.Previous.String != r1.ReleaseID {
		t.Errorf("apps.previous_release_id = %+v, want %q", third.Previous, r1.ReleaseID)
	}
	if !third.Candidate.Valid || third.Candidate.String != r3.ReleaseID {
		t.Errorf("apps.candidate_release_id = %+v, want %q", third.Candidate, r3.ReleaseID)
	}
	if third.CreatedAt != first.CreatedAt {
		t.Errorf("apps.created_at = %q, want %q", third.CreatedAt, first.CreatedAt)
	}

	_, gotState, known, err = d.App(ctx, r3.AppID)
	if err != nil {
		t.Fatalf("App(%q) after a third candidate error = %v, want nil", r3.AppID, err)
	}
	if !known {
		t.Errorf("App(%q) after a third candidate known = false, want true", r3.AppID)
	}
	if !reflect.DeepEqual(gotState, s3) {
		t.Errorf("App(%q) state after a third candidate = %+v, want %+v", r3.AppID, gotState, s3)
	}
}

func TestRecordCandidateRejectsOtherBinding(t *testing.T) {
	ctx := context.Background()
	b := sampleBinding()
	cases := []struct {
		name  string
		other Binding
	}{
		{"different project", Binding{Project: "other-proj", Environment: b.Environment, Service: b.Service}},
		{"different environment", Binding{Project: b.Project, Environment: "other-env", Service: b.Service}},
		{"different service", Binding{Project: b.Project, Environment: b.Environment, Service: "other-svc"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := openTemp(t)
			t1 := testTime()
			r1 := sampleRecord("app-1", "rel-1")
			s1 := mustDeploy(t, release.State{}, r1)
			mustRecord(t, d, b, r1, s1, t1)

			before := snapshot(t, d)
			r2 := sampleRecord("app-1", "rel-2")
			s2 := mustDeploy(t, mustSwitch(t, s1, r1.ReleaseID), r2)
			err := d.RecordCandidate(ctx, testCommand(t, r2.AppID), tc.other, r2, s2, t1.Add(time.Hour))
			if err == nil {
				t.Fatalf("RecordCandidate with binding %+v for an app bound to %+v error = nil, want an error naming the stored binding", tc.other, b)
			}
			for _, want := range []string{b.Project, b.Environment, b.Service} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("RecordCandidate error = %q, want it to name the stored binding %q", err, want)
				}
			}
			if after := snapshot(t, d); !reflect.DeepEqual(before, after) {
				t.Errorf("RecordCandidate with a different binding changed the rows:\n before = %v\n after  = %v", before, after)
			}
		})
	}
}

func TestRecordCandidateIsAtomic(t *testing.T) {
	ctx := context.Background()
	b := sampleBinding()
	t1 := testTime()

	type attempt struct {
		binding Binding
		record  Record
		state   release.State
	}
	cases := []struct {
		name    string
		known   bool
		prepare func(t *testing.T, d *DB) attempt
	}{
		{
			name:  "duplicate release id",
			known: true,
			prepare: func(t *testing.T, d *DB) attempt {
				r := sampleRecord("app-1", "rel-1")
				s := mustDeploy(t, release.State{}, r)
				mustRecord(t, d, b, r, s, t1)
				return attempt{binding: b, record: r, state: s}
			},
		},
		{
			name:  "state candidate is another release",
			known: true,
			prepare: func(t *testing.T, d *DB) attempt {
				r1 := sampleRecord("app-1", "rel-1")
				s1 := mustDeploy(t, release.State{}, r1)
				mustRecord(t, d, b, r1, s1, t1)
				r2 := sampleRecord("app-1", "rel-2")
				return attempt{binding: b, record: r2, state: s1}
			},
		},
		{
			name:  "state has no candidate",
			known: true,
			prepare: func(t *testing.T, d *DB) attempt {
				r1 := sampleRecord("app-1", "rel-1")
				s1 := mustDeploy(t, release.State{}, r1)
				mustRecord(t, d, b, r1, s1, t1)
				switched := mustSwitch(t, s1, r1.ReleaseID)
				r2 := sampleRecord("app-1", "rel-2")
				return attempt{binding: b, record: r2, state: switched}
			},
		},
		{
			name:  "duplicate release id from a failed row on an unknown app",
			known: false,
			prepare: func(t *testing.T, d *DB) attempt {
				r := sampleRecord("app-1", "rel-1")
				if err := d.RecordFailed(ctx, testCommand(t, r.AppID), r, "pull failed: manifest unknown", t1); err != nil {
					t.Fatalf("RecordFailed(app %q, release %q) error = %v, want nil", r.AppID, r.ReleaseID, err)
				}
				s := mustDeploy(t, release.State{}, r)
				return attempt{binding: b, record: r, state: s}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := openTemp(t)
			a := tc.prepare(t, d)
			before := snapshot(t, d)

			err := d.RecordCandidate(ctx, testCommand(t, a.record.AppID), a.binding, a.record, a.state, t1.Add(time.Hour))
			if err == nil {
				t.Fatalf("RecordCandidate(app %q, release %q) error = nil, want an error", a.record.AppID, a.record.ReleaseID)
			}
			if after := snapshot(t, d); !reflect.DeepEqual(before, after) {
				t.Errorf("a refused RecordCandidate changed the rows:\n before = %v\n after  = %v", before, after)
			}
			_, _, known, appErr := d.App(ctx, a.record.AppID)
			if appErr != nil {
				t.Fatalf("App(%q) error = %v, want nil", a.record.AppID, appErr)
			}
			if known != tc.known {
				t.Errorf("App(%q) known = %v after a refused RecordCandidate, want %v", a.record.AppID, known, tc.known)
			}
		})
	}
}

func TestRecordFailedLeavesStateUnchanged(t *testing.T) {
	ctx := context.Background()
	const detail = "pull failed: manifest unknown"
	cases := []struct {
		name  string
		known bool
	}{
		{"unknown app", false},
		{"known app", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := openTemp(t)
			b := sampleBinding()
			now := testTime()
			if tc.known {
				r0 := sampleRecord("app-1", "rel-0")
				s0 := mustDeploy(t, release.State{}, r0)
				mustRecord(t, d, b, r0, s0, now)
			}

			wantBinding, wantState, wantKnown, err := d.App(ctx, "app-1")
			if err != nil {
				t.Fatalf("App(%q) before RecordFailed error = %v, want nil", "app-1", err)
			}
			if wantKnown != tc.known {
				t.Fatalf("App(%q) before RecordFailed known = %v, want %v", "app-1", wantKnown, tc.known)
			}
			appRowsBefore := tableRows(t, d, "apps")

			r := sampleRecord("app-1", "rel-failed")
			failedAt := now.Add(time.Hour)
			if err := d.RecordFailed(ctx, testCommand(t, r.AppID), r, detail, failedAt); err != nil {
				t.Fatalf("RecordFailed(app %q, release %q) error = %v, want nil", r.AppID, r.ReleaseID, err)
			}
			assertReleaseRow(t, readRelease(t, d, r.AppID, r.ReleaseID), r, "failed", detail, failedAt)

			wantRows := 1
			if tc.known {
				wantRows = 2
			}
			var count int
			if err := d.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM releases`).Scan(&count); err != nil {
				t.Fatalf("count releases rows: %v", err)
			}
			if count != wantRows {
				t.Errorf("releases rows = %d, want %d", count, wantRows)
			}

			if after := tableRows(t, d, "apps"); !reflect.DeepEqual(appRowsBefore, after) {
				t.Errorf("RecordFailed changed the apps rows:\n before = %v\n after  = %v", appRowsBefore, after)
			}
			gotBinding, gotState, gotKnown, err := d.App(ctx, r.AppID)
			if err != nil {
				t.Fatalf("App(%q) after RecordFailed error = %v, want nil", r.AppID, err)
			}
			if gotKnown != tc.known {
				t.Errorf("App(%q) after RecordFailed known = %v, want %v", r.AppID, gotKnown, tc.known)
			}
			if gotBinding != wantBinding {
				t.Errorf("App(%q) binding after RecordFailed = %+v, want %+v", r.AppID, gotBinding, wantBinding)
			}
			if !reflect.DeepEqual(gotState, wantState) {
				t.Errorf("App(%q) state after RecordFailed = %+v, want %+v", r.AppID, gotState, wantState)
			}

			beforeSecond := snapshot(t, d)
			secondErr := d.RecordFailed(ctx, testCommand(t, r.AppID), r, detail, failedAt.Add(time.Hour))
			if secondErr == nil {
				t.Fatalf("second RecordFailed(app %q, release %q) error = nil, want an error", r.AppID, r.ReleaseID)
			}
			if strings.Contains(secondErr.Error(), detail) {
				t.Errorf("second RecordFailed error = %q, want it not to contain the stored detail %q", secondErr, detail)
			}
			if after := snapshot(t, d); !reflect.DeepEqual(beforeSecond, after) {
				t.Errorf("the refused RecordFailed changed the rows:\n before = %v\n after  = %v", beforeSecond, after)
			}
		})
	}

	t.Run("environment value and detail stay out of errors and logs", func(t *testing.T) {
		const envValue = "canary-env-6b1f0d7c"
		t.Setenv("MOONGLASS_ENVIRONMENT", envValue)

		d, _ := openTemp(t)
		b := sampleBinding()
		now := testTime()

		r1 := sampleRecord("app-1", "rel-1")
		s1 := mustDeploy(t, release.State{}, r1)
		mustRecord(t, d, b, r1, s1, now)

		r := sampleRecord("app-1", "rel-failed")
		other := Binding{Project: "other-proj", Environment: b.Environment, Service: b.Service}
		r2 := sampleRecord("app-1", "rel-2")
		s2 := mustDeploy(t, mustSwitch(t, s1, r1.ReleaseID), r2)

		var errs []error
		logs := captureStandardLogs(t, func() {
			if err := d.RecordFailed(ctx, testCommand(t, r.AppID), r, detail, now); err != nil {
				t.Errorf("RecordFailed(app %q, release %q) error = %v, want nil", r.AppID, r.ReleaseID, err)
			}
			errs = append(errs, d.RecordFailed(ctx, testCommand(t, r.AppID), r, detail, now.Add(time.Hour)))
			errs = append(errs, d.RecordCandidate(ctx, testCommand(t, r2.AppID), other, r2, s2, now))
		})
		if len(errs) != 2 {
			t.Fatalf("errors collected = %d, want 2", len(errs))
		}
		for i, err := range errs {
			if err == nil {
				t.Fatalf("refused write %d error = nil, want an error", i)
			}
			if strings.Contains(err.Error(), envValue) {
				t.Errorf("refused write %d error = %q, want it not to contain the environment value %q", i, err, envValue)
			}
			if strings.Contains(err.Error(), detail) {
				t.Errorf("refused write %d error = %q, want it not to contain the stored detail %q", i, err, detail)
			}
		}
		for _, want := range []string{b.Project, b.Environment, b.Service} {
			if !strings.Contains(errs[1].Error(), want) {
				t.Errorf("RecordCandidate error = %q, want it to name the stored binding %q", errs[1], want)
			}
		}
		if strings.Contains(logs, envValue) {
			t.Errorf("logs = %q, want them not to contain the environment value %q", logs, envValue)
		}
		if strings.Contains(logs, detail) {
			t.Errorf("logs = %q, want them not to contain the stored detail %q", logs, detail)
		}
	})
}

func TestRecordStoresTextVerbatim(t *testing.T) {
	ctx := context.Background()

	t.Run("hostile text", func(t *testing.T) {
		d, _ := openTemp(t)
		now := testTime()
		b := sampleBinding()
		r := sampleRecord("app-1", "rel-1'; DROP TABLE releases; --")
		r.ImageRef = `ghcr.io/acme/web:1.0@sha256:abc"; DROP TABLE apps; --`
		r.Domains = []string{`evil.example"; DROP TABLE apps; --`, "b.example"}
		s := mustDeploy(t, release.State{}, r)
		mustRecord(t, d, b, r, s, now)

		assertReleaseRow(t, readRelease(t, d, r.AppID, r.ReleaseID), r, "candidate", "", now)
		if got := tableNames(t, d); !reflect.DeepEqual(got, []string{"apps", "commands", "releases", "schema_migrations"}) {
			t.Errorf("tables after a record with hostile text = %v, want [apps commands releases schema_migrations]", got)
		}
		gotBinding, gotState, known, err := d.App(ctx, r.AppID)
		if err != nil {
			t.Fatalf("App(%q) error = %v, want nil", r.AppID, err)
		}
		if !known {
			t.Errorf("App(%q) known = false, want true", r.AppID)
		}
		if gotBinding != b {
			t.Errorf("App(%q) binding = %+v, want %+v", r.AppID, gotBinding, b)
		}
		if !reflect.DeepEqual(gotState, s) {
			t.Errorf("App(%q) state = %+v, want %+v", r.AppID, gotState, s)
		}
	})

	t.Run("no domains is an empty JSON array", func(t *testing.T) {
		d, _ := openTemp(t)
		now := testTime()
		r := sampleRecord("app-1", "rel-1")
		r.Domains = nil
		detail := `pull failed: "not found"; --`
		if err := d.RecordFailed(ctx, testCommand(t, r.AppID), r, detail, now); err != nil {
			t.Fatalf("RecordFailed(app %q, release %q) error = %v, want nil", r.AppID, r.ReleaseID, err)
		}
		row := readRelease(t, d, r.AppID, r.ReleaseID)
		if row.Domains != "[]" {
			t.Errorf("releases.domains with a nil slice = %q, want %q", row.Domains, "[]")
		}
		if row.Error != detail {
			t.Errorf("releases.error = %q, want %q", row.Error, detail)
		}
	})
}
