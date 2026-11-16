package protocol

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

func TestEnrollRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.EnrollRequest{}).ProtoReflect().Descriptor())
	assertNoShellStringFields(t, (&agentv1.EnrollResponse{}).ProtoReflect().Descriptor())

	req := &agentv1.EnrollRequest{
		TokenId:               "tok-test-1",
		AgentSigningPublicKey: []byte("fake-ed25519-signing-key-bytes!!"),
		AgentAgeRecipient:     []byte("fake-x25519-recipient-bytes!!!!"),
	}
	env, err := Wrap(agentv1.MessageType_ENROLL_REQUEST, "enroll-1", req)
	if err != nil {
		t.Fatalf("Wrap(EnrollRequest) error = %v", err)
	}
	b, err := Marshal(env)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	gotEnv, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	var gotReq agentv1.EnrollRequest
	if openErr := Open(gotEnv, &gotReq); openErr != nil {
		t.Fatalf("Open(EnrollRequest) error = %v", openErr)
	}
	if !proto.Equal(req, &gotReq) {
		t.Fatalf("EnrollRequest: expected %v, got %v", req, &gotReq)
	}

	resp := &agentv1.EnrollResponse{
		ControlPlanePublicKey: []byte("fake-control-plane-public-key!!"),
		Result:                agentv1.EnrollResult_ACCEPTED,
	}
	env, err = Wrap(agentv1.MessageType_ENROLL_RESPONSE, "enroll-1", resp)
	if err != nil {
		t.Fatalf("Wrap(EnrollResponse) error = %v", err)
	}
	b, err = Marshal(env)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	gotEnv, err = Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	var gotResp agentv1.EnrollResponse
	if openErr := Open(gotEnv, &gotResp); openErr != nil {
		t.Fatalf("Open(EnrollResponse) error = %v", openErr)
	}
	if !proto.Equal(resp, &gotResp) {
		t.Fatalf("EnrollResponse: expected %v, got %v", resp, &gotResp)
	}

	for _, result := range []agentv1.EnrollResult{
		agentv1.EnrollResult_ACCEPTED,
		agentv1.EnrollResult_TOKEN_INVALID,
		agentv1.EnrollResult_TOKEN_USED,
	} {
		t.Run(result.String(), func(t *testing.T) {
			t.Parallel()
			msg := &agentv1.EnrollResponse{Result: result}
			env, err := Wrap(agentv1.MessageType_ENROLL_RESPONSE, "enroll-r", msg)
			if err != nil {
				t.Fatalf("Wrap(%v) error = %v", result, err)
			}
			var got agentv1.EnrollResponse
			if err := Open(env, &got); err != nil {
				t.Fatalf("Open(%v) error = %v", result, err)
			}
			if got.GetResult() != result {
				t.Fatalf("result: expected %v, got %v", result, got.GetResult())
			}
		})
	}

	for _, tt := range []struct {
		name   string
		result agentv1.EnrollResult
	}{
		{name: "unspecified", result: agentv1.EnrollResult_ENROLL_RESULT_UNSPECIFIED},
		{name: "unknown", result: agentv1.EnrollResult(99)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env, err := Wrap(agentv1.MessageType_ENROLL_RESPONSE, "enroll-r", &agentv1.EnrollResponse{Result: tt.result})
			if err != nil {
				t.Fatalf("Wrap(%s) error = %v", tt.name, err)
			}
			var got agentv1.EnrollResponse
			if err := Open(env, &got); err == nil {
				t.Fatalf("Open(%s) error = nil, got %#v", tt.name, &got)
			}
		})
	}
}

func assertNoShellStringFields(t *testing.T, desc protoreflect.MessageDescriptor) {
	t.Helper()
	banned := []string{"command", "shell", "cmd", "exec", "script"}
	fields := desc.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.Kind() != protoreflect.StringKind {
			continue
		}
		name := string(f.Name())
		lower := strings.ToLower(name)
		for _, b := range banned {
			if lower == b || strings.Contains(lower, b) {
				t.Errorf("%s string field %q looks like a host command", desc.FullName(), name)
			}
		}
	}
}
