package protocol

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// stopRemoveCase is one of the two delete-class commands: its message type, the
// approved protocol number, and how to build its payload and its output.
type stopRemoveCase struct {
	name   string
	mt     agentv1.MessageType
	number int32
	mk     func(id string) proto.Message
	out    func() proto.Message
}

func stopRemoveCases() []stopRemoveCase {
	return []stopRemoveCase{
		{
			name:   "StopApp",
			mt:     agentv1.MessageType_STOP_APP,
			number: 15,
			mk:     func(id string) proto.Message { return &agentv1.StopApp{AppId: id} },
			out:    func() proto.Message { return &agentv1.StopApp{} },
		},
		{
			name:   "RemoveApp",
			mt:     agentv1.MessageType_REMOVE_APP,
			number: 16,
			mk:     func(id string) proto.Message { return &agentv1.RemoveApp{AppId: id} },
			out:    func() proto.Message { return &agentv1.RemoveApp{} },
		},
	}
}

func TestStopAndRemoveAppRoundTrip(t *testing.T) {
	t.Parallel()

	ids := []struct{ name, id string }{
		{"1 byte", "a"},
		{"64 bytes", strings.Repeat("a", 64)},
		{"every allowed punctuation", "a.b_c-D9"},
	}
	for _, tt := range stopRemoveCases() {
		for _, id := range ids {
			t.Run(tt.name+"/"+id.name, func(t *testing.T) {
				t.Parallel()
				msg := tt.mk(id.id)
				roundTrip(t, tt.mt, msg, tt.out())

				env, err := Wrap(tt.mt, "req-1", msg)
				if err != nil {
					t.Fatalf("Wrap(%v) error = %v", tt.mt, err)
				}
				b, err := Marshal(env)
				if err != nil {
					t.Fatalf("Marshal(%v) error = %v", tt.mt, err)
				}
				got, err := Unmarshal(b)
				if err != nil {
					t.Fatalf("Unmarshal(%v) error = %v", tt.mt, err)
				}
				if n := int32(got.GetType()); n != tt.number {
					t.Fatalf("%s envelope type: expected %d, got %d", tt.name, tt.number, n)
				}
			})
		}
	}
}

func TestStopAndRemoveAppRejectMalformed(t *testing.T) {
	t.Parallel()

	bad := []struct{ name, id string }{
		{"empty", ""},
		{"65 bytes", strings.Repeat("a", 65)},
		{"slash", "a/b"},
		{"space", "a b"},
		{"NUL", "a\x00b"},
		{"colon", "a:b"},
		{"non-ASCII byte", "aéb"},
		{"traversal", "../x"},
	}
	for _, tt := range stopRemoveCases() {
		for _, b := range bad {
			t.Run(tt.name+"/"+b.name, func(t *testing.T) {
				t.Parallel()
				env, err := Wrap(tt.mt, "req-1", tt.mk(b.id))
				if err != nil {
					t.Fatalf("Wrap(%v, app_id %q) error = %v", tt.mt, b.id, err)
				}
				err = Open(env, tt.out())
				if !errors.Is(err, ErrMalformed) {
					t.Fatalf("Open(%v, app_id %q): expected ErrMalformed, got %v", tt.mt, b.id, err)
				}
				if !strings.Contains(err.Error(), "app_id") {
					t.Fatalf("Open(%v, app_id %q) error = %q, expected it to name app_id", tt.mt, b.id, err)
				}
			})
		}
	}
}

func TestStopAndRemoveAppProtocolAbuse(t *testing.T) {
	t.Parallel()

	for _, tt := range stopRemoveCases() {
		t.Run("unknown field/"+tt.name, func(t *testing.T) {
			t.Parallel()
			payload, err := proto.Marshal(tt.mk("app-1"))
			if err != nil {
				t.Fatalf("proto.Marshal(%s) error = %v", tt.name, err)
			}
			payload = protowire.AppendTag(payload, 99, protowire.VarintType)
			payload = protowire.AppendVarint(payload, 1)
			env := &agentv1.Envelope{
				Version:   Version,
				Type:      tt.mt,
				RequestId: "r",
				Payload:   payload,
			}
			if err := Open(env, tt.out()); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(%s with unknown field): expected ErrMalformed, got %v", tt.name, err)
			}
		})
	}

	t.Run("type mismatches", func(t *testing.T) {
		t.Parallel()
		stopEnv, err := Wrap(agentv1.MessageType_STOP_APP, "r", &agentv1.StopApp{AppId: "a"})
		if err != nil {
			t.Fatalf("Wrap(STOP_APP) error = %v", err)
		}
		removeEnv, err := Wrap(agentv1.MessageType_REMOVE_APP, "r", &agentv1.RemoveApp{AppId: "a"})
		if err != nil {
			t.Fatalf("Wrap(REMOVE_APP) error = %v", err)
		}

		for _, badOpen := range []struct {
			name string
			env  *agentv1.Envelope
			out  proto.Message
		}{
			{"STOP_APP as RemoveApp", stopEnv, &agentv1.RemoveApp{}},
			{"STOP_APP as Rollback", stopEnv, &agentv1.Rollback{}},
			{"REMOVE_APP as StopApp", removeEnv, &agentv1.StopApp{}},
		} {
			t.Run(badOpen.name, func(t *testing.T) {
				t.Parallel()
				if err := Open(badOpen.env, badOpen.out); !errors.Is(err, ErrTypeMismatch) {
					t.Fatalf("Open(%s): expected ErrTypeMismatch, got %v", badOpen.name, err)
				}
			})
		}

		for _, badWrap := range []struct {
			name string
			mt   agentv1.MessageType
			msg  proto.Message
		}{
			{"STOP_APP with RemoveApp", agentv1.MessageType_STOP_APP, &agentv1.RemoveApp{AppId: "a"}},
			{"REMOVE_APP with StopApp", agentv1.MessageType_REMOVE_APP, &agentv1.StopApp{AppId: "a"}},
			{"STOP_APP with Rollback", agentv1.MessageType_STOP_APP, &agentv1.Rollback{AppId: "a"}},
		} {
			t.Run("wrap "+badWrap.name, func(t *testing.T) {
				t.Parallel()
				if _, err := Wrap(badWrap.mt, "r", badWrap.msg); !errors.Is(err, ErrTypeMismatch) {
					t.Fatalf("Wrap(%s): expected ErrTypeMismatch, got %v", badWrap.name, err)
				}
			})
		}
	})

	for _, tt := range stopRemoveCases() {
		t.Run("error field/"+tt.name, func(t *testing.T) {
			t.Parallel()
			env, err := Wrap(tt.mt, "r", tt.mk("app-1"))
			if err != nil {
				t.Fatalf("Wrap(%s) error = %v", tt.name, err)
			}
			env.Error = &agentv1.Error{Code: agentv1.ErrorCode_INVALID}
			if _, err := Marshal(env); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Marshal(%s with error): expected ErrMalformed, got %v", tt.name, err)
			}
			body, err := proto.MarshalOptions{Deterministic: true}.Marshal(env)
			if err != nil {
				t.Fatalf("proto.Marshal(%s with error) error = %v", tt.name, err)
			}
			if _, err := Unmarshal(mustFrame(t, body)); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Unmarshal(%s with error): expected ErrMalformed, got %v", tt.name, err)
			}
		})
	}

	for _, tt := range stopRemoveCases() {
		t.Run("field shape/"+tt.name, func(t *testing.T) {
			t.Parallel()
			desc := tt.out().ProtoReflect().Descriptor()
			if n := desc.Fields().Len(); n != 1 {
				t.Fatalf("%s field count: expected 1 (app_id only), got %d", tt.name, n)
			}
			f := desc.Fields().Get(0)
			if name := string(f.Name()); name != "app_id" {
				t.Fatalf("%s field 0 name: expected %q, got %q", tt.name, "app_id", name)
			}
			if n := int32(f.Number()); n != 1 {
				t.Fatalf("%s app_id number: expected 1, got %d", tt.name, n)
			}
			if k := f.Kind(); k != protoreflect.StringKind {
				t.Fatalf("%s app_id kind: expected %v, got %v", tt.name, protoreflect.StringKind, k)
			}
			assertNoShellStringFields(t, desc)
		})
	}
}

func FuzzOpenStopAndRemoveApp(f *testing.F) {
	valid, err := proto.Marshal(&agentv1.StopApp{AppId: "app-1"})
	if err != nil {
		f.Fatalf("proto.Marshal(valid StopApp) error = %v", err)
	}
	f.Add(valid)

	maximum, err := proto.Marshal(&agentv1.StopApp{AppId: strings.Repeat("a", 64)})
	if err != nil {
		f.Fatalf("proto.Marshal(64 byte app_id StopApp) error = %v", err)
	}
	f.Add(maximum)
	f.Add([]byte(nil))

	unknown := append([]byte(nil), valid...)
	unknown = protowire.AppendTag(unknown, 99, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	f.Add(unknown)

	withSlash, err := proto.Marshal(&agentv1.StopApp{AppId: "a/b"})
	if err != nil {
		f.Fatalf("proto.Marshal(slash app_id StopApp) error = %v", err)
	}
	f.Add(withSlash)

	f.Fuzz(func(t *testing.T, in []byte) {
		for _, tt := range stopRemoveCases() {
			env := &agentv1.Envelope{
				Version:   Version,
				Type:      tt.mt,
				RequestId: "fuzz",
				Payload:   in,
			}
			first := tt.out()
			if err := Open(env, first); err != nil {
				if !errors.Is(err, ErrMalformed) {
					t.Fatalf("Open(%s, %x) error = %v, expected nil or ErrMalformed", tt.name, in, err)
				}
				continue
			}
			id, ok := first.(interface{ GetAppId() string })
			if !ok {
				t.Fatalf("%s opened into %T, expected a message with GetAppId", tt.name, first)
			}
			if err := validateID("app_id", id.GetAppId()); err != nil {
				t.Fatalf("Open(%s) accepted %x but validateID failed: %v", tt.name, in, err)
			}
			wrapped, err := Wrap(tt.mt, "fuzz", first)
			if err != nil {
				t.Fatalf("Wrap(accepted %s %v) error = %v", tt.name, first, err)
			}
			again := tt.out()
			if err := Open(wrapped, again); err != nil {
				t.Fatalf("Open(%s) after re-wrap error = %v", tt.name, err)
			}
			if !proto.Equal(first, again) {
				t.Fatalf("%s round-trip mismatch: first %v, again %v", tt.name, first, again)
			}
		}
	})
}
