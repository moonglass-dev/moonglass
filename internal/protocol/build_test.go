package protocol

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// validBuild returns a Build that passes every validator.
func validBuild() *agentv1.Build {
	return &agentv1.Build{
		AppId:       "app-1",
		ReleaseId:   "rel-1",
		Project:     "web",
		Environment: "prod",
		Service:     "api",
		Repository:  "octo-org/app.repo_1",
		Commit:      strings.Repeat("a", 40),
	}
}

func TestBuildRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Build{}).ProtoReflect().Descriptor())

	tests := []struct {
		name string
		msg  *agentv1.Build
	}{
		{"every field set", validBuild()},
		{
			"64 byte identifiers",
			&agentv1.Build{
				AppId:       strings.Repeat("A", 64),
				ReleaseId:   strings.Repeat("z", 64),
				Project:     "web",
				Environment: "prod",
				Service:     "api",
				Repository:  "octo-org/app",
				Commit:      strings.Repeat("0", 40),
			},
		},
		{
			"63 byte names",
			&agentv1.Build{
				AppId:       "app-1",
				ReleaseId:   "rel-1",
				Project:     strings.Repeat("p", 63),
				Environment: strings.Repeat("e", 63),
				Service:     strings.Repeat("s", 63),
				Repository:  "octo-org/app",
				Commit:      strings.Repeat("a", 40),
			},
		},
		{
			"1 byte names",
			&agentv1.Build{
				AppId:       "app-1",
				ReleaseId:   "rel-1",
				Project:     "p",
				Environment: "e",
				Service:     "s",
				Repository:  "octo-org/app",
				Commit:      strings.Repeat("a", 40),
			},
		},
		{
			"39 byte owner and 100 byte repo",
			&agentv1.Build{
				AppId:       "app-1",
				ReleaseId:   "rel-1",
				Project:     "web",
				Environment: "prod",
				Service:     "api",
				Repository:  strings.Repeat("o", 39) + "/" + strings.Repeat("r", 100),
				Commit:      strings.Repeat("a", 40),
			},
		},
		{
			"single character owner and repo",
			&agentv1.Build{
				AppId:       "app-1",
				ReleaseId:   "rel-1",
				Project:     "web",
				Environment: "prod",
				Service:     "api",
				Repository:  "a/b",
				Commit:      strings.Repeat("a", 40),
			},
		},
		{
			"repo with dot underscore and dash after the first byte",
			&agentv1.Build{
				AppId:       "app-1",
				ReleaseId:   "rel-1",
				Project:     "web",
				Environment: "prod",
				Service:     "api",
				Repository:  "o/a.b_c-d",
				Commit:      strings.Repeat("a", 40),
			},
		},
		{
			"owner with dash after the first byte",
			&agentv1.Build{
				AppId:       "app-1",
				ReleaseId:   "rel-1",
				Project:     "web",
				Environment: "prod",
				Service:     "api",
				Repository:  "a-b/b",
				Commit:      strings.Repeat("a", 40),
			},
		},
		{
			"all digit commit",
			&agentv1.Build{
				AppId:       "app-1",
				ReleaseId:   "rel-1",
				Project:     "web",
				Environment: "prod",
				Service:     "api",
				Repository:  "octo-org/app",
				Commit:      strings.Repeat("1234567890", 4),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			roundTrip(t, agentv1.MessageType_BUILD, tt.msg, &agentv1.Build{})
		})
	}
}

func TestBuildRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*agentv1.Build)
		field  string
	}{
		{"app_id empty", func(b *agentv1.Build) { b.AppId = "" }, "app_id"},
		{"app_id 65 bytes", func(b *agentv1.Build) { b.AppId = strings.Repeat("a", 65) }, "app_id"},
		{"app_id with slash", func(b *agentv1.Build) { b.AppId = "a/b" }, "app_id"},
		{"release_id empty", func(b *agentv1.Build) { b.ReleaseId = "" }, "release_id"},
		{"release_id 65 bytes", func(b *agentv1.Build) { b.ReleaseId = strings.Repeat("a", 65) }, "release_id"},
		{"release_id with slash", func(b *agentv1.Build) { b.ReleaseId = "a/b" }, "release_id"},

		{"project empty", func(b *agentv1.Build) { b.Project = "" }, "project"},
		{"project 64 bytes", func(b *agentv1.Build) { b.Project = strings.Repeat("p", 64) }, "project"},
		{"project uppercase", func(b *agentv1.Build) { b.Project = "Web" }, "project"},
		{"project underscore", func(b *agentv1.Build) { b.Project = "a_b" }, "project"},
		{"project leading dash", func(b *agentv1.Build) { b.Project = "-a" }, "project"},
		{"environment empty", func(b *agentv1.Build) { b.Environment = "" }, "environment"},
		{"environment 64 bytes", func(b *agentv1.Build) { b.Environment = strings.Repeat("e", 64) }, "environment"},
		{"environment uppercase", func(b *agentv1.Build) { b.Environment = "Prod" }, "environment"},
		{"environment underscore", func(b *agentv1.Build) { b.Environment = "a_b" }, "environment"},
		{"environment leading dash", func(b *agentv1.Build) { b.Environment = "-a" }, "environment"},
		{"service empty", func(b *agentv1.Build) { b.Service = "" }, "service"},
		{"service 64 bytes", func(b *agentv1.Build) { b.Service = strings.Repeat("s", 64) }, "service"},
		{"service uppercase", func(b *agentv1.Build) { b.Service = "Api" }, "service"},
		{"service underscore", func(b *agentv1.Build) { b.Service = "a_b" }, "service"},
		{"service leading dash", func(b *agentv1.Build) { b.Service = "-a" }, "service"},

		{"repository empty", func(b *agentv1.Build) { b.Repository = "" }, "repository"},
		{"repository with no slash", func(b *agentv1.Build) { b.Repository = "owneronly" }, "repository"},
		{"repository with two slashes", func(b *agentv1.Build) { b.Repository = "a/b/c" }, "repository"},
		{"repository with empty owner", func(b *agentv1.Build) { b.Repository = "/b" }, "repository"},
		{"repository with empty repo", func(b *agentv1.Build) { b.Repository = "a/" }, "repository"},
		{"repository owner 40 bytes", func(b *agentv1.Build) { b.Repository = strings.Repeat("o", 40) + "/b" }, "repository"},
		{"repository owner leading dash", func(b *agentv1.Build) { b.Repository = "-a/b" }, "repository"},
		{"repository owner underscore", func(b *agentv1.Build) { b.Repository = "a_b/b" }, "repository"},
		{"repository owner dot", func(b *agentv1.Build) { b.Repository = "a.b/b" }, "repository"},
		{"repository repo 101 bytes", func(b *agentv1.Build) { b.Repository = "a/" + strings.Repeat("r", 101) }, "repository"},
		{"repository repo dot", func(b *agentv1.Build) { b.Repository = "a/." }, "repository"},
		{"repository repo dotdot", func(b *agentv1.Build) { b.Repository = "a/.." }, "repository"},
		{"repository repo leading dot", func(b *agentv1.Build) { b.Repository = "a/.a" }, "repository"},
		{"repository repo leading dash", func(b *agentv1.Build) { b.Repository = "a/-a" }, "repository"},
		{"repository with colon", func(b *agentv1.Build) { b.Repository = "a/b:c" }, "repository"},
		{"repository with at sign", func(b *agentv1.Build) { b.Repository = "a/b@c" }, "repository"},
		{"repository with space", func(b *agentv1.Build) { b.Repository = "a/b c" }, "repository"},
		{"repository with NUL", func(b *agentv1.Build) { b.Repository = "a/b\x00c" }, "repository"},
		{"repository with non-ASCII byte", func(b *agentv1.Build) { b.Repository = "a/bé" }, "repository"},
		{"repository with scheme", func(b *agentv1.Build) { b.Repository = "https://github.com/a/b" }, "repository"},
		{"repository with host", func(b *agentv1.Build) { b.Repository = "github.com/a/b" }, "repository"},
		{"repository as ssh url", func(b *agentv1.Build) { b.Repository = "git@github.com:a/b" }, "repository"},

		{"commit empty", func(b *agentv1.Build) { b.Commit = "" }, "commit"},
		{"commit 39 characters", func(b *agentv1.Build) { b.Commit = strings.Repeat("a", 39) }, "commit"},
		{"commit 41 characters", func(b *agentv1.Build) { b.Commit = strings.Repeat("a", 41) }, "commit"},
		{"commit uppercase hex", func(b *agentv1.Build) { b.Commit = strings.Repeat("A", 40) }, "commit"},
		{"commit non-hex letter", func(b *agentv1.Build) { b.Commit = strings.Repeat("g", 40) }, "commit"},
		{"commit branch name", func(b *agentv1.Build) { b.Commit = "main" }, "commit"},
		{"commit leading dash", func(b *agentv1.Build) { b.Commit = "-" + strings.Repeat("a", 39) }, "commit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validBuild()
			tt.mutate(m)
			env, err := Wrap(agentv1.MessageType_BUILD, "req-1", m)
			if err != nil {
				t.Fatalf("Wrap(BUILD) error = %v", err)
			}
			err = Open(env, &agentv1.Build{})
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(%s): expected ErrMalformed, got %v", tt.name, err)
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Open(%s) error = %q, expected it to name field %q", tt.name, err, tt.field)
			}
		})
	}
}

func TestBuildProtocolAbuse(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Build{}).ProtoReflect().Descriptor())

	t.Run("unknown top-level field", func(t *testing.T) {
		t.Parallel()
		payload, err := proto.Marshal(validBuild())
		if err != nil {
			t.Fatalf("proto.Marshal(Build) error = %v", err)
		}
		payload = protowire.AppendTag(payload, 99, protowire.VarintType)
		payload = protowire.AppendVarint(payload, 1)
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_BUILD,
			RequestId: "r",
			Payload:   payload,
		}
		if err := Open(env, &agentv1.Build{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open with unknown Build field: expected ErrMalformed, got %v", err)
		}
	})

	t.Run("type mismatches", func(t *testing.T) {
		t.Parallel()
		buildEnv, err := Wrap(agentv1.MessageType_BUILD, "r", validBuild())
		if err != nil {
			t.Fatalf("Wrap(BUILD) error = %v", err)
		}
		if err := Open(buildEnv, &agentv1.Deploy{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Open(BUILD as Deploy): expected ErrTypeMismatch, got %v", err)
		}

		deployEnv, err := Wrap(agentv1.MessageType_DEPLOY, "r", validDeployMessage())
		if err != nil {
			t.Fatalf("Wrap(DEPLOY) error = %v", err)
		}
		if err := Open(deployEnv, &agentv1.Build{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Open(DEPLOY as Build): expected ErrTypeMismatch, got %v", err)
		}

		if _, err := Wrap(agentv1.MessageType_BUILD, "r", &agentv1.Deploy{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Wrap(BUILD, Deploy): expected ErrTypeMismatch, got %v", err)
		}
		if _, err := Wrap(agentv1.MessageType_DEPLOY, "r", &agentv1.Build{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Wrap(DEPLOY, Build): expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("error field on build", func(t *testing.T) {
		t.Parallel()
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_BUILD,
			RequestId: "r",
			Error:     &agentv1.Error{Code: agentv1.ErrorCode_INVALID},
		}
		if _, err := Marshal(env); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Marshal(BUILD with error): expected ErrMalformed, got %v", err)
		}
		body, err := proto.Marshal(env)
		if err != nil {
			t.Fatalf("proto.Marshal() error = %v", err)
		}
		if _, err := Unmarshal(mustFrame(t, body)); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Unmarshal(BUILD with error): expected ErrMalformed, got %v", err)
		}
	})
}

func FuzzOpenBuild(f *testing.F) {
	minimal, err := proto.Marshal(validBuild())
	if err != nil {
		f.Fatalf("proto.Marshal(minimal Build) error = %v", err)
	}
	f.Add(minimal)

	maximum, err := proto.Marshal(&agentv1.Build{
		AppId:       strings.Repeat("a", 64),
		ReleaseId:   strings.Repeat("z", 64),
		Project:     strings.Repeat("p", 63),
		Environment: strings.Repeat("e", 63),
		Service:     strings.Repeat("s", 63),
		Repository:  strings.Repeat("o", 39) + "/" + strings.Repeat("r", 100),
		Commit:      strings.Repeat("f", 40),
	})
	if err != nil {
		f.Fatalf("proto.Marshal(maximum Build) error = %v", err)
	}
	f.Add(maximum)
	f.Add([]byte(nil))

	unknown := append([]byte(nil), minimal...)
	unknown = protowire.AppendTag(unknown, 99, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	f.Add(unknown)

	sshURL, err := proto.Marshal(&agentv1.Build{
		AppId:       "app-1",
		ReleaseId:   "rel-1",
		Project:     "web",
		Environment: "prod",
		Service:     "api",
		Repository:  "git@github.com:a/b",
		Commit:      strings.Repeat("a", 40),
	})
	if err != nil {
		f.Fatalf("proto.Marshal(ssh url Build) error = %v", err)
	}
	f.Add(sshURL)

	f.Fuzz(func(t *testing.T, in []byte) {
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_BUILD,
			RequestId: "fuzz",
			Payload:   in,
		}
		var first agentv1.Build
		if err := Open(env, &first); err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(BUILD, %x) error = %v, expected nil or ErrMalformed", in, err)
			}
			return
		}
		if err := validateBuild(&first); err != nil {
			t.Fatalf("Open accepted BUILD payload %x but validateBuild failed: %v", in, err)
		}
		wrapped, err := Wrap(agentv1.MessageType_BUILD, "fuzz", &first)
		if err != nil {
			t.Fatalf("Wrap(accepted Build %v) error = %v", &first, err)
		}
		var again agentv1.Build
		if err := Open(wrapped, &again); err != nil {
			t.Fatalf("Open after re-wrap error = %v", err)
		}
		if !proto.Equal(&first, &again) {
			t.Fatalf("round-trip mismatch: first %v, again %v", &first, &again)
		}
	})
}
