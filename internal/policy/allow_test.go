package policy

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestAllowEveryKeyAllowsItsField(t *testing.T) {
	l := newLayout(t)
	set := func(f func(s *Spec)) Spec { s := baseSpec(); f(&s); return s }
	// build_entitlements is covered by TestCheckBuildEntitlements: no block can match a build.
	tests := []struct {
		key   string
		block string
		spec  Spec
	}{
		{"privileged", "privileged = true", set(func(s *Spec) { s.Privileged = true })},
		{"bind_mounts", fmt.Sprintf("bind_mounts = [%q]", l.Srv+":/data"), bindSpec(l.Srv, "/data", false)},
		{"data_dirs", `data_dirs = ["db"]`, bindSpec(l.Mounts+"/monitoring/db", "/data", false)},
		{"docker_socket", "docker_socket = true", bindSpec(l.Sock, "/var/run/docker.sock", false)},
		{"containerd_socket", "containerd_socket = true", bindSpec(l.Containerd, "/run/containerd.sock", false)},
		{"docker_data_root", "docker_data_root = true", bindSpec(l.DockerSub, "/x", false)},
		{"network_host", "network_host = true", set(func(s *Spec) { s.NetworkMode = "host" })},
		{"network_container", "network_container = true", set(func(s *Spec) { s.NetworkMode = "container:other1" })},
		{"pid_host", "pid_host = true", set(func(s *Spec) { s.PID = "host" })},
		{"ipc_host", "ipc_host = true", set(func(s *Spec) { s.IPC = "host" })},
		{"uts_host", "uts_host = true", set(func(s *Spec) { s.UTS = "host" })},
		{"userns_host", "userns_host = true", set(func(s *Spec) { s.UserNS = "host" })},
		{"cap_add", `cap_add = ["net_admin"]`, set(func(s *Spec) { s.CapAdd = []string{"CAP_NET_ADMIN"} })},
		{"devices", `devices = ["/dev/fuse"]`, set(func(s *Spec) { s.Devices = []string{"/dev/fuse:/dev/fuse:rwm"} })},
		{"device_cgroup_rules", `device_cgroup_rules = ["c 10:229 rwm"]`, set(func(s *Spec) { s.DeviceCgroupRules = []string{"c 10:229 rwm"} })},
		{"security_opt", `security_opt = ["seccomp=unconfined"]`, set(func(s *Spec) { s.SecurityOpt = []string{"seccomp=unconfined"} })},
		{"sysctls", `sysctls = ["net.core.rmem_max"]`, sysctlSpec("net.core.rmem_max", "7")},
		{"cgroup_parent", `cgroup_parent = "custom.slice"`, set(func(s *Spec) { s.CgroupParent = "custom.slice" })},
		{"runtime", `runtime = "runsc"`, set(func(s *Spec) { s.Runtime = "runsc" })},
	}
	var cases []checkCase
	for _, tc := range tests {
		cases = append(cases,
			checkCase{name: tc.key + "/with block", block: blockTOML(tc.block), spec: tc.spec},
			checkCase{name: tc.key + "/without block", spec: tc.spec, want: []string{tc.key}},
		)
	}
	runCheckCases(t, l, cases)
}

func TestAllowBlockMatchesEveryNamedKey(t *testing.T) {
	l := newLayout(t)
	pid := func(env string) Spec {
		s := baseSpec()
		s.PID, s.Environment = "host", env
		return s
	}
	full := func(project, image, env, service string) string {
		return rawBlock(project, image, fmt.Sprintf("environment = %q\nservice = %q\npid_host = true", env, service))
	}
	runCheckCases(t, l, []checkCase{
		{name: "everything matches", block: full("monitoring", imgA, "production", "node-exporter"), spec: pid("production")},
		{name: "project and image only", block: rawBlock("monitoring", imgA, "pid_host = true"), spec: pid("production")},
		{name: "narrowing keys never widen", block: rawBlock("monitoring", imgA, "pid_host = true"), spec: pid("staging")},
		{name: "wrong project", block: full("other", imgA, "production", "node-exporter"), spec: pid("production"), want: []string{"pid_host"}},
		{name: "wrong digest", block: full("monitoring", imgB, "production", "node-exporter"), spec: pid("production"), want: []string{"pid_host"}},
		{name: "wrong environment", block: full("monitoring", imgA, "staging", "node-exporter"), spec: pid("production"), want: []string{"pid_host"}},
		{name: "spec has no environment", block: full("monitoring", imgA, "production", "node-exporter"), spec: pid(""), want: []string{"pid_host"}},
		{name: "wrong service", block: full("monitoring", imgA, "production", "other"), spec: pid("production"), want: []string{"pid_host"}},
	})
}

func TestAllowOnlyListedFields(t *testing.T) {
	l := newLayout(t)
	set := func(f func(s *Spec)) Spec { s := baseSpec(); f(&s); return s }
	binds := fmt.Sprintf("bind_mounts = [%q]", l.Srv+":/data")
	roBinds := fmt.Sprintf("bind_mounts = [%q]", l.Srv+":/data:ro")
	runCheckCases(t, l, []checkCase{
		{name: "pid_host does not allow ipc host", block: blockTOML("pid_host = true"), spec: set(func(s *Spec) { s.IPC = "host" }), want: []string{"ipc_host"}},
		{name: "NET_ADMIN does not allow SYS_ADMIN", block: blockTOML(`cap_add = ["NET_ADMIN"]`), spec: set(func(s *Spec) { s.CapAdd = []string{"SYS_ADMIN"} }), want: []string{"cap_add"}},
		{name: "one of two capabilities", block: blockTOML(`cap_add = ["NET_ADMIN"]`), spec: set(func(s *Spec) { s.CapAdd = []string{"NET_ADMIN", "SYS_ADMIN"} }), want: []string{"cap_add"}},
		{name: "another device", block: blockTOML(`devices = ["/dev/fuse"]`), spec: set(func(s *Spec) { s.Devices = []string{"/dev/kvm"} }), want: []string{"devices"}},
		{name: "another runtime", block: blockTOML(`runtime = "runsc"`), spec: set(func(s *Spec) { s.Runtime = "kata" }), want: []string{"runtime"}},
		{name: "another cgroup parent", block: blockTOML(`cgroup_parent = "a.slice"`), spec: set(func(s *Spec) { s.CgroupParent = "b.slice" }), want: []string{"cgroup_parent"}},
		{name: "listed bind mount", block: blockTOML(binds), spec: bindSpec(l.Srv, "/data", false)},
		{name: "another host path", block: blockTOML(binds), spec: bindSpec(l.State, "/data", false), want: []string{"bind_mounts"}},
		{name: "another target", block: blockTOML(binds), spec: bindSpec(l.Srv, "/other", false), want: []string{"bind_mounts"}},
		{name: "trailing slash ignored", block: blockTOML(binds), spec: bindSpec(l.Srv+"/", "/data", false)},
		{name: "rw entry allows read-only mount", block: blockTOML(binds), spec: bindSpec(l.Srv, "/data", true)},
		{name: "ro entry allows read-only mount", block: blockTOML(roBinds), spec: bindSpec(l.Srv, "/data", true)},
		{name: "ro entry does not allow rw mount", block: blockTOML(roBinds), spec: bindSpec(l.Srv, "/data", false), want: []string{"bind_mounts"}},
		{name: "bind_mounts does not allow a privileged spec", block: blockTOML(binds), spec: set(func(s *Spec) { s.Privileged = true }), want: []string{"privileged"}},
	})

	// A block naming one field never clears any denied row it does not list.
	p := mustParse(t, l.Env, blockTOML(`cgroup_parent = "zzz.slice"`))
	for _, row := range DeniedFields {
		t.Run("unrelated block/"+row.Field, func(t *testing.T) {
			mk, ok := deniedCases[row.Field]
			if !ok {
				t.Fatalf("no deniedCases entry for DeniedFields row %q", row.Field)
			}
			_, err := p.Check(mk(l))
			var r *Rejection
			if !errors.As(err, &r) || !reflect.DeepEqual(keysOf(err), []string{row.Key}) {
				t.Errorf("Check(%s) with a cgroup_parent block: error = %v, want a rejection with key [%s]", row.Field, err, row.Key)
			}
		})
	}
}

func TestAllowNeverMatchesTagOrMissingDigest(t *testing.T) {
	l := newLayout(t)
	pid := func(image string) Spec { s := baseSpec(); s.PID, s.Image = "host", image; return s }
	xBlock := rawBlock("monitoring", "docker.io/library/x@"+digestA, "pid_host = true")
	runCheckCases(t, l, []checkCase{
		{name: "tag only", block: blockTOML("pid_host = true"), spec: pid("prom/node-exporter:latest"), want: []string{"pid_host"}},
		{name: "no tag or digest", block: blockTOML("pid_host = true"), spec: pid("prom/node-exporter"), want: []string{"pid_host"}},
		{name: "no image", block: blockTOML("pid_host = true"), spec: pid(""), want: []string{"pid_host"}},
		{name: "tag and digest matches on digest", block: blockTOML("pid_host = true"), spec: pid("prom/node-exporter:v1@" + digestA)},
		{name: "tag and wrong digest", block: blockTOML("pid_host = true"), spec: pid("prom/node-exporter:v1@" + digestB), want: []string{"pid_host"}},
		{name: "other repository same digest", block: blockTOML("pid_host = true"), spec: pid("prom/other@" + digestA), want: []string{"pid_host"}},
		{name: "docker.io prefix", block: blockTOML("pid_host = true"), spec: pid("docker.io/prom/node-exporter@" + digestA)},
		{name: "library implied, bare", block: xBlock, spec: pid("x@" + digestA)},
		{name: "library implied, explicit", block: xBlock, spec: pid("library/x@" + digestA)},
		{name: "library implied, full", block: xBlock, spec: pid("docker.io/library/x@" + digestA)},
		{name: "other registry is another repository", block: xBlock, spec: pid("ghcr.io/library/x@" + digestA), want: []string{"pid_host"}},
	})
}

func TestAllowUnitesMatchingBlocks(t *testing.T) {
	l := newLayout(t)
	blocks := blockTOML("pid_host = true") + blockTOML("ipc_host = true") + rawBlock("other", imgA, "uts_host = true")
	with := func(f func(s *Spec)) Spec { s := baseSpec(); f(&s); return s }
	runCheckCases(t, l, []checkCase{
		{name: "both listed fields", block: blocks, spec: with(func(s *Spec) { s.PID, s.IPC = "host", "host" })},
		{name: "a field in neither block", block: blocks, spec: with(func(s *Spec) { s.PID, s.IPC, s.UserNS = "host", "host", "host" }), want: []string{"userns_host"}},
		{name: "a block for another project adds nothing", block: blocks, spec: with(func(s *Spec) { s.UTS = "host" }), want: []string{"uts_host"}},
	})
}
