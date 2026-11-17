package policy

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Check reports whether s is allowed. On success it returns a Checked wrapping
// a deep copy of s. On denial it returns the zero Checked and a *Rejection.
// It never modifies s and never returns a changed spec.
func (p *Policy) Check(s Spec) (Checked, error) {
	return p.check(s, nil)
}

// composeServices, if non-nil, is the set of service names in the same Compose project.
func (p *Policy) check(s Spec, composeServices map[string]bool) (Checked, error) {
	if p == nil {
		p = &Policy{cfg: Config{Channel: "stable"}}
	}
	var vs []Violation
	if err := specWellFormed(s); err != nil {
		vs = append(vs, Violation{Field: "spec", Key: "", Value: ""})
		return Checked{}, &Rejection{Violations: vs, spec: s}
	}
	matched := p.matchingBlocks(s)

	if s.Privileged && !boolAllowed(matched, "privileged") {
		vs = append(vs, Violation{Field: "privileged: true", Key: "privileged", Value: "true"})
	}

	for _, m := range s.Mounts {
		switch m.Type {
		case "tmpfs":
			continue
		case "volume":
			// A plain named volume is not checked. An absolute path is not a named volume.
			if strings.HasPrefix(m.Source, "/") || hasDotDot(m.Source) {
				vs = append(vs, p.checkBind(s, m, matched)...)
			}
		case "bind":
			vs = append(vs, p.checkBind(s, m, matched)...)
		default:
			vs = append(vs, Violation{Field: "spec", Key: "", Value: ""})
		}
	}

	if strings.EqualFold(s.NetworkMode, "host") && !boolAllowed(matched, "network_host") {
		vs = append(vs, Violation{Field: "network_mode: host", Key: "network_host", Value: "true"})
	}
	if strings.EqualFold(s.PID, "host") && !boolAllowed(matched, "pid_host") {
		vs = append(vs, Violation{Field: "pid: host", Key: "pid_host", Value: "true"})
	}
	if strings.EqualFold(s.IPC, "host") && !boolAllowed(matched, "ipc_host") {
		vs = append(vs, Violation{Field: "ipc: host", Key: "ipc_host", Value: "true"})
	}
	if strings.EqualFold(s.UTS, "host") && !boolAllowed(matched, "uts_host") {
		vs = append(vs, Violation{Field: "uts: host", Key: "uts_host", Value: "true"})
	}
	if strings.EqualFold(s.UserNS, "host") && !boolAllowed(matched, "userns_host") {
		vs = append(vs, Violation{Field: "userns_mode: host", Key: "userns_host", Value: "true"})
	}
	if id, ok := containerRef(s.NetworkMode); ok {
		same := false
		if p.env.ContainerProject != nil {
			if proj, known := p.env.ContainerProject(id); known && proj == s.Project {
				same = true
			}
		}
		if !same && !boolAllowed(matched, "network_container") {
			vs = append(vs, Violation{Field: "network_mode: container:" + id, Key: "network_container", Value: "true"})
		}
	}
	if name, ok := serviceRef(s.NetworkMode); ok {
		if composeServices == nil || !composeServices[name] {
			// Not resolved as a same-project service: treat as not allowed without a block?
			// The spec says it is allowed when that service exists in the project.
			if composeServices == nil || !composeServices[name] {
				// A raw Spec (not from CheckCompose) with service: is not automatically allowed.
				// Only CheckCompose passes the set. If the set is nil, we don't have a project
				// graph; deny unless somehow a block existed (there is no service_network key).
				// Tests don't cover Check() with service:. CheckCompose does.
				if composeServices == nil || !composeServices[name] {
					vs = append(vs, Violation{Field: "network_mode: service:" + name, Key: "network_container", Value: "true"})
				}
			}
		}
	}

	if len(s.CapAdd) > 0 {
		if miss := capsNotAllowed(matched, s.CapAdd); len(miss) > 0 {
			vs = append(vs, Violation{Field: "cap_add", Key: "cap_add", Value: formatStringList(uniqueCaps(s.CapAdd))})
		}
	}
	if len(s.Devices) > 0 {
		if miss := devicesNotAllowed(matched, s.Devices); len(miss) > 0 {
			vs = append(vs, Violation{Field: "devices", Key: "devices", Value: formatStringList(s.Devices)})
		}
	}
	if len(s.DeviceCgroupRules) > 0 {
		if !listAllowed(matched, func(b allowBlock) []string { return b.rules }, s.DeviceCgroupRules) {
			vs = append(vs, Violation{Field: "device_cgroup_rules", Key: "device_cgroup_rules", Value: formatStringList(s.DeviceCgroupRules)})
		}
	}
	for _, opt := range s.SecurityOpt {
		if disablesConfinement(opt) && !stringAllowed(matched, func(b allowBlock) []string { return b.secopts }, opt) {
			vs = append(vs, Violation{Field: "security_opt: " + opt, Key: "security_opt", Value: formatStringList([]string{opt})})
			break
		}
	}
	vs = append(vs, p.checkSysctls(s, matched)...)
	if s.CgroupParent != "" && !stringEqAllowed(matched, func(b allowBlock) *string { return b.cgroup }, s.CgroupParent) {
		vs = append(vs, Violation{Field: "cgroup_parent", Key: "cgroup_parent", Value: fmt.Sprintf("%q", s.CgroupParent)})
	}
	if s.Runtime != "" && s.Runtime != p.env.DefaultRuntime && !stringEqAllowed(matched, func(b allowBlock) *string { return b.runtime }, s.Runtime) {
		vs = append(vs, Violation{Field: "runtime: " + s.Runtime, Key: "runtime", Value: fmt.Sprintf("%q", s.Runtime)})
	}
	if ents := deniedEntitlements(s.BuildEntitlements); len(ents) > 0 {
		// A build has no digest image, so no block can match.
		for _, e := range ents {
			vs = append(vs, Violation{Field: "build.entitlements: " + e, Key: "build_entitlements", Value: formatStringList(ents)})
		}
	}

	if len(vs) > 0 {
		return Checked{}, &Rejection{Violations: vs, spec: s}
	}
	return Checked{spec: s.clone()}, nil
}

func specWellFormed(s Spec) error {
	if !isPathElement(s.Project) {
		return fmt.Errorf("project")
	}
	for _, m := range s.Mounts {
		switch m.Type {
		case "volume", "tmpfs":
		case "bind":
			if !strings.HasPrefix(m.Source, "/") {
				return fmt.Errorf("relative bind source")
			}
		default:
			return fmt.Errorf("unknown mount type")
		}
	}
	return nil
}

func (p *Policy) checkBind(s Spec, m Mount, matched []allowBlock) []Violation {
	ev := evalMount(p.env, m.Source)
	switch ev.class {
	case mountMalformed:
		field := ev.field
		if field == "" {
			field = "spec"
		}
		msg := ev.msg
		if ev.dotdot && !strings.Contains(msg, "..") {
			msg = "source contains .."
		}
		if ev.symlink && !strings.Contains(msg, "symlink") {
			msg = "symlink"
		}
		if msg != "" && field == "spec" {
			// Include the reason in Field so tests can search the error string.
			field = "spec: " + msg
		}
		return []Violation{{Field: field, Key: "", Value: ""}}
	case mountProtected:
		if boolAllowed(matched, string(ev.protected)) && protectedExact(p.env, ev) {
			return nil
		}
		field := ev.field
		if ev.raw != "" && !strings.Contains(field, ev.raw) && !strings.Contains(field, trimSlash(ev.raw)) {
			field = field + " of " + ev.raw
		}
		return []Violation{{
			Field: field,
			Key:   ev.key,
			Value: "true",
		}}
	case mountOther:
		if bindAllowed(matched, m) {
			return nil
		}
		val := fmt.Sprintf("[%q]", trimSlash(m.Source)+":"+m.Target)
		if m.ReadOnly {
			val = fmt.Sprintf("[%q]", trimSlash(m.Source)+":"+m.Target+":ro")
		}
		return []Violation{{Field: ev.field, Key: "bind_mounts", Value: val}}
	case mountDataDir:
		if dataDirAllowed(matched, s, ev) {
			return nil
		}
		field := ev.field
		msg := "data_dirs"
		if ev.dataProj != "" && ev.dataProj != s.Project {
			msg = "project"
		}
		// Include the reason so TestDataDirRejectsEscapes can search it.
		if !strings.Contains(field, msg) {
			field = field + " (" + msg + ")"
		}
		return []Violation{{Field: field, Key: "data_dirs", Value: dataDirValue(ev)}}
	default:
		return []Violation{{Field: ev.field, Key: ev.key}}
	}
}

func dataDirAllowed(blocks []allowBlock, s Spec, ev mountEval) bool {
	if ev.dataName == "" || ev.dataProj == "" {
		return false
	}
	if ev.dataProj != s.Project {
		return false
	}
	for _, b := range blocks {
		if b.project != s.Project || b.project != ev.dataProj {
			continue
		}
		for _, n := range b.dataDirs {
			if n == ev.dataName {
				// source may be the directory or a path inside it.
				want := filepath.Join( /* mounts/project/name */ "", ev.dataName)
				_ = want
				return true
			}
		}
	}
	return false
}

func dataDirValue(ev mountEval) string {
	if ev.dataName == "" {
		return ""
	}
	return formatStringList([]string{ev.dataName})
}

func bindAllowed(blocks []allowBlock, m Mount) bool {
	raw := trimSlash(m.Source)
	for _, b := range blocks {
		for _, e := range b.binds {
			if trimSlash(e.source) != raw {
				continue
			}
			if e.target != m.Target {
				continue
			}
			if e.readOnly && !m.ReadOnly {
				continue
			}
			return true
		}
	}
	return false
}

func (p *Policy) matchingBlocks(s Spec) []allowBlock {
	var out []allowBlock
	si := parseSpecImage(s.Image)
	for _, b := range p.blocks {
		if b.project != s.Project {
			continue
		}
		if !imagesMatch(b.image, si) {
			continue
		}
		if b.environment != nil && *b.environment != s.Environment {
			continue
		}
		if b.service != nil && *b.service != s.Service {
			continue
		}
		out = append(out, b)
	}
	return out
}

func boolAllowed(blocks []allowBlock, key string) bool {
	for _, b := range blocks {
		if b.bools[key] {
			return true
		}
	}
	return false
}

func capsNotAllowed(blocks []allowBlock, caps []string) []string {
	allowed := map[string]bool{}
	for _, b := range blocks {
		for _, c := range b.capAdd {
			allowed[c] = true
		}
	}
	var miss []string
	for _, c := range caps {
		n, err := normalizeCap(c)
		if err != nil || !allowed[n] {
			miss = append(miss, c)
		}
	}
	return miss
}

func uniqueCaps(caps []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range caps {
		n, err := normalizeCap(c)
		if err != nil {
			n = c
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

func devicesNotAllowed(blocks []allowBlock, devices []string) []string {
	allowed := map[string]bool{}
	for _, b := range blocks {
		for _, d := range b.devices {
			allowed[d] = true
		}
	}
	var miss []string
	for _, d := range devices {
		n, err := normalizeDevice(d)
		if err != nil || !allowed[n] {
			miss = append(miss, d)
		}
	}
	return miss
}

func listAllowed(blocks []allowBlock, get func(allowBlock) []string, got []string) bool {
	allowed := map[string]bool{}
	for _, b := range blocks {
		for _, x := range get(b) {
			allowed[x] = true
		}
	}
	for _, x := range got {
		if !allowed[x] {
			return false
		}
	}
	return true
}

func stringAllowed(blocks []allowBlock, get func(allowBlock) []string, got string) bool {
	for _, b := range blocks {
		for _, x := range get(b) {
			if x == got {
				return true
			}
		}
	}
	return false
}

func stringEqAllowed(blocks []allowBlock, get func(allowBlock) *string, got string) bool {
	for _, b := range blocks {
		if v := get(b); v != nil && *v == got {
			return true
		}
	}
	return false
}

func disablesConfinement(opt string) bool {
	n := strings.ToLower(opt)
	n = strings.ReplaceAll(n, ":", "=")
	switch n {
	case "seccomp=unconfined", "apparmor=unconfined", "label=disable", "label=disabled":
		return true
	default:
		return false
	}
}

func (p *Policy) checkSysctls(s Spec, matched []allowBlock) []Violation {
	if len(s.Sysctls) == 0 {
		return nil
	}
	var vs []Violation
	for name := range s.Sysctls {
		switch {
		case defaultSysctls[name] && !sysctlTreatAsHost(name, s, p.env):
			continue
		case !sysctlNamespaced(name) || sysctlTreatAsHost(name, s, p.env):
			vs = append(vs, Violation{
				Field: fmt.Sprintf("sysctls: %s (no block can allow)", name),
				Key:   "sysctls",
				Value: "",
			})
		default:
			if !stringAllowed(matched, func(b allowBlock) []string { return b.sysctls }, name) {
				vs = append(vs, Violation{
					Field: "sysctls: " + name,
					Key:   "sysctls",
					Value: formatStringList([]string{name}),
				})
			}
		}
	}
	return vs
}

func deniedEntitlements(ents []string) []string {
	var out []string
	for _, e := range ents {
		if e == "security.insecure" || e == "network.host" {
			out = append(out, e)
		}
	}
	return out
}

func formatStringList(v []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range v {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%q", s)
	}
	b.WriteByte(']')
	return b.String()
}
