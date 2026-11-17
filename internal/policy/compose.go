package policy

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
)

// CheckCompose resolves a Compose project with compose-go and checks every
// service, including those behind profiles. Interpolation uses only in.Env.
// A loader error is returned as a plain error (not *Rejection). A policy
// failure is one *Rejection listing every failing service. On failure, no
// Checked specs are returned.
func (p *Policy) CheckCompose(ctx context.Context, in ComposeInput) ([]Checked, error) {
	if p == nil {
		p = &Policy{cfg: Config{Channel: "stable"}}
	}
	proj, err := loadCompose(ctx, in)
	if err != nil {
		return nil, err
	}
	services := proj.AllServices()
	names := map[string]bool{}
	for name := range services {
		names[name] = true
	}
	var checked []Checked
	rej := &Rejection{byService: map[string][]Violation{}, spec: Spec{Project: in.Project, Environment: in.Environment, Kind: "compose"}}
	for name, svc := range services {
		spec, volumesFrom := serviceToSpec(in, proj, svc, name)
		c, err := p.check(spec, names)
		var r *Rejection
		if err != nil && !asRejection(err, &r) {
			return nil, err
		}
		if len(volumesFrom) > 0 {
			if r == nil {
				r = &Rejection{spec: spec}
			}
			v := Violation{Field: "volumes_from", Key: "", Value: ""}
			r.Violations = append(r.Violations, v)
			err = r
		}
		if err != nil {
			rej.byService[name] = append(rej.byService[name], r.Violations...)
			rej.Violations = append(rej.Violations, r.Violations...)
			continue
		}
		checked = append(checked, c)
	}
	if len(rej.Violations) > 0 {
		return nil, rej
	}
	return checked, nil
}

func asRejection(err error, target **Rejection) bool {
	return errors.As(err, target)
}

func loadCompose(ctx context.Context, in ComposeInput) (*types.Project, error) {
	env := []string{}
	for k, v := range in.Env {
		env = append(env, k+"="+v)
	}
	opts, err := cli.NewProjectOptions(
		in.Files,
		cli.WithWorkingDirectory(in.WorkDir),
		cli.WithName(in.Project),
		cli.WithEnv(env),
		cli.WithInterpolation(true),
		cli.WithNormalization(true),
		cli.WithResolvedPaths(true),
		cli.WithConsistency(false),
	)
	if err != nil {
		return nil, err
	}
	// Do not call WithOsEnv or WithDotEnv: only in.Env is used for interpolation.
	return opts.LoadProject(ctx)
}

func serviceToSpec(in ComposeInput, proj *types.Project, svc types.ServiceConfig, name string) (Spec, []string) {
	s := Spec{
		Kind:              "compose",
		Project:           in.Project,
		Environment:       in.Environment,
		Service:           name,
		Image:             svc.Image,
		Privileged:        svc.Privileged,
		NetworkMode:       svc.NetworkMode,
		PID:               svc.Pid,
		IPC:               svc.Ipc,
		UTS:               utsOf(svc),
		UserNS:            usernsOf(svc),
		CapAdd:            append([]string(nil), svc.CapAdd...),
		CgroupParent:      svc.CgroupParent,
		Runtime:           svc.Runtime,
		SecurityOpt:       append([]string(nil), svc.SecurityOpt...),
		DeviceCgroupRules: append([]string(nil), svc.DeviceCgroupRules...),
	}
	if len(svc.Sysctls) > 0 {
		s.Sysctls = map[string]string{}
		for k, v := range svc.Sysctls {
			s.Sysctls[k] = v
		}
	}
	for _, d := range svc.Devices {
		s.Devices = append(s.Devices, formatDevice(d))
	}
	s.Mounts = volumesToMounts(in.WorkDir, proj, svc)
	if svc.Build != nil {
		s.Kind = "compose" // still a compose service that also builds; entitlements still apply
		s.BuildEntitlements = append([]string(nil), svc.Build.Entitlements...)
		if svc.Build.Privileged {
			s.BuildEntitlements = append(s.BuildEntitlements, "security.insecure")
		}
		if strings.EqualFold(svc.Build.Network, "host") {
			s.BuildEntitlements = append(s.BuildEntitlements, "network.host")
		}
		// A build-only service keeps an empty image so no allow block can match.
	}
	return s, svc.VolumesFrom
}

func utsOf(svc types.ServiceConfig) string {
	// Field name in compose-go v2.
	return svc.Uts
}

func usernsOf(svc types.ServiceConfig) string {
	return svc.UserNSMode
}

func formatDevice(d types.DeviceMapping) string {
	host := d.Source
	cont := d.Target
	if cont == "" {
		cont = host
	}
	perms := d.Permissions
	if perms == "" {
		perms = "rwm"
	}
	return host + ":" + cont + ":" + perms
}

func volumesToMounts(workDir string, proj *types.Project, svc types.ServiceConfig) []Mount {
	var out []Mount
	for _, v := range svc.Volumes {
		if ms, ok := volumeToMountsOf(workDir, proj, v); ok {
			out = append(out, ms...)
		}
	}
	for _, t := range svc.Tmpfs {
		target := t
		// tmpfs entries may be "/tmp" or "/tmp:size=1".
		if i := strings.Index(t, ":"); i >= 0 {
			target = t[:i]
		}
		out = append(out, Mount{Type: "tmpfs", Target: target})
	}
	return out
}

func volumeToMountsOf(workDir string, proj *types.Project, v types.ServiceVolumeConfig) ([]Mount, bool) {
	typ := v.Type
	if typ == "" {
		typ = types.VolumeTypeVolume
	}
	one := func(m Mount) ([]Mount, bool) { return []Mount{m}, true }
	switch typ {
	case types.VolumeTypeTmpfs:
		return one(Mount{Type: "tmpfs", Target: v.Target})
	case types.VolumeTypeBind:
		src := v.Source
		if src != "" && !filepath.IsAbs(src) {
			src = filepath.Join(workDir, src)
		}
		return one(Mount{Type: "bind", Source: src, Target: v.Target, ReadOnly: v.ReadOnly})
	case types.VolumeTypeVolume:
		if paths, ok := namedVolumeBindPaths(proj, v.Source); ok {
			var ms []Mount
			for _, src := range paths {
				ms = append(ms, Mount{Type: "bind", Source: src, Target: v.Target, ReadOnly: v.ReadOnly})
			}
			return ms, true
		}
		return one(Mount{Type: "volume", Source: v.Source, Target: v.Target, ReadOnly: v.ReadOnly})
	default:
		// npipe, image, cluster: surface as an unknown type so Check rejects as malformed.
		return one(Mount{Type: typ, Source: v.Source, Target: v.Target, ReadOnly: v.ReadOnly})
	}
}

func namedVolumeBindPaths(proj *types.Project, source string) ([]string, bool) {
	if proj == nil || source == "" {
		return nil, false
	}
	vol, ok := lookupVolume(proj, source)
	if !ok {
		return nil, false
	}
	if vol.Driver != "" && vol.Driver != "local" {
		return nil, false
	}
	opts := vol.DriverOpts
	if len(opts) == 0 {
		return nil, false
	}
	device := opts["device"]
	typ := opts["type"]
	o := opts["o"]
	var paths []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	if typ == "none" || strings.Contains(o, "bind") {
		add(device)
	}
	if strings.HasPrefix(device, "/") {
		add(device)
	}
	for _, path := range hostPathsInDriverOpts(opts) {
		add(path)
	}
	if len(paths) == 0 {
		return nil, false
	}
	return paths, true
}

func hostPathsInDriverOpts(opts types.Options) []string {
	var out []string
	for _, v := range opts {
		out = append(out, absolutePathsIn(v)...)
	}
	return out
}

func absolutePathsIn(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if i := strings.Index(part, "="); i >= 0 {
			part = part[i+1:]
		}
		if strings.HasPrefix(part, "/") {
			out = append(out, part)
		}
	}
	return out
}

func lookupVolume(proj *types.Project, source string) (types.VolumeConfig, bool) {
	if v, ok := proj.Volumes[source]; ok {
		return v, true
	}
	// After normalization the map key may be the short name while Source is project_name.
	for key, v := range proj.Volumes {
		if v.Name == source || key == source {
			return v, true
		}
		// compose-go often prefixes with project name.
		if strings.HasSuffix(source, "_"+key) || strings.HasSuffix(v.Name, "_"+key) {
			return v, true
		}
	}
	return types.VolumeConfig{}, false
}
