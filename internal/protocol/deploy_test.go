package protocol

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// roundTrip wraps msg, marshals, unmarshals and opens it into out.
func roundTrip(t *testing.T, mt agentv1.MessageType, msg, out proto.Message) {
	t.Helper()
	env, err := Wrap(mt, "req-1", msg)
	if err != nil {
		t.Fatalf("Wrap(%v) error = %v", mt, err)
	}
	b, err := Marshal(env)
	if err != nil {
		t.Fatalf("Marshal(%v) error = %v", mt, err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal(%v) error = %v", mt, err)
	}
	if err := Open(got, out); err != nil {
		t.Fatalf("Open(%v) error = %v", mt, err)
	}
	if !proto.Equal(msg, out) {
		t.Fatalf("%v: expected %v, got %v", mt, msg, out)
	}
}

// openRejected wraps msg (Wrap does not validate payloads) and expects Open to fail with ErrMalformed.
func openRejected(t *testing.T, mt agentv1.MessageType, msg, out proto.Message) {
	t.Helper()
	env, err := Wrap(mt, "req-1", msg)
	if err != nil {
		t.Fatalf("Wrap(%v) error = %v", mt, err)
	}
	err = Open(env, out)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Open(%v): expected ErrMalformed, got %v", msg, err)
	}
}

func TestDeployRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Deploy{}).ProtoReflect().Descriptor())

	valid := []*agentv1.Deploy{
		{AppId: "app-1", ReleaseId: "rel_1.0", Image: "registry.example.com/app:1.2", Project: "web", Environment: "prod", Service: "api"},
		{AppId: strings.Repeat("a", 64), ReleaseId: strings.Repeat("Z", 64), Image: strings.Repeat("i", 512), Project: "web", Environment: "prod", Service: "api"},
		{AppId: "a", ReleaseId: "r", Image: "app@sha256:abcdef", Project: "web", Environment: "prod", Service: "api"},
	}
	for _, m := range valid {
		t.Run("valid/"+m.GetAppId()[:1], func(t *testing.T) {
			t.Parallel()
			roundTrip(t, agentv1.MessageType_DEPLOY, m, &agentv1.Deploy{})
		})
	}

	invalid := []struct {
		name string
		msg  *agentv1.Deploy
	}{
		{"empty app id", &agentv1.Deploy{AppId: "", ReleaseId: "r", Image: "img", Project: "web", Environment: "prod", Service: "api"}},
		{"65 byte app id", &agentv1.Deploy{AppId: strings.Repeat("a", 65), ReleaseId: "r", Image: "img", Project: "web", Environment: "prod", Service: "api"}},
		{"app id with slash", &agentv1.Deploy{AppId: "a/b", ReleaseId: "r", Image: "img", Project: "web", Environment: "prod", Service: "api"}},
		{"release id with slash", &agentv1.Deploy{AppId: "a", ReleaseId: "r/1", Image: "img", Project: "web", Environment: "prod", Service: "api"}},
		{"release id with space", &agentv1.Deploy{AppId: "a", ReleaseId: "r 1", Image: "img", Project: "web", Environment: "prod", Service: "api"}},
		{"empty release id", &agentv1.Deploy{AppId: "a", ReleaseId: "", Image: "img", Project: "web", Environment: "prod", Service: "api"}},
		{"empty image", &agentv1.Deploy{AppId: "a", ReleaseId: "r", Image: "", Project: "web", Environment: "prod", Service: "api"}},
		{"513 byte image", &agentv1.Deploy{AppId: "a", ReleaseId: "r", Image: strings.Repeat("i", 513), Project: "web", Environment: "prod", Service: "api"}},
		{"image with space", &agentv1.Deploy{AppId: "a", ReleaseId: "r", Image: "img name", Project: "web", Environment: "prod", Service: "api"}},
		{"image with newline", &agentv1.Deploy{AppId: "a", ReleaseId: "r", Image: "img\nname", Project: "web", Environment: "prod", Service: "api"}},
		{"image with control char", &agentv1.Deploy{AppId: "a", ReleaseId: "r", Image: "img\x01", Project: "web", Environment: "prod", Service: "api"}},
		{"image with leading dash", &agentv1.Deploy{AppId: "a", ReleaseId: "r", Image: "-img", Project: "web", Environment: "prod", Service: "api"}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			openRejected(t, agentv1.MessageType_DEPLOY, tt.msg, &agentv1.Deploy{})
		})
	}
}

func TestSwitchReleaseRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.SwitchRelease{}).ProtoReflect().Descriptor())

	roundTrip(t, agentv1.MessageType_SWITCH_RELEASE,
		&agentv1.SwitchRelease{AppId: "app-1", ReleaseId: "rel-2"}, &agentv1.SwitchRelease{})

	invalid := []struct {
		name string
		msg  *agentv1.SwitchRelease
	}{
		{"empty app id", &agentv1.SwitchRelease{AppId: "", ReleaseId: "r"}},
		{"bad app id", &agentv1.SwitchRelease{AppId: "a b", ReleaseId: "r"}},
		{"empty release id", &agentv1.SwitchRelease{AppId: "a", ReleaseId: ""}},
		{"65 byte release id", &agentv1.SwitchRelease{AppId: "a", ReleaseId: strings.Repeat("r", 65)}},
		{"bad release id", &agentv1.SwitchRelease{AppId: "a", ReleaseId: "r;rm"}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			openRejected(t, agentv1.MessageType_SWITCH_RELEASE, tt.msg, &agentv1.SwitchRelease{})
		})
	}
}

func TestRollbackRoundTrip(t *testing.T) {
	t.Parallel()
	desc := (&agentv1.Rollback{}).ProtoReflect().Descriptor()
	assertNoShellStringFields(t, desc)
	if n := desc.Fields().Len(); n != 1 {
		t.Fatalf("Rollback field count: expected 1 (app_id only), got %d", n)
	}

	roundTrip(t, agentv1.MessageType_ROLLBACK, &agentv1.Rollback{AppId: "app-1"}, &agentv1.Rollback{})

	for _, tt := range []struct{ name, id string }{
		{"empty", ""},
		{"65 bytes", strings.Repeat("a", 65)},
		{"space", "a b"},
		{"slash", "a/b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			openRejected(t, agentv1.MessageType_ROLLBACK, &agentv1.Rollback{AppId: tt.id}, &agentv1.Rollback{})
		})
	}
}

func TestRunPreDeployRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.RunPreDeploy{}).ProtoReflect().Descriptor())

	for _, rev := range []bool{true, false} {
		m := &agentv1.RunPreDeploy{
			AppId:      "app-1",
			ReleaseId:  "rel-2",
			Args:       []string{"bin/migrate", "--up", "a b; rm -rf /"},
			Reversible: rev,
		}
		var out agentv1.RunPreDeploy
		roundTrip(t, agentv1.MessageType_RUN_PRE_DEPLOY, m, &out)
		if out.GetReversible() != rev {
			t.Fatalf("reversible: expected %v, got %v", rev, out.GetReversible())
		}
		if got := out.GetArgs(); len(got) != 3 || got[2] != "a b; rm -rf /" {
			t.Fatalf("args: expected 3 unjoined entries, got %q", got)
		}
	}

	many := make([]string, 33)
	for i := range many {
		many[i] = "x"
	}
	invalid := []struct {
		name string
		msg  *agentv1.RunPreDeploy
	}{
		{"nil args", &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r"}},
		{"33 args", &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r", Args: many}},
		{"empty first arg", &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r", Args: []string{"", "x"}}},
		{"empty later arg", &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r", Args: []string{"x", ""}}},
		{"NUL in arg", &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r", Args: []string{"x", "a\x00b"}}},
		{"1025 byte arg", &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r", Args: []string{strings.Repeat("a", 1025)}}},
		{"bad app id", &agentv1.RunPreDeploy{AppId: "", ReleaseId: "r", Args: []string{"x"}}},
		{"bad release id", &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r r", Args: []string{"x"}}},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			openRejected(t, agentv1.MessageType_RUN_PRE_DEPLOY, tt.msg, &agentv1.RunPreDeploy{})
		})
	}

	edge := make([]string, 32)
	for i := range edge {
		edge[i] = strings.Repeat("a", 1024)
	}
	roundTrip(t, agentv1.MessageType_RUN_PRE_DEPLOY,
		&agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r", Args: edge}, &agentv1.RunPreDeploy{})
}

func TestDeployMessagesRejectUnknownType(t *testing.T) {
	t.Parallel()

	for _, mt := range []agentv1.MessageType{agentv1.MessageType_MESSAGE_TYPE_UNSPECIFIED, 2147483647, 99} {
		t.Run(mt.String(), func(t *testing.T) {
			t.Parallel()
			env := &agentv1.Envelope{Version: Version, Type: mt, RequestId: "r"}
			if _, err := Marshal(env); err == nil {
				t.Fatalf("Marshal(type %d): expected error, got nil", int32(mt))
			}
			if _, err := Wrap(mt, "r", &agentv1.Rollback{AppId: "a"}); err == nil {
				t.Fatalf("Wrap(type %d): expected error, got nil", int32(mt))
			}
		})
	}

	deploy := &agentv1.Deploy{AppId: "a", ReleaseId: "r", Image: "img", Project: "web", Environment: "prod", Service: "api"}
	sw := &agentv1.SwitchRelease{AppId: "a", ReleaseId: "r"}
	rb := &agentv1.Rollback{AppId: "a"}
	pre := &agentv1.RunPreDeploy{AppId: "a", ReleaseId: "r", Args: []string{"x"}}
	mismatches := []struct {
		name string
		mt   agentv1.MessageType
		msg  proto.Message
		out  proto.Message
	}{
		{"deploy as switch", agentv1.MessageType_DEPLOY, deploy, &agentv1.SwitchRelease{}},
		{"switch as deploy", agentv1.MessageType_SWITCH_RELEASE, sw, &agentv1.Deploy{}},
		{"rollback as run pre-deploy", agentv1.MessageType_ROLLBACK, rb, &agentv1.RunPreDeploy{}},
		{"run pre-deploy as heartbeat", agentv1.MessageType_RUN_PRE_DEPLOY, pre, &agentv1.Heartbeat{}},
		{"deploy as rollback", agentv1.MessageType_DEPLOY, deploy, &agentv1.Rollback{}},
	}
	for _, tt := range mismatches {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env, err := Wrap(tt.mt, "r", tt.msg)
			if err != nil {
				t.Fatalf("Wrap(%v) error = %v", tt.mt, err)
			}
			if err := Open(env, tt.out); !errors.Is(err, ErrTypeMismatch) {
				t.Fatalf("Open: expected ErrTypeMismatch, got %v", err)
			}
		})
	}

	t.Run("wrap with mismatched type", func(t *testing.T) {
		t.Parallel()
		if _, err := Wrap(agentv1.MessageType_DEPLOY, "r", rb); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Wrap(DEPLOY, Rollback): expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("unknown payload field", func(t *testing.T) {
		t.Parallel()
		payload, err := proto.Marshal(rb)
		if err != nil {
			t.Fatalf("Marshal error = %v", err)
		}
		payload = protowire.AppendTag(payload, 99, protowire.VarintType)
		payload = protowire.AppendVarint(payload, 1)
		env := &agentv1.Envelope{Version: Version, Type: agentv1.MessageType_ROLLBACK, RequestId: "r", Payload: payload}
		if err := Open(env, &agentv1.Rollback{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open with unknown field: expected ErrMalformed, got %v", err)
		}
	})
}

// validDeployMessage returns a Deploy with a valid identity and no service settings.
func validDeployMessage() *agentv1.Deploy {
	return &agentv1.Deploy{
		AppId:       "app-1",
		ReleaseId:   "rel-1",
		Image:       "registry.example.com/app:1",
		Project:     "web",
		Environment: "prod",
		Service:     "api",
	}
}

// domain253 returns a valid 253-byte domain with three 63-byte labels and a 61-byte label.
func domain253() string {
	return strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
}

// sixteenDomains returns 16 distinct valid domains, including a 253-byte one and a 63-byte label.
func sixteenDomains() []string {
	domains := []string{domain253(), strings.Repeat("b", 63) + ".example.com"}
	for i := 3; i <= 16; i++ {
		domains = append(domains, fmt.Sprintf("d%d.example.com", i))
	}
	return domains
}

func TestDeployIdentityRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Deploy{}).ProtoReflect().Descriptor())

	tests := []struct {
		msg  *agentv1.Deploy
		name string
	}{
		{
			name: "every field at its maximum",
			msg: &agentv1.Deploy{
				AppId:        "app-1",
				ReleaseId:    "rel-1",
				Image:        "registry.example.com/app:1",
				Project:      strings.Repeat("p", 63),
				Environment:  strings.Repeat("e", 63),
				Service:      strings.Repeat("s", 63),
				Port:         1,
				HealthPath:   "/" + strings.Repeat("a", 255),
				Domains:      sixteenDomains(),
				HasPreDeploy: true,
			},
		},
		{
			name: "single character names and maximum port",
			msg: &agentv1.Deploy{
				AppId:        "app-1",
				ReleaseId:    "rel-1",
				Image:        "registry.example.com/app:1",
				Project:      "p",
				Environment:  "e",
				Service:      "s",
				Port:         65535,
				Domains:      []string{domain253(), strings.Repeat("b", 63) + ".example.com"},
				HasPreDeploy: false,
			},
		},
		{
			name: "no port",
			msg: &agentv1.Deploy{
				AppId:        "app-1",
				ReleaseId:    "rel-1",
				Image:        "registry.example.com/app:1",
				Project:      "web",
				Environment:  "prod",
				Service:      "api",
				Port:         0,
				HasPreDeploy: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			roundTrip(t, agentv1.MessageType_DEPLOY, tt.msg, &agentv1.Deploy{})
		})
	}
}

func TestDeployRejectsBadIdentity(t *testing.T) {
	t.Parallel()

	badValues := []struct {
		name  string
		value string
	}{
		{"empty", ""},
		{"64 bytes", strings.Repeat("a", 64)},
		{"uppercase", "Web"},
		{"underscore", "a_b"},
		{"dot", "a.b"},
		{"space", "a b"},
		{"non-ASCII byte", "aéb"},
		{"leading dash", "-a"},
		{"trailing dash", "a-"},
	}
	fields := []string{"project", "environment", "service"}

	for _, field := range fields {
		for _, bad := range badValues {
			t.Run(field+"/"+bad.name, func(t *testing.T) {
				t.Parallel()
				m := validDeployMessage()
				switch field {
				case "project":
					m.Project = bad.value
				case "environment":
					m.Environment = bad.value
				case "service":
					m.Service = bad.value
				}
				env, err := Wrap(agentv1.MessageType_DEPLOY, "req-1", m)
				if err != nil {
					t.Fatalf("Wrap error = %v", err)
				}
				err = Open(env, &agentv1.Deploy{})
				if !errors.Is(err, ErrMalformed) {
					t.Fatalf("Open(%s=%q): expected ErrMalformed, got %v", field, bad.value, err)
				}
				if !strings.Contains(err.Error(), field) {
					t.Fatalf("Open(%s=%q) error = %q, expected it to name field %q", field, bad.value, err, field)
				}
			})
		}
	}
}

func TestDeployRejectsBadServiceSettings(t *testing.T) {
	t.Parallel()

	seventeenDomains := make([]string, 17)
	for i := range seventeenDomains {
		seventeenDomains[i] = fmt.Sprintf("d%d.example.com", i+1)
	}
	// 63 + 1 + 63 + 1 + 63 + 1 + 62 = 254 bytes.
	domain254 := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 62)

	withDomain := func(domain string) func(*agentv1.Deploy) {
		return func(d *agentv1.Deploy) {
			d.Port = 8080
			d.Domains = []string{domain}
		}
	}
	withHealthPath := func(path string) func(*agentv1.Deploy) {
		return func(d *agentv1.Deploy) {
			d.Port = 8080
			d.HealthPath = path
		}
	}

	tests := []struct {
		name   string
		mutate func(*agentv1.Deploy)
	}{
		{"port 65536", func(d *agentv1.Deploy) { d.Port = 65536 }},
		{"port max uint32", func(d *agentv1.Deploy) { d.Port = math.MaxUint32 }},
		{"health path without port", func(d *agentv1.Deploy) { d.HealthPath = "/healthz" }},
		{"health path without leading slash", withHealthPath("healthz")},
		{"health path 257 bytes", withHealthPath("/" + strings.Repeat("a", 256))},
		{"health path with question mark", withHealthPath("/he?alth")},
		{"health path with percent", withHealthPath("/he%alth")},
		{"health path with space", withHealthPath("/health z")},
		{"health path with hash", withHealthPath("/health#z")},
		{"health path with NUL", withHealthPath("/health\x00z")},
		{"domains without port", func(d *agentv1.Deploy) { d.Domains = []string{"example.com"} }},
		{"17 domains", func(d *agentv1.Deploy) { d.Port = 8080; d.Domains = seventeenDomains }},
		{"duplicate domain", func(d *agentv1.Deploy) {
			d.Port = 8080
			d.Domains = []string{"example.com", "example.com"}
		}},
		{"single label domain", withDomain("localhost")},
		{"wildcard domain", withDomain("*.example.com")},
		{"trailing dot domain", withDomain("example.com.")},
		{"uppercase domain", withDomain("Example.com")},
		{"empty domain entry", withDomain("")},
		{"empty label", withDomain("a..com")},
		{"64 byte label", withDomain(strings.Repeat("a", 64) + ".com")},
		{"label with leading dash", withDomain("-a.com")},
		{"label with trailing dash", withDomain("a-.com")},
		{"254 byte domain", withDomain(domain254)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validDeployMessage()
			tt.mutate(m)
			env, err := Wrap(agentv1.MessageType_DEPLOY, "req-1", m)
			if err != nil {
				t.Fatalf("Wrap(%s) error = %v", tt.name, err)
			}
			if err := Open(env, &agentv1.Deploy{}); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(%s): expected ErrMalformed, got %v", tt.name, err)
			}
		})
	}
}
