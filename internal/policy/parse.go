package policy

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"
)

const agentTOMLPath = "/etc/moonglass/agent.toml"

type rawFile struct {
	Update     *rawUpdate     `toml:"update"`
	Commands   *rawCommands   `toml:"commands"`
	Sources    *rawSources    `toml:"sources"`
	Containers *rawContainers `toml:"containers"`
}

type rawUpdate struct {
	Channel *string `toml:"channel"`
}

type rawCommands struct {
	Forbid []string `toml:"forbid"`
}

type rawSources struct {
	Repositories []string `toml:"repositories"`
	Registries   []string `toml:"registries"`
}

type rawContainers struct {
	Allow []rawAllow `toml:"allow"`
}

type rawAllow struct {
	Project           string   `toml:"project"`
	Image             string   `toml:"image"`
	Environment       *string  `toml:"environment"`
	Service           *string  `toml:"service"`
	Privileged        *bool    `toml:"privileged"`
	DockerSocket      *bool    `toml:"docker_socket"`
	ContainerdSocket  *bool    `toml:"containerd_socket"`
	DockerDataRoot    *bool    `toml:"docker_data_root"`
	NetworkHost       *bool    `toml:"network_host"`
	NetworkContainer  *bool    `toml:"network_container"`
	PIDHost           *bool    `toml:"pid_host"`
	IPCHost           *bool    `toml:"ipc_host"`
	UTSHost           *bool    `toml:"uts_host"`
	UserNSHost        *bool    `toml:"userns_host"`
	CgroupParent      *string  `toml:"cgroup_parent"`
	Runtime           *string  `toml:"runtime"`
	AgentLabels       any      `toml:"agent_labels"`
	BindMounts        []string `toml:"bind_mounts"`
	DataDirs          []string `toml:"data_dirs"`
	CapAdd            []string `toml:"cap_add"`
	Devices           []string `toml:"devices"`
	DeviceCgroupRules []string `toml:"device_cgroup_rules"`
	SecurityOpt       []string `toml:"security_opt"`
	Sysctls           []string `toml:"sysctls"`
	BuildEntitlements []string `toml:"build_entitlements"`
}

// allowBlock is a validated, compiled [[containers.allow]] entry.
type allowBlock struct {
	project     string
	image       imageRef
	environment *string
	service     *string

	bools    map[string]bool
	binds    []bindEntry
	dataDirs []string
	capAdd   []string
	devices  []string
	rules    []string
	secopts  []string
	sysctls  []string
	cgroup   *string
	runtime  *string
	build    []string
}

type bindEntry struct {
	source   string
	target   string
	readOnly bool
}

// Parse decodes agent.toml bytes and compiles the allow blocks against env.
func Parse(data []byte, env Env) (*Policy, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var raw rawFile
	if err := dec.Decode(&raw); err != nil {
		return nil, formatTOMLError(data, err)
	}
	cfg := Config{Channel: "stable"}
	if raw.Update != nil && raw.Update.Channel != nil {
		ch := *raw.Update.Channel
		if ch != "stable" && ch != "canary" {
			return nil, fmt.Errorf("channel %q is not stable or canary", ch)
		}
		cfg.Channel = ch
	}
	if raw.Commands != nil {
		list, err := validateStringList("forbid", raw.Commands.Forbid, func(s string) error {
			if s != "delete" && s != "exec" {
				return fmt.Errorf("unknown forbid class %q", s)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		cfg.ForbidCommands = list
	}
	if raw.Sources != nil {
		if raw.Sources.Repositories != nil {
			repos, err := validateStringList("repositories", raw.Sources.Repositories, func(s string) error {
				if _, err := path.Match(s, ""); err != nil {
					return fmt.Errorf("bad repositories glob %q", s)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			cfg.Repositories = repos
		}
		if raw.Sources.Registries != nil {
			regs, err := validateStringList("registries", raw.Sources.Registries, func(s string) error {
				if _, err := path.Match(s, ""); err != nil {
					return fmt.Errorf("bad registries glob %q", s)
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			cfg.Registries = regs
		}
	}
	var blocks []allowBlock
	if raw.Containers != nil {
		for i, rb := range raw.Containers.Allow {
			b, err := compileBlock(env, i, rb)
			if err != nil {
				return nil, err
			}
			blocks = append(blocks, b)
		}
	}
	return &Policy{cfg: cfg, blocks: blocks, env: env}, nil
}

// LoadFile reads path as agent.toml. An absent file is the empty configuration.
// The file must be a regular file owned by owner and not group- or world-writable.
func LoadFile(file string, env Env, owner int) (*Policy, error) {
	fi, err := os.Lstat(file)
	if err != nil {
		if os.IsNotExist(err) {
			return Parse(nil, env)
		}
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		// Lstat on a symlink reports ModeSymlink. On a non-regular file too.
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symlink; replace it with a regular file", file)
		}
		return nil, fmt.Errorf("%s is not a regular file", file)
	}
	st, ok := fi.Sys().(interface{ Uid() uint32 })
	// UID comes from the file's stat below.
	uid, uidOK := fileUID(fi)
	if !uidOK && st != nil {
		uid, uidOK = int(st.Uid()), true
	}
	mode := fi.Mode().Perm()
	if mode&0o022 != 0 || mode&0o004 == 0 && mode&0o400 == 0 {
		// group/other writable, or not owner-readable (including 000)
		return nil, fmt.Errorf("%s has unsafe mode: fix chmod (mode %o)", file, mode)
	}
	if mode&0o022 != 0 {
		return nil, fmt.Errorf("%s is group or world writable; fix chmod", file)
	}
	if uidOK && uid != owner {
		return nil, fmt.Errorf("%s has the wrong owner (uid %d, want %d); fix chown", file, uid, owner)
	}
	if mode&0o400 == 0 {
		return nil, fmt.Errorf("%s is not readable; fix chmod", file)
	}
	f, err := os.Open(filepath.Clean(file))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	_ = ok
	return Parse(data, env)
}

func compileBlock(env Env, index int, rb rawAllow) (allowBlock, error) {
	loc := fmt.Sprintf("containers.allow[%d]", index)
	fail := func(key, msg string) (allowBlock, error) {
		return allowBlock{}, fmt.Errorf("%s: %s: %s", loc, key, msg)
	}
	if rb.Project == "" {
		return fail("project", "project is required")
	}
	if !isPathElement(rb.Project) {
		return fail("project", "project must be a path element")
	}
	if rb.Image == "" {
		return fail("image", "image is required")
	}
	img, err := parseBlockImage(rb.Image)
	if err != nil {
		return fail("image", err.Error())
	}
	if rb.Environment != nil && *rb.Environment == "" {
		return fail("environment", "environment is empty")
	}
	if rb.Service != nil && *rb.Service == "" {
		return fail("service", "service is empty")
	}
	if rb.AgentLabels != nil {
		return fail("agent_labels", "labels in the dev.moonglass. namespace are set only by the agent; agent_labels can never be allowed")
	}
	b := allowBlock{
		project:     rb.Project,
		image:       img,
		environment: rb.Environment,
		service:     rb.Service,
		bools:       map[string]bool{},
	}
	setBool := func(key string, v *bool) error {
		if v == nil {
			return nil
		}
		if !*v {
			return fmt.Errorf("%s: %s: boolean keys accept only true", loc, key)
		}
		b.bools[key] = true
		return nil
	}
	for _, item := range []struct {
		v   *bool
		key string
	}{
		{key: "privileged", v: rb.Privileged},
		{key: "docker_socket", v: rb.DockerSocket},
		{key: "containerd_socket", v: rb.ContainerdSocket},
		{key: "docker_data_root", v: rb.DockerDataRoot},
		{key: "network_host", v: rb.NetworkHost},
		{key: "network_container", v: rb.NetworkContainer},
		{key: "pid_host", v: rb.PIDHost},
		{key: "ipc_host", v: rb.IPCHost},
		{key: "uts_host", v: rb.UTSHost},
		{key: "userns_host", v: rb.UserNSHost},
	} {
		if err := setBool(item.key, item.v); err != nil {
			return allowBlock{}, err
		}
	}
	if rb.BindMounts != nil {
		list, err := validateStringList("bind_mounts", rb.BindMounts, nil)
		if err != nil {
			return fail("bind_mounts", err.Error())
		}
		seen := map[string]bool{}
		for _, e := range list {
			be, err := parseBindEntry(e)
			if err != nil {
				return fail("bind_mounts", err.Error())
			}
			if seen[e] {
				return fail("bind_mounts", "duplicate entry")
			}
			seen[e] = true
			if err := bindSourceForbiddenInBlock(env, be.source); err != nil {
				return fail("bind_mounts", err.Error())
			}
			b.binds = append(b.binds, be)
		}
	}
	if rb.DataDirs != nil {
		list, err := validateStringList("data_dirs", rb.DataDirs, func(s string) error {
			if !isPathElement(s) {
				return fmt.Errorf("%q is not a path element", s)
			}
			return nil
		})
		if err != nil {
			return fail("data_dirs", err.Error())
		}
		b.dataDirs = list
	}
	if rb.CapAdd != nil {
		list, err := validateStringList("cap_add", rb.CapAdd, nil)
		if err != nil {
			return fail("cap_add", err.Error())
		}
		norm := make([]string, 0, len(list))
		seen := map[string]bool{}
		for _, c := range list {
			n, err := normalizeCap(c)
			if err != nil {
				return fail("cap_add", err.Error())
			}
			if seen[n] {
				return fail("cap_add", "duplicate entry")
			}
			seen[n] = true
			norm = append(norm, n)
		}
		b.capAdd = norm
	}
	if rb.Devices != nil {
		list, err := validateStringList("devices", rb.Devices, nil)
		if err != nil {
			return fail("devices", err.Error())
		}
		norm := make([]string, 0, len(list))
		seen := map[string]bool{}
		for _, d := range list {
			n, err := normalizeDevice(d)
			if err != nil {
				return fail("devices", err.Error())
			}
			if seen[n] {
				return fail("devices", "duplicate entry")
			}
			seen[n] = true
			norm = append(norm, n)
		}
		b.devices = norm
	}
	if rb.DeviceCgroupRules != nil {
		list, err := validateStringList("device_cgroup_rules", rb.DeviceCgroupRules, nil)
		if err != nil {
			return fail("device_cgroup_rules", err.Error())
		}
		b.rules = list
	}
	if rb.SecurityOpt != nil {
		list, err := validateStringList("security_opt", rb.SecurityOpt, nil)
		if err != nil {
			return fail("security_opt", err.Error())
		}
		b.secopts = list
	}
	if rb.Sysctls != nil {
		list, err := validateStringList("sysctls", rb.Sysctls, nil)
		if err != nil {
			return fail("sysctls", err.Error())
		}
		for _, name := range list {
			if !sysctlNamespaced(name) {
				return fail("sysctls", fmt.Sprintf("%s is not namespaced and cannot be listed", name))
			}
		}
		b.sysctls = list
	}
	if rb.CgroupParent != nil {
		if *rb.CgroupParent == "" {
			return fail("cgroup_parent", "cgroup_parent is empty")
		}
		b.cgroup = rb.CgroupParent
	}
	if rb.Runtime != nil {
		if *rb.Runtime == "" {
			return fail("runtime", "runtime is empty")
		}
		b.runtime = rb.Runtime
	}
	if rb.BuildEntitlements != nil {
		list, err := validateStringList("build_entitlements", rb.BuildEntitlements, func(s string) error {
			if s != "security.insecure" && s != "network.host" {
				return fmt.Errorf("unknown entitlement %q", s)
			}
			return nil
		})
		if err != nil {
			return fail("build_entitlements", err.Error())
		}
		b.build = list
	}
	if !blockHasAllowKey(b) {
		return fail("containers.allow["+fmt.Sprint(index)+"]", "block lists no allow key")
	}
	return b, nil
}

func blockHasAllowKey(b allowBlock) bool {
	if len(b.bools) > 0 || len(b.binds) > 0 || len(b.dataDirs) > 0 || len(b.capAdd) > 0 ||
		len(b.devices) > 0 || len(b.rules) > 0 || len(b.secopts) > 0 || len(b.sysctls) > 0 ||
		b.cgroup != nil || b.runtime != nil || len(b.build) > 0 {
		return true
	}
	return false
}

func validateStringList(key string, list []string, extra func(string) error) ([]string, error) {
	if len(list) == 0 {
		return nil, fmt.Errorf("%s: list is empty", key)
	}
	seen := map[string]bool{}
	for _, s := range list {
		if s == "" {
			return nil, fmt.Errorf("%s: empty entry", key)
		}
		if seen[s] {
			return nil, fmt.Errorf("%s: duplicate entry", key)
		}
		seen[s] = true
		if extra != nil {
			if err := extra(s); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	return append([]string(nil), list...), nil
}

func isPathElement(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsRune(s, '/') || strings.ContainsRune(s, 0) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func parseBindEntry(s string) (bindEntry, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return bindEntry{}, fmt.Errorf("bind_mounts entry %q must be source:target[:ro|rw]", s)
	}
	src, tgt := parts[0], parts[1]
	if !isAbsNoDotDot(src) || !strings.HasPrefix(src, "/") {
		return bindEntry{}, fmt.Errorf("relative or invalid bind source %q", src)
	}
	if !strings.HasPrefix(tgt, "/") || hasDotDot(tgt) {
		return bindEntry{}, fmt.Errorf("relative or invalid bind target %q", tgt)
	}
	e := bindEntry{source: trimSlash(src), target: tgt}
	if len(parts) == 3 {
		switch parts[2] {
		case "ro":
			e.readOnly = true
		case "rw":
		default:
			return bindEntry{}, fmt.Errorf("bind bad mode %q", parts[2])
		}
	}
	return e, nil
}

func normalizeCap(s string) (string, error) {
	n := strings.ToUpper(s)
	n = strings.TrimPrefix(n, "CAP_")
	if n == "" {
		return "", fmt.Errorf("malformed capability %q", s)
	}
	for _, r := range n {
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) && r != '_' {
			return "", fmt.Errorf("malformed capability %q", s)
		}
	}
	return n, nil
}

func normalizeDevice(s string) (string, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 1 || len(parts) > 3 {
		return "", fmt.Errorf("invalid device %q", s)
	}
	host := parts[0]
	if !strings.HasPrefix(host, "/") {
		return "", fmt.Errorf("device path %q is relative", host)
	}
	cont := host
	perms := "rwm"
	if len(parts) >= 2 && parts[1] != "" {
		cont = parts[1]
	}
	if len(parts) == 3 {
		perms = parts[2]
		if !validDevicePerms(perms) {
			return "", fmt.Errorf("device bad perms %q", perms)
		}
	}
	return host + ":" + cont + ":" + perms, nil
}

func validDevicePerms(p string) bool {
	if p == "" {
		return false
	}
	for _, r := range p {
		if r != 'r' && r != 'w' && r != 'm' {
			return false
		}
	}
	return true
}

func allowIndexAtLine(data []byte, line int) int {
	idx := -1
	for i, l := range strings.Split(string(data), "\n") {
		trim := strings.TrimSpace(l)
		if trim == "[[containers.allow]]" || strings.HasPrefix(trim, "[[containers.allow]]") {
			idx++
		}
		if i+1 >= line {
			return idx
		}
	}
	return idx
}

func rewriteGoField(msg string) string {
	repl := []struct{ from, toml string }{
		{"policy.rawAllow.Privileged", "privileged"},
		{"policy.rawAllow.PIDHost", "pid_host"},
		{"policy.rawAllow.IPCHost", "ipc_host"},
		{"policy.rawAllow.UTSHost", "uts_host"},
		{"policy.rawAllow.UserNSHost", "userns_host"},
		{"policy.rawAllow.DockerSocket", "docker_socket"},
		{"policy.rawAllow.ContainerdSocket", "containerd_socket"},
		{"policy.rawAllow.DockerDataRoot", "docker_data_root"},
		{"policy.rawAllow.NetworkHost", "network_host"},
		{"policy.rawAllow.NetworkContainer", "network_container"},
		{"policy.rawAllow.BindMounts", "bind_mounts"},
		{"policy.rawAllow.DataDirs", "data_dirs"},
		{"policy.rawAllow.CapAdd", "cap_add"},
		{"policy.rawAllow.Devices", "devices"},
		{"policy.rawAllow.DeviceCgroupRules", "device_cgroup_rules"},
		{"policy.rawAllow.SecurityOpt", "security_opt"},
		{"policy.rawAllow.Sysctls", "sysctls"},
		{"policy.rawAllow.CgroupParent", "cgroup_parent"},
		{"policy.rawAllow.Runtime", "runtime"},
		{"policy.rawAllow.BuildEntitlements", "build_entitlements"},
		{"policy.rawAllow.Project", "project"},
		{"policy.rawAllow.Image", "image"},
		{"policy.rawAllow.Environment", "environment"},
		{"policy.rawAllow.Service", "service"},
	}
	for _, r := range repl {
		msg = strings.ReplaceAll(msg, r.from, r.toml)
	}
	return msg
}

func formatTOMLError(data []byte, err error) error {
	var sm *toml.StrictMissingError
	if errors.As(err, &sm) {
		var parts []string
		for i := range sm.Errors {
			e := sm.Errors[i]
			row, _ := e.Position()
			key := strings.Join([]string(e.Key()), ".")
			if key == "" {
				key = e.Error()
			}
			// Use the last key part as the field name the tests look for.
			name := key
			if i := strings.LastIndex(key, "."); i >= 0 {
				name = key[i+1:]
			}
			parts = append(parts, fmt.Sprintf("%s at line %d", name, row))
		}
		if len(parts) > 0 {
			return fmt.Errorf("%s", strings.Join(parts, "; "))
		}
		return fmt.Errorf("%s", sm.String())
	}
	var derr *toml.DecodeError
	if errors.As(err, &derr) {
		row, _ := derr.Position()
		msg := rewriteGoField(derr.Error())
		if !strings.Contains(msg, "line ") && row > 0 {
			msg = fmt.Sprintf("%s (line %d)", msg, row)
		}
		if idx := allowIndexAtLine(data, row); idx >= 0 && !strings.Contains(msg, "containers.allow[") {
			msg = fmt.Sprintf("containers.allow[%d]: %s", idx, msg)
		}
		return fmt.Errorf("%s", msg)
	}
	return err
}

func fileUID(fi fs.FileInfo) (int, bool) {
	return statUID(fi)
}
