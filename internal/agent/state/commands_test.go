package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// commandFixture is one Command a state write records. The request id comes from
// prefix, so every case in a test owns its row, and every result field is set, so
// a column that drops one shows up in the comparison.
func commandFixture(prefix, typ, appID string) Command {
	return Command{
		RequestID: "c" + prefix,
		Type:      typ,
		AppID:     appID,
		Received:  testTime(),
		Result: &agentv1.CommandResult{
			Outcome:    agentv1.CommandOutcome_SUCCEEDED,
			Detail:     "",
			App:        &agentv1.AppState{CurrentReleaseId: "rel-current", CandidateReleaseId: "rel-candidate", Phase: agentv1.ReleasePhase_READY},
			ImageId:    "sha256:image-" + prefix,
			RepoDigest: "sha256:digest-" + prefix,
		},
	}
}

// commandRowAt reads one commands row straight from the database. It fails the
// test when the row is missing, because every recorded command must have one.
func commandRowAt(t *testing.T, d *DB, requestID string) (typ, appID string, result []byte, received, finished string) {
	t.Helper()
	err := d.db.QueryRowContext(context.Background(),
		`SELECT type, app_id, result, received_at, finished_at FROM commands WHERE request_id = ?`, requestID).
		Scan(&typ, &appID, &result, &received, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("commands row for request id %q is missing, want one", requestID)
	}
	if err != nil {
		t.Fatalf("read commands row for request id %q: %v", requestID, err)
	}
	return typ, appID, result, received, finished
}

// commandStateSnapshot renders every apps, releases and commands row, so a
// refused write can be shown to change nothing at all.
func commandStateSnapshot(t *testing.T, d *DB) []string {
	t.Helper()
	out := []string{}
	for _, table := range []string{"apps", "releases", "commands"} {
		for _, row := range tableRows(t, d, table) {
			out = append(out, table+"|"+row)
		}
	}
	return out
}

// commandExists reports whether a commands row for requestID exists.
func commandExists(t *testing.T, d *DB, requestID string) bool {
	t.Helper()
	var exists bool
	if err := d.db.QueryRowContext(context.Background(),
		`SELECT EXISTS(SELECT 1 FROM commands WHERE request_id = ?)`, requestID).Scan(&exists); err != nil {
		t.Fatalf("read whether request id %q has a commands row: %v", requestID, err)
	}
	return exists
}

// commandResultFor reads a stored result without failing when there is none.
func commandResultFor(t *testing.T, d *DB, requestID string) (*agentv1.CommandResult, bool) {
	t.Helper()
	got, found, err := d.CommandResult(context.Background(), requestID)
	if err != nil {
		t.Fatalf("CommandResult(%q) error = %v, want nil", requestID, err)
	}
	return got, found
}

// mustAbort is State.Abort with the failure reported.
func mustAbort(t *testing.T, s release.State, releaseID string) release.State {
	t.Helper()
	next, err := s.Abort(releaseID)
	if err != nil {
		t.Fatalf("State.Abort(%q) error = %v, want nil", releaseID, err)
	}
	return next
}

// TestCommandsResultIsNotNull covers "commands (W2-10a)" in
// docs/specs/agent-state.md: a command row is written only with its result, and
// the migration that creates the table follows 0001_releases.sql.
func TestCommandsResultIsNotNull(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)

	_, err := d.db.ExecContext(ctx,
		`INSERT INTO commands (request_id, type, app_id, result, received_at, finished_at)
		VALUES (?, ?, ?, NULL, ?, ?)`,
		"c-null-result", CommandDeploy, "app-1", formatTime(testTime()), formatTime(testTime()))
	if err == nil {
		t.Fatalf("inserting a commands row with a NULL result error = nil, want a NOT NULL constraint error")
	}
	if !strings.Contains(err.Error(), "NOT NULL") {
		t.Errorf("insert error = %q, want it to contain %q", err, "NOT NULL")
	}

	rows, err := d.db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()
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
	want := []string{"0001_releases.sql", "0002_commands.sql"}
	if !reflect.DeepEqual(versions, want) {
		t.Errorf("schema_migrations versions = %v, want %v", versions, want)
	}
}

// TestStateChangeWritesCommandRowInOneTransaction covers "Executor API
// (W2-10a)" in docs/specs/agent-state.md: each Record* write stores its Command
// in the same transaction as the state change it reports, and a refusal stores
// neither the change nor a new or changed command row.
func TestStateChangeWritesCommandRowInOneTransaction(t *testing.T) {
	ctx := context.Background()

	// seedCandidate and seedSwitch record the stored states the cases build on.
	seedCandidate := func(t *testing.T, d *DB, prefix string, r Record, st release.State) {
		t.Helper()
		if err := d.RecordCandidate(ctx, commandFixture(prefix, CommandDeploy, r.AppID), sampleBinding(), r, st, testTime()); err != nil {
			t.Fatalf("seed RecordCandidate(%q) error = %v, want nil", r.ReleaseID, err)
		}
	}
	seedSwitch := func(t *testing.T, d *DB, prefix, appID, releaseID string, next release.State) {
		t.Helper()
		if err := d.RecordSwitch(ctx, commandFixture(prefix, CommandSwitchRelease, appID), appID, releaseID, next, testTime()); err != nil {
			t.Fatalf("seed RecordSwitch(%q) error = %v, want nil", releaseID, err)
		}
	}

	type writeCase struct {
		// prepare brings a fresh database to the state the write needs and
		// returns the command plus the write bound to that state.
		prepare func(t *testing.T, d *DB) (Command, func(Command) error)
		// assert checks the release or app change that the successful write made.
		assert func(t *testing.T, d *DB)
		// refused runs the write with a state that does not follow from the
		// stored one, and returns the command it used.
		refused func(t *testing.T, d *DB) (Command, error)
		name    string
		// replay reports whether a second successful write of the same release
		// is refused because the release is already recorded.
		replay bool
	}

	cases := []writeCase{
		{
			name: "RecordCandidate",
			prepare: func(t *testing.T, d *DB) (Command, func(Command) error) {
				r := sampleRecord("app-1", "rel-1")
				next := mustDeploy(t, release.State{}, r)
				return commandFixture("candidate", CommandDeploy, r.AppID), func(cmd Command) error {
					return d.RecordCandidate(ctx, cmd, sampleBinding(), r, next, testTime())
				}
			},
			assert: func(t *testing.T, d *DB) {
				row := readRelease(t, d, "app-1", "rel-1")
				if row.Status != statusCandidate {
					t.Errorf("releases.status = %q, want %q", row.Status, statusCandidate)
				}
				app := readApp(t, d, "app-1")
				if !app.Candidate.Valid || app.Candidate.String != "rel-1" {
					t.Errorf("apps.candidate_release_id = %+v, want %q", app.Candidate, "rel-1")
				}
				if want := release.PhaseReady.String(); app.Phase != want {
					t.Errorf("apps.phase = %q, want %q", app.Phase, want)
				}
			},
			refused: func(t *testing.T, d *DB) (Command, error) {
				r := sampleRecord("app-1", "rel-2")
				// The candidate the state names is not the release being recorded.
				bad := release.State{Candidate: &release.Release{ID: "rel-other", Image: r.ImageRef}, Phase: release.PhaseReady}
				cmd := commandFixture("candidate-refused", CommandDeploy, r.AppID)
				return cmd, d.RecordCandidate(ctx, cmd, sampleBinding(), r, bad, testTime())
			},
			replay: true,
		},
		{
			name: "RecordPreDeploy",
			prepare: func(t *testing.T, d *DB) (Command, func(Command) error) {
				r := sampleRecord("app-1", "rel-1")
				r.HasPreDeploy = true
				pending := mustDeploy(t, release.State{}, r)
				seedCandidate(t, d, "predeploy-seed", r, pending)
				ready := mustPreDeployDone(t, pending, r.ReleaseID, true)
				return commandFixture("predeploy", CommandRunPreDeploy, r.AppID), func(cmd Command) error {
					return d.RecordPreDeploy(ctx, cmd, r.AppID, r.ReleaseID, ready, true, "", testTime())
				}
			},
			assert: func(t *testing.T, d *DB) {
				row := readRelease(t, d, "app-1", "rel-1")
				if row.Status != statusCandidate {
					t.Errorf("releases.status = %q, want %q", row.Status, statusCandidate)
				}
				if !row.PreDeployReversible.Valid || row.PreDeployReversible.String != "1" {
					t.Errorf("releases.pre_deploy_reversible = %+v, want a valid 1", row.PreDeployReversible)
				}
				app := readApp(t, d, "app-1")
				if want := release.PhaseReady.String(); app.Phase != want {
					t.Errorf("apps.phase = %q, want %q", app.Phase, want)
				}
			},
			refused: func(t *testing.T, d *DB) (Command, error) {
				r := sampleRecord("app-1", "rel-1")
				r.HasPreDeploy = true
				pending := mustDeploy(t, release.State{}, r)
				seedCandidate(t, d, "predeploy-refused-seed", r, pending)
				// The stored release has a pre-deploy step, so an aborted state
				// for a release the app never recorded cannot follow from it.
				bad := release.State{Candidate: &release.Release{ID: "rel-other", Image: r.ImageRef}, Phase: release.PhaseReady}
				cmd := commandFixture("predeploy-refused", CommandRunPreDeploy, r.AppID)
				return cmd, d.RecordPreDeploy(ctx, cmd, r.AppID, "rel-other", bad, true, "", testTime())
			},
		},
		{
			name: "RecordSwitch",
			prepare: func(t *testing.T, d *DB) (Command, func(Command) error) {
				r := sampleRecord("app-1", "rel-1")
				pending := mustDeploy(t, release.State{}, r)
				seedCandidate(t, d, "switch-seed", r, pending)
				next := mustSwitch(t, pending, r.ReleaseID)
				return commandFixture("switch", CommandSwitchRelease, r.AppID), func(cmd Command) error {
					return d.RecordSwitch(ctx, cmd, r.AppID, r.ReleaseID, next, testTime())
				}
			},
			assert: func(t *testing.T, d *DB) {
				row := readRelease(t, d, "app-1", "rel-1")
				if row.Status != statusCurrent {
					t.Errorf("releases.status = %q, want %q", row.Status, statusCurrent)
				}
				app := readApp(t, d, "app-1")
				if !app.Current.Valid || app.Current.String != "rel-1" {
					t.Errorf("apps.current_release_id = %+v, want %q", app.Current, "rel-1")
				}
			},
			refused: func(t *testing.T, d *DB) (Command, error) {
				r := sampleRecord("app-1", "rel-1")
				pending := mustDeploy(t, release.State{}, r)
				seedCandidate(t, d, "switch-refused-seed", r, pending)
				bad := release.State{Current: &release.Release{ID: "rel-other"}, Candidate: pending.Candidate, Phase: release.PhaseReady}
				cmd := commandFixture("switch-refused", CommandSwitchRelease, r.AppID)
				return cmd, d.RecordSwitch(ctx, cmd, r.AppID, r.ReleaseID, bad, testTime())
			},
		},
		{
			name: "RecordRollback",
			prepare: func(t *testing.T, d *DB) (Command, func(Command) error) {
				r1 := sampleRecord("app-1", "rel-1")
				pending1 := mustDeploy(t, release.State{}, r1)
				seedCandidate(t, d, "rollback-candidate-1", r1, pending1)
				switched1 := mustSwitch(t, pending1, r1.ReleaseID)
				seedSwitch(t, d, "rollback-switch-1", "app-1", r1.ReleaseID, switched1)

				r2 := sampleRecord("app-1", "rel-2")
				pending2 := mustDeploy(t, switched1, r2)
				seedCandidate(t, d, "rollback-candidate-2", r2, pending2)
				switched2 := mustSwitch(t, pending2, r2.ReleaseID)
				seedSwitch(t, d, "rollback-switch-2", "app-1", r2.ReleaseID, switched2)

				next := mustRollback(t, switched2)
				return commandFixture("rollback", CommandRollback, "app-1"), func(cmd Command) error {
					return d.RecordRollback(ctx, cmd, "app-1", next, testTime())
				}
			},
			assert: func(t *testing.T, d *DB) {
				row := readRelease(t, d, "app-1", "rel-1")
				if row.Status != statusCurrent {
					t.Errorf("releases.status of the restored release = %q, want %q", row.Status, statusCurrent)
				}
				rolledFrom := readRelease(t, d, "app-1", "rel-2")
				if rolledFrom.Status != statusRetired {
					t.Errorf("releases.status of the rolled-back-from release = %q, want %q", rolledFrom.Status, statusRetired)
				}
			},
			refused: func(t *testing.T, d *DB) (Command, error) {
				r1 := sampleRecord("app-1", "rel-1")
				pending1 := mustDeploy(t, release.State{}, r1)
				seedCandidate(t, d, "rollback-refused-candidate-1", r1, pending1)
				switched1 := mustSwitch(t, pending1, r1.ReleaseID)
				seedSwitch(t, d, "rollback-refused-switch-1", "app-1", r1.ReleaseID, switched1)

				r2 := sampleRecord("app-1", "rel-2")
				pending2 := mustDeploy(t, switched1, r2)
				seedCandidate(t, d, "rollback-refused-candidate-2", r2, pending2)
				switched2 := mustSwitch(t, pending2, r2.ReleaseID)
				seedSwitch(t, d, "rollback-refused-switch-2", "app-1", r2.ReleaseID, switched2)

				bad := release.State{Current: &release.Release{ID: "rel-other"}}
				cmd := commandFixture("rollback-refused", CommandRollback, "app-1")
				return cmd, d.RecordRollback(ctx, cmd, "app-1", bad, testTime())
			},
		},
		{
			name: "RecordAbort",
			prepare: func(t *testing.T, d *DB) (Command, func(Command) error) {
				r := sampleRecord("app-1", "rel-1")
				pending := mustDeploy(t, release.State{}, r)
				seedCandidate(t, d, "abort-seed", r, pending)
				aborted := mustAbort(t, pending, r.ReleaseID)
				return commandFixture("abort", CommandSwitchRelease, r.AppID), func(cmd Command) error {
					return d.RecordAbort(ctx, cmd, r.AppID, r.ReleaseID, aborted, "health gate failed", testTime())
				}
			},
			assert: func(t *testing.T, d *DB) {
				row := readRelease(t, d, "app-1", "rel-1")
				if row.Status != statusAborted {
					t.Errorf("releases.status = %q, want %q", row.Status, statusAborted)
				}
				if row.Error != "health gate failed" {
					t.Errorf("releases.error = %q, want %q", row.Error, "health gate failed")
				}
				app := readApp(t, d, "app-1")
				if app.Candidate.Valid {
					t.Errorf("apps.candidate_release_id = %q, want NULL", app.Candidate.String)
				}
				if want := release.PhaseNone.String(); app.Phase != want {
					t.Errorf("apps.phase = %q, want %q", app.Phase, want)
				}
			},
			refused: func(t *testing.T, d *DB) (Command, error) {
				r := sampleRecord("app-1", "rel-1")
				pending := mustDeploy(t, release.State{}, r)
				seedCandidate(t, d, "abort-refused-seed", r, pending)
				bad := release.State{Current: &release.Release{ID: "rel-other"}}
				cmd := commandFixture("abort-refused", CommandSwitchRelease, r.AppID)
				return cmd, d.RecordAbort(ctx, cmd, r.AppID, r.ReleaseID, bad, "health gate failed", testTime())
			},
		},
		{
			name: "RecordFailed",
			prepare: func(t *testing.T, d *DB) (Command, func(Command) error) {
				r := sampleRecord("app-1", "rel-1")
				return commandFixture("failed", CommandDeploy, r.AppID), func(cmd Command) error {
					return d.RecordFailed(ctx, cmd, r, "pull failed: manifest unknown", testTime())
				}
			},
			assert: func(t *testing.T, d *DB) {
				row := readRelease(t, d, "app-1", "rel-1")
				if row.Status != statusFailed {
					t.Errorf("releases.status = %q, want %q", row.Status, statusFailed)
				}
				if row.Error != "pull failed: manifest unknown" {
					t.Errorf("releases.error = %q, want %q", row.Error, "pull failed: manifest unknown")
				}
				if rows := tableRows(t, d, "apps"); len(rows) != 0 {
					t.Errorf("apps rows after RecordFailed = %v, want none", rows)
				}
			},
			replay: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("the change and its command row commit together", func(t *testing.T) {
				d, _ := openTemp(t)
				cmd, run := tc.prepare(t, d)

				if err := run(cmd); err != nil {
					t.Fatalf("%s error = %v, want nil", tc.name, err)
				}
				tc.assert(t, d)

				got, found := commandResultFor(t, d, cmd.RequestID)
				if !found {
					t.Fatalf("CommandResult(%q) found = false, want the stored result", cmd.RequestID)
				}
				if !proto.Equal(got, cmd.Result) {
					t.Errorf("CommandResult(%q) = %v, want %v", cmd.RequestID, got, cmd.Result)
				}
				typ, appID, _, _, _ := commandRowAt(t, d, cmd.RequestID)
				if typ != cmd.Type {
					t.Errorf("commands.type = %q, want %q", typ, cmd.Type)
				}
				if appID != cmd.AppID {
					t.Errorf("commands.app_id = %q, want %q", appID, cmd.AppID)
				}
			})

			if tc.refused != nil {
				t.Run("a state that does not follow writes nothing", func(t *testing.T) {
					d, _ := openTemp(t)
					cmd, refusedErr := tc.refused(t, d)
					if refusedErr == nil {
						t.Fatalf("%s with a state that does not follow error = nil, want an error", tc.name)
					}
					if got, found := commandResultFor(t, d, cmd.RequestID); found {
						t.Errorf("CommandResult(%q) found = true with %v after a refused write, want none", cmd.RequestID, got)
					}
					if commandExists(t, d, cmd.RequestID) {
						t.Errorf("a refused %s wrote a commands row for %q, want none", tc.name, cmd.RequestID)
					}
				})
			}

			t.Run("a request id that already has a row writes nothing", func(t *testing.T) {
				d, _ := openTemp(t)
				cmd, run := tc.prepare(t, d)
				if err := d.RecordCommand(ctx, cmd, testTime()); err != nil {
					t.Fatalf("RecordCommand(%q) error = %v, want nil", cmd.RequestID, err)
				}
				before := commandStateSnapshot(t, d)

				err := run(cmd)

				if err == nil {
					t.Fatalf("%s with a request id that already has a row error = nil, want an error", tc.name)
				}
				if after := commandStateSnapshot(t, d); !reflect.DeepEqual(before, after) {
					t.Errorf("a refused %s changed the rows:\n before = %v\n after  = %v", tc.name, before, after)
				}
			})

			if tc.replay {
				t.Run("a release that is already recorded writes nothing", func(t *testing.T) {
					d, _ := openTemp(t)
					cmd, run := tc.prepare(t, d)
					if err := run(cmd); err != nil {
						t.Fatalf("the first %s error = %v, want nil", tc.name, err)
					}

					second := cmd
					second.RequestID = cmd.RequestID + "-again"
					before := commandStateSnapshot(t, d)

					err := run(second)

					if err == nil {
						t.Fatalf("the second %s of an already recorded release error = nil, want an error", tc.name)
					}
					if after := commandStateSnapshot(t, d); !reflect.DeepEqual(before, after) {
						t.Errorf("a refused second %s changed the rows:\n before = %v\n after  = %v", tc.name, before, after)
					}
				})
			}
		})
	}
}

// TestCommandResultReturnsStoredResult covers DB.CommandResult: a stored result
// comes back equal to the one written, an unknown request id is no error, and
// the two times are RFC 3339 UTC with a Z whatever zone they were given in.
func TestCommandResultReturnsStoredResult(t *testing.T) {
	ctx := context.Background()
	d, _ := openTemp(t)

	zone := time.FixedZone("UTC+13", 13*60*60)
	received := time.Date(2026, 11, 20, 9, 30, 0, 0, zone)
	finished := received.Add(90 * time.Second)
	cmd := Command{
		RequestID: "c-stored",
		Type:      CommandDeploy,
		AppID:     "app-1",
		Received:  received,
		Result: &agentv1.CommandResult{
			Outcome:    agentv1.CommandOutcome_FAILED,
			Detail:     "pull failed: manifest unknown",
			App:        &agentv1.AppState{CurrentReleaseId: "rel-1", Phase: agentv1.ReleasePhase_READY},
			ImageId:    "sha256:image-1",
			RepoDigest: "sha256:digest-1",
		},
	}
	if err := d.RecordCommand(ctx, cmd, finished); err != nil {
		t.Fatalf("RecordCommand(%q) error = %v, want nil", cmd.RequestID, err)
	}

	got, found, err := d.CommandResult(ctx, cmd.RequestID)
	if err != nil {
		t.Fatalf("CommandResult(%q) error = %v, want nil", cmd.RequestID, err)
	}
	if !found {
		t.Fatalf("CommandResult(%q) found = false, want the stored result", cmd.RequestID)
	}
	if !proto.Equal(got, cmd.Result) {
		t.Errorf("CommandResult(%q) = %v, want %v", cmd.RequestID, got, cmd.Result)
	}

	unknown, found, err := d.CommandResult(ctx, "c-unknown")
	if err != nil {
		t.Fatalf("CommandResult(%q) error = %v, want nil", "c-unknown", err)
	}
	if found {
		t.Errorf("CommandResult(%q) found = true with %v, want false", "c-unknown", unknown)
	}
	if unknown != nil {
		t.Errorf("CommandResult(%q) = %v, want nil", "c-unknown", unknown)
	}

	_, _, _, receivedAt, finishedAt := commandRowAt(t, d, cmd.RequestID)
	shape := regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	for _, column := range []struct {
		name string
		text string
		want string
	}{
		{"received_at", receivedAt, received.UTC().Format(time.RFC3339)},
		{"finished_at", finishedAt, finished.UTC().Format(time.RFC3339)},
	} {
		if !shape.MatchString(column.text) {
			t.Errorf("commands.%s = %q, want RFC 3339 UTC with a Z", column.name, column.text)
		}
		if column.text != column.want {
			t.Errorf("commands.%s = %q, want %q", column.name, column.text, column.want)
		}
	}
}

// commandRetainFixture builds one agent database with the release rows retention
// keeps and prunes: six switched releases on app-1, the stops of r1 to r3 carried
// out, and a failed row more than seven days old. The command rows a caller adds
// are the only difference between two databases it builds.
func commandRetainFixture(t *testing.T, now time.Time) *DB {
	t.Helper()
	ctx := context.Background()
	d, _ := openTemp(t)
	base := testTime()

	st := release.State{}
	for i := 1; i <= 6; i++ {
		rel := fmt.Sprintf("r%d", i)
		r := sampleRecord("app-1", rel)
		pending := mustDeploy(t, st, r)
		if err := d.RecordCandidate(ctx, commandFixture("retain-candidate-"+rel, CommandDeploy, r.AppID), sampleBinding(), r, pending, base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("seed RecordCandidate(%q) error = %v, want nil", rel, err)
		}
		switched := mustSwitch(t, pending, rel)
		if err := d.RecordSwitch(ctx, commandFixture("retain-switch-"+rel, CommandSwitchRelease, r.AppID), r.AppID, rel, switched, base.Add(time.Duration(i*10)*time.Minute)); err != nil {
			t.Fatalf("seed RecordSwitch(%q) error = %v, want nil", rel, err)
		}
		st = switched
	}

	for i := 1; i <= 3; i++ {
		if err := d.RecordStopped(ctx, "app-1", fmt.Sprintf("r%d", i), now); err != nil {
			t.Fatalf("RecordStopped(r%d) error = %v, want nil", i, err)
		}
	}

	failed := sampleRecord("app-1", "r-failed")
	if err := d.RecordFailed(ctx, commandFixture("retain-failed", CommandDeploy, failed.AppID), failed, "pull failed: manifest unknown", now.Add(-8*24*time.Hour)); err != nil {
		t.Fatalf("seed RecordFailed(%q) error = %v, want nil", failed.ReleaseID, err)
	}
	return d
}

// TestRetainPrunesCommandsAfterSevenDays covers the command row of "Retention"
// in docs/specs/agent-state.md: a row finished more than seven days before now is
// deleted, the row just inside the seven days stays, and the release rows and
// returned images are exactly what they would be with no command row at all.
func TestRetainPrunesCommandsAfterSevenDays(t *testing.T) {
	ctx := context.Background()
	now := testTime().Add(24 * time.Hour)
	retention := 7 * 24 * time.Hour

	withCommands := commandRetainFixture(t, now)
	without := commandRetainFixture(t, now)

	old := commandFixture("retain-old", CommandDeploy, "app-1")
	old.Received = now.Add(-retention - 2*time.Second)
	oldFinished := now.Add(-retention - time.Second)
	kept := commandFixture("retain-kept", CommandDeploy, "app-1")
	kept.Received = now.Add(-retention + 2*time.Second)
	keptFinished := now.Add(-retention + time.Second)
	for _, cmd := range []struct {
		command  Command
		finished time.Time
	}{{old, oldFinished}, {kept, keptFinished}} {
		if err := withCommands.RecordCommand(ctx, cmd.command, cmd.finished); err != nil {
			t.Fatalf("RecordCommand(%q) error = %v, want nil", cmd.command.RequestID, err)
		}
	}

	gotImages, err := withCommands.Retain(ctx, now)
	if err != nil {
		t.Fatalf("Retain(%s) with command rows error = %v, want nil", now, err)
	}
	wantImages, err := without.Retain(ctx, now)
	if err != nil {
		t.Fatalf("Retain(%s) without command rows error = %v, want nil", now, err)
	}

	if commandExists(t, withCommands, old.RequestID) {
		t.Errorf("commands row %q finished at %s is still present, want it deleted",
			old.RequestID, formatTime(oldFinished))
	}
	got, found := commandResultFor(t, withCommands, kept.RequestID)
	if !found {
		t.Fatalf("commands row %q finished at %s is missing, want it kept", kept.RequestID, formatTime(keptFinished))
	}
	if !proto.Equal(got, kept.Result) {
		t.Errorf("CommandResult(%q) = %v, want %v", kept.RequestID, got, kept.Result)
	}

	if !reflect.DeepEqual(gotImages, wantImages) {
		t.Errorf("Retain images with command rows = %v, want %v, the same as without them", gotImages, wantImages)
	}
	gotRows := tableRows(t, withCommands, "releases")
	wantRows := tableRows(t, without, "releases")
	if !reflect.DeepEqual(gotRows, wantRows) {
		t.Errorf("releases rows with command rows = %v, want %v, the same as without them", gotRows, wantRows)
	}
}
