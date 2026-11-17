package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func hasDotDot(p string) bool {
	for _, e := range strings.Split(p, string(filepath.Separator)) {
		if e == ".." {
			return true
		}
	}
	return false
}

func isAbsNoDotDot(p string) bool {
	return strings.HasPrefix(p, "/") && !hasDotDot(p)
}

func trimSlash(p string) string {
	if p == "/" {
		return p
	}
	return strings.TrimRight(p, "/")
}

func pathEqual(a, b string) bool {
	return trimSlash(filepath.Clean(a)) == trimSlash(filepath.Clean(b))
}

func pathInside(child, parent string) bool {
	c := trimSlash(filepath.Clean(child))
	p := trimSlash(filepath.Clean(parent))
	if p == "/" {
		return c != "/" && strings.HasPrefix(c, "/")
	}
	return c == p || strings.HasPrefix(c, p+string(filepath.Separator))
}

func pathAncestor(path, of string) bool {
	// path is an ancestor of of (of is inside path), and not equal.
	return pathInside(of, path) && !pathEqual(path, of)
}

// resolveSource resolves symlinks on the longest existing prefix and appends the rest lexically.
func resolveSource(source string) (string, error) {
	cur := source
	var missing []string
	for {
		_, err := os.Lstat(cur)
		if err == nil {
			resolved, evErr := filepath.EvalSymlinks(cur)
			if evErr != nil {
				return "", evErr
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			// Still try the parent; a permission error on a missing leaf is unusual.
			parent := filepath.Dir(cur)
			if parent == cur {
				return "", err
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			for i := len(missing) - 1; i >= 0; i-- {
				cur = filepath.Join(cur, missing[i])
			}
			return cur, nil
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

func sameFile(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(fa, fb)
}

type mountClass int

const (
	mountOther mountClass = iota
	mountProtected
	mountDataDir
	mountMalformed
)

type mountKind string

const (
	kindDockerSocket     mountKind = "docker_socket"
	kindContainerdSocket mountKind = "containerd_socket"
	kindDockerRoot       mountKind = "docker_data_root"
	kindDataDir          mountKind = "data_dirs"
	kindBind             mountKind = "bind_mounts"
)

type mountEval struct {
	key       string // allow key, empty when malformed
	field     string
	msg       string
	raw       string
	resolved  string
	dataProj  string
	dataName  string
	protected mountKind // which protected kind, if any
	class     mountClass
	symlink   bool
	dotdot    bool
}

func evalMount(env Env, src string) mountEval {
	ev := mountEval{raw: src, field: "volumes: bind mount of " + trimSlash(src)}
	if !strings.HasPrefix(src, "/") || hasDotDot(src) {
		ev.class = mountMalformed
		ev.field = "spec"
		if hasDotDot(src) {
			ev.dotdot = true
			ev.msg = "source contains .."
		} else {
			ev.msg = "relative bind source"
		}
		return ev
	}
	resolved, err := resolveSource(src)
	if err != nil {
		// Still classify on the raw path.
		resolved = src
	}
	ev.resolved = resolved

	// File identity against configured sockets.
	if k, ok := socketIdentity(env, src, resolved); ok {
		ev.class = mountProtected
		ev.key = string(k)
		ev.protected = k
		ev.field = "volumes: " + protectedField(k)
		return ev
	}

	if k, ok := classifyProtected(env, resolved, src); ok {
		ev.class = mountProtected
		ev.key = string(k)
		ev.protected = k
		ev.field = "volumes: " + protectedField(k)
		return ev
	}

	if underMounts(env, resolved, src) {
		ev.class = mountDataDir
		ev.key = "data_dirs"
		ev.field = "volumes: agent data directory"
		if err := dataDirSymlink(env, src); err != nil {
			ev.class = mountMalformed
			ev.key = ""
			ev.field = "spec"
			ev.symlink = true
			ev.msg = "symlink"
			return ev
		}
		ev.dataProj, ev.dataName = dataDirParts(env, resolved)
		return ev
	}
	ev.class = mountOther
	ev.key = "bind_mounts"
	return ev
}

func protectedField(k mountKind) string {
	switch k {
	case kindDockerSocket:
		return "docker.sock"
	case kindContainerdSocket:
		return "containerd.sock"
	case kindDockerRoot:
		return "Docker data root"
	case kindDataDir, kindBind:
		return string(k)
	default:
		return string(k)
	}
}

func socketIdentity(env Env, raw, resolved string) (mountKind, bool) {
	for _, s := range env.Paths.DockerSockets {
		if s != "" && (sameFile(raw, s) || sameFile(resolved, s) || pathEqual(resolved, s) || pathEqual(raw, s)) {
			return kindDockerSocket, true
		}
	}
	for _, s := range env.Paths.ContainerdSockets {
		if s != "" && (sameFile(raw, s) || sameFile(resolved, s) || pathEqual(resolved, s) || pathEqual(raw, s)) {
			return kindContainerdSocket, true
		}
	}
	return "", false
}

func classifyProtected(env Env, resolved, raw string) (mountKind, bool) {
	// Exact socket or inside/ancestor of sockets and docker root, or basename.
	type cand struct {
		kind mountKind
		path string
	}
	var cs []cand
	for _, s := range env.Paths.DockerSockets {
		if s != "" {
			cs = append(cs, cand{kindDockerSocket, s})
		}
	}
	for _, s := range env.Paths.ContainerdSockets {
		if s != "" {
			cs = append(cs, cand{kindContainerdSocket, s})
		}
	}
	if env.Paths.DockerRoot != "" {
		cs = append(cs, cand{kindDockerRoot, env.Paths.DockerRoot})
	}

	base := filepath.Base(trimSlash(raw))
	rbase := filepath.Base(trimSlash(resolved))
	if base == "docker.sock" || rbase == "docker.sock" {
		return kindDockerSocket, true
	}
	if base == "containerd.sock" || rbase == "containerd.sock" {
		return kindContainerdSocket, true
	}

	// Prefer an exact or "inside" match over a mere ancestor, so "/" doesn't
	// hide a more specific classification when both apply.
	var ancestor mountKind
	var hasAncestor bool
	for _, c := range cs {
		if pathEqual(resolved, c.path) || pathInside(resolved, c.path) {
			return c.kind, true
		}
		if pathAncestor(resolved, c.path) {
			if !hasAncestor {
				ancestor, hasAncestor = c.kind, true
			}
		}
	}
	if hasAncestor {
		return ancestor, true
	}
	return "", false
}

func underMounts(env Env, resolved, raw string) bool {
	root := env.Paths.MountsRoot
	if root == "" {
		return false
	}
	// Use both the configured root (maybe a symlink) and its resolved form.
	rroot, err := filepath.EvalSymlinks(root)
	if err != nil {
		rroot = root
	}
	return pathEqual(resolved, root) || pathInside(resolved, root) || pathAncestor(resolved, root) ||
		pathEqual(resolved, rroot) || pathInside(resolved, rroot) || pathAncestor(resolved, rroot) ||
		pathEqual(raw, root) || pathInside(raw, root) || pathAncestor(raw, root)
}

func dataDirSymlink(env Env, src string) error {
	root := env.Paths.MountsRoot
	// Walk every element from MountsRoot through src, inclusive.
	// If MountsRoot is a symlink, or any element under it is, reject.
	candidates := []string{root}
	// Also walk the source's prefixes that sit under root (raw, not resolved).
	cur := src
	var chain []string
	for {
		chain = append(chain, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	candidates = append(candidates, chain...)
	seen := map[string]bool{}
	for _, p := range candidates {
		if seen[p] {
			continue
		}
		seen[p] = true
		// Only consider paths at or under MountsRoot, plus MountsRoot itself.
		if p != root && !pathInside(p, root) && !pathEqual(p, root) {
			// If root is a symlink, also compare against it as given.
			continue
		}
		fi, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink")
		}
	}
	// If MountsRoot itself is a symlink, always reject a source that uses it.
	if fi, err := os.Lstat(root); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink")
	}
	return nil
}

func dataDirParts(env Env, resolved string) (project, name string) {
	root := env.Paths.MountsRoot
	rroot, err := filepath.EvalSymlinks(root)
	if err != nil {
		rroot = root
	}
	rel, err := filepath.Rel(rroot, resolved)
	if err != nil {
		rel, err = filepath.Rel(root, resolved)
		if err != nil {
			return "", ""
		}
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") {
		return "", ""
	}
	parts := strings.Split(rel, "/")
	if len(parts) >= 1 {
		project = parts[0]
	}
	if len(parts) >= 2 {
		name = parts[1]
	}
	return project, name
}

func bindSourceForbiddenInBlock(env Env, src string) error {
	ev := evalMount(env, src)
	if ev.class == mountProtected || ev.class == mountDataDir || ev.class == mountMalformed && ev.symlink {
		return fmt.Errorf("bind_mounts source %q is protected or inside the mounts tree", src)
	}
	// Ancestor of MountsRoot is also forbidden (mount rules 3 and 4).
	if ev.class == mountMalformed && ev.dotdot {
		return fmt.Errorf("bind_mounts source %q is not allowed", src)
	}
	if underMounts(env, ev.resolved, src) {
		return fmt.Errorf("bind_mounts source %q is inside or an ancestor of the mounts tree", src)
	}
	if ev.class == mountProtected || ev.protected != "" {
		return fmt.Errorf("bind_mounts source %q is protected", src)
	}
	// classifyProtected may miss if resolve failed; re-check ancestor of sockets/root
	if _, ok := classifyProtected(env, ev.resolved, src); ok {
		return fmt.Errorf("bind_mounts source %q is protected", src)
	}
	if ev.resolved != "" {
		if _, ok := classifyProtected(env, ev.resolved, src); ok {
			return fmt.Errorf("bind_mounts source %q is protected", src)
		}
	}
	return nil
}

func protectedExact(env Env, ev mountEval) bool {
	switch ev.protected {
	case kindDataDir, kindBind:
		return false
	case kindDockerSocket:
		for _, s := range env.Paths.DockerSockets {
			if s != "" && (pathEqual(ev.resolved, s) || pathEqual(ev.raw, s) || sameFile(ev.raw, s) || sameFile(ev.resolved, s)) {
				return true
			}
		}
		return false
	case kindContainerdSocket:
		for _, s := range env.Paths.ContainerdSockets {
			if s != "" && (pathEqual(ev.resolved, s) || pathEqual(ev.raw, s) || sameFile(ev.raw, s) || sameFile(ev.resolved, s)) {
				return true
			}
		}
		return false
	case kindDockerRoot:
		root := env.Paths.DockerRoot
		if root == "" {
			return false
		}
		rroot, err := filepath.EvalSymlinks(root)
		if err != nil {
			rroot = root
		}
		return pathEqual(ev.resolved, root) || pathInside(ev.resolved, root) ||
			pathEqual(ev.resolved, rroot) || pathInside(ev.resolved, rroot) ||
			pathEqual(ev.raw, root) || pathInside(ev.raw, root)
	}
	return false
}
