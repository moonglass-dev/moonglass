package policy

import (
	"reflect"
	"strings"
	"testing"
)

func TestRejectionNamesFieldAndAllowLines(t *testing.T) {
	l := newLayout(t)
	p := mustParse(t, l.Env, "")

	golden := `container policy rejected service "node-exporter" (project "monitoring"):
  pid: host is not allowed
  volumes: bind mount of /proc is not allowed
To allow these, add to /etc/moonglass/agent.toml:

  [[containers.allow]]
  project     = "monitoring"
  image       = "` + imgA + `"
  pid_host    = true
  bind_mounts = ["/proc:/host/proc:ro"]

Then restart the agent. The file is read at start-up only.`

	tests := []struct {
		name        string
		spec        Spec
		want        string   // exact rendered error, when set
		contains    []string // substrings
		notContains []string
	}{
		{
			name: "field, allow lines and merged violations",
			spec: func() Spec {
				s := bindSpec("/proc", "/host/proc", true)
				s.Kind, s.PID = "compose", "host"
				return s
			}(),
			want: golden,
		},
		{
			name:        "host sysctl has no snippet",
			spec:        sysctlSpec("kernel.core_pattern", "|/x"),
			contains:    []string{"kernel.core_pattern", "no block can allow"},
			notContains: []string{"[[containers.allow]]"},
		},
		{
			name: "tag shows the digest form",
			spec: func() Spec { s := baseSpec(); s.PID, s.Image = "host", "prom/node-exporter:latest"; return s }(),
			contains: []string{
				`image       = "prom/node-exporter@sha256:<digest>"`, "[[containers.allow]]",
				"pid_host    = true", "restart the agent",
			},
		},
		{
			name:        "snippet never names environment or service",
			spec:        func() Spec { s := baseSpec(); s.PID, s.Environment = "host", "production"; return s }(),
			contains:    []string{`project     = "monitoring"`, "pid_host"},
			notContains: []string{"environment =", "service     ="},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Check(tc.spec)
			if err == nil {
				t.Fatalf("Check(%+v) error = nil, want a rejection", tc.spec)
			}
			got := err.Error()
			if tc.want != "" && got != tc.want {
				t.Errorf("error =\n%s\nwant\n%s", got, tc.want)
			}
			for _, s := range tc.contains {
				if !strings.Contains(got, s) {
					t.Errorf("error = %q, want it to contain %q", got, s)
				}
			}
			for _, s := range tc.notContains {
				if strings.Contains(got, s) {
					t.Errorf("error = %q, want it not to contain %q", got, s)
				}
			}
		})
	}
}

func TestRejectNeverStrips(t *testing.T) {
	l := newLayout(t)
	p := mustParse(t, l.Env, "")
	build := func() Spec {
		s := bindSpec(l.Sock, "/var/run/docker.sock", false)
		s.Privileged, s.PID, s.CapAdd = true, "host", []string{"SYS_ADMIN"}
		s.Sysctls = map[string]string{"kernel.core_pattern": "x"}
		s.SecurityOpt = []string{"seccomp=unconfined", "no-new-privileges"}
		return s
	}
	in, before := build(), build()
	c, err := p.Check(in)
	if !hasKey(err, "privileged", "pid_host", "cap_add", "docker_socket", "sysctls", "security_opt") {
		t.Fatalf("Check() error = %v, want a rejection", err)
	}
	if !reflect.DeepEqual(in, before) {
		t.Errorf("input after Check = %+v, want unchanged %+v", in, before)
	}
	if !reflect.DeepEqual(c, Checked{}) {
		t.Errorf("Checked = %+v, want the zero value after a rejection", c)
	}
}

func TestRejectionHasNoEnvironmentValues(t *testing.T) {
	l := newLayout(t)
	p := mustParse(t, l.Env, "")
	env := map[string]string{
		"DOCKER_SOCK": l.Sock,
		"DB_PASSWORD": "s3cr3t-pw-1",
		"API_TOKEN":   "tok-4f9a-2",
		"SYSCTL_VAL":  "val-hunter2",
	}
	s := bindSpec(env["DOCKER_SOCK"], "/var/run/docker.sock", false)
	s.Sysctls = map[string]string{"kernel.core_pattern": env["SYSCTL_VAL"]}
	_, err := p.Check(s)
	if err == nil {
		t.Fatal("Check() error = nil, want a rejection")
	}
	for _, want := range []string{l.Sock, "kernel.core_pattern"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
	for _, k := range []string{"DB_PASSWORD", "API_TOKEN", "SYSCTL_VAL"} {
		if strings.Contains(err.Error(), env[k]) {
			t.Errorf("error = %q, must not contain the value of %s (%q)", err.Error(), k, env[k])
		}
	}
}
