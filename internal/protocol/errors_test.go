package protocol

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

func TestErrorCodes(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Error{}).ProtoReflect().Descriptor())
	fields := (&agentv1.Error{}).ProtoReflect().Descriptor().Fields()
	if fields.Len() != 2 {
		t.Fatalf("Error fields: expected 2 (code, detail), got %d", fields.Len())
	}

	var codes []agentv1.ErrorCode
	vals := agentv1.ErrorCode(0).Descriptor().Values()
	for i := 0; i < vals.Len(); i++ {
		code := agentv1.ErrorCode(vals.Get(i).Number())
		if code == agentv1.ErrorCode_ERROR_CODE_UNSPECIFIED {
			continue
		}
		codes = append(codes, code)
	}
	if len(codes) == 0 {
		t.Fatal("ErrorCode enum has no documented values")
	}
	for _, code := range codes {
		for _, detail := range []string{"", "not found"} {
			t.Run(code.String()+"/"+detail, func(t *testing.T) {
				t.Parallel()
				env := &agentv1.Envelope{
					Version:   Version,
					Type:      agentv1.MessageType_ERROR,
					RequestId: "e1",
					Error:     &agentv1.Error{Code: code, Detail: detail},
				}
				b, err := Marshal(env)
				if err != nil {
					t.Fatalf("Marshal() error = %v", err)
				}
				got, err := Unmarshal(b)
				if err != nil {
					t.Fatalf("Unmarshal() error = %v", err)
				}
				if got.GetError().GetCode() != code {
					t.Errorf("code: expected %v, got %v", code, got.GetError().GetCode())
				}
				if got.GetError().GetDetail() != detail {
					t.Errorf("detail: expected %q, got %q", detail, got.GetError().GetDetail())
				}
			})
		}
	}

	for _, tt := range []struct {
		env  *agentv1.Envelope
		name string
	}{
		{
			name: "unspecified code",
			env: &agentv1.Envelope{
				Version: Version, Type: agentv1.MessageType_ERROR, RequestId: "e1",
				Error: &agentv1.Error{Code: agentv1.ErrorCode_ERROR_CODE_UNSPECIFIED, Detail: "x"},
			},
		},
		{
			name: "unknown numeric code",
			env: &agentv1.Envelope{
				Version: Version, Type: agentv1.MessageType_ERROR, RequestId: "e1",
				Error: &agentv1.Error{Code: agentv1.ErrorCode(999), Detail: "x"},
			},
		},
		{
			name: "detail too long",
			env: &agentv1.Envelope{
				Version: Version, Type: agentv1.MessageType_ERROR, RequestId: "e1",
				Error: &agentv1.Error{Code: agentv1.ErrorCode_INVALID, Detail: strings.Repeat("d", MaxErrorDetailLen+1)},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b, err := Marshal(tt.env)
			if err == nil {
				t.Fatalf("Marshal() error = nil, got %d bytes", len(b))
			}
		})
	}

	// Unknown code on the wire (bypassing Marshal validation) must not panic.
	raw, err := mustRawError(agentv1.ErrorCode(999), "x")
	if err != nil {
		t.Fatalf("setup error = %v", err)
	}
	got, err := Unmarshal(raw)
	if err == nil {
		t.Fatalf("Unmarshal(unknown code) error = nil, got %#v", got)
	}

	t.Run("wrap and open each documented code", func(t *testing.T) {
		t.Parallel()
		for _, code := range codes {
			env, err := Wrap(agentv1.MessageType_ERROR, "e1", &agentv1.Error{Code: code, Detail: "d"})
			if err != nil {
				t.Fatalf("Wrap(%v) error = %v", code, err)
			}
			var opened agentv1.Error
			if err := Open(env, &opened); err != nil {
				t.Fatalf("Open(%v) error = %v", code, err)
			}
			if opened.GetCode() != code || opened.GetDetail() != "d" {
				t.Fatalf("Open(%v) got code=%v detail=%q", code, opened.GetCode(), opened.GetDetail())
			}
		}
	})

	t.Run("open rejects invalid error values", func(t *testing.T) {
		t.Parallel()
		for _, tt := range []struct {
			err  *agentv1.Error
			name string
		}{
			{name: "unspecified", err: &agentv1.Error{Code: agentv1.ErrorCode_ERROR_CODE_UNSPECIFIED, Detail: "x"}},
			{name: "unknown", err: &agentv1.Error{Code: agentv1.ErrorCode(999), Detail: "x"}},
			{name: "detail too long", err: &agentv1.Error{Code: agentv1.ErrorCode_INVALID, Detail: strings.Repeat("d", MaxErrorDetailLen+1)}},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				env, err := Wrap(agentv1.MessageType_ERROR, "e1", tt.err)
				if err == nil {
					t.Fatalf("Wrap(%s) error = nil, env=%#v", tt.name, env)
				}
			})
		}
	})

	t.Run("marshal rejects mismatched error field", func(t *testing.T) {
		t.Parallel()
		if b, err := Marshal(&agentv1.Envelope{Version: Version, Type: agentv1.MessageType_ERROR, RequestId: "e1"}); err == nil {
			t.Fatalf("Marshal(ERROR with no error) error = nil, got %d bytes", len(b))
		}
		if b, err := Marshal(&agentv1.Envelope{
			Version: Version, Type: agentv1.MessageType_HEARTBEAT, RequestId: "e1",
			Error: &agentv1.Error{Code: agentv1.ErrorCode_INVALID},
		}); err == nil {
			t.Fatalf("Marshal(HEARTBEAT with error) error = nil, got %d bytes", len(b))
		}
	})
}

func mustRawError(code agentv1.ErrorCode, detail string) ([]byte, error) {
	body, err := proto.Marshal(&agentv1.Envelope{
		Version:   Version,
		Type:      agentv1.MessageType_ERROR,
		RequestId: "e1",
		Error:     &agentv1.Error{Code: code, Detail: detail},
	})
	if err != nil {
		return nil, err
	}
	return frameBody(body)
}
