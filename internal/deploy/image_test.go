package deploy

import "testing"

func TestResolveRefParsing(t *testing.T) {
	t.Run("localProject", func(t *testing.T) {
		cases := []struct {
			ref         string
			name        string
			wantProject string
			wantOK      bool
			wantErr     bool
		}{
			{
				name:        "own build",
				ref:         "moonglass.local/proj/web:r1",
				wantProject: "proj",
				wantOK:      true,
			},
			{
				name: "not a local build",
				ref:  "docker.io/library/nginx:1.2",
			},
			{
				name:    "no tag",
				ref:     "moonglass.local/proj/web",
				wantErr: true,
			},
			{
				name:    "bare project",
				ref:     "moonglass.local/proj",
				wantErr: true,
			},
			{
				name:    "empty project",
				ref:     "moonglass.local//web:r1",
				wantErr: true,
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				project, ok, err := localProject(tc.ref)
				requireEqual(t, "localProject("+tc.ref+") project", project, tc.wantProject)
				requireEqual(t, "localProject("+tc.ref+") ok", ok, tc.wantOK)
				requireEqual(t, "localProject("+tc.ref+") error", err != nil, tc.wantErr)
			})
		}
	})

	t.Run("splitRef", func(t *testing.T) {
		cases := []struct {
			ref        string
			name       string
			wantRepo   string
			wantDigest string
		}{
			{name: "bare name", ref: "nginx", wantRepo: "nginx"},
			{name: "registry with a port", ref: "localhost:5000/acme/web", wantRepo: "localhost:5000/acme/web"},
			{name: "registry port and tag", ref: "localhost:5000/acme/web:v1", wantRepo: "localhost:5000/acme/web"},
			{name: "tag", ref: "acme/web:v1", wantRepo: "acme/web"},
			{name: "digest", ref: "nginx@sha256:abc", wantRepo: "nginx", wantDigest: "sha256:abc"},
			{
				name:       "tag and digest",
				ref:        "acme/web:v1@sha256:abc",
				wantRepo:   "acme/web",
				wantDigest: "sha256:abc",
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				repo, digest := splitRef(tc.ref)
				requireEqual(t, "splitRef("+tc.ref+") repo", repo, tc.wantRepo)
				requireEqual(t, "splitRef("+tc.ref+") digest", digest, tc.wantDigest)
			})
		}
	})

	t.Run("normalizeRepo", func(t *testing.T) {
		cases := []struct {
			repo string
			name string
			want string
		}{
			{name: "single element", repo: "nginx", want: "docker.io/library/nginx"},
			{name: "two elements", repo: "prom/node-exporter", want: "docker.io/prom/node-exporter"},
			{name: "already normalised", repo: "docker.io/library/nginx", want: "docker.io/library/nginx"},
			{name: "registry host", repo: "ghcr.io/acme/web", want: "ghcr.io/acme/web"},
			{name: "registry port", repo: "localhost:5000/acme/web", want: "localhost:5000/acme/web"},
			{name: "bare localhost", repo: "localhost/acme/web", want: "localhost/acme/web"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				requireEqual(t, "normalizeRepo("+tc.repo+")", normalizeRepo(tc.repo), tc.want)
			})
		}
	})
}
