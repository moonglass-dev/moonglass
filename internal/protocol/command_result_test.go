package protocol

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// validCommandResult returns a CommandResult that passes every validator.
func validCommandResult() *agentv1.CommandResult {
	return &agentv1.CommandResult{
		Outcome: agentv1.CommandOutcome_SUCCEEDED,
		App:     &agentv1.AppState{Phase: agentv1.ReleasePhase_READY},
	}
}

func TestCommandResultRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.CommandResult{}).ProtoReflect().Descriptor())
	assertNoShellStringFields(t, (&agentv1.AppState{}).ProtoReflect().Descriptor())

	imageID := "sha256:" + strings.Repeat("a", 64)
	repoDigest := "registry.example.com/app@sha256:" + strings.Repeat("b", 64)

	tests := []struct {
		msg  *agentv1.CommandResult
		name string
	}{
		{
			name: "succeeded",
			msg: &agentv1.CommandResult{
				Outcome: agentv1.CommandOutcome_SUCCEEDED,
				App: &agentv1.AppState{
					CurrentReleaseId:   "rel-2",
					PreviousReleaseId:  "rel-1",
					CandidateReleaseId: "rel-3",
					Phase:              agentv1.ReleasePhase_READY,
				},
				ImageId:    imageID,
				RepoDigest: repoDigest,
			},
		},
		{
			name: "failed",
			msg: &agentv1.CommandResult{
				Outcome: agentv1.CommandOutcome_FAILED,
				Detail:  strings.Repeat("d", 4096),
				App: &agentv1.AppState{
					CandidateReleaseId: "rel-3",
					Phase:              agentv1.ReleasePhase_PENDING,
				},
				ImageId: imageID,
			},
		},
		{
			name: "rejected",
			msg: &agentv1.CommandResult{
				Outcome: agentv1.CommandOutcome_REJECTED,
				Detail:  "identity mismatch: app-1 is bound to web/prod/api",
				App:     &agentv1.AppState{Phase: agentv1.ReleasePhase_NONE},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			roundTrip(t, agentv1.MessageType_COMMAND_RESULT, tt.msg, &agentv1.CommandResult{})
		})
	}
}

func TestCommandResultRejectsInvalid(t *testing.T) {
	t.Parallel()

	lowerHex := strings.Repeat("a", 64)
	upperHex := strings.Repeat("A", 64)

	tests := []struct {
		name   string
		mutate func(*agentv1.CommandResult)
	}{
		{"outcome unspecified", func(r *agentv1.CommandResult) {
			r.Outcome = agentv1.CommandOutcome_COMMAND_OUTCOME_UNSPECIFIED
		}},
		{"outcome unknown", func(r *agentv1.CommandResult) {
			r.Outcome = agentv1.CommandOutcome(99)
		}},
		{"detail 4097 bytes", func(r *agentv1.CommandResult) {
			r.Outcome = agentv1.CommandOutcome_FAILED
			r.Detail = strings.Repeat("d", 4097)
		}},
		{"detail on success", func(r *agentv1.CommandResult) {
			r.Detail = "not empty"
		}},
		{"missing app", func(r *agentv1.CommandResult) {
			r.App = nil
		}},
		{"phase unspecified", func(r *agentv1.CommandResult) {
			r.App = &agentv1.AppState{Phase: agentv1.ReleasePhase_RELEASE_PHASE_UNSPECIFIED}
		}},
		{"phase unknown", func(r *agentv1.CommandResult) {
			r.App = &agentv1.AppState{Phase: agentv1.ReleasePhase(99)}
		}},
		{"current release id with slash", func(r *agentv1.CommandResult) {
			r.App = &agentv1.AppState{CurrentReleaseId: "a/b", Phase: agentv1.ReleasePhase_READY}
		}},
		{"previous release id with slash", func(r *agentv1.CommandResult) {
			r.App = &agentv1.AppState{PreviousReleaseId: "a/b", Phase: agentv1.ReleasePhase_READY}
		}},
		{"candidate release id with slash", func(r *agentv1.CommandResult) {
			r.App = &agentv1.AppState{CandidateReleaseId: "a/b", Phase: agentv1.ReleasePhase_READY}
		}},
		{"image id without prefix", func(r *agentv1.CommandResult) {
			r.ImageId = lowerHex
		}},
		{"image id 63 hex characters", func(r *agentv1.CommandResult) {
			r.ImageId = "sha256:" + strings.Repeat("a", 63)
		}},
		{"image id 65 hex characters", func(r *agentv1.CommandResult) {
			r.ImageId = "sha256:" + strings.Repeat("a", 65)
		}},
		{"image id uppercase hex", func(r *agentv1.CommandResult) {
			r.ImageId = "sha256:" + upperHex
		}},
		{"repo digest tag", func(r *agentv1.CommandResult) {
			r.RepoDigest = "repo:tag"
		}},
		{"repo digest empty repository", func(r *agentv1.CommandResult) {
			r.RepoDigest = "@sha256:" + lowerHex
		}},
		{"repo digest uppercase hex", func(r *agentv1.CommandResult) {
			r.RepoDigest = "repo@sha256:" + upperHex
		}},
		{"repo digest whitespace", func(r *agentv1.CommandResult) {
			r.RepoDigest = "repo name@sha256:" + lowerHex
		}},
		{"repo digest leading dash", func(r *agentv1.CommandResult) {
			r.RepoDigest = "-repo@sha256:" + lowerHex
		}},
		{"repo digest 513 bytes", func(r *agentv1.CommandResult) {
			r.RepoDigest = strings.Repeat("a", 441) + "@sha256:" + lowerHex
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := validCommandResult()
			tt.mutate(m)
			openRejected(t, agentv1.MessageType_COMMAND_RESULT, m, &agentv1.CommandResult{})
		})
	}
}

func TestCommandResultProtocolAbuse(t *testing.T) {
	t.Parallel()

	t.Run("unknown top-level field", func(t *testing.T) {
		t.Parallel()
		payload, err := proto.Marshal(validCommandResult())
		if err != nil {
			t.Fatalf("proto.Marshal() error = %v", err)
		}
		payload = protowire.AppendTag(payload, 99, protowire.VarintType)
		payload = protowire.AppendVarint(payload, 1)
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_COMMAND_RESULT,
			RequestId: "r",
			Payload:   payload,
		}
		if err := Open(env, &agentv1.CommandResult{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open with unknown top-level field: expected ErrMalformed, got %v", err)
		}
	})

	t.Run("unknown nested AppState field", func(t *testing.T) {
		t.Parallel()
		app, err := proto.Marshal(&agentv1.AppState{Phase: agentv1.ReleasePhase_READY})
		if err != nil {
			t.Fatalf("proto.Marshal(AppState) error = %v", err)
		}
		app = protowire.AppendTag(app, 99, protowire.VarintType)
		app = protowire.AppendVarint(app, 1)
		payload, err := proto.Marshal(&agentv1.CommandResult{Outcome: agentv1.CommandOutcome_SUCCEEDED})
		if err != nil {
			t.Fatalf("proto.Marshal(CommandResult) error = %v", err)
		}
		// app is field 3 of CommandResult.
		payload = protowire.AppendTag(payload, 3, protowire.BytesType)
		payload = protowire.AppendBytes(payload, app)
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_COMMAND_RESULT,
			RequestId: "r",
			Payload:   payload,
		}
		if err := Open(env, &agentv1.CommandResult{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open with unknown AppState field: expected ErrMalformed, got %v", err)
		}
	})

	t.Run("unknown Deploy field", func(t *testing.T) {
		t.Parallel()
		payload, err := proto.Marshal(validDeployMessage())
		if err != nil {
			t.Fatalf("proto.Marshal(Deploy) error = %v", err)
		}
		payload = protowire.AppendTag(payload, 99, protowire.VarintType)
		payload = protowire.AppendVarint(payload, 1)
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_DEPLOY,
			RequestId: "r",
			Payload:   payload,
		}
		if err := Open(env, &agentv1.Deploy{}); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open(Deploy) with unknown field: expected ErrMalformed, got %v", err)
		}
	})

	t.Run("type mismatches", func(t *testing.T) {
		t.Parallel()
		resultEnv, err := Wrap(agentv1.MessageType_COMMAND_RESULT, "r", validCommandResult())
		if err != nil {
			t.Fatalf("Wrap(COMMAND_RESULT) error = %v", err)
		}
		if err := Open(resultEnv, &agentv1.Deploy{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Open(COMMAND_RESULT as Deploy): expected ErrTypeMismatch, got %v", err)
		}

		deployEnv, err := Wrap(agentv1.MessageType_DEPLOY, "r", validDeployMessage())
		if err != nil {
			t.Fatalf("Wrap(DEPLOY) error = %v", err)
		}
		if err := Open(deployEnv, &agentv1.CommandResult{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Open(DEPLOY as CommandResult): expected ErrTypeMismatch, got %v", err)
		}

		if _, err := Wrap(agentv1.MessageType_COMMAND_RESULT, "r", validDeployMessage()); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Wrap(COMMAND_RESULT, Deploy): expected ErrTypeMismatch, got %v", err)
		}
	})

	t.Run("error field on command result", func(t *testing.T) {
		t.Parallel()
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_COMMAND_RESULT,
			RequestId: "r",
			Error:     &agentv1.Error{Code: agentv1.ErrorCode_INVALID},
		}
		if _, err := Marshal(env); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Marshal(COMMAND_RESULT with error): expected ErrMalformed, got %v", err)
		}
		body, err := proto.Marshal(env)
		if err != nil {
			t.Fatalf("proto.Marshal() error = %v", err)
		}
		if _, err := Unmarshal(mustFrame(t, body)); !errors.Is(err, ErrMalformed) {
			t.Fatalf("Unmarshal(COMMAND_RESULT with error): expected ErrMalformed, got %v", err)
		}
	})

	t.Run("oversized command result", func(t *testing.T) {
		t.Parallel()
		big := &agentv1.CommandResult{
			Outcome: agentv1.CommandOutcome_FAILED,
			Detail:  strings.Repeat("x", MaxEnvelopeSize),
			App:     &agentv1.AppState{Phase: agentv1.ReleasePhase_NONE},
		}
		env, err := Wrap(agentv1.MessageType_COMMAND_RESULT, "r", big)
		if err != nil {
			t.Fatalf("Wrap(oversized CommandResult) error = %v", err)
		}
		if b, err := Marshal(env); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Marshal(oversized CommandResult): expected ErrTooLarge, got %v (encoded %d bytes)", err, len(b))
		}
	})
}
