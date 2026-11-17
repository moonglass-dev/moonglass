package policy

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mut(f func(l layout, s *Spec)) func(layout) Spec {
	return func(l layout) Spec {
		s := baseSpec()
		f(l, &s)
		return s
	}
}

func bindOf(src func(l layout) string) func(layout) Spec {
	return func(l layout) Spec { return bindSpec(src(l), "/mnt/x", false) }
}

// deniedCases has one spec per row of DeniedFields, keyed by DeniedField.Field.
// TestEscapeSuiteCoversEveryDeniedField reads it.
var deniedCases = map[string]func(layout) Spec{
	"privileged: true":                      mut(func(_ layout, s *Spec) { s.Privileged = true }),
	"volumes: bind mount of a host path":    bindOf(func(l layout) string { return l.Srv }),
	"volumes: agent data directory":         bindOf(func(l layout) string { return l.Mounts + "/monitoring/db" }),
	"volumes: docker.sock":                  bindOf(func(l layout) string { return l.Sock }),
	"volumes: containerd.sock":              bindOf(func(l layout) string { return l.Containerd }),
	"volumes: Docker data root":             bindOf(func(l layout) string { return l.DockerSub }),
	"network_mode: host":                    mut(func(_ layout, s *Spec) { s.NetworkMode = "host" }),
	"pid: host":                             mut(func(_ layout, s *Spec) { s.PID = "host" }),
	"ipc: host":                             mut(func(_ layout, s *Spec) { s.IPC = "host" }),
	"uts: host":                             mut(func(_ layout, s *Spec) { s.UTS = "host" }),
	"userns_mode: host":                     mut(func(_ layout, s *Spec) { s.UserNS = "host" }),
	"network_mode: other container":         mut(func(_ layout, s *Spec) { s.NetworkMode = "container:other1" }),
	"cap_add":                               mut(func(_ layout, s *Spec) { s.CapAdd = []string{"SYS_ADMIN"} }),
	"devices":                               mut(func(_ layout, s *Spec) { s.Devices = []string{"/dev/fuse"} }),
	"device_cgroup_rules":                   mut(func(_ layout, s *Spec) { s.DeviceCgroupRules = []string{"c 1:3 mr"} }),
	"security_opt: disables confinement":    mut(func(_ layout, s *Spec) { s.SecurityOpt = []string{"seccomp=unconfined"} }),
	"sysctls: not allow-listed":             mut(func(_ layout, s *Spec) { s.Sysctls = map[string]string{"net.core.rmem_max": "1"} }),
	"cgroup_parent":                         mut(func(_ layout, s *Spec) { s.CgroupParent = "x.slice" }),
	"runtime: non-default":                  mut(func(_ layout, s *Spec) { s.Runtime = "runsc" }),
	"build.entitlements: security.insecure": buildSpec("security.insecure"),
	"build.entitlements: network.host":      buildSpec("network.host"),
}

func buildSpec(ent string) func(layout) Spec {
	return func(layout) Spec {
		s := baseSpec()
		s.Kind, s.Image, s.BuildEntitlements = "build", "", []string{ent}
		return s
	}
}

func TestCheckAllowsBenignSpec(t *testing.T) {
	l := newLayout(t)
	benign := func(f func(s *Spec)) Spec {
		s := baseSpec()
		s.Mounts = []Mount{{Type: "volume", Source: "data", Target: "/data"}, {Type: "tmpfs", Target: "/tmp"}}
		s.SecurityOpt = []string{"no-new-privileges"}
		s.Sysctls = map[string]string{"net.core.somaxconn": "1024", "net.ipv4.ip_local_port_range": "1024 65000"}
		s.NetworkMode = "container:web1"
		s.Runtime = "runc"
		if f != nil {
			f(&s)
		}
		return s
	}
	runCheckCases(t, l, []checkCase{
		{name: "everything benign", spec: benign(nil)},
		{name: "bridge", spec: benign(func(s *Spec) { s.NetworkMode = "bridge" })},
		{name: "none", spec: benign(func(s *Spec) { s.NetworkMode = "none" })},
		{name: "named network", spec: benign(func(s *Spec) { s.NetworkMode = "mynet" })},
		{name: "empty runtime", spec: benign(func(s *Spec) { s.Runtime = "" })},
		{name: "minimal", spec: baseSpec()},
	})
}

func TestCheckDeniesEveryField(t *testing.T) {
	l := newLayout(t)
	if len(DeniedFields) == 0 {
		t.Fatal("DeniedFields is empty, want one row per ADR-0004 denied field")
	}
	seen := map[string]bool{}
	for _, row := range DeniedFields {
		t.Run(row.Field, func(t *testing.T) {
			if row.Key == "" || seen[row.Field] {
				t.Fatalf("DeniedFields row %+v: want a non-empty Key and a unique Field", row)
			}
			seen[row.Field] = true
			mk, ok := deniedCases[row.Field]
			if !ok {
				t.Fatalf("no deniedCases entry for DeniedFields row %q", row.Field)
			}
			spec, before := mk(l), mk(l)
			p := mustParse(t, l.Env, "")
			c, err := p.Check(spec)
			var r *Rejection
			if !errors.As(err, &r) {
				t.Fatalf("Check(%s) error = %v, want *Rejection", row.Field, err)
			}
			if want := strings.SplitN(row.Field, ":", 2)[0]; !hasField(err, want) {
				t.Errorf("Check(%s) violations = %+v, want a Field starting with %q", row.Field, r.Violations, want)
			}
			if got := keysOf(err); !reflect.DeepEqual(got, []string{row.Key}) {
				t.Errorf("Check(%s) keys = %v, want [%s]", row.Field, got, row.Key)
			}
			if !reflect.DeepEqual(spec, before) {
				t.Errorf("Check changed its input: got %+v, want %+v", spec, before)
			}
			if !reflect.DeepEqual(c, Checked{}) {
				t.Errorf("Check returned a Checked %+v with a rejection, want the zero value", c)
			}
		})
	}
	for field := range deniedCases {
		if !seen[field] {
			t.Errorf("deniedCases entry %q has no row in DeniedFields", field)
		}
	}
}

func sysctlSpec(kv ...string) Spec {
	s := baseSpec()
	s.Sysctls = map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		s.Sysctls[kv[i]] = kv[i+1]
	}
	return s
}

func TestCheckSysctlAllowList(t *testing.T) {
	l := newLayout(t)
	runCheckCases(t, l, []checkCase{
		{name: "somaxconn", spec: sysctlSpec("net.core.somaxconn", "1")},
		{name: "port range", spec: sysctlSpec("net.ipv4.ip_local_port_range", "1024 65000")},
		{name: "namespaced not listed", spec: sysctlSpec("net.core.rmem_max", "1"), want: []string{"sysctls"}},
		{name: "mqueue not listed", spec: sysctlSpec("fs.mqueue.msg_max", "1"), want: []string{"sysctls"}},
		{name: "allowed by block", block: blockTOML(`sysctls = ["net.core.rmem_max"]`), spec: sysctlSpec("net.core.rmem_max", "1")},
		{name: "block lists another", block: blockTOML(`sysctls = ["net.core.wmem_max"]`), spec: sysctlSpec("net.core.rmem_max", "1"), want: []string{"sysctls"}},
		{name: "one of two unlisted", block: blockTOML(`sysctls = ["net.core.rmem_max"]`), spec: sysctlSpec("net.core.rmem_max", "1", "net.core.wmem_max", "1"), want: []string{"sysctls"}},
	})
}

func TestCheckHostSysctlNeverAllowed(t *testing.T) {
	l := newLayout(t)
	hostNet := func(kv ...string) Spec { s := sysctlSpec(kv...); s.NetworkMode = "host"; return s }
	hostIPC := func(kv ...string) Spec { s := sysctlSpec(kv...); s.IPC = "host"; return s }
	tests := []struct {
		name string
		spec Spec
	}{
		{"core_pattern", sysctlSpec("kernel.core_pattern", "|/x")},
		{"vm", sysctlSpec("vm.max_map_count", "1")},
		{"suid_dumpable", sysctlSpec("fs.suid_dumpable", "2")},
		{"net under network host", hostNet("net.core.somaxconn", "1")},
		{"net under container of another project", func() Spec {
			s := sysctlSpec("net.core.rmem_max", "1")
			s.NetworkMode = "container:other1"
			return s
		}()},
		{"shm under ipc host", hostIPC("kernel.shmmax", "1")},
		{"mqueue under ipc host", hostIPC("fs.mqueue.msg_max", "1")},
	}
	blocks := map[string]string{
		"no block":     "",
		"every host":   blockTOML("network_host = true\nnetwork_container = true\nipc_host = true\nprivileged = true"),
		"sysctls list": blockTOML(`sysctls = ["net.core.rmem_max", "net.core.somaxconn", "kernel.shmmax", "fs.mqueue.msg_max"]`),
	}
	for _, tc := range tests {
		for bname, block := range blocks {
			t.Run(tc.name+"/"+bname, func(t *testing.T) {
				p := mustParse(t, l.Env, block)
				_, err := p.Check(tc.spec)
				if !hasKey(err, "sysctls") {
					t.Fatalf("Check(%+v) error = %v, want a sysctls rejection", tc.spec.Sysctls, err)
				}
				if !strings.Contains(err.Error(), "no block can allow") {
					t.Errorf("error = %q, want it to contain %q", err.Error(), "no block can allow")
				}
			})
		}
	}
}

func TestCheckNetworkModeContainerSameProject(t *testing.T) {
	l := newLayout(t)
	mode := func(m string) Spec { s := baseSpec(); s.NetworkMode = m; return s }
	runCheckCases(t, l, []checkCase{
		{name: "same project", spec: mode("container:web1")},
		{name: "other project", spec: mode("container:other1"), want: []string{"network_container"}},
		{name: "unknown container", spec: mode("container:ghost"), want: []string{"network_container"}},
		{name: "other project with block", block: blockTOML("network_container = true"), spec: mode("container:other1")},
		{name: "host", spec: mode("host"), want: []string{"network_host"}},
		{name: "HOST", spec: mode("HOST"), want: []string{"network_host"}},
		{name: "Host", spec: mode("Host"), want: []string{"network_host"}},
		{name: "host with container block", block: blockTOML("network_container = true"), spec: mode("host"), want: []string{"network_host"}},
	})
	noLookup := l.Env
	noLookup.ContainerProject = nil
	runCheckCases(t, l, []checkCase{
		{name: "nil lookup means unknown", env: &noLookup, spec: mode("container:web1"), want: []string{"network_container"}},
	})
}

func TestCheckSecurityOptDisablesConfinement(t *testing.T) {
	l := newLayout(t)
	opt := func(o string) Spec { s := baseSpec(); s.SecurityOpt = []string{o}; return s }
	runCheckCases(t, l, []checkCase{
		{name: "seccomp=unconfined", spec: opt("seccomp=unconfined"), want: []string{"security_opt"}},
		{name: "seccomp:unconfined", spec: opt("seccomp:unconfined"), want: []string{"security_opt"}},
		{name: "SECCOMP=Unconfined", spec: opt("SECCOMP=Unconfined"), want: []string{"security_opt"}},
		{name: "apparmor=unconfined", spec: opt("apparmor=unconfined"), want: []string{"security_opt"}},
		{name: "label=disable", spec: opt("label=disable"), want: []string{"security_opt"}},
		{name: "label:disable", spec: opt("label:disable"), want: []string{"security_opt"}},
		{name: "no-new-privileges", spec: opt("no-new-privileges")},
		{name: "no-new-privileges=true", spec: opt("no-new-privileges=true")},
		{name: "allowed by block", block: blockTOML(`security_opt = ["seccomp=unconfined"]`), spec: opt("seccomp=unconfined")},
	})
}

func TestCheckBuildEntitlements(t *testing.T) {
	l := newLayout(t)
	build := func(ents ...string) Spec {
		s := baseSpec()
		s.Kind, s.Image, s.BuildEntitlements = "build", "", ents
		return s
	}
	runCheckCases(t, l, []checkCase{
		{name: "security.insecure", spec: build("security.insecure"), want: []string{"build_entitlements"}},
		{name: "network.host", spec: build("network.host"), want: []string{"build_entitlements"}},
		{name: "no entitlements", spec: build()},
		{
			name:  "a block never matches a build",
			block: blockTOML(`build_entitlements = ["security.insecure", "network.host"]`),
			spec:  build("security.insecure"),
			want:  []string{"build_entitlements"},
		},
	})
}

func TestCheckMalformedSpec(t *testing.T) {
	l := newLayout(t)
	with := func(f func(s *Spec)) Spec { s := baseSpec(); f(&s); return s }
	runCheckCases(t, l, []checkCase{
		{name: "empty project", spec: with(func(s *Spec) { s.Project = "" }), want: []string{"-"}, field: "spec"},
		{name: "project not an element", spec: with(func(s *Spec) { s.Project = "a/b" }), want: []string{"-"}, field: "spec"},
		{name: "relative bind source", spec: bindSpec("rel/path", "/x", false), want: []string{"-"}, field: "spec"},
		{name: "unknown mount type", spec: with(func(s *Spec) { s.Mounts = []Mount{{Type: "weird", Source: "x", Target: "/x"}} }), want: []string{"-"}, field: "spec"},
	})
}
