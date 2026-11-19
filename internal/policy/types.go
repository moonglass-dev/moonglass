// Package policy loads agent.toml and checks a container spec or a resolved
// Compose project against the deny-by-default rules in ADR-0004.
package policy

import (
	"maps"
	"slices"
)

// Paths are the host locations the mount check is evaluated against.
type Paths struct {
	MountsRoot        string
	DockerRoot        string
	DockerSockets     []string
	ContainerdSockets []string
}

// Env is the host and engine context a Policy is compiled against.
type Env struct {
	ContainerProject func(ref string) (string, bool)
	DefaultRuntime   string
	Paths            Paths
}

// Mount is one volume of a Spec.
type Mount struct {
	Type     string
	Source   string
	Target   string
	ReadOnly bool
}

// LogConfig is the container's log driver and its options.
type LogConfig struct {
	Options map[string]string
	Driver  string
}

// Spec is the neutral description of one container or build the agent is about to create.
type Spec struct {
	Sysctls map[string]string
	// Env is the container's environment.
	Env map[string]string
	// Labels are the user-supplied labels; a key in the dev.moonglass. namespace is rejected.
	Labels map[string]string
	// AgentLabels are the labels agent code sets; every key is in the dev.moonglass. namespace.
	AgentLabels map[string]string
	// LogConfig is the log driver and its options.
	LogConfig    LogConfig
	CgroupParent string
	Runtime      string
	Service      string
	Kind         string
	NetworkMode  string
	PID          string
	IPC          string
	UTS          string
	Environment  string
	UserNS       string
	Image        string
	Project      string
	// RestartPolicy is no, on-failure or unless-stopped; empty means no.
	RestartPolicy     string
	CapAdd            []string
	Devices           []string
	DeviceCgroupRules []string
	SecurityOpt       []string
	BuildEntitlements []string
	Mounts            []Mount
	// Cmd is an exec argv that replaces the image's command. It is never joined or passed to a shell.
	Cmd []string
	// Networks are the Docker networks the container is attached to.
	Networks   []string
	Privileged bool
}

// Config is the non-allow part of agent.toml. A list is nil when its key is
// absent and empty when the key is present and lists nothing. Forbids,
// CheckImageSource and CheckRepositorySource answer from this parsed
// configuration alone; enforcing them is the agent's job.
type Config struct {
	Channel        string
	ForbidCommands []string
	Repositories   []string
	Registries     []string
}

// Policy is a compiled agent.toml, ready to Check a Spec.
type Policy struct {
	blocks []allowBlock
	env    Env
	cfg    Config
}

// Config returns the parsed update, commands and sources sections.
func (p *Policy) Config() Config {
	if p == nil {
		return Config{Channel: "stable"}
	}
	return p.cfg.clone()
}

// Checked is a Spec that has passed Check. Only Check can construct one.
type Checked struct {
	spec Spec
}

// Spec returns a deep copy of the checked spec.
func (c Checked) Spec() Spec { return c.spec.clone() }

// ComposeInput is a Compose project to resolve and check.
type ComposeInput struct {
	Env         map[string]string
	Project     string
	Environment string
	WorkDir     string
	Files       []string
}

// DeniedField is one row of the deny list. Field is the catalog name used by
// the escape suite; Key is the allow key in agent.toml.
type DeniedField struct {
	Field string
	Key   string
}

// DeniedFields is the ordered deny list from ADR-0004 and this package's spec.
var DeniedFields = []DeniedField{
	{Field: "privileged: true", Key: "privileged"},
	{Field: "volumes: bind mount of a host path", Key: "bind_mounts"},
	{Field: "volumes: agent data directory", Key: "data_dirs"},
	{Field: "volumes: docker.sock", Key: "docker_socket"},
	{Field: "volumes: containerd.sock", Key: "containerd_socket"},
	{Field: "volumes: Docker data root", Key: "docker_data_root"},
	{Field: "network_mode: host", Key: "network_host"},
	{Field: "pid: host", Key: "pid_host"},
	{Field: "ipc: host", Key: "ipc_host"},
	{Field: "uts: host", Key: "uts_host"},
	{Field: "userns_mode: host", Key: "userns_host"},
	{Field: "network_mode: other container", Key: "network_container"},
	{Field: "cap_add", Key: "cap_add"},
	{Field: "devices", Key: "devices"},
	{Field: "device_cgroup_rules", Key: "device_cgroup_rules"},
	{Field: "security_opt: disables confinement", Key: "security_opt"},
	{Field: "sysctls: not allow-listed", Key: "sysctls"},
	{Field: "cgroup_parent", Key: "cgroup_parent"},
	{Field: "runtime: non-default", Key: "runtime"},
	{Field: "build.entitlements: security.insecure", Key: "build_entitlements"},
	{Field: "build.entitlements: network.host", Key: "build_entitlements"},
	{Field: "labels: dev.moonglass. namespace", Key: "agent_labels"},
}

func (s Spec) clone() Spec {
	out := s
	out.Mounts = append([]Mount(nil), s.Mounts...)
	out.CapAdd = append([]string(nil), s.CapAdd...)
	out.Devices = append([]string(nil), s.Devices...)
	out.DeviceCgroupRules = append([]string(nil), s.DeviceCgroupRules...)
	out.SecurityOpt = append([]string(nil), s.SecurityOpt...)
	out.BuildEntitlements = append([]string(nil), s.BuildEntitlements...)
	out.Cmd = slices.Clone(s.Cmd)
	out.Networks = slices.Clone(s.Networks)
	out.Env = maps.Clone(s.Env)
	out.Labels = maps.Clone(s.Labels)
	out.AgentLabels = maps.Clone(s.AgentLabels)
	out.LogConfig.Options = maps.Clone(s.LogConfig.Options)
	if s.Sysctls != nil {
		out.Sysctls = make(map[string]string, len(s.Sysctls))
		for k, v := range s.Sysctls {
			out.Sysctls[k] = v
		}
	}
	return out
}

func (c Config) clone() Config {
	out := c
	out.ForbidCommands = slices.Clone(c.ForbidCommands)
	out.Repositories = slices.Clone(c.Repositories)
	out.Registries = slices.Clone(c.Registries)
	return out
}
