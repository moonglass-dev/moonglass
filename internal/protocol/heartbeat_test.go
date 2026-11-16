package protocol

import (
	"testing"

	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

func TestHeartbeatRoundTrip(t *testing.T) {
	t.Parallel()
	hb := &agentv1.Heartbeat{AgentVersion: "dev"}
	env, err := Wrap(agentv1.MessageType_HEARTBEAT, "hb-9", hb)
	if err != nil {
		t.Fatalf("Wrap(Heartbeat) error = %v", err)
	}
	b, err := Marshal(env)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	gotEnv, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if gotEnv.GetType() != agentv1.MessageType_HEARTBEAT {
		t.Fatalf("type: expected %v, got %v", agentv1.MessageType_HEARTBEAT, gotEnv.GetType())
	}
	var got agentv1.Heartbeat
	if openErr := Open(gotEnv, &got); openErr != nil {
		t.Fatalf("Open(Heartbeat) error = %v", openErr)
	}
	if !proto.Equal(hb, &got) {
		t.Fatalf("Heartbeat: expected %v, got %v", hb, &got)
	}

	ack := &agentv1.HeartbeatAck{}
	env, err = Wrap(agentv1.MessageType_HEARTBEAT_ACK, "hb-9", ack)
	if err != nil {
		t.Fatalf("Wrap(HeartbeatAck) error = %v", err)
	}
	b, err = Marshal(env)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	gotEnv, err = Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if gotEnv.GetType() != agentv1.MessageType_HEARTBEAT_ACK {
		t.Fatalf("type: expected %v, got %v", agentv1.MessageType_HEARTBEAT_ACK, gotEnv.GetType())
	}
	var gotAck agentv1.HeartbeatAck
	if openErr := Open(gotEnv, &gotAck); openErr != nil {
		t.Fatalf("Open(HeartbeatAck) error = %v", openErr)
	}

	var wrong agentv1.EnrollRequest
	if openErr := Open(gotEnv, &wrong); openErr == nil {
		t.Fatalf("Open(HeartbeatAck envelope as EnrollRequest) error = nil, expected type mismatch")
	}
}
