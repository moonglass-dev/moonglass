package policy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

var (
	digestA = "sha256:" + strings.Repeat("a", 64)
	digestB = "sha256:" + strings.Repeat("b", 64)
	imgA    = "prom/node-exporter@" + digestA
	imgB    = "prom/node-exporter@" + digestB
)

func must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatal(err)
	}
}

// layout is a fake host under a temp directory. Nothing touches /var, /run or /etc.
type layout struct {
	Env Env

	Root   string
	Mounts string
	State  string
	Srv    string

	Sock     string
	SockDir  string
	SockLink string
	SockHard string
	// SockAlias reaches Sock through the var/run symlink, like /var/run -> /run.
	SockAlias string

	Containerd    string
	ContainerdDir string

	Docker    string
	DockerSub string
}

func newLayout(tb testing.TB) layout {
	tb.Helper()
	root, err := filepath.EvalSymlinks(tb.TempDir())
	must(tb, err)
	l := layout{Root: root}
	l.Mounts = filepath.Join(root, "mounts")
	l.State = filepath.Join(root, "state")
	l.Srv = filepath.Join(root, "srv", "data")
	l.SockDir = filepath.Join(root, "run")
	l.Sock = filepath.Join(l.SockDir, "docker.sock")
	l.SockAlias = filepath.Join(root, "var", "run", "docker.sock")
	l.SockLink = filepath.Join(root, "sock.link")
	l.SockHard = filepath.Join(root, "sock.hard")
	l.ContainerdDir = filepath.Join(root, "containerd")
	l.Containerd = filepath.Join(l.ContainerdDir, "containerd.sock")
	l.Docker = filepath.Join(root, "docker")
	l.DockerSub = filepath.Join(l.Docker, "volumes", "x")
	for _, d := range []string{
		filepath.Join(l.Mounts, "monitoring", "db"), filepath.Join(l.Mounts, "other", "db"),
		l.State, l.Srv, l.SockDir, filepath.Join(root, "var"), l.ContainerdDir, l.DockerSub,
	} {
		must(tb, os.MkdirAll(d, 0o755))
	}
	must(tb, os.WriteFile(l.Sock, nil, 0o600))
	must(tb, os.WriteFile(l.Containerd, nil, 0o600))
	must(tb, os.Symlink("../run", filepath.Join(root, "var", "run")))
	must(tb, os.Symlink(l.Sock, l.SockLink))
	must(tb, os.Link(l.Sock, l.SockHard))
	must(tb, os.Symlink(l.State, filepath.Join(l.Mounts, "monitoring", "link")))
	projects := map[string]string{"web1": "monitoring", "other1": "other"}
	l.Env = Env{
		Paths: Paths{
			MountsRoot:        l.Mounts,
			DockerSockets:     []string{l.SockAlias, l.Sock},
			ContainerdSockets: []string{l.Containerd},
			DockerRoot:        l.Docker,
		},
		DefaultRuntime: "runc",
		ContainerProject: func(ref string) (string, bool) {
			p, ok := projects[ref]
			return p, ok
		},
	}
	return l
}

// replacer substitutes the fixture tokens with this layout's paths.
func (l layout) replacer() *strings.Replacer {
	return strings.NewReplacer(
		"@IMAGE_B@", imgB, "@IMAGE@", imgA, "@MOUNTS@", l.Mounts, "@STATE@", l.State, "@SRV@", l.Srv,
		"@DOCKER_SOCK@", l.Sock, "@SOCK_ALIAS@", l.SockAlias, "@SOCK_LINK@", l.SockLink,
		"@SOCK_HARD@", l.SockHard, "@SOCK_DIR@", l.SockDir, "@CONTAINERD_SOCK@", l.Containerd,
		"@CONTAINERD_DIR@", l.ContainerdDir, "@DOCKER_ROOT@", l.Docker, "@DOCKER_SUB@", l.DockerSub,
	)
}

func blockTOML(body string) string { return rawBlock("monitoring", imgA, body) }

func rawBlock(project, image, body string) string {
	return fmt.Sprintf("[[containers.allow]]\nproject = %q\nimage = %q\n%s\n", project, image, body)
}

func mustParse(tb testing.TB, env Env, toml string) *Policy {
	tb.Helper()
	p, err := Parse([]byte(toml), env)
	if err != nil {
		tb.Fatalf("Parse(%q) error = %v, want nil", toml, err)
	}
	return p
}

func baseSpec() Spec {
	return Spec{Kind: "app", Project: "monitoring", Service: "node-exporter", Image: imgA}
}

func bindSpec(src, target string, ro bool) Spec {
	s := baseSpec()
	s.Mounts = []Mount{{Type: "bind", Source: src, Target: target, ReadOnly: ro}}
	return s
}

// keysOf returns the sorted violation keys of a *Rejection; "-" stands for an empty Key.
func keysOf(err error) []string {
	var r *Rejection
	if !errors.As(err, &r) {
		return nil
	}
	var keys []string
	for _, v := range r.Violations {
		k := v.Key
		if k == "" {
			k = "-"
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hasKey(err error, keys ...string) bool {
	for _, got := range keysOf(err) {
		for _, k := range keys {
			if got == k {
				return true
			}
		}
	}
	return false
}

func hasField(err error, prefix string) bool {
	var r *Rejection
	if !errors.As(err, &r) {
		return false
	}
	for _, v := range r.Violations {
		if strings.HasPrefix(v.Field, prefix) {
			return true
		}
	}
	return false
}

type checkCase struct {
	name  string
	env   *Env     // overrides the layout's Env
	block string   // agent.toml text; empty means no file
	spec  Spec     // the spec to check
	want  []string // violation keys, "-" for an empty Key; nil means the spec passes untouched
	msg   string   // substring of the error, optional
	field string   // prefix of a violation Field, optional
}

func runCheckCases(t *testing.T, l layout, cases []checkCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := l.Env
			if tc.env != nil {
				env = *tc.env
			}
			p, err := Parse([]byte(tc.block), env)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v, want nil", tc.block, err)
			}
			c, err := p.Check(tc.spec)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("Check(%+v) error = %v, want nil", tc.spec, err)
				}
				if got := c.Spec(); !reflect.DeepEqual(got, tc.spec) {
					t.Fatalf("Checked.Spec() = %+v, want %+v", got, tc.spec)
				}
				return
			}
			var r *Rejection
			if !errors.As(err, &r) {
				t.Fatalf("Check(%+v) error = %v, want *Rejection with keys %v", tc.spec, err, tc.want)
			}
			if got := keysOf(err); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("violation keys = %v, want %v (error: %v)", got, tc.want, err)
			}
			if tc.msg != "" && !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.msg)
			}
			if tc.field != "" && !hasField(err, tc.field) {
				t.Errorf("error = %v, want a violation Field starting with %q", err, tc.field)
			}
		})
	}
}
