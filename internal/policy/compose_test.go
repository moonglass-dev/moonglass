package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const fixtureRoot = "../../testdata/escape/compose"

// requiredFixtures are the cases the spec's "Required coverage" names, plus one per denied field.
var requiredFixtures = []string{
	"benign", "deny-every-field", "profile", "extends-one-level", "extends-chained", "include", "anchor-merge",
	"privileged-default-interpolation", "privileged-from-env", "extends-adds-socket",
	"socket-aliases", "socket-dot-dot", "socket-interpolated", "socket-volume-driver-opts",
	"containerd-socket", "docker-data-root", "host-sysctl", "host-sysctl-block", "sysctl-allow-list",
	"tag-in-spec", "tag-in-block", "datadir-allowed", "datadir-symlink-out", "datadir-other-project",
	"datadir-other-image", "agent-state", "allow-wrong-key", "allow-pid-host", "block-wrong-environment",
	"block-wrong-service", "block-wrong-image", "network-service", "allow-docker-socket-exact",
	"allow-docker-socket-parent",
	"user-agent-label",
}

func readFixture(t *testing.T, l layout, name string) (dir string, expect []string, env map[string]string, toml string, hasTOML bool) {
	t.Helper()
	src := filepath.Join(fixtureRoot, name)
	dir = t.TempDir()
	rep := l.replacer()
	entries, err := os.ReadDir(src)
	must(t, err)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		must(t, err)
		text := rep.Replace(string(b))
		must(t, os.WriteFile(filepath.Join(dir, e.Name()), []byte(text), 0o600))
		switch e.Name() {
		case "expect":
			expect = strings.Fields(text)
		case "agent.toml":
			toml, hasTOML = text, true
		case "env":
			env = map[string]string{}
			for _, line := range strings.Split(text, "\n") {
				if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
					env[k] = v
				}
			}
		}
	}
	return dir, expect, env, toml, hasTOML
}

func composeIn(dir string, env map[string]string) ComposeInput {
	return ComposeInput{
		Project: "monitoring", Environment: "production", WorkDir: dir,
		Files: []string{filepath.Join(dir, "compose.yaml")}, Env: env,
	}
}

// TestComposeEscapes runs every directory in testdata/escape/compose. Each `expect` holds the
// violation Key of every violation ("-" for an empty Key), "ok", or "invalid-config" when
// agent.toml itself must stop the agent.
func TestComposeEscapes(t *testing.T) {
	for _, name := range requiredFixtures {
		if _, err := os.Stat(filepath.Join(fixtureRoot, name)); err != nil {
			t.Errorf("required fixture %q is missing: %v", name, err)
		}
	}
	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v, want the fixture directory", fixtureRoot, err)
	}
	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			l := newLayout(t)
			dir, expect, env, toml, _ := readFixture(t, l, e.Name())
			if len(expect) == 0 {
				t.Fatalf("fixture %s has an empty expect file", e.Name())
			}
			p, perr := Parse([]byte(toml), l.Env)
			if expect[0] == "invalid-config" {
				if perr == nil {
					t.Fatalf("Parse(agent.toml) error = nil, want the agent not to start")
				}
				return
			}
			if perr != nil {
				t.Fatalf("Parse(agent.toml) error = %v, want nil", perr)
			}
			checked, err := p.CheckCompose(context.Background(), composeIn(dir, env))
			if expect[0] == "ok" {
				if err != nil || len(checked) == 0 {
					t.Fatalf("CheckCompose() = %d specs, error %v; want specs and no error", len(checked), err)
				}
				return
			}
			var r *Rejection
			if !errors.As(err, &r) {
				t.Fatalf("CheckCompose() error = %v, want *Rejection with keys %v", err, expect)
			}
			want := append([]string(nil), expect...)
			sort.Strings(want)
			if got := keysOf(err); !reflect.DeepEqual(got, want) {
				t.Errorf("violation keys = %v, want %v (error: %v)", got, want, err)
			}
			if checked != nil {
				t.Errorf("CheckCompose() returned %d specs with a rejection, want none", len(checked))
			}
		})
	}
}

func writeCompose(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(yaml), 0o600))
	return dir
}

func TestComposeBenignProjectPasses(t *testing.T) {
	l := newLayout(t)
	dir := writeCompose(t, `services:
  a:
    image: `+imgA+`
    volumes:
      - data:/data
  b:
    image: `+imgB+`
    tmpfs:
      - /tmp
volumes:
  data:
`)
	p := mustParse(t, l.Env, "")
	got, err := p.CheckCompose(context.Background(), composeIn(dir, nil))
	if err != nil {
		t.Fatalf("CheckCompose() error = %v, want nil", err)
	}
	want := map[string]Spec{
		"a": {Kind: "compose", Project: "monitoring", Environment: "production", Service: "a", Image: imgA,
			Mounts: []Mount{{Type: "volume", Source: "data", Target: "/data"}}},
		"b": {Kind: "compose", Project: "monitoring", Environment: "production", Service: "b", Image: imgB,
			Mounts: []Mount{{Type: "tmpfs", Target: "/tmp"}}},
	}
	if len(got) != len(want) {
		t.Fatalf("CheckCompose() returned %d specs, want %d", len(got), len(want))
	}
	for _, c := range got {
		s := c.Spec()
		if w, ok := want[s.Service]; !ok || !reflect.DeepEqual(s, w) {
			t.Errorf("Checked spec for %q = %+v, want %+v", s.Service, s, w)
		}
	}
}

func TestComposeChecksEveryProfile(t *testing.T) {
	l := newLayout(t)
	dir := writeCompose(t, `services:
  web:
    image: `+imgA+`
  debug:
    image: `+imgA+`
    profiles: [debug]
    pid: host
`)
	p := mustParse(t, l.Env, "")
	_, err := p.CheckCompose(context.Background(), composeIn(dir, nil))
	if !hasKey(err, "pid_host") {
		t.Fatalf("CheckCompose() error = %v, want a pid_host rejection for the service behind an inactive profile", err)
	}
}

func TestComposeUsesSuppliedEnvOnly(t *testing.T) {
	l := newLayout(t)
	t.Setenv("PRIV", "true")
	dir := writeCompose(t, "services:\n  web:\n    image: "+imgA+"\n    privileged: ${PRIV:-false}\n")
	p := mustParse(t, l.Env, "")
	tests := []struct {
		name     string
		env      map[string]string
		wantPriv bool
	}{
		{"process env is ignored", nil, false},
		{"supplied env is used", map[string]string{"PRIV": "true"}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.CheckCompose(context.Background(), composeIn(dir, tc.env))
			if tc.wantPriv {
				if !hasKey(err, "privileged") {
					t.Fatalf("CheckCompose(env %v) error = %v, want a privileged rejection", tc.env, err)
				}
				return
			}
			if err != nil || len(got) != 1 || got[0].Spec().Privileged {
				t.Fatalf("CheckCompose(env %v) = %v, %v; want one unprivileged spec", tc.env, got, err)
			}
		})
	}
}

func TestComposeReturnsEveryViolation(t *testing.T) {
	l := newLayout(t)
	dir := writeCompose(t, `services:
  first:
    image: `+imgA+`
    pid: host
  second:
    image: `+imgA+`
    ipc: host
  fine:
    image: `+imgA+`
`)
	p := mustParse(t, l.Env, "")
	got, err := p.CheckCompose(context.Background(), composeIn(dir, nil))
	var r *Rejection
	if !errors.As(err, &r) {
		t.Fatalf("CheckCompose() error = %v, want one *Rejection", err)
	}
	if want := []string{"ipc_host", "pid_host"}; !reflect.DeepEqual(keysOf(err), want) {
		t.Errorf("violation keys = %v, want %v", keysOf(err), want)
	}
	for _, svc := range []string{`"first"`, `"second"`} {
		if !strings.Contains(err.Error(), svc) {
			t.Errorf("error = %q, want it to name service %s", err.Error(), svc)
		}
	}
	if strings.Contains(err.Error(), `"fine"`) {
		t.Errorf("error = %q, must not name the passing service", err.Error())
	}
	if got != nil {
		t.Errorf("CheckCompose() returned %d specs with a rejection, want none", len(got))
	}
}

func TestComposeLoaderErrorChecksNothing(t *testing.T) {
	l := newLayout(t)
	tests := []struct{ name, yaml string }{
		{"invalid yaml", "services: [\n  - : :\n"},
		{"unset required variable", "services:\n  web:\n    image: ${IMG:?the image is required}\n    pid: host\n"},
		{"missing file content", "services:\n  web: 3\n"},
	}
	p := mustParse(t, l.Env, "")
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.CheckCompose(context.Background(), composeIn(writeCompose(t, tc.yaml), nil))
			var r *Rejection
			if err == nil || errors.As(err, &r) {
				t.Fatalf("CheckCompose() error = %v, want a loader error that is not a *Rejection", err)
			}
			if got != nil {
				t.Errorf("CheckCompose() returned %d specs with a loader error, want none", len(got))
			}
		})
	}
}

func TestEscapeSuiteCoversEveryDeniedField(t *testing.T) {
	entries, err := os.ReadDir(fixtureRoot)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v, want the fixture directory", fixtureRoot, err)
	}
	fixtureKeys := map[string]bool{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(fixtureRoot, e.Name(), "expect"))
		must(t, err)
		for _, k := range strings.Fields(string(b)) {
			fixtureKeys[k] = true
		}
	}
	for _, row := range DeniedFields {
		_, inTable := deniedCases[row.Field]
		if !inTable && !fixtureKeys[row.Key] {
			t.Errorf("denied field %q (key %q) has no row in TestCheckDeniesEveryField and no fixture expecting that key", row.Field, row.Key)
		}
	}
}

func TestCheckComposeFillsLabelsOnly(t *testing.T) {
	l := newLayout(t)
	want := map[string]string{"app.tier": "web", "team": "core"}
	tests := map[string]string{
		"map form": `services:
  web:
    image: ` + imgA + `
    labels:
      app.tier: web
      team: core
`,
		"list form": `services:
  web:
    image: ` + imgA + `
    labels:
      - app.tier=web
      - team=core
`,
	}
	p := mustParse(t, l.Env, "")
	for name, yaml := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := p.CheckCompose(context.Background(), composeIn(writeCompose(t, yaml), nil))
			if err != nil {
				t.Fatalf("CheckCompose() error = %v, want nil", err)
			}
			if len(got) != 1 {
				t.Fatalf("CheckCompose() returned %d specs, want 1", len(got))
			}
			s := got[0].Spec()
			if !reflect.DeepEqual(s.Labels, want) {
				t.Errorf("Spec().Labels = %v, want %v", s.Labels, want)
			}
			if len(s.AgentLabels) != 0 {
				t.Errorf("Spec().AgentLabels = %v, want empty", s.AgentLabels)
			}
		})
	}
}
