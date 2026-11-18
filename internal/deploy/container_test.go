package deploy

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"

	"moonglass.dev/moonglass/internal/policy"
)

// testSpecImage is a digest reference, the form the executor checks and runs.
var testSpecImage = "registry.example.com/acme/web@sha256:" + strings.Repeat("a", 64)

// appSpecFixture is the container spec of an app deploy before its image is set.
func appSpecFixture() policy.Spec {
	return policy.Spec{
		Kind:          "app",
		Project:       "proj",
		Environment:   testEnvironment,
		Service:       testService,
		Image:         testSpecImage,
		AgentLabels:   appAgentLabels(),
		Networks:      []string{"mg-proj"},
		RestartPolicy: "unless-stopped",
		LogConfig: policy.LogConfig{
			Driver:  "local",
			Options: map[string]string{"max-size": "10m", "max-file": "3"},
		},
	}
}

func appAgentLabels() map[string]string {
	return map[string]string{
		testLabelManaged:     "true",
		testLabelProject:     "proj",
		testLabelEnvironment: testEnvironment,
		testLabelService:     testService,
		testLabelApp:         "app-1",
		testLabelRelease:     "r1",
	}
}

func TestCreateOptionsCarriesCheckedSpec(t *testing.T) {
	agentLabels := appAgentLabels()
	passThroughLabels := map[string]string{"team": "acme"}
	for k, v := range agentLabels {
		passThroughLabels[k] = v
	}

	cases := []struct {
		wantLabels map[string]string
		env        map[string]string
		labels     map[string]string
		name       string
		wantCmd    []string
		wantEnv    []string
		cmd        []string
	}{
		{
			name:       "app spec",
			wantLabels: agentLabels,
		},
		{
			name:       "pass-through fields",
			cmd:        []string{"/bin/migrate", "up"},
			env:        map[string]string{"B": "2", "A": "1"},
			labels:     map[string]string{"team": "acme"},
			wantCmd:    []string{"/bin/migrate", "up"},
			wantEnv:    []string{"A=1", "B=2"},
			wantLabels: passThroughLabels,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := appSpecFixture()
			spec.Cmd = tc.cmd
			spec.Env = tc.env
			spec.Labels = tc.labels

			checked, err := emptyPolicy(t, policy.Env{}).Check(spec)
			if err != nil {
				t.Fatalf("policy.Check(app spec) error = %v, want nil", err)
			}
			opts, err := createOptions(checked, "mg-app-1-r1")
			if err != nil {
				t.Fatalf("createOptions error = %v, want nil", err)
			}

			requireEqual(t, "Name", opts.Name, "mg-app-1-r1")
			if opts.Config == nil {
				t.Fatalf("Config = nil, want the checked spec's config")
			}
			if opts.HostConfig == nil {
				t.Fatalf("HostConfig = nil, want the checked spec's host config")
			}
			if opts.NetworkingConfig == nil {
				t.Fatalf("NetworkingConfig = nil, want the checked spec's networks")
			}

			requireEqual(t, "Config.Image", opts.Config.Image, testSpecImage)
			requireEqual(t, "Config.Labels", opts.Config.Labels, tc.wantLabels)
			requireEqual(t, "len(Config.Cmd)", len(opts.Config.Cmd), len(tc.wantCmd))
			if len(tc.wantCmd) > 0 {
				requireEqual(t, "Config.Cmd", opts.Config.Cmd, tc.wantCmd)
			}
			requireEqual(t, "len(Config.Env)", len(opts.Config.Env), len(tc.wantEnv))
			if len(tc.wantEnv) > 0 {
				requireEqual(t, "Config.Env", opts.Config.Env, tc.wantEnv)
			}

			requireEqual(t, "HostConfig.NetworkMode", opts.HostConfig.NetworkMode,
				container.NetworkMode("mg-proj"))
			requireEqual(t, "HostConfig.RestartPolicy", opts.HostConfig.RestartPolicy,
				container.RestartPolicy{Name: container.RestartPolicyUnlessStopped})
			requireEqual(t, "HostConfig.LogConfig.Type", opts.HostConfig.LogConfig.Type, "local")
			requireEqual(t, "HostConfig.LogConfig.Config", opts.HostConfig.LogConfig.Config,
				map[string]string{"max-size": "10m", "max-file": "3"})
			requireEqual(t, "EndpointsConfig networks",
				mapKeys(opts.NetworkingConfig.EndpointsConfig), []string{"mg-proj"})
			for name, endpoint := range opts.NetworkingConfig.EndpointsConfig {
				if endpoint == nil {
					t.Errorf("EndpointsConfig[%q] = nil, want an empty endpoint", name)
				}
			}

			requireEqual(t, "HostConfig.Privileged", opts.HostConfig.Privileged, false)
			requireEqual(t, "len(HostConfig.Mounts)", len(opts.HostConfig.Mounts), 0)
			requireEqual(t, "len(HostConfig.PortBindings)", len(opts.HostConfig.PortBindings), 0)
			requireEqual(t, "len(HostConfig.CapAdd)", len(opts.HostConfig.CapAdd), 0)
			requireEqual(t, "len(HostConfig.SecurityOpt)", len(opts.HostConfig.SecurityOpt), 0)
			requireEqual(t, "len(HostConfig.Devices)", len(opts.HostConfig.Devices), 0)
			requireEqual(t, "len(HostConfig.Sysctls)", len(opts.HostConfig.Sysctls), 0)
			requireEqual(t, "HostConfig.Runtime", opts.HostConfig.Runtime, "")
			requireEqual(t, "HostConfig.PidMode", opts.HostConfig.PidMode, container.PidMode(""))
			requireEqual(t, "HostConfig.IpcMode", opts.HostConfig.IpcMode, container.IpcMode(""))
			requireEqual(t, "HostConfig.UTSMode", opts.HostConfig.UTSMode, container.UTSMode(""))
			requireEqual(t, "HostConfig.UsernsMode", opts.HostConfig.UsernsMode, container.UsernsMode(""))
		})
	}
}

func TestCreateOptionsRefusesUncarriedFields(t *testing.T) {
	const secret = "s3cr3t-value"

	cases := []struct {
		env       policy.Env
		mutate    func(*policy.Spec)
		name      string
		field     string
		wantNoEnv bool
	}{
		{
			name: "named volume mount",
			mutate: func(s *policy.Spec) {
				s.Mounts = []policy.Mount{{Type: "volume", Source: "data", Target: "/data"}}
			},
			field: "Mounts",
		},
		{
			name: "security opt that keeps confinement",
			mutate: func(s *policy.Spec) {
				s.SecurityOpt = []string{"no-new-privileges"}
			},
			field: "SecurityOpt",
		},
		{
			name: "allow-listed sysctl",
			mutate: func(s *policy.Spec) {
				s.Sysctls = map[string]string{"net.core.somaxconn": "1024"}
			},
			field: "Sysctls",
		},
		{
			name: "bridge network mode",
			mutate: func(s *policy.Spec) {
				s.NetworkMode = "bridge"
			},
			field: "NetworkMode",
		},
		{
			name: "default runtime",
			env:  policy.Env{DefaultRuntime: "runc"},
			mutate: func(s *policy.Spec) {
				s.Runtime = "runc"
			},
			field: "Runtime",
		},
		{
			name: "environment value stays out of the error",
			mutate: func(s *policy.Spec) {
				s.Env = map[string]string{"TOKEN": secret}
				s.Mounts = []policy.Mount{{Type: "volume", Source: "data", Target: "/data"}}
			},
			field:     "Mounts",
			wantNoEnv: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := appSpecFixture()
			tc.mutate(&spec)

			checked, err := emptyPolicy(t, tc.env).Check(spec)
			if err != nil {
				t.Fatalf("policy.Check(fixture) error = %v, want the fixture allowed", err)
			}
			_, err = createOptions(checked, "mg-app-1-r1")
			if err == nil {
				t.Fatalf("createOptions error = nil, want the refusal for field %s", tc.field)
			}
			want := "container spec field " + tc.field +
				" is set, but the executor does not send it to the Docker Engine; nothing was created"
			requireEqual(t, "createOptions error", err.Error(), want)
			if tc.wantNoEnv && strings.Contains(err.Error(), secret) {
				t.Errorf("error = %q, contains the environment value %q, want it absent", err.Error(), secret)
			}
		})
	}
}

func TestRunningAfterStart(t *testing.T) {
	cases := []struct {
		state *container.State
		name  string
		want  string
	}{
		{
			name:  "running",
			state: &container.State{Status: "running", Running: true},
		},
		{
			name:  "exited",
			state: &container.State{Status: "exited", ExitCode: 1},
			want:  "container exited right after start (status exited, exit code 1); see its logs",
		},
		{
			name:  "restarting",
			state: &container.State{Status: "restarting", Running: true, Restarting: true},
			want:  "container exited right after start (status restarting, exit code 0); see its logs",
		},
		{
			name:  "created",
			state: &container.State{Status: "created"},
			want:  "container exited right after start (status created, exit code 0); see its logs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runningAfterStart(tc.state)
			if tc.want == "" {
				if err != nil {
					t.Errorf("runningAfterStart(%+v) error = %v, want nil", tc.state, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("runningAfterStart(%+v) error = nil, want %q", tc.state, tc.want)
			}
			requireEqual(t, "runningAfterStart error", err.Error(), tc.want)
		})
	}
}
