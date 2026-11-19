package policy

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Class is a class of command that [commands] forbid can refuse (ADR-0004
// decision 13). The agent maps each command type to one class; this package
// only answers whether a class is forbidden.
type Class uint8

const (
	ClassNone Class = iota
	ClassDelete
	ClassExec
)

// forbidNames maps a class to its agent.toml name. A class absent from the
// map, such as ClassNone, is never forbidden.
var forbidNames = map[Class]string{
	ClassDelete: "delete",
	ClassExec:   "exec",
}

// Forbids reports whether agent.toml forbids this class of command.
func (p *Policy) Forbids(c Class) bool {
	name, ok := forbidNames[c]
	if !ok {
		return false
	}
	return slices.Contains(p.cfg.ForbidCommands, name)
}

// SourceRejection is the error CheckImageSource and CheckRepositorySource
// return. It names the rejected source and never the patterns that were tried.
type SourceRejection struct {
	key    string
	source string
}

func (r *SourceRejection) Error() string {
	noun := "images"
	if r.key == "repositories" {
		noun = "builds"
	}
	return fmt.Sprintf("agent.toml on this server allows %s only from [sources] %s; %s matches none of them. Add a pattern to [sources] %s in %s and restart the agent.",
		noun, r.key, r.source, r.key, agentTOMLPath)
}

// imageSource returns the normal form of ref without its tag or digest, or ""
// for a reference that cannot be split.
func imageSource(ref string) string {
	if ref == "" {
		return ""
	}
	repo, _, _, err := splitImage(ref)
	if err != nil || repo == "" {
		return ""
	}
	return normalizeRepo(repo)
}

// CheckImageSource reports whether ref may be pulled under [sources]
// registries. A moonglass.local reference is admitted here; the agent applies
// the moonglass.local rule itself, since only it can look at the image store.
func (p *Policy) CheckImageSource(ref string) error {
	if p.cfg.Registries == nil {
		return nil
	}
	src := imageSource(ref)
	host, rest, _ := strings.Cut(src, "/")
	// The Engine reads a first element with upper case as the registry host and
	// never accepts an upper-case path, so a normal form whose path has upper
	// case would name a different host than the one the Engine pulls from.
	if rest != strings.ToLower(rest) {
		return &SourceRejection{key: "registries", source: src}
	}
	if host == "moonglass.local" {
		return nil
	}
	for _, pattern := range p.cfg.Registries {
		if matched, err := path.Match(pattern, src); err == nil && matched {
			return nil
		}
	}
	return &SourceRejection{key: "registries", source: src}
}

// CheckRepositorySource reports whether repo may be cloned under [sources]
// repositories, matching it as github.com/<owner>/<repo>, lower-cased.
func (p *Policy) CheckRepositorySource(repo string) error {
	if p.cfg.Repositories == nil {
		return nil
	}
	src := "github.com/" + strings.ToLower(repo)
	parts := strings.Split(repo, "/")
	if len(parts) == 2 && isPathElement(parts[0]) && isPathElement(parts[1]) {
		for _, pattern := range p.cfg.Repositories {
			if matched, err := path.Match(pattern, src); err == nil && matched {
				return nil
			}
		}
	}
	return &SourceRejection{key: "repositories", source: src}
}
