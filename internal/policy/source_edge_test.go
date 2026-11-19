package policy

import (
	"errors"
	"strings"
	"testing"
)

// TestCheckImageSourceRejectsUpperCasePath covers references the Docker Engine
// resolves to a registry the normal form does not name: splitDockerDomain reads
// a first element with upper case as the host, and the Engine never accepts an
// upper-case path, so the check must fail closed on the path's case.
func TestCheckImageSourceRejectsUpperCasePath(t *testing.T) {
	tests := []struct {
		name       string
		toml       string
		ref        string
		wantErr    bool
		wantSource string
	}{
		{
			name:       "upper-case first element under a docker.io wildcard",
			toml:       onlyRegistries("docker.io/*/*"),
			ref:        "Evil/web:latest",
			wantErr:    true,
			wantSource: "docker.io/Evil/web",
		},
		{
			name:       "upper-case owner under a ghcr.io wildcard",
			toml:       onlyRegistries("ghcr.io/*/*"),
			ref:        "ghcr.io/Acme/web",
			wantErr:    true,
			wantSource: "ghcr.io/Acme/web",
		},
		{
			name:       "upper-case first element the Engine reads as a host",
			toml:       onlyRegistries("docker.io/*/*"),
			ref:        "LOCALHOST/web",
			wantErr:    true,
			wantSource: "docker.io/LOCALHOST/web",
		},
		{
			name:       "upper-case path under moonglass.local",
			toml:       onlyRegistries("ghcr.io/acme/*"),
			ref:        "moonglass.local/Evil/web",
			wantErr:    true,
			wantSource: "moonglass.local/Evil/web",
		},
		{
			name: "lower-case path under a docker.io wildcard",
			toml: onlyRegistries("docker.io/*/*"),
			ref:  "docker.io/acme/web:latest",
		},
		{
			name: "lower-case path under a ghcr.io wildcard",
			toml: onlyRegistries("ghcr.io/*/*"),
			ref:  "ghcr.io/acme/web",
		},
		{
			name: "an upper-case host is still folded",
			toml: onlyRegistries("ghcr.io/acme/*"),
			ref:  "GHCR.io/acme/web",
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
			if got := err.Error(); !strings.Contains(got, tc.wantSource) {
				t.Errorf("CheckImageSource(%q) error = %q, want it to name %q", tc.ref, got, tc.wantSource)
			}
			if strings.Contains(err.Error(), "*") {
				t.Errorf("CheckImageSource(%q) error = %q, must not list a pattern", tc.ref, err.Error())
			}
		})
	}
}

// TestRepositoryPatternSuggestion checks that the load error suggests a
// pattern that names the rejected owner and itself loads.
func TestRepositoryPatternSuggestion(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"github.com/other", "github.com/other/*"},
		{"github.com/acme", "github.com/acme/*"},
		{"github.com/acme/", "github.com/acme/*"},
		{"github.com//x", "github.com/acme/*"},
		{"gitlab.com/acme/*", "github.com/acme/*"},
		{"github.com/acme/web/x", "github.com/acme/*"},
	}
	for _, tc := range tests {
		t.Run(tc.pattern, func(t *testing.T) {
			if got := repositoryPatternSuggestion(tc.pattern); got != tc.want {
				t.Errorf("repositoryPatternSuggestion(%q) = %q, want %q", tc.pattern, got, tc.want)
			}
			if err := validRepositoryPattern(tc.want); err != nil {
				t.Errorf("validRepositoryPattern(%q) = %v, want nil: the suggestion must load", tc.want, err)
			}
			_, err := Parse([]byte(onlyRepositories(tc.pattern)), Env{})
			if err == nil {
				t.Fatalf("Parse(repositories = [%q]) error = nil, want an error suggesting %q", tc.pattern, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse(repositories = [%q]) error = %q, want it to contain %q", tc.pattern, err.Error(), tc.want)
			}
		})
	}
}
