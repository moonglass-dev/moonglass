package protocol

import (
	"errors"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

func FuzzOpenDeploy(f *testing.F) {
	full, err := proto.Marshal(&agentv1.Deploy{
		AppId:        "app-1",
		ReleaseId:    "rel-1",
		Image:        "registry.example.com/app:1",
		Project:      strings.Repeat("p", 63),
		Environment:  "prod",
		Service:      "api",
		Port:         1,
		HealthPath:   "/healthz",
		Domains:      sixteenDomains(),
		HasPreDeploy: true,
	})
	if err != nil {
		f.Fatalf("proto.Marshal(full Deploy) error = %v", err)
	}
	f.Add(full)

	minimal, err := proto.Marshal(validDeployMessage())
	if err != nil {
		f.Fatalf("proto.Marshal(minimal Deploy) error = %v", err)
	}
	f.Add(minimal)
	f.Add([]byte(nil))

	unknown := append([]byte(nil), minimal...)
	unknown = protowire.AppendTag(unknown, 99, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	f.Add(unknown)

	withEmptyDomain, err := proto.Marshal(&agentv1.Deploy{
		AppId:       "app-1",
		ReleaseId:   "rel-1",
		Image:       "registry.example.com/app:1",
		Project:     "web",
		Environment: "prod",
		Service:     "api",
		Port:        8080,
		Domains:     []string{""},
	})
	if err != nil {
		f.Fatalf("proto.Marshal(empty domain Deploy) error = %v", err)
	}
	f.Add(withEmptyDomain)

	f.Fuzz(func(t *testing.T, in []byte) {
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_DEPLOY,
			RequestId: "fuzz",
			Payload:   in,
		}
		var first agentv1.Deploy
		if err := Open(env, &first); err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(DEPLOY, %x) error = %v, expected nil or ErrMalformed", in, err)
			}
			return
		}
		if err := validateDeploy(&first); err != nil {
			t.Fatalf("Open accepted DEPLOY payload %x but validateDeploy failed: %v", in, err)
		}
		wrapped, err := Wrap(agentv1.MessageType_DEPLOY, "fuzz", &first)
		if err != nil {
			t.Fatalf("Wrap(accepted Deploy %v) error = %v", &first, err)
		}
		var again agentv1.Deploy
		if err := Open(wrapped, &again); err != nil {
			t.Fatalf("Open after re-wrap error = %v", err)
		}
		if !proto.Equal(&first, &again) {
			t.Fatalf("round-trip mismatch: first %v, again %v", &first, &again)
		}
	})
}

func FuzzOpenCommandResult(f *testing.F) {
	base := &agentv1.CommandResult{
		Outcome: agentv1.CommandOutcome_SUCCEEDED,
		App: &agentv1.AppState{
			CurrentReleaseId:  "rel-1",
			PreviousReleaseId: "rel-0",
			Phase:             agentv1.ReleasePhase_READY,
		},
		ImageId:    "sha256:" + strings.Repeat("a", 64),
		RepoDigest: "registry.example.com/app@sha256:" + strings.Repeat("b", 64),
	}
	for _, outcome := range []agentv1.CommandOutcome{
		agentv1.CommandOutcome_SUCCEEDED,
		agentv1.CommandOutcome_FAILED,
		agentv1.CommandOutcome_REJECTED,
	} {
		m := proto.Clone(base).(*agentv1.CommandResult)
		m.Outcome = outcome
		if outcome != agentv1.CommandOutcome_SUCCEEDED {
			m.Detail = "command did not finish"
		}
		payload, err := proto.Marshal(m)
		if err != nil {
			f.Fatalf("proto.Marshal(%v CommandResult) error = %v", outcome, err)
		}
		f.Add(payload)
	}
	f.Add([]byte(nil))

	// A valid result whose nested AppState carries an unknown field.
	app, err := proto.Marshal(&agentv1.AppState{Phase: agentv1.ReleasePhase_READY})
	if err != nil {
		f.Fatalf("proto.Marshal(AppState) error = %v", err)
	}
	app = protowire.AppendTag(app, 99, protowire.VarintType)
	app = protowire.AppendVarint(app, 1)
	nested, err := proto.Marshal(&agentv1.CommandResult{Outcome: agentv1.CommandOutcome_SUCCEEDED})
	if err != nil {
		f.Fatalf("proto.Marshal(CommandResult) error = %v", err)
	}
	// app is field 3 of CommandResult.
	nested = protowire.AppendTag(nested, 3, protowire.BytesType)
	nested = protowire.AppendBytes(nested, app)
	f.Add(nested)

	f.Fuzz(func(t *testing.T, in []byte) {
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_COMMAND_RESULT,
			RequestId: "fuzz",
			Payload:   in,
		}
		var first agentv1.CommandResult
		if err := Open(env, &first); err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(COMMAND_RESULT, %x) error = %v, expected nil or ErrMalformed", in, err)
			}
			return
		}
		if err := validateCommandResult(&first); err != nil {
			t.Fatalf("Open accepted COMMAND_RESULT payload %x but validateCommandResult failed: %v", in, err)
		}
		wrapped, err := Wrap(agentv1.MessageType_COMMAND_RESULT, "fuzz", &first)
		if err != nil {
			t.Fatalf("Wrap(accepted CommandResult %v) error = %v", &first, err)
		}
		var again agentv1.CommandResult
		if err := Open(wrapped, &again); err != nil {
			t.Fatalf("Open after re-wrap error = %v", err)
		}
		if !proto.Equal(&first, &again) {
			t.Fatalf("round-trip mismatch: first %v, again %v", &first, &again)
		}
	})
}
