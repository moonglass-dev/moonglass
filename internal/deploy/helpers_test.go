package deploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"moonglass.dev/moonglass/internal/agent/state"
	"moonglass.dev/moonglass/internal/policy"
	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
	"moonglass.dev/moonglass/internal/release"
)

// The label keys and the binding the executor sets, repeated here so the tests
// assert the contract rather than the package's own constants.
const (
	testLabelManaged     = "dev.moonglass.managed"
	testLabelProject     = "dev.moonglass.project"
	testLabelEnvironment = "dev.moonglass.environment"
	testLabelService     = "dev.moonglass.service"
	testLabelApp         = "dev.moonglass.app"
	testLabelRelease     = "dev.moonglass.release"

	testEnvironment = "production"
	testService     = "web"
)

// requireEqual fails with both values when got and want differ.
func requireEqual(t *testing.T, field string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", field, got, want)
	}
}

func randHex(t *testing.T, n int) string {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b)
}

// project is one test run's project name plus the networks and images the test
// created, so its cleanup removes everything it made.
type project struct {
	name     string
	networks map[string]bool
	images   map[string]bool
}

// testProject returns a project name unique to this run and registers its
// cleanup: every container and network carrying the project label, then every
// network and image the test registered (docs/specs/deploy-suite.md).
func testProject(t *testing.T, e *Engine) *project {
	t.Helper()
	p := &project{name: "t" + randHex(t, 12), networks: map[string]bool{}, images: map[string]bool{}}
	t.Cleanup(func() { p.remove(t, e) })
	return p
}

func (p *project) registerNetwork(name string) { p.networks[name] = true }

func (p *project) registerImage(ref string) { p.images[ref] = true }

func (p *project) remove(t *testing.T, e *Engine) {
	t.Helper()
	ctx := context.Background()

	containers, err := e.cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", testLabelProject+"="+p.name),
	})
	if err != nil {
		t.Errorf("cleanup: list containers of project %q: %v", p.name, err)
	}
	if err == nil {
		for _, c := range containers.Items {
			if _, err := e.cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
				t.Errorf("cleanup: remove container %s: %v", c.ID, err)
			}
		}
	}

	names := map[string]bool{}
	networks, err := e.cli.NetworkList(ctx, client.NetworkListOptions{
		Filters: make(client.Filters).Add("label", testLabelProject+"="+p.name),
	})
	if err != nil {
		t.Errorf("cleanup: list networks of project %q: %v", p.name, err)
	}
	if err == nil {
		for _, n := range networks.Items {
			names[n.Name] = true
		}
	}
	for name := range p.networks {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	for _, name := range sorted {
		if _, err := e.cli.NetworkInspect(ctx, name, client.NetworkInspectOptions{}); err != nil {
			continue
		}
		if _, err := e.cli.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil {
			t.Errorf("cleanup: remove network %s: %v", name, err)
		}
	}

	refs := make([]string, 0, len(p.images))
	for ref := range p.images {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	for _, ref := range refs {
		if _, err := e.cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: true}); err != nil {
			t.Errorf("cleanup: remove image %s: %v", ref, err)
		}
	}
}

// testEngine connects to the Engine in DOCKER_HOST, or the default socket. It
// skips when no Engine answers, and fails under CI, which must provide Docker.
func testEngine(t *testing.T) *Engine {
	t.Helper()
	engine, err := Connect(context.Background())
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("Connect() error = %v, want a Docker Engine: CI must provide Docker", err)
		}
		t.Skipf("no Docker Engine: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

// emptyPolicy is the agent.toml with no allow blocks.
func emptyPolicy(t *testing.T, env policy.Env) *policy.Policy {
	t.Helper()
	pol, err := policy.Parse(nil, env)
	if err != nil {
		t.Fatalf("policy.Parse(empty) error = %v, want nil", err)
	}
	return pol
}

// newExecutor returns an executor over a fresh agent database and an empty
// agent.toml, and that database's data directory.
func newExecutor(t *testing.T, e *Engine) (*Executor, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := state.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("state.Open(%q) error = %v, want nil", dir, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(e, emptyPolicy(t, policy.Env{}), db), dir
}

// importImage builds ./testdata/server for the Engine's platform and imports it
// as ref with entrypoint as its ENTRYPOINT. It returns the image ID.
func importImage(t *testing.T, e *Engine, ref string, entrypoint []string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "server")
	build := exec.Command("go", "build", "-o", binary, "./testdata/server")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./testdata/server: %v\n%s", err, out)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatalf("read %s: %v", binary, err)
	}

	var rootfs bytes.Buffer
	tw := tar.NewWriter(&rootfs)
	header := &tar.Header{Name: "app", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}

	changes := make([]string, 0, 1)
	if len(entrypoint) > 0 {
		encoded, err := json.Marshal(entrypoint)
		if err != nil {
			t.Fatalf("encode ENTRYPOINT %v: %v", entrypoint, err)
		}
		changes = append(changes, "ENTRYPOINT "+string(encoded))
	}

	ctx := context.Background()
	res, err := e.cli.ImageImport(ctx,
		client.ImageImportSource{Source: bytes.NewReader(rootfs.Bytes()), SourceName: "-"},
		ref,
		client.ImageImportOptions{Changes: changes},
	)
	if err != nil {
		t.Fatalf("ImageImport(%q) error = %v, want nil", ref, err)
	}
	if _, err := io.Copy(io.Discard, res); err != nil {
		_ = res.Close()
		t.Fatalf("ImageImport(%q) read: %v", ref, err)
	}
	if err := res.Close(); err != nil {
		t.Fatalf("ImageImport(%q) close: %v", ref, err)
	}
	return inspectImage(t, e, ref).ID
}

func inspectImage(t *testing.T, e *Engine, ref string) client.ImageInspectResult {
	t.Helper()
	res, err := e.cli.ImageInspect(context.Background(), ref)
	if err != nil {
		t.Fatalf("ImageInspect(%q) error = %v, want nil", ref, err)
	}
	return res
}

// deployMsg is one Deploy the protocol boundary accepts: app and release are
// identifiers, project a name, image a reference.
func deployMsg(projectName, app, rel, image string) *agentv1.Deploy {
	return &agentv1.Deploy{
		AppId:       app,
		ReleaseId:   rel,
		Image:       image,
		Project:     projectName,
		Environment: testEnvironment,
		Service:     testService,
	}
}

// appID is an app identifier unique to this run, so the container names built
// from it cannot collide with another worktree's run.
func testAppID(p *project, suffix string) string {
	return "app-" + suffix + "-" + p.name
}

// containerName is the name the executor gives the container of app at rel.
func testContainerName(app, rel string) string {
	return "mg-" + app + "-" + rel
}

// deployImported imports an image as moonglass.local/<project>/web:<rel>,
// deploys it as app and requires SUCCEEDED. It returns the Deploy it sent.
func deployImported(t *testing.T, e *Engine, x *Executor, p *project, app, rel string) *agentv1.Deploy {
	t.Helper()
	ref := "moonglass.local/" + p.name + "/web:" + rel
	p.registerImage(ref)
	importImage(t, e, ref, []string{"/app"})
	msg := deployMsg(p.name, app, rel, ref)
	res := x.Deploy(context.Background(), msg)
	if got, want := res.GetOutcome(), agentv1.CommandOutcome_SUCCEEDED; got != want {
		t.Fatalf("Deploy(%s/%s) outcome = %v with detail %q, want %v", app, rel, got, res.GetDetail(), want)
	}
	return msg
}

// releaseRow is the part of a releases row these tests assert.
type releaseRow struct {
	Status      string
	ImageID     string
	RepoDigest  string
	ContainerID string
	Err         string
}

// storedRow reads the releases row of rel directly from the agent database the
// executor wrote. found is false when there is no such row.
func storedRow(t *testing.T, dataDir, app, rel string) (releaseRow, bool) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "agent.db"))
	if err != nil {
		t.Fatalf("sql.Open(agent.db) error = %v, want nil", err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	var row releaseRow
	err = db.QueryRowContext(context.Background(),
		`SELECT status, image_id, repo_digest, container_id, error FROM releases WHERE app_id = ? AND release_id = ?`,
		app, rel).Scan(&row.Status, &row.ImageID, &row.RepoDigest, &row.ContainerID, &row.Err)
	if errors.Is(err, sql.ErrNoRows) {
		return releaseRow{}, false
	}
	if err != nil {
		t.Fatalf("read releases row of %s/%s: %v", app, rel, err)
	}
	return row, true
}

// storedApp reads the app's binding and release state through the state package.
func storedApp(t *testing.T, dataDir, app string) (state.Binding, release.State, bool) {
	t.Helper()
	ctx := context.Background()
	db, err := state.Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("state.Open(%q) error = %v, want nil", dataDir, err)
	}
	defer func() { _ = db.Close() }()
	binding, st, known, err := db.App(ctx, app)
	if err != nil {
		t.Fatalf("App(%q) error = %v, want nil", app, err)
	}
	return binding, st, known
}

// containerIDsNamed lists the IDs of the containers whose name is exactly name.
func containerIDsNamed(t *testing.T, e *Engine, name string) []string {
	t.Helper()
	res, err := e.cli.ContainerList(context.Background(), client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("name", "^/"+regexp.QuoteMeta(name)+"$"),
	})
	if err != nil {
		t.Fatalf("ContainerList(name=%q) error = %v, want nil", name, err)
	}
	var ids []string
	for _, c := range res.Items {
		for _, n := range c.Names {
			if n == "/"+name {
				ids = append(ids, c.ID)
			}
		}
	}
	sort.Strings(ids)
	return ids
}

func singleContainerID(t *testing.T, e *Engine, name string) string {
	t.Helper()
	ids := containerIDsNamed(t, e, name)
	if len(ids) != 1 {
		t.Fatalf("containers named %q = %v, want exactly 1", name, ids)
	}
	return ids[0]
}

// requireContainer returns the inspect result of the one container named name.
func requireContainer(t *testing.T, e *Engine, name string) container.InspectResponse {
	t.Helper()
	res, err := e.cli.ContainerInspect(context.Background(), singleContainerID(t, e, name), client.ContainerInspectOptions{})
	if err != nil {
		t.Fatalf("ContainerInspect(%q) error = %v, want nil", name, err)
	}
	return res.Container
}

func requireNoContainerNamed(t *testing.T, e *Engine, name string) {
	t.Helper()
	if ids := containerIDsNamed(t, e, name); len(ids) != 0 {
		t.Errorf("containers named %q = %v, want none", name, ids)
	}
}

func requireRunning(t *testing.T, e *Engine, name string) {
	t.Helper()
	c := requireContainer(t, e, name)
	if c.State == nil || !c.State.Running {
		t.Errorf("container %s state = %+v, want running", name, c.State)
	}
}

// projectContainerIDs lists, sorted, every container carrying the project label.
func projectContainerIDs(t *testing.T, e *Engine, p *project) []string {
	t.Helper()
	res, err := e.cli.ContainerList(context.Background(), client.ContainerListOptions{
		All:     true,
		Filters: make(client.Filters).Add("label", testLabelProject+"="+p.name),
	})
	if err != nil {
		t.Fatalf("ContainerList(label project=%s) error = %v, want nil", p.name, err)
	}
	ids := make([]string, 0, len(res.Items))
	for _, c := range res.Items {
		ids = append(ids, c.ID)
	}
	sort.Strings(ids)
	return ids
}

// projectNetworkNames lists, sorted, every network carrying the project label.
func projectNetworkNames(t *testing.T, e *Engine, p *project) []string {
	t.Helper()
	res, err := e.cli.NetworkList(context.Background(), client.NetworkListOptions{
		Filters: make(client.Filters).Add("label", testLabelProject+"="+p.name),
	})
	if err != nil {
		t.Fatalf("NetworkList(label project=%s) error = %v, want nil", p.name, err)
	}
	names := make([]string, 0, len(res.Items))
	for _, n := range res.Items {
		names = append(names, n.Name)
	}
	sort.Strings(names)
	return names
}

func requireNetwork(t *testing.T, e *Engine, name string) network.Inspect {
	t.Helper()
	res, err := e.cli.NetworkInspect(context.Background(), name, client.NetworkInspectOptions{})
	if err != nil {
		t.Fatalf("NetworkInspect(%q) error = %v, want nil", name, err)
	}
	return res.Network
}

// fixtureSpec is a container the executor did not create: an existing managed
// container of app at rel, or an unmanaged one carrying only the project label.
func fixtureSpec(p *project, app, rel, image string, managed bool) policy.Spec {
	labels := map[string]string{testLabelProject: p.name}
	if managed {
		labels[testLabelManaged] = "true"
		labels[testLabelEnvironment] = testEnvironment
		labels[testLabelService] = testService
		labels[testLabelApp] = app
		labels[testLabelRelease] = rel
	}
	return policy.Spec{
		Kind:        "app",
		Project:     p.name,
		Environment: testEnvironment,
		Service:     testService,
		Image:       image,
		AgentLabels: labels,
	}
}

// createFixture creates a fixture container through policy.Check and the
// package's one create path, at name. It returns the container ID.
func createFixture(t *testing.T, e *Engine, pol *policy.Policy, spec policy.Spec, name string) string {
	t.Helper()
	checked, err := pol.Check(spec)
	if err != nil {
		t.Fatalf("policy.Check(fixture %s) error = %v, want nil", name, err)
	}
	id, err := e.create(context.Background(), checked, name)
	if err != nil {
		t.Fatalf("create(%s) error = %v, want nil", name, err)
	}
	return id
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
