package policy

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// tomlList renders items as a TOML array of basic strings.
func tomlList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// sourcesTOML renders a [sources] table. A nil list is absent; an empty,
// non-nil list is present and empty.
func sourcesTOML(registries, repositories []string) string {
	var b strings.Builder
	b.WriteString("[sources]\n")
	if registries != nil {
		fmt.Fprintf(&b, "registries = %s\n", tomlList(registries))
	}
	if repositories != nil {
		fmt.Fprintf(&b, "repositories = %s\n", tomlList(repositories))
	}
	return b.String()
}

func onlyRegistries(patterns ...string) string {
	if patterns == nil {
		patterns = []string{}
	}
	return sourcesTOML(patterns, nil)
}

func onlyRepositories(patterns ...string) string {
	if patterns == nil {
		patterns = []string{}
	}
	return sourcesTOML(nil, patterns)
}

// commandsTOML renders a [commands] table. A nil list is absent.
func commandsTOML(forbid []string) string {
	if forbid == nil {
		return ""
	}
	return "[commands]\nforbid = " + tomlList(forbid) + "\n"
}

func mustSourcePolicy(t *testing.T, toml string) *Policy {
	t.Helper()
	p, err := Parse([]byte(toml), Env{})
	if err != nil {
		t.Fatalf("Parse(%q) error = %v, want nil", toml, err)
	}
	return p
}

func TestImageSourceNormalForm(t *testing.T) {
	tests := []struct{ name, ref, want string }{
		{"official image with tag", "nginx:1.27", "docker.io/library/nginx"},
		{"owner image with digest", "acme/web@" + digestA, "docker.io/acme/web"},
		{"upper-case host with tag", "GHCR.io/acme/web:v2", "ghcr.io/acme/web"},
		{"localhost with port", "localhost:5000/web", "localhost:5000/web"},
		{"three-element path", "ghcr.io/acme/team/web", "ghcr.io/acme/team/web"},
		{"index.docker.io folded", "index.docker.io/acme/web", "docker.io/acme/web"},
		{"docker.io official image", "docker.io/nginx", "docker.io/library/nginx"},
		{"localhost without port", "localhost/web", "localhost/web"},
		{"index.docker.io official image", "index.docker.io/nginx", "docker.io/library/nginx"},
		{"tag and digest ignored", "localhost:5000/web:v1@" + digestA, "localhost:5000/web"},
		{"empty reference", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := imageSource(tc.ref); got != tc.want {
				t.Errorf("imageSource(%q) = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

func TestImageSourceMatching(t *testing.T) {
	tests := []struct {
		name     string
		toml     string
		ref      string
		wantErr  bool
		wantText string
	}{
		{
			name: "listed registry matches",
			toml: onlyRegistries("ghcr.io/acme/*"),
			ref:  "ghcr.io/acme/web:v1",
		},
		{
			name:    "unlisted registry is rejected",
			toml:    onlyRegistries("ghcr.io/acme/*"),
			ref:     "ghcr.io/acme/team/web",
			wantErr: true,
			wantText: "agent.toml on this server allows images only from [sources] registries; " +
				"ghcr.io/acme/team/web matches none of them. " +
				"Add a pattern to [sources] registries in /etc/moonglass/agent.toml and restart the agent.",
		},
		{
			name: "a star does not cross a slash",
			toml: onlyRegistries("ghcr.io/acme/*/*"),
			ref:  "ghcr.io/acme/team/web",
		},
		{
			name: "official image needs library",
			toml: onlyRegistries("docker.io/library/*"),
			ref:  "nginx:1.27",
		},
		{
			name: "digest does not affect matching",
			toml: onlyRegistries("docker.io/acme/*"),
			ref:  "docker.io/acme/web@" + digestA,
		},
		{
			name: "tag and digest do not affect matching",
			toml: onlyRegistries("docker.io/acme/*"),
			ref:  "docker.io/acme/web:v1@" + digestA,
		},
		{
			name:    "tag does not rescue an unlisted repository",
			toml:    onlyRegistries("docker.io/acme/other/*"),
			ref:     "docker.io/acme/web:v1",
			wantErr: true,
			wantText: "agent.toml on this server allows images only from [sources] registries; " +
				"docker.io/acme/web matches none of them. " +
				"Add a pattern to [sources] registries in /etc/moonglass/agent.toml and restart the agent.",
		},
		{
			name:    "an upper-case path never matches",
			toml:    onlyRegistries("ghcr.io/acme/*"),
			ref:     "ghcr.io/Acme/web",
			wantErr: true,
		},
		{
			name: "moonglass.local is never matched",
			toml: onlyRegistries("ghcr.io/acme/*"),
			ref:  "moonglass.local/p/s",
		},
		{
			name: "moonglass.local host is case-insensitive",
			toml: onlyRegistries("ghcr.io/acme/*"),
			ref:  "MoonGlass.Local/p/s:v1",
		},
		{
			name:    "moonglass.local with a port is an ordinary host",
			toml:    onlyRegistries("ghcr.io/acme/*"),
			ref:     "moonglass.local:5000/x",
			wantErr: true,
		},
		{
			name: "absent registries does not restrict",
			toml: "",
			ref:  "nginx:1.27",
		},
		{
			name: "absent registries admits a ported moonglass.local host",
			toml: "",
			ref:  "moonglass.local:5000/x",
		},
		{
			name:    "an empty reference is rejected",
			toml:    onlyRegistries("ghcr.io/acme/*"),
			ref:     "",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := mustSourcePolicy(t, tc.toml)
			err := p.CheckImageSource(tc.ref)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("CheckImageSource(%q) with %q: error = %v, want nil", tc.ref, tc.toml, err)
				}
				return
			}
			var rej *SourceRejection
			if !errors.As(err, &rej) {
				t.Fatalf("CheckImageSource(%q) with %q: error = %v, want *SourceRejection", tc.ref, tc.toml, err)
			}
			if tc.wantText != "" {
				if got := err.Error(); got != tc.wantText {
					t.Errorf("CheckImageSource(%q) error = %q, want %q", tc.ref, got, tc.wantText)
				}
				for _, pattern := range []string{"ghcr.io/acme/*", "docker.io/acme/other/*"} {
					if strings.Contains(err.Error(), pattern) {
						t.Errorf("CheckImageSource(%q) error = %q, must not list the pattern %q", tc.ref, err.Error(), pattern)
					}
				}
			}
		})
	}
}

func TestRepositorySourceMatching(t *testing.T) {
	tests := []struct {
		name     string
		toml     string
		repo     string
		wantErr  bool
		wantText string
	}{
		{
			name: "owner glob matches",
			toml: onlyRepositories("github.com/acme/*"),
			repo: "Acme/Web",
		},
		{
			name: "any owner matches",
			toml: onlyRepositories("github.com/*/*"),
			repo: "Acme/Web",
		},
		{
			name: "other owner is rejected",
			toml: onlyRepositories("github.com/other/*"),
			repo: "Acme/Web",
			wantText: "agent.toml on this server allows builds only from [sources] repositories; " +
				"github.com/acme/web matches none of them. " +
				"Add a pattern to [sources] repositories in /etc/moonglass/agent.toml and restart the agent.",
			wantErr: true,
		},
		{
			name:    "a bare owner is rejected",
			toml:    onlyRepositories("github.com/*/*"),
			repo:    "acme",
			wantErr: true,
		},
		{
			name:    "three elements are rejected",
			toml:    onlyRepositories("github.com/*/*"),
			repo:    "a/b/c",
			wantErr: true,
		},
		{
			name:    "dot dot is rejected",
			toml:    onlyRepositories("github.com/*/*"),
			repo:    "../x",
			wantErr: true,
		},
		{
			name:    "an empty repository is rejected",
			toml:    onlyRepositories("github.com/*/*"),
			repo:    "",
			wantErr: true,
		},
		{
			name: "absent repositories does not restrict",
			toml: "",
			repo: "acme",
		},
		{
			name: "absent repositories admits a malformed repository",
			toml: "",
			repo: "../x",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := mustSourcePolicy(t, tc.toml)
			err := p.CheckRepositorySource(tc.repo)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("CheckRepositorySource(%q) with %q: error = %v, want nil", tc.repo, tc.toml, err)
				}
				return
			}
			var rej *SourceRejection
			if !errors.As(err, &rej) {
				t.Fatalf("CheckRepositorySource(%q) with %q: error = %v, want *SourceRejection", tc.repo, tc.toml, err)
			}
			if tc.wantText != "" {
				if got := err.Error(); got != tc.wantText {
					t.Errorf("CheckRepositorySource(%q) error = %q, want %q", tc.repo, got, tc.wantText)
				}
			}
		})
	}
}

func TestSourcePatternsValidatedAtLoad(t *testing.T) {
	tests := []struct {
		name    string
		toml    string
		wantErr bool
		wants   []string
	}{
		{
			name:    "upper-case registry host",
			toml:    onlyRegistries("GHCR.io/acme/*"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "star in the registry host",
			toml:    onlyRegistries("*.io/acme/*"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "question mark in the registry host",
			toml:    onlyRegistries("ghcr?io/x"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "class in the registry host",
			toml:    onlyRegistries("[g]hcr.io/x"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "first element is not a host",
			toml:    onlyRegistries("acme/*"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "host without a path",
			toml:    onlyRegistries("ghcr.io"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "index.docker.io is not the normal form",
			toml:    onlyRegistries("index.docker.io/acme/*"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "moonglass.local is never matched",
			toml:    onlyRegistries("moonglass.local/*"),
			wantErr: true,
			wants:   []string{"registries", "docker.io/library/nginx"},
		},
		{
			name:    "upper-case repository",
			toml:    onlyRepositories("github.com/Acme/*"),
			wantErr: true,
			wants:   []string{"repositories"},
		},
		{
			name:    "another forge",
			toml:    onlyRepositories("gitlab.com/acme/*"),
			wantErr: true,
			wants:   []string{"repositories"},
		},
		{
			name:    "four element repository",
			toml:    onlyRepositories("github.com/acme/web/x"),
			wantErr: true,
			wants:   []string{"repositories"},
		},
		{
			name:    "empty repository element",
			toml:    onlyRepositories("github.com//x"),
			wantErr: true,
			wants:   []string{"repositories"},
		},
		{
			name:    "short repository suggests a pattern",
			toml:    onlyRepositories("github.com/acme"),
			wantErr: true,
			wants:   []string{"repositories", "github.com/acme/*"},
		},
		{
			name: "docker.io library pattern loads",
			toml: onlyRegistries("docker.io/library/*"),
		},
		{
			name: "localhost with a port loads",
			toml: onlyRegistries("localhost:5000/*"),
		},
		{
			name: "two-element registry path loads",
			toml: onlyRegistries("ghcr.io/acme/*/*"),
		},
		{
			name: "repository owner glob loads",
			toml: onlyRepositories("github.com/acme/*"),
		},
		{
			name: "repository any-owner glob loads",
			toml: onlyRepositories("github.com/*/*"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse([]byte(tc.toml), Env{})
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Parse(%q) error = %v, want nil", tc.toml, err)
				}
				if p == nil {
					t.Fatalf("Parse(%q) = nil, nil; want a policy", tc.toml)
				}
				return
			}
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want an error containing %v", tc.toml, tc.wants)
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Parse(%q) error = %q, want it to contain %q", tc.toml, err.Error(), want)
				}
			}
		})
	}
}

func TestEmptySourceListAllowsNothing(t *testing.T) {
	p := mustSourcePolicy(t, sourcesTOML([]string{}, []string{}))

	for _, ref := range []string{"nginx:1.27", "docker.io/library/nginx", "ghcr.io/acme/team/web@" + digestA} {
		var rej *SourceRejection
		if err := p.CheckImageSource(ref); !errors.As(err, &rej) {
			t.Errorf("CheckImageSource(%q) with registries = []: error = %v, want *SourceRejection", ref, err)
		}
	}
	if err := p.CheckImageSource("moonglass.local/p/s"); err != nil {
		t.Errorf("CheckImageSource(%q) with registries = []: error = %v, want nil", "moonglass.local/p/s", err)
	}

	for _, repo := range []string{"acme/web", "", "../x"} {
		var rej *SourceRejection
		if err := p.CheckRepositorySource(repo); !errors.As(err, &rej) {
			t.Errorf("CheckRepositorySource(%q) with repositories = []: error = %v, want *SourceRejection", repo, err)
		}
	}

	cfg := p.Config()
	if cfg.Registries == nil || len(cfg.Registries) != 0 {
		t.Errorf("Config().Registries = %#v, want a present empty list (non-nil, length 0)", cfg.Registries)
	}
	if cfg.Repositories == nil || len(cfg.Repositories) != 0 {
		t.Errorf("Config().Repositories = %#v, want a present empty list (non-nil, length 0)", cfg.Repositories)
	}

	absent := mustSourcePolicy(t, "").Config()
	if absent.Registries != nil {
		t.Errorf("Config().Registries with [sources] absent = %#v, want nil", absent.Registries)
	}
	if absent.Repositories != nil {
		t.Errorf("Config().Repositories with [sources] absent = %#v, want nil", absent.Repositories)
	}

	emptyRegistries := mustSourcePolicy(t, onlyRegistries()).Config()
	if emptyRegistries.Registries == nil || len(emptyRegistries.Registries) != 0 {
		t.Errorf("Config().Registries with registries = [] = %#v, want a present empty list", emptyRegistries.Registries)
	}
	if emptyRegistries.Repositories != nil {
		t.Errorf("Config().Repositories with only registries = [] = %#v, want nil", emptyRegistries.Repositories)
	}

	emptyRepositories := mustSourcePolicy(t, onlyRepositories()).Config()
	if emptyRepositories.Repositories == nil || len(emptyRepositories.Repositories) != 0 {
		t.Errorf("Config().Repositories with repositories = [] = %#v, want a present empty list", emptyRepositories.Repositories)
	}
	if emptyRepositories.Registries != nil {
		t.Errorf("Config().Registries with only repositories = [] = %#v, want nil", emptyRepositories.Registries)
	}
}

func TestForbidsClasses(t *testing.T) {
	tests := []struct {
		name   string
		forbid []string
		delete bool
		exec   bool
	}{
		{name: "absent", forbid: nil},
		{name: "delete only", forbid: []string{"delete"}, delete: true},
		{name: "exec only", forbid: []string{"exec"}, exec: true},
		{name: "both", forbid: []string{"delete", "exec"}, delete: true, exec: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := mustSourcePolicy(t, commandsTOML(tc.forbid))
			cases := []struct {
				name  string
				class Class
				want  bool
			}{
				{"ClassNone", ClassNone, false},
				{"ClassDelete", ClassDelete, tc.delete},
				{"ClassExec", ClassExec, tc.exec},
			}
			for _, c := range cases {
				if got := p.Forbids(c.class); got != c.want {
					t.Errorf("Forbids(%s) with forbid = %v = %v, want %v", c.name, tc.forbid, got, c.want)
				}
			}
		})
	}

	if got := mustSourcePolicy(t, commandsTOML([]string{"exec"})).Config().ForbidCommands; len(got) != 1 || got[0] != "exec" {
		t.Errorf("Config().ForbidCommands with forbid = [\"exec\"] = %v, want [exec]", got)
	}
}

func TestAllowBlockImageUsesSourceNormalForm(t *testing.T) {
	l := newLayout(t)
	tests := []struct {
		name       string
		blockImage string
		specImage  string
		wantDenied bool
	}{
		{
			name:       "index.docker.io block matches the bare name",
			blockImage: "index.docker.io/prom/node-exporter@" + digestA,
			specImage:  "prom/node-exporter@" + digestA,
		},
		{
			name:       "localhost block matches the same reference",
			blockImage: "localhost/web@" + digestA,
			specImage:  "localhost/web@" + digestA,
		},
		{
			name:       "localhost block does not match a docker.io path",
			blockImage: "localhost/web@" + digestA,
			specImage:  "docker.io/localhost/web@" + digestA,
			wantDenied: true,
		},
		{
			name:       "localhost block needs a digest",
			blockImage: "localhost/web@" + digestA,
			specImage:  "localhost/web:v1",
			wantDenied: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Parse([]byte(rawBlock("monitoring", tc.blockImage, "pid_host = true")), l.Env)
			if err != nil {
				t.Fatalf("Parse(block image %q) error = %v, want nil", tc.blockImage, err)
			}
			s := baseSpec()
			s.PID = "host"
			s.Image = tc.specImage
			_, err = p.Check(s)
			if !tc.wantDenied {
				if err != nil {
					t.Fatalf("Check(image %q) with block image %q: error = %v, want nil", tc.specImage, tc.blockImage, err)
				}
				return
			}
			if !hasKey(err, "pid_host") {
				t.Errorf("Check(image %q) with block image %q: error = %v, want a pid_host rejection", tc.specImage, tc.blockImage, err)
			}
		})
	}
}
