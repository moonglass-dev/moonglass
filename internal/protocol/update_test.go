package protocol

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

const (
	updateVersion   = "v1.2.3"
	updateRequestID = "u0123456789abcdef0123456789abcdef"
)

// updateReportPair is one outcome and one reason that belong together.
type updateReportPair struct {
	outcome agentv1.UpdateOutcome
	reason  agentv1.UpdateReason
}

// validUpdateReportPairs returns the outcome-reason binding of docs/specs/update.md
// §6, written here independently of the code's own table.
func validUpdateReportPairs() []updateReportPair {
	return []updateReportPair{
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED, agentv1.UpdateReason_UPDATE_REASON_NOT_NEWER},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED, agentv1.UpdateReason_UPDATE_REASON_NOT_ON_CHANNEL},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED, agentv1.UpdateReason_UPDATE_REASON_REVERTED_BEFORE},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED, agentv1.UpdateReason_UPDATE_REASON_TRIAL_IN_PROGRESS},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED, agentv1.UpdateReason_UPDATE_REASON_POINTER},

		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_DISK},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_DOWNLOAD},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_TOO_LARGE},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_SIGNATURE},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_CHECKSUM},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_POINTER_MISMATCH},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_WRITE},

		{agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_PASSED, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED},

		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateReason_UPDATE_REASON_RESTARTED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateReason_UPDATE_REASON_NOT_CONNECTED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateReason_UPDATE_REASON_EXE_MISMATCH},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateReason_UPDATE_REASON_PROXY},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateReason_UPDATE_REASON_DEADLINE},

		{agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED, agentv1.UpdateReason_UPDATE_REASON_RESTARTED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED, agentv1.UpdateReason_UPDATE_REASON_NOT_CONNECTED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED, agentv1.UpdateReason_UPDATE_REASON_EXE_MISMATCH},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED, agentv1.UpdateReason_UPDATE_REASON_PROXY},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED, agentv1.UpdateReason_UPDATE_REASON_DEADLINE},
	}
}

// updateReportFor returns a report with a valid version, the sha256 shape its
// outcome needs and proxy_restarted false.
func updateReportFor(o agentv1.UpdateOutcome, r agentv1.UpdateReason) *agentv1.UpdateReport {
	m := &agentv1.UpdateReport{Version: updateVersion, Outcome: o, Reason: r}
	if o != agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED {
		m.Sha256 = strings.Repeat("a", 64)
	}
	return m
}

// openUpdate frames payload in an envelope of type typ by hand, then runs
// Marshal, Unmarshal and Open. Wrap is not used because it does not validate
// payloads; building the envelope directly also keeps the failure at Open.
func openUpdate(t *testing.T, typ agentv1.MessageType, payload, out proto.Message) error {
	t.Helper()
	body, err := proto.Marshal(payload)
	if err != nil {
		t.Fatalf("proto.Marshal(%T) error = %v", payload, err)
	}
	return openUpdatePayload(t, typ, body, out)
}

// openUpdatePayload is openUpdate for a payload that is already encoded.
func openUpdatePayload(t *testing.T, typ agentv1.MessageType, payload []byte, out proto.Message) error {
	t.Helper()
	env := &agentv1.Envelope{
		Version:   Version,
		Type:      typ,
		RequestId: updateRequestID,
		Payload:   payload,
	}
	b, err := Marshal(env)
	if err != nil {
		t.Fatalf("Marshal(%v) error = %v", typ, err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatalf("Unmarshal(%v) error = %v", typ, err)
	}
	return Open(got, out)
}

// expectUpdateRejected expects err to wrap ErrMalformed and to name field.
func expectUpdateRejected(t *testing.T, name string, err error, field string) {
	t.Helper()
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("%s: Open error = %v, expected %v", name, err, ErrMalformed)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("%s: Open error = %q, expected it to name field %q", name, err, field)
	}
}

// appendUnknownPayload appends one field no message declares.
func appendUnknownPayload(payload []byte) []byte {
	out := append([]byte(nil), payload...)
	out = protowire.AppendTag(out, 99, protowire.BytesType)
	out = protowire.AppendBytes(out, []byte("x"))
	return out
}

func TestUpdateOfferRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.UpdateOffer{}).ProtoReflect().Descriptor())

	desc := (&agentv1.UpdateOffer{}).ProtoReflect().Descriptor()
	if got := desc.Fields().Len(); got != 1 {
		t.Fatalf("UpdateOffer field count: expected 1, got %d", got)
	}
	field := desc.Fields().Get(0)
	if got := string(field.Name()); got != "version" {
		t.Fatalf("UpdateOffer field 1: expected name \"version\", got %q", got)
	}
	if got := field.Kind(); got != protoreflect.StringKind {
		t.Fatalf("UpdateOffer.version kind: expected %v, got %v", protoreflect.StringKind, got)
	}

	tests := []struct {
		name    string
		version string
	}{
		{"zero", "v0.0.0"},
		{"small", "v1.2.3"},
		{"multi digit", "v10.20.30"},
		{"wider than uint64", "v18446744073709551616.0.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := &agentv1.UpdateOffer{Version: tt.version}
			var got agentv1.UpdateOffer
			roundTrip(t, agentv1.MessageType_UPDATE_OFFER, msg, &got)
			if got.GetVersion() != tt.version {
				t.Fatalf("version: expected %q, got %q", tt.version, got.GetVersion())
			}
		})
	}
}

func TestUpdateOfferRejectsInvalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
	}{
		{"empty", ""},
		{"no v", "1.2.3"},
		{"uppercase V", "V1.2.3"},
		{"two identifiers", "v1.2"},
		{"four identifiers", "v1.2.3.4"},
		{"major leading zero", "v01.2.3"},
		{"minor leading zero", "v1.02.3"},
		{"patch leading zero", "v1.2.03"},
		{"prerelease", "v1.2.3-rc.1"},
		{"build metadata", "v1.2.3+build"},
		{"empty minor", "v1..3"},
		{"empty major", "v.1.2"},
		{"empty patch", "v1.2."},
		{"negative", "v-1.2.3"},
		{"leading space", " v1.2.3"},
		{"trailing newline", "v1.2.3\n"},
		{"trailing space", "v1.2.3 "},
		{"path traversal", "v1.2.3/../x"},
		{"https url", "https://moonglass.dev/releases/v1.2.3"},
		{"NUL at end", "v1.2.3\x00"},
		{"NUL after v", "v\x001.2.3"},
		{"non-ASCII digit", "v１.2.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := &agentv1.UpdateOffer{Version: tt.version}
			err := openUpdate(t, agentv1.MessageType_UPDATE_OFFER, msg, &agentv1.UpdateOffer{})
			expectUpdateRejected(t, "version "+fmt.Sprintf("%q", tt.version), err, "version")
		})
	}
}

func TestUpdateReportRoundTrip(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.UpdateReport{}).ProtoReflect().Descriptor())

	fields := (&agentv1.UpdateReport{}).ProtoReflect().Descriptor().Fields()
	stringFields := make(map[string]bool)
	for i := 0; i < fields.Len(); i++ {
		if f := fields.Get(i); f.Kind() == protoreflect.StringKind {
			stringFields[string(f.Name())] = true
		}
	}
	if len(stringFields) != 2 || !stringFields["version"] || !stringFields["sha256"] {
		t.Fatalf("UpdateReport string fields: expected exactly {version, sha256}, got %v", stringFields)
	}

	for _, pair := range validUpdateReportPairs() {
		t.Run(fmt.Sprintf("%v/%v", pair.outcome, pair.reason), func(t *testing.T) {
			t.Parallel()
			msg := updateReportFor(pair.outcome, pair.reason)
			var got agentv1.UpdateReport
			roundTrip(t, agentv1.MessageType_UPDATE_REPORT, msg, &got)
			if got.GetOutcome() != pair.outcome || got.GetReason() != pair.reason {
				t.Fatalf("outcome/reason: expected %v/%v, got %v/%v",
					pair.outcome, pair.reason, got.GetOutcome(), got.GetReason())
			}
		})
	}

	restarted := []struct {
		name    string
		outcome agentv1.UpdateOutcome
		reason  agentv1.UpdateReason
	}{
		{"switching", agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED},
		{"passed", agentv1.UpdateOutcome_UPDATE_OUTCOME_PASSED, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED},
		{"reverted", agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateReason_UPDATE_REASON_RESTARTED},
		{"check failed", agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED, agentv1.UpdateReason_UPDATE_REASON_RESTARTED},
	}
	for _, tt := range restarted {
		t.Run("proxy restarted/"+tt.name, func(t *testing.T) {
			t.Parallel()
			msg := updateReportFor(tt.outcome, tt.reason)
			msg.ProxyRestarted = true
			var got agentv1.UpdateReport
			roundTrip(t, agentv1.MessageType_UPDATE_REPORT, msg, &got)
			if !got.GetProxyRestarted() {
				t.Fatalf("proxy_restarted: expected true, got false")
			}
		})
	}
}

func TestUpdateReportRejectsInvalid(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name  string
		msg   *agentv1.UpdateReport
		field string
	}
	var tests []testCase
	add := func(name string, msg *agentv1.UpdateReport, field string) {
		tests = append(tests, testCase{name: name, msg: msg, field: field})
	}

	outcomes := []agentv1.UpdateOutcome{
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_PASSED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED,
	}

	for _, outcome := range []agentv1.UpdateOutcome{
		agentv1.UpdateOutcome_UPDATE_OUTCOME_UNSPECIFIED,
		agentv1.UpdateOutcome(7),
		agentv1.UpdateOutcome(math.MaxInt32),
	} {
		add(fmt.Sprintf("outcome %d", int32(outcome)),
			&agentv1.UpdateReport{
				Version: updateVersion,
				Outcome: outcome,
				Reason:  agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED,
			}, "outcome")
	}

	for _, reason := range []agentv1.UpdateReason{
		agentv1.UpdateReason(18),
		agentv1.UpdateReason(math.MaxInt32),
	} {
		add(fmt.Sprintf("reason %d", int32(reason)),
			updateReportFor(agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, reason), "reason")
	}

	for _, outcome := range []agentv1.UpdateOutcome{
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED,
	} {
		add(fmt.Sprintf("reason UNSPECIFIED with %v", outcome),
			updateReportFor(outcome, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED), "reason")
	}

	allowed := make(map[updateReportPair]bool, len(validUpdateReportPairs()))
	for _, pair := range validUpdateReportPairs() {
		allowed[pair] = true
	}
	reasonValues := agentv1.UpdateReason(0).Descriptor().Values()
	for _, outcome := range outcomes {
		for i := 0; i < reasonValues.Len(); i++ {
			reason := agentv1.UpdateReason(reasonValues.Get(i).Number())
			if reason == agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED {
				continue
			}
			if allowed[updateReportPair{outcome: outcome, reason: reason}] {
				continue
			}
			add(fmt.Sprintf("reason %v with outcome %v", reasonValues.Get(i).Name(), outcome),
				updateReportFor(outcome, reason), "reason")
		}
	}

	validReasonFor := map[agentv1.UpdateOutcome]agentv1.UpdateReason{
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED:      agentv1.UpdateReason_UPDATE_REASON_NOT_NEWER,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED: agentv1.UpdateReason_UPDATE_REASON_DISK,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING:     agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_PASSED:        agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED:      agentv1.UpdateReason_UPDATE_REASON_RESTARTED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED:  agentv1.UpdateReason_UPDATE_REASON_RESTARTED,
	}

	withHash := updateReportFor(agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED, agentv1.UpdateReason_UPDATE_REASON_NOT_NEWER)
	withHash.Sha256 = strings.Repeat("a", 64)
	add("sha256 non-empty with REJECTED", withHash, "sha256")

	badHashes := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"63 characters", strings.Repeat("a", 63)},
		{"65 characters", strings.Repeat("a", 65)},
		{"uppercase hex", strings.Repeat("A", 64)},
		{"non-hex letter", strings.Repeat("g", 64)},
	}
	for _, outcome := range []agentv1.UpdateOutcome{
		agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_PASSED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED,
	} {
		for _, hash := range badHashes {
			msg := updateReportFor(outcome, validReasonFor[outcome])
			msg.Sha256 = hash.hash
			add(fmt.Sprintf("sha256 %s with %v", hash.name, outcome), msg, "sha256")
		}
	}

	for _, outcome := range []agentv1.UpdateOutcome{
		agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED,
		agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED,
	} {
		msg := updateReportFor(outcome, validReasonFor[outcome])
		msg.ProxyRestarted = true
		add(fmt.Sprintf("proxy_restarted true with %v", outcome), msg, "proxy_restarted")
	}

	for _, version := range []string{"", "1.2.3", "v01.2.3", "v1.2.3-rc.1"} {
		msg := updateReportFor(agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED)
		msg.Version = version
		add(fmt.Sprintf("version %q", version), msg, "version")
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := openUpdate(t, agentv1.MessageType_UPDATE_REPORT, tt.msg, &agentv1.UpdateReport{})
			expectUpdateRejected(t, tt.name, err, tt.field)
		})
	}
}

func TestHeartbeatUpdateChannel(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.Heartbeat{}).ProtoReflect().Descriptor())

	pub := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)
	key := deployKeyLine(t, pub)

	shapes := []struct {
		name string
		msg  func(channel string) *agentv1.Heartbeat
	}{
		{"alone", func(channel string) *agentv1.Heartbeat {
			return &agentv1.Heartbeat{AgentVersion: "dev", UpdateChannel: channel}
		}},
		{"with disk", func(channel string) *agentv1.Heartbeat {
			return &agentv1.Heartbeat{AgentVersion: "dev", UpdateChannel: channel, Disk: validDiskReport()}
		}},
		{"with deploy key", func(channel string) *agentv1.Heartbeat {
			return &agentv1.Heartbeat{AgentVersion: "dev", UpdateChannel: channel, DeployKey: key}
		}},
		{"with disk and deploy key", func(channel string) *agentv1.Heartbeat {
			return &agentv1.Heartbeat{AgentVersion: "dev", UpdateChannel: channel, Disk: validDiskReport(), DeployKey: key}
		}},
	}
	for _, shape := range shapes {
		for _, channel := range []string{"", "STABLE", "CANARY"} {
			t.Run(shape.name+"/"+fmt.Sprintf("%q", channel), func(t *testing.T) {
				t.Parallel()
				msg := shape.msg(channel)
				var got agentv1.Heartbeat
				roundTrip(t, agentv1.MessageType_HEARTBEAT, msg, &got)
				if got.GetUpdateChannel() != channel {
					t.Fatalf("update_channel: expected %q, got %q", channel, got.GetUpdateChannel())
				}
			})
		}
	}

	rejects := []struct {
		name  string
		value string
	}{
		{"lowercase stable", "stable"},
		{"lowercase canary", "canary"},
		{"mixed case", "Stable"},
		{"UNSPECIFIED", "UNSPECIFIED"},
		{"BETA", "BETA"},
		{"leading space", " STABLE"},
		{"trailing newline", "STABLE\n"},
		{"NUL", "STABLE\x00"},
		{"1 KiB", strings.Repeat("S", 1024)},
	}
	for _, tt := range rejects {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			msg := &agentv1.Heartbeat{AgentVersion: "dev", UpdateChannel: tt.value}
			env, err := Wrap(agentv1.MessageType_HEARTBEAT, updateRequestID, msg)
			if err != nil {
				t.Fatalf("Wrap(HEARTBEAT) error = %v", err)
			}
			err = Open(env, &agentv1.Heartbeat{})
			expectUpdateRejected(t, tt.name, err, "update_channel")
			if strings.Contains(err.Error(), tt.value) && tt.name == "1 KiB" {
				t.Fatalf("Open error = %q, expected the 1 KiB value not to be echoed in full", err)
			}
		})
	}
}

func TestUpdateMessagesProtocolAbuse(t *testing.T) {
	t.Parallel()
	assertNoShellStringFields(t, (&agentv1.UpdateOffer{}).ProtoReflect().Descriptor())
	assertNoShellStringFields(t, (&agentv1.UpdateReport{}).ProtoReflect().Descriptor())

	t.Run("unknown offer field", func(t *testing.T) {
		t.Parallel()
		payload, err := proto.Marshal(&agentv1.UpdateOffer{Version: updateVersion})
		if err != nil {
			t.Fatalf("proto.Marshal(UpdateOffer) error = %v", err)
		}
		err = openUpdatePayload(t, agentv1.MessageType_UPDATE_OFFER, appendUnknownPayload(payload), &agentv1.UpdateOffer{})
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open(UPDATE_OFFER with unknown field): expected %v, got %v", ErrMalformed, err)
		}
	})

	t.Run("unknown report field", func(t *testing.T) {
		t.Parallel()
		payload, err := proto.Marshal(updateReportFor(agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED))
		if err != nil {
			t.Fatalf("proto.Marshal(UpdateReport) error = %v", err)
		}
		err = openUpdatePayload(t, agentv1.MessageType_UPDATE_REPORT, appendUnknownPayload(payload), &agentv1.UpdateReport{})
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("Open(UPDATE_REPORT with unknown field): expected %v, got %v", ErrMalformed, err)
		}
	})

	t.Run("open type mismatches", func(t *testing.T) {
		t.Parallel()
		offerEnv, err := Wrap(agentv1.MessageType_UPDATE_OFFER, updateRequestID, &agentv1.UpdateOffer{Version: updateVersion})
		if err != nil {
			t.Fatalf("Wrap(UPDATE_OFFER) error = %v", err)
		}
		reportEnv, err := Wrap(agentv1.MessageType_UPDATE_REPORT, updateRequestID,
			updateReportFor(agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED))
		if err != nil {
			t.Fatalf("Wrap(UPDATE_REPORT) error = %v", err)
		}
		cases := []struct {
			name string
			env  *agentv1.Envelope
			out  proto.Message
		}{
			{"offer as report", offerEnv, &agentv1.UpdateReport{}},
			{"report as offer", reportEnv, &agentv1.UpdateOffer{}},
			{"offer as deploy", offerEnv, &agentv1.Deploy{}},
		}
		for _, tt := range cases {
			if err := Open(tt.env, tt.out); !errors.Is(err, ErrTypeMismatch) {
				t.Fatalf("Open(%s): expected %v, got %v", tt.name, ErrTypeMismatch, err)
			}
		}
	})

	t.Run("wrap type mismatches", func(t *testing.T) {
		t.Parallel()
		if _, err := Wrap(agentv1.MessageType_UPDATE_OFFER, updateRequestID, &agentv1.UpdateReport{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Wrap(UPDATE_OFFER, UpdateReport): expected %v, got %v", ErrTypeMismatch, err)
		}
		if _, err := Wrap(agentv1.MessageType_UPDATE_REPORT, updateRequestID, &agentv1.UpdateOffer{}); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("Wrap(UPDATE_REPORT, UpdateOffer): expected %v, got %v", ErrTypeMismatch, err)
		}
	})

	t.Run("error field on update messages", func(t *testing.T) {
		t.Parallel()
		for _, typ := range []agentv1.MessageType{
			agentv1.MessageType_UPDATE_OFFER,
			agentv1.MessageType_UPDATE_REPORT,
		} {
			env := &agentv1.Envelope{
				Version:   Version,
				Type:      typ,
				RequestId: updateRequestID,
				Error:     &agentv1.Error{Code: agentv1.ErrorCode_INVALID},
			}
			if _, err := Marshal(env); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Marshal(%v with error): expected %v, got %v", typ, ErrMalformed, err)
			}
			body, err := proto.Marshal(env)
			if err != nil {
				t.Fatalf("proto.Marshal(%v with error) error = %v", typ, err)
			}
			if _, err := Unmarshal(mustFrame(t, body)); !errors.Is(err, ErrMalformed) {
				t.Fatalf("Unmarshal(%v with error): expected %v, got %v", typ, ErrMalformed, err)
			}
		}
	})

	t.Run("oversize offer", func(t *testing.T) {
		t.Parallel()
		offer := &agentv1.UpdateOffer{Version: strings.Repeat("v", MaxEnvelopeSize)}
		env, err := Wrap(agentv1.MessageType_UPDATE_OFFER, updateRequestID, offer)
		if err != nil {
			t.Fatalf("Wrap(oversize UPDATE_OFFER) error = %v", err)
		}
		if b, err := Marshal(env); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Marshal(oversize UPDATE_OFFER): expected %v, got %v (and %d bytes)", ErrTooLarge, err, len(b))
		}
		body, err := proto.Marshal(env)
		if err != nil {
			t.Fatalf("proto.Marshal(oversize UPDATE_OFFER) error = %v", err)
		}
		framed := make([]byte, 4+len(body))
		binary.BigEndian.PutUint32(framed, uint32(len(body)))
		copy(framed[4:], body)
		got, err := Unmarshal(framed)
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Unmarshal(%d framed bytes): expected %v, got %v", len(framed), ErrTooLarge, err)
		}
		if got != nil {
			t.Fatalf("Unmarshal(%d framed bytes) envelope = %#v, expected nil", len(framed), got)
		}
	})
}

func FuzzOpenUpdateOffer(f *testing.F) {
	valid, err := proto.Marshal(&agentv1.UpdateOffer{Version: updateVersion})
	if err != nil {
		f.Fatalf("proto.Marshal(valid UpdateOffer) error = %v", err)
	}
	f.Add(valid)
	f.Add([]byte(nil))
	f.Add(appendUnknownPayload(valid))

	f.Fuzz(func(t *testing.T, in []byte) {
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_UPDATE_OFFER,
			RequestId: "fuzz",
			Payload:   in,
		}
		var first agentv1.UpdateOffer
		if err := Open(env, &first); err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(UPDATE_OFFER, %x) error = %v, expected nil or %v", in, err, ErrMalformed)
			}
			return
		}
		if err := validateUpdateOffer(&first); err != nil {
			t.Fatalf("Open accepted UPDATE_OFFER payload %x but validateUpdateOffer failed: %v", in, err)
		}
		wrapped, err := Wrap(agentv1.MessageType_UPDATE_OFFER, "fuzz", &first)
		if err != nil {
			t.Fatalf("Wrap(accepted UpdateOffer %v) error = %v", &first, err)
		}
		var again agentv1.UpdateOffer
		if err := Open(wrapped, &again); err != nil {
			t.Fatalf("Open after re-wrap error = %v", err)
		}
		if !proto.Equal(&first, &again) {
			t.Fatalf("round-trip mismatch: first %v, again %v", &first, &again)
		}
	})
}

func FuzzOpenUpdateReport(f *testing.F) {
	for _, pair := range []updateReportPair{
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED, agentv1.UpdateReason_UPDATE_REASON_NOT_NEWER},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED, agentv1.UpdateReason_UPDATE_REASON_DISK},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_PASSED, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateReason_UPDATE_REASON_RESTARTED},
		{agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED, agentv1.UpdateReason_UPDATE_REASON_RESTARTED},
	} {
		payload, err := proto.Marshal(updateReportFor(pair.outcome, pair.reason))
		if err != nil {
			f.Fatalf("proto.Marshal(%v/%v UpdateReport) error = %v", pair.outcome, pair.reason, err)
		}
		f.Add(payload)
	}
	base, err := proto.Marshal(updateReportFor(agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED))
	if err != nil {
		f.Fatalf("proto.Marshal(valid UpdateReport) error = %v", err)
	}
	f.Add(appendUnknownPayload(base))
	f.Add([]byte(nil))

	f.Fuzz(func(t *testing.T, in []byte) {
		env := &agentv1.Envelope{
			Version:   Version,
			Type:      agentv1.MessageType_UPDATE_REPORT,
			RequestId: "fuzz",
			Payload:   in,
		}
		var first agentv1.UpdateReport
		if err := Open(env, &first); err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Open(UPDATE_REPORT, %x) error = %v, expected nil or %v", in, err, ErrMalformed)
			}
			return
		}
		if err := validateUpdateReport(&first); err != nil {
			t.Fatalf("Open accepted UPDATE_REPORT payload %x but validateUpdateReport failed: %v", in, err)
		}
		wrapped, err := Wrap(agentv1.MessageType_UPDATE_REPORT, "fuzz", &first)
		if err != nil {
			t.Fatalf("Wrap(accepted UpdateReport %v) error = %v", &first, err)
		}
		var again agentv1.UpdateReport
		if err := Open(wrapped, &again); err != nil {
			t.Fatalf("Open after re-wrap error = %v", err)
		}
		if !proto.Equal(&first, &again) {
			t.Fatalf("round-trip mismatch: first %v, again %v", &first, &again)
		}
	})
}
