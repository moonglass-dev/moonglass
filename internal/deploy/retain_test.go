package deploy

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	"moonglass.dev/moonglass/internal/release"
)

// stopImagePresent reports whether the Engine still has the image with id.
func stopImagePresent(t *testing.T, e *Engine, id string) bool {
	t.Helper()
	if _, err := e.cli.ImageInspect(context.Background(), id); err != nil {
		if isNotFound(err) {
			return false
		}
		t.Fatalf("ImageInspect(%q) error = %v, want an answer or not found", id, err)
	}
	return true
}

// TestRetainRemovesImagesOutsideFive covers retention's image step: the images
// of the retired rows retention deleted are removed without force, an image a
// kept release names or a container still uses stays in the store without an
// error, and no volume or network is removed.
func TestRetainRemovesImagesOutsideFive(t *testing.T) {
	ctx := context.Background()
	e := testEngine(t)
	p := testProject(t, e)
	x, dir := newExecutor(t, e)
	app := testAppID(p, "retain")
	base := time.Now().Add(-2 * time.Hour)

	p.registerNetwork("mg-" + p.name)
	if err := e.ensureNetwork(ctx, p.name); err != nil {
		t.Fatalf("ensureNetwork(%q) error = %v, want nil", p.name, err)
	}
	volName := "mg." + p.name + ".retain"
	if _, err := e.cli.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   volName,
		Labels: map[string]string{testLabelManaged: "true", testLabelProject: p.name},
	}); err != nil {
		t.Fatalf("VolumeCreate(%q) error = %v, want nil", volName, err)
	}
	t.Cleanup(func() {
		if _, err := e.cli.VolumeRemove(context.Background(), volName, client.VolumeRemoveOptions{Force: true}); err != nil {
			t.Errorf("cleanup: remove volume %s: %v", volName, err)
		}
	})

	// Nine imported images with distinct entrypoints, so their IDs differ.
	refs := make([]string, 10)
	ids := make([]string, 10)
	seen := map[string]string{}
	for i := 1; i <= 9; i++ {
		ref := "moonglass.local/" + p.name + "/web:r" + strconv.Itoa(i)
		p.registerImage(ref)
		id := importImage(t, e, ref, []string{"/app", "print", "retain-" + strconv.Itoa(i), "0"})
		if other, duplicate := seen[id]; duplicate {
			t.Fatalf("images %s and %s have the same ID %s, want distinct images", other, ref, id)
		}
		seen[id] = ref
		refs[i], ids[i] = ref, id
	}

	// r9 is current, r8 previous and r1 to r7 retired. The rows are written
	// directly: no container is created for any of them.
	b := state.Binding{Project: p.name, Environment: testEnvironment, Service: testService}
	st := release.State{}
	for i := 1; i <= 9; i++ {
		rel := "r" + strconv.Itoa(i)
		rec := state.Record{
			AppID: app, ReleaseID: rel, ImageRef: refs[i], ImageID: ids[i], Port: 8080,
			Domains: []string{},
		}
		next, err := st.Deploy(rel, refs[i], false)
		if err != nil {
			t.Fatalf("State.Deploy(%q, %q, false) error = %v, want nil", rel, refs[i], err)
		}
		created := base.Add(time.Duration(i) * time.Minute)
		if err := x.db.RecordCandidate(ctx, seedCommand(t, rec.AppID), b, rec, next, created); err != nil {
			t.Fatalf("RecordCandidate(%q) error = %v, want nil", rel, err)
		}
		switched, err := next.Switch(rel)
		if err != nil {
			t.Fatalf("State.Switch(%q) error = %v, want nil", rel, err)
		}
		if err := x.db.RecordSwitch(ctx, seedCommand(t, app), app, rel, switched, created.Add(30*time.Minute)); err != nil {
			t.Fatalf("RecordSwitch(%q) error = %v, want nil", rel, err)
		}
		st = switched
	}

	// Every retired row has stopped, so retention may delete the four outside
	// the five.
	now := time.Now()
	for i := 1; i <= 7; i++ {
		if err := x.db.RecordStopped(ctx, app, "r"+strconv.Itoa(i), now); err != nil {
			t.Fatalf("RecordStopped(r%d) error = %v, want nil", i, err)
		}
	}

	// r1 is one of the deleted rows and names the image r7 keeps.
	execAgentSQL(t, dir,
		`UPDATE releases SET image_id = ? WHERE app_id = ? AND release_id = ?`, ids[7], app, "r1")

	// r2 is another deleted row, and a container still uses its image.
	holder := createFixture(t, e, emptyPolicy(t, policy.Env{}),
		fixtureSpec(p, app, "r2", refs[2], true), "mg-"+p.name+"-holder")

	// The Engine no longer has r3's image, which retention must also accept.
	if _, err := e.cli.ImageRemove(ctx, ids[3], client.ImageRemoveOptions{}); err != nil {
		t.Fatalf("ImageRemove(%s) error = %v, want nil", ids[3], err)
	}
	delete(p.images, refs[3])

	if err := x.retain(ctx, now); err != nil {
		t.Fatalf("retain() error = %v, want nil even with an image in use", err)
	}

	for _, rel := range []string{"r1", "r2", "r3", "r4"} {
		if _, found := storedRow(t, dir, app, rel); found {
			t.Errorf("releases row for %s/%s is still present, want the row deleted", app, rel)
		}
	}
	for _, rel := range []string{"r5", "r6", "r7", "r8", "r9"} {
		if _, found := storedRow(t, dir, app, rel); !found {
			t.Errorf("releases row for %s/%s is missing, want it kept", app, rel)
		}
	}

	if !stopImagePresent(t, e, ids[7]) {
		t.Errorf("image %s shared with the kept row r7 is gone, want it kept", ids[7])
	}
	if !stopImagePresent(t, e, ids[2]) {
		t.Errorf("image %s of the deleted row r2 is gone, want it kept while container %s uses it", ids[2], holder)
	}
	if stopImagePresent(t, e, ids[3]) {
		t.Errorf("image %s of the deleted row r3 is present, want it removed", ids[3])
	}
	if stopImagePresent(t, e, ids[4]) {
		t.Errorf("image %s of the deleted row r4 is present, want it removed", ids[4])
	}
	delete(p.images, refs[4])

	if _, err := e.cli.VolumeInspect(ctx, volName, client.VolumeInspectOptions{}); err != nil {
		t.Errorf("VolumeInspect(%q) error = %v, want the volume kept", volName, err)
	}
	if _, err := e.cli.NetworkInspect(ctx, "mg-"+p.name, client.NetworkInspectOptions{}); err != nil {
		t.Errorf("NetworkInspect(%q) error = %v, want the network kept", "mg-"+p.name, err)
	}
}
