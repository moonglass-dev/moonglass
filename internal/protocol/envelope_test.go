package protocol

import (
	"bytes"
	"errors"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		requestID string
		payload   []byte
		msgType   agentv1.MessageType
	}{
		{name: "enroll request empty payload", msgType: agentv1.MessageType_ENROLL_REQUEST, requestID: "req-1", payload: nil},
		{name: "enroll request payload", msgType: agentv1.MessageType_ENROLL_REQUEST, requestID: "req-2", payload: []byte{0x01, 0x02, 0x03}},
		{name: "enroll response", msgType: agentv1.MessageType_ENROLL_RESPONSE, requestID: "req-3", payload: []byte("ok")},
		{name: "heartbeat", msgType: agentv1.MessageType_HEARTBEAT, requestID: "hb-1", payload: []byte{0xff}},
		{name: "heartbeat ack", msgType: agentv1.MessageType_HEARTBEAT_ACK, requestID: "hb-1", payload: []byte{}},
		{name: "error type", msgType: agentv1.MessageType_ERROR, requestID: "err-1", payload: nil},
		{name: "max request id", msgType: agentv1.MessageType_HEARTBEAT, requestID: string(bytes.Repeat([]byte("a"), MaxRequestIDLen)), payload: []byte{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := &agentv1.Envelope{
				Version:   Version,
				Type:      tt.msgType,
				RequestId: tt.requestID,
				Payload:   append([]byte(nil), tt.payload...),
			}
			if tt.msgType == agentv1.MessageType_ERROR {
				env.Error = &agentv1.Error{Code: agentv1.ErrorCode_INVALID, Detail: "x"}
			}
			b, err := Marshal(env)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			b2, err := Marshal(env)
			if err != nil {
				t.Fatalf("second Marshal() error = %v", err)
			}
			if !bytes.Equal(b, b2) {
				t.Fatalf("Marshal is not deterministic:\n first  %x\n second %x", b, b2)
			}
			got, err := Unmarshal(b)
			if err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if got.GetVersion() != Version {
				t.Errorf("version: expected %d, got %d", Version, got.GetVersion())
			}
			if got.GetType() != tt.msgType {
				t.Errorf("type: expected %v, got %v", tt.msgType, got.GetType())
			}
			if got.GetRequestId() != tt.requestID {
				t.Errorf("request id: expected %q, got %q", tt.requestID, got.GetRequestId())
			}
			if !bytes.Equal(got.GetPayload(), tt.payload) {
				t.Errorf("payload: expected %x, got %x", tt.payload, got.GetPayload())
			}
		})
	}
}

func TestEnvelopeRejectsMalformed(t *testing.T) {
	t.Parallel()
	valid, err := Marshal(&agentv1.Envelope{
		Version:   Version,
		Type:      agentv1.MessageType_HEARTBEAT,
		RequestId: "ok",
		Payload:   []byte{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("setup Marshal() error = %v", err)
	}

	tests := []struct {
		name  string
		input []byte
	}{
		{name: "nil", input: nil},
		{name: "empty", input: []byte{}},
		{name: "trailing garbage", input: append(append([]byte{}, valid...), 0x00)},
		{name: "unknown field", input: appendUnknownField(t, valid)},
		{name: "wrong version", input: mustMarshal(t, &agentv1.Envelope{Version: 2, Type: agentv1.MessageType_HEARTBEAT, RequestId: "ok"})},
		{name: "unspecified type", input: mustMarshal(t, &agentv1.Envelope{Version: Version, Type: agentv1.MessageType_MESSAGE_TYPE_UNSPECIFIED, RequestId: "ok"})},
		{name: "unknown type", input: mustMarshal(t, &agentv1.Envelope{Version: Version, Type: agentv1.MessageType(99), RequestId: "ok"})},
		{name: "empty request id", input: mustMarshal(t, &agentv1.Envelope{Version: Version, Type: agentv1.MessageType_HEARTBEAT, RequestId: ""})},
		{name: "over-long request id", input: mustMarshal(t, &agentv1.Envelope{Version: Version, Type: agentv1.MessageType_HEARTBEAT, RequestId: string(bytes.Repeat([]byte("b"), MaxRequestIDLen+1))})},
		{name: "error set on heartbeat", input: mustMarshal(t, &agentv1.Envelope{
			Version: Version, Type: agentv1.MessageType_HEARTBEAT, RequestId: "ok",
			Error: &agentv1.Error{Code: agentv1.ErrorCode_INVALID},
		})},
		{name: "error type missing error", input: mustMarshal(t, &agentv1.Envelope{Version: Version, Type: agentv1.MessageType_ERROR, RequestId: "e1"})},
		{name: "unknown field in error", input: mustMarshalUnknownErrorField(t)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Unmarshal(tt.input)
			if err == nil {
				t.Fatalf("Unmarshal() error = nil, got envelope %#v", got)
			}
			if got != nil {
				t.Fatalf("Unmarshal() envelope = %#v, expected nil", got)
			}
		})
	}

	for i := 1; i < len(valid); i++ {
		t.Run("truncation", func(t *testing.T) {
			t.Parallel()
			got, err := Unmarshal(valid[:i])
			if err == nil {
				t.Fatalf("Unmarshal(valid[:%d]) error = nil, got %#v", i, got)
			}
			if got != nil {
				t.Fatalf("Unmarshal(valid[:%d]) envelope = %#v, expected nil", i, got)
			}
		})
	}
}

func TestEnvelopeRejectsOversized(t *testing.T) {
	t.Parallel()
	tooBig := bytes.Repeat([]byte{0x00}, MaxEnvelopeSize+1)
	got, err := Unmarshal(tooBig)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Unmarshal(MaxEnvelopeSize+1) error = %v, expected %v", err, ErrTooLarge)
	}
	if got != nil {
		t.Fatalf("Unmarshal(MaxEnvelopeSize+1) envelope = %#v, expected nil", got)
	}

	exact := make([]byte, MaxEnvelopeSize)
	_, err = Unmarshal(exact)
	if errors.Is(err, ErrTooLarge) {
		t.Fatalf("Unmarshal(MaxEnvelopeSize) was rejected as too large; size check should pass")
	}

	// A payload that would encode past the limit must be refused by Marshal.
	huge := &agentv1.Envelope{
		Version:   Version,
		Type:      agentv1.MessageType_HEARTBEAT,
		RequestId: "ok",
		Payload:   bytes.Repeat([]byte{0x01}, MaxEnvelopeSize),
	}
	b, err := Marshal(huge)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Marshal(oversized envelope) error = %v, expected %v (got %d bytes)", err, ErrTooLarge, len(b))
	}
	if b != nil {
		t.Fatalf("Marshal(oversized envelope) bytes = %d, expected nil", len(b))
	}
}

func mustFrame(t *testing.T, body []byte) []byte {
	t.Helper()
	out, err := frameBody(body)
	if err != nil {
		t.Fatalf("frameBody() error = %v", err)
	}
	return out
}

func mustMarshal(t *testing.T, env *agentv1.Envelope) []byte {
	t.Helper()
	b, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("proto.Marshal() error = %v", err)
	}
	return mustFrame(t, b)
}

func appendUnknownField(t *testing.T, valid []byte) []byte {
	t.Helper()
	if len(valid) < 4 {
		t.Fatalf("valid envelope too short")
	}
	body := append(append([]byte{}, valid[4:]...), 0x78, 0x01)
	return mustFrame(t, body)
}

func mustMarshalUnknownErrorField(t *testing.T) []byte {
	t.Helper()
	errBody, err := proto.Marshal(&agentv1.Error{Code: agentv1.ErrorCode_INVALID, Detail: "x"})
	if err != nil {
		t.Fatalf("proto.Marshal(Error) error = %v", err)
	}
	errBody = protowire.AppendTag(errBody, 15, protowire.VarintType)
	errBody = protowire.AppendVarint(errBody, 1)
	env := &agentv1.Envelope{
		Version:   Version,
		Type:      agentv1.MessageType_ERROR,
		RequestId: "x",
	}
	body, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("proto.Marshal(Envelope) error = %v", err)
	}
	body = protowire.AppendTag(body, 5, protowire.BytesType)
	body = protowire.AppendBytes(body, errBody)
	return mustFrame(t, body)
}

func TestWrapRejectsTypeMismatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  proto.Message
		name string
		typ  agentv1.MessageType
	}{
		{name: "enroll labeled heartbeat", typ: agentv1.MessageType_HEARTBEAT, msg: &agentv1.EnrollRequest{TokenId: "tok"}},
		{name: "heartbeat labeled enroll", typ: agentv1.MessageType_ENROLL_REQUEST, msg: &agentv1.Heartbeat{AgentVersion: "dev"}},
		{name: "error labeled heartbeat", typ: agentv1.MessageType_HEARTBEAT, msg: &agentv1.Error{Code: agentv1.ErrorCode_INVALID}},
		{name: "heartbeat labeled error", typ: agentv1.MessageType_ERROR, msg: &agentv1.Heartbeat{AgentVersion: "dev"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env, err := Wrap(tt.typ, "x", tt.msg)
			if !errors.Is(err, ErrTypeMismatch) {
				t.Fatalf("Wrap() error = %v, expected %v", err, ErrTypeMismatch)
			}
			if env != nil {
				t.Fatalf("Wrap() envelope = %#v, expected nil", env)
			}
		})
	}
}

func TestEnvelopeRoundTripEveryType(t *testing.T) {
	t.Parallel()
	vals := agentv1.MessageType(0).Descriptor().Values()
	for i := 0; i < vals.Len(); i++ {
		tnum := agentv1.MessageType(vals.Get(i).Number())
		name := string(vals.Get(i).Name())
		if tnum == agentv1.MessageType_MESSAGE_TYPE_UNSPECIFIED {
			continue
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := &agentv1.Envelope{Version: Version, Type: tnum, RequestId: "all-types"}
			if tnum == agentv1.MessageType_ERROR {
				env.Error = &agentv1.Error{Code: agentv1.ErrorCode_INVALID}
			}
			b, err := Marshal(env)
			if err != nil {
				t.Fatalf("Marshal(%s) error = %v", name, err)
			}
			got, err := Unmarshal(b)
			if err != nil {
				t.Fatalf("Unmarshal(%s) error = %v", name, err)
			}
			if got.GetType() != tnum {
				t.Fatalf("type: expected %v, got %v", tnum, got.GetType())
			}
		})
	}
}
