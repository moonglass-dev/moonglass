package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func fullExample() string {
	return `[update]
channel = "canary"

[commands]
forbid = ["delete", "exec"]

[sources]
repositories = ["github.com/acme/*"]
registries   = ["ghcr.io/acme/*"]

[[containers.allow]]
project     = "monitoring"
image       = "` + imgA + `"
pid_host    = true
bind_mounts = ["/proc:/host/proc:ro", "/sys:/host/sys:ro"]
`
}

func TestParseFullExample(t *testing.T) {
	l := newLayout(t)
	p, err := Parse([]byte(fullExample()), l.Env)
	if err != nil {
		t.Fatalf("Parse() error = %v, want nil", err)
	}
	want := Config{
		Channel:        "canary",
		ForbidCommands: []string{"delete", "exec"},
		Repositories:   []string{"github.com/acme/*"},
		Registries:     []string{"ghcr.io/acme/*"},
	}
	if got := p.Config(); !reflect.DeepEqual(got, want) {
		t.Errorf("Config() = %+v, want %+v", got, want)
	}
	cases := []checkCase{
		{name: "listed fields pass", block: fullExample(), spec: func() Spec {
			s := bindSpec("/proc", "/host/proc", true)
			s.PID = "host"
			s.Mounts = append(s.Mounts, Mount{Type: "bind", Source: "/sys", Target: "/host/sys", ReadOnly: true})
			return s
		}()},
		{name: "unlisted path rejected", block: fullExample(), spec: bindSpec("/etc", "/host/etc", true), want: []string{"bind_mounts"}},
		{name: "unlisted field rejected", block: fullExample(), spec: func() Spec { s := baseSpec(); s.IPC = "host"; return s }(), want: []string{"ipc_host"}},
	}
	runCheckCases(t, l, cases)
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	l := newLayout(t)
	block := fmt.Sprintf("[[containers.allow]]\nproject = %q\nimage = %q\n", "monitoring", imgA)
	tests := []struct {
		name string
		toml string
		key  string
		line int
	}{
		{"top level", "bogus = 1\n", "bogus", 1},
		{"unknown table", "[bogus]\nx = 1\n", "bogus", 1},
		{"update", "[update]\nchannel = \"stable\"\nbogus = 1\n", "bogus", 3},
		{"commands", "[commands]\nforbid = [\"delete\"]\nbogus = 1\n", "bogus", 3},
		{"sources", "[sources]\nbogus = [\"x\"]\n", "bogus", 2},
		{"allow block", block + "bogus = true\n", "bogus", 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.toml), l.Env)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want an error naming %q at line %d", tc.toml, tc.key, tc.line)
			}
			if want := fmt.Sprintf("line %d", tc.line); !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q and %q", err.Error(), tc.key, want)
			}
		})
	}
}

func TestParseRejectsInvalidUpdateCommandsSources(t *testing.T) {
	l := newLayout(t)
	tests := []struct {
		name string
		toml string
		key  string
	}{
		{"bad channel", "[update]\nchannel = \"beta\"\n", "channel"},
		{"unknown forbid class", "[commands]\nforbid = [\"restart\"]\n", "forbid"},
		{"duplicate forbid class", "[commands]\nforbid = [\"exec\", \"exec\"]\n", "forbid"},
		{"empty forbid list", "[commands]\nforbid = []\n", "forbid"},
		{"empty forbid entry", "[commands]\nforbid = [\"\"]\n", "forbid"},
		{"bad repository glob", "[sources]\nrepositories = [\"github.com/[acme\"]\n", "repositories"},
		{"bad registry glob", "[sources]\nregistries = [\"ghcr.io/[\"]\n", "registries"},
		{"empty repositories", "[sources]\nrepositories = []\n", "repositories"},
		{"empty registries", "[sources]\nregistries = []\n", "registries"},
		{"empty entry", "[sources]\nregistries = [\"\"]\n", "registries"},
		{"duplicate entry", "[sources]\nrepositories = [\"a/*\", \"a/*\"]\n", "repositories"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.toml), l.Env)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want an error naming %q", tc.toml, tc.key)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.key)
			}
		})
	}
}

func TestParseRejectsInvalidAllowBlocks(t *testing.T) {
	l := newLayout(t)
	good := blockTOML("pid_host = true")
	noProject := fmt.Sprintf("[[containers.allow]]\nimage = %q\npid_host = true\n", imgA)
	noImage := "[[containers.allow]]\nproject = \"monitoring\"\npid_host = true\n"
	tests := []struct {
		name  string
		block string // the second block, index 1
		key   string
	}{
		{"missing project", noProject, "project"},
		{"empty project", rawBlock("", imgA, "pid_host = true"), "project"},
		{"missing image", noImage, "image"},
		{"empty image", rawBlock("monitoring", "", "pid_host = true"), "image"},
		{"empty environment", blockTOML("environment = \"\"\npid_host = true"), "environment"},
		{"empty service", blockTOML("service = \"\"\npid_host = true"), "service"},
		{"no allow key", blockTOML(""), "containers.allow[1]"},
		{"only narrowing keys", blockTOML("environment = \"production\""), "containers.allow[1]"},
		{"boolean false", blockTOML("pid_host = false"), "pid_host"},
		{"boolean not a bool", blockTOML("privileged = \"yes\""), "privileged"},
		{"empty list", blockTOML("cap_add = []"), "cap_add"},
		{"empty entry", blockTOML("cap_add = [\"\"]"), "cap_add"},
		{"duplicate entry", blockTOML("cap_add = [\"NET_ADMIN\", \"CAP_NET_ADMIN\"]"), "cap_add"},
		{"malformed capability", blockTOML("cap_add = [\"NET ADMIN\"]"), "cap_add"},
		{"relative bind source", blockTOML("bind_mounts = [\"etc:/x\"]"), "bind_mounts"},
		{"relative bind target", blockTOML("bind_mounts = [\"/etc:x\"]"), "bind_mounts"},
		{"bind without target", blockTOML("bind_mounts = [\"/etc\"]"), "bind_mounts"},
		{"bind bad mode", blockTOML("bind_mounts = [\"/etc:/x:rx\"]"), "bind_mounts"},
		{"duplicate bind", blockTOML("bind_mounts = [\"/etc:/x\", \"/etc:/x\"]"), "bind_mounts"},
		{"device relative", blockTOML("devices = [\"dev/fuse\"]"), "devices"},
		{"device bad perms", blockTOML("devices = [\"/dev/fuse:/dev/fuse:zzz\"]"), "devices"},
		{"unknown entitlement", blockTOML("build_entitlements = [\"bogus\"]"), "build_entitlements"},
		{"project not an element", rawBlock("a/b", imgA, "pid_host = true"), "project"},
		{"project dot dot", rawBlock("..", imgA, "pid_host = true"), "project"},
		{"data_dirs slash", blockTOML("data_dirs = [\"a/b\"]"), "data_dirs"},
		{"data_dirs dot", blockTOML("data_dirs = [\".\"]"), "data_dirs"},
		{"data_dirs dot dot", blockTOML("data_dirs = [\"..\"]"), "data_dirs"},
		{"data_dirs control char", blockTOML("data_dirs = [\"a\\u0001b\"]"), "data_dirs"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(good+tc.block), l.Env)
			if err == nil {
				t.Fatalf("Parse() error = nil, want an error naming containers.allow[1] and %q", tc.key)
			}
			for _, want := range []string{"containers.allow[1]", tc.key} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestParseRejectsTagImage(t *testing.T) {
	l := newLayout(t)
	tests := []struct{ name, image string }{
		{"tag", "prom/node-exporter:latest"},
		{"tag and digest", "prom/node-exporter:v1@" + digestA},
		{"short digest", "prom/node-exporter@sha256:abc123"},
		{"non-sha256 digest", "prom/node-exporter@sha512:" + strings.Repeat("a", 64)},
		{"uppercase hex", "prom/node-exporter@sha256:" + strings.Repeat("A", 64)},
		{"no tag or digest", "prom/node-exporter"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(rawBlock("monitoring", tc.image, "pid_host = true")), l.Env)
			if err == nil {
				t.Fatalf("Parse(image %q) error = nil, want an error", tc.image)
			}
			for _, want := range []string{"image", "name@sha256:<digest>"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestParseRejectsHostSysctlBlock(t *testing.T) {
	l := newLayout(t)
	tests := []struct {
		name    string
		sysctls string
		valid   bool
	}{
		{"core_pattern", `["kernel.core_pattern"]`, false},
		{"vm", `["vm.max_map_count"]`, false},
		{"suid_dumpable", `["fs.suid_dumpable"]`, false},
		{"one bad among good", `["net.core.rmem_max", "vm.swappiness"]`, false},
		{"net", `["net.core.rmem_max"]`, true},
		{"mqueue", `["fs.mqueue.msg_max"]`, true},
		{"shm", `["kernel.shmmax"]`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(blockTOML("sysctls = "+tc.sysctls)), l.Env)
			if tc.valid {
				if err != nil {
					t.Fatalf("Parse() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "sysctls") {
				t.Fatalf("Parse() error = %v, want an error naming sysctls", err)
			}
		})
	}
}

func TestParseRejectsBlockOverProtectedOrMountsPath(t *testing.T) {
	l := newLayout(t)
	sources := map[string]string{
		"docker socket":         l.Sock,
		"docker socket alias":   l.SockAlias,
		"containerd socket":     l.Containerd,
		"docker root":           l.Docker,
		"inside docker root":    l.DockerSub,
		"parent of docker sock": l.SockDir,
		"parent of containerd":  l.ContainerdDir,
		"root":                  "/",
		"mounts root":           l.Mounts,
		"inside mounts":         filepath.Join(l.Mounts, "monitoring", "db"),
		"parent of mounts":      l.Root,
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(blockTOML(fmt.Sprintf("bind_mounts = [%q]", src+":/x"))), l.Env)
			if err == nil || !strings.Contains(err.Error(), "bind_mounts") {
				t.Fatalf("Parse(bind_mounts %q) error = %v, want an error naming bind_mounts", src, err)
			}
		})
	}
	if _, err := Parse([]byte(blockTOML(fmt.Sprintf("bind_mounts = [%q]", l.Srv+":/x"))), l.Env); err != nil {
		t.Errorf("Parse(bind_mounts of an ordinary path) error = %v, want nil", err)
	}
}

func TestLoadFileAbsentIsEmptyConfig(t *testing.T) {
	l := newLayout(t)
	p, err := LoadFile(filepath.Join(t.TempDir(), "agent.toml"), l.Env, os.Getuid())
	if err != nil {
		t.Fatalf("LoadFile(absent) error = %v, want nil", err)
	}
	if got, want := p.Config(), (Config{Channel: "stable"}); !reflect.DeepEqual(got, want) {
		t.Errorf("Config() = %+v, want %+v", got, want)
	}
	s := baseSpec()
	s.PID = "host"
	if _, err := p.Check(s); !hasKey(err, "pid_host") {
		t.Errorf("Check(pid: host) error = %v, want a pid_host rejection", err)
	}
}

func TestLoadFileRejectsUnsafeFile(t *testing.T) {
	l := newLayout(t)
	uid := os.Getuid()
	write := func(t *testing.T, mode os.FileMode) string {
		path := filepath.Join(t.TempDir(), "agent.toml")
		must(t, os.WriteFile(path, []byte("[update]\nchannel = \"stable\"\n"), 0o600))
		must(t, os.Chmod(path, mode))
		return path
	}
	tests := []struct {
		name  string
		setup func(t *testing.T) (string, int)
		hint  string
	}{
		{"symlink", func(t *testing.T) (string, int) {
			link := filepath.Join(t.TempDir(), "agent.toml")
			must(t, os.Symlink(write(t, 0o600), link))
			return link, uid
		}, "symlink"},
		{"group writable", func(t *testing.T) (string, int) { return write(t, 0o620), uid }, "chmod"},
		{"world writable", func(t *testing.T) (string, int) { return write(t, 0o602), uid }, "chmod"},
		{"wrong owner", func(t *testing.T) (string, int) { return write(t, 0o600), uid + 1 }, "chown"},
		{"unreadable", func(t *testing.T) (string, int) {
			if uid == 0 {
				t.Skip("root can read any file")
			}
			return write(t, 0o000), uid
		}, "chmod"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, owner := tc.setup(t)
			_, err := LoadFile(path, l.Env, owner)
			if err == nil || !strings.Contains(err.Error(), tc.hint) {
				t.Fatalf("LoadFile(%s) error = %v, want an error containing %q", tc.name, err, tc.hint)
			}
		})
	}
	good := write(t, 0o600)
	if _, err := LoadFile(good, l.Env, uid); err != nil {
		t.Errorf("LoadFile(safe file) error = %v, want nil", err)
	}
}

func FuzzParse(f *testing.F) {
	l := newLayout(f)
	for _, seed := range []string{fullExample(), "", "[update]\nchannel = \"x\"", blockTOML("pid_host = true"), "[[containers.allow]]"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data, l.Env)
		if err != nil {
			return
		}
		if p == nil {
			t.Fatalf("Parse(%q) = nil, nil; want a policy or an error", data)
		}
		if ch := p.Config().Channel; ch != "stable" && ch != "canary" {
			t.Fatalf("Parse(%q) Config().Channel = %q, want stable or canary", data, ch)
		}
		// A parsed policy must keep every check total: no panic, a rejection or a pass.
		s := baseSpec()
		s.Privileged, s.PID = true, "host"
		_, _ = p.Check(s)
	})
}
