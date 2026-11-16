package protocol

import (
	"bytes"
	"testing"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

func FuzzEnvelopeDecode(f *testing.F) {
	valid, err := Marshal(&agentv1.Envelope{
		Version:   Version,
		Type:      agentv1.MessageType_HEARTBEAT,
		RequestId: "seed",
		Payload:   []byte{1, 2, 3},
	})
	if err != nil {
		f.Fatalf("seed Marshal() error = %v", err)
	}
	f.Add(valid)
	f.Add([]byte(nil))
	f.Add([]byte{})
	f.Add(valid[:len(valid)/2])
	f.Add(append(append([]byte{}, valid...), 0xff))
	f.Add(bytes.Repeat([]byte{0x00}, 16))

	f.Fuzz(func(t *testing.T, in []byte) {
		env, err := Unmarshal(in)
		if err != nil {
			return
		}
		out, err := Marshal(env)
		if err != nil {
			t.Fatalf("Marshal of a successfully unmarshaled envelope failed: %v", err)
		}
		again, err := Unmarshal(out)
		if err != nil {
			t.Fatalf("Unmarshal after remarchal failed: %v", err)
		}
		if env.GetVersion() != again.GetVersion() || env.GetType() != again.GetType() || env.GetRequestId() != again.GetRequestId() || !bytes.Equal(env.GetPayload(), again.GetPayload()) {
			t.Fatalf("round-trip mismatch")
		}
		if len(in) > MaxEnvelopeSize {
			t.Fatalf("Unmarshal accepted input of length %d, max is %d", len(in), MaxEnvelopeSize)
		}
	})
}
