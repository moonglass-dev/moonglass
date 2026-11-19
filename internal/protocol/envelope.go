package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

// Version is the envelope version for protocol v0.
const Version uint32 = 1

// MaxEnvelopeSize is the maximum size of a complete encoded envelope, including payload, in bytes.
const MaxEnvelopeSize = 1 << 20

// MaxRequestIDLen is the maximum length of an envelope request id in bytes.
const MaxRequestIDLen = 64

// MaxErrorDetailLen is the maximum length of an error detail string in bytes.
const MaxErrorDetailLen = 1024

var (
	// ErrTooLarge is returned when an envelope exceeds MaxEnvelopeSize.
	ErrTooLarge = errors.New("protocol: envelope too large")
	// ErrMalformed is returned when an envelope cannot be parsed or fails validation.
	ErrMalformed = errors.New("protocol: malformed envelope")
	// ErrUnknownType is returned when an envelope type is not a known v0 message type.
	ErrUnknownType = errors.New("protocol: unknown message type")
	// ErrUnknownCode is returned when an error code is not a known v0 code.
	ErrUnknownCode = errors.New("protocol: unknown error code")
	// ErrTypeMismatch is returned when Open is called with a message that does not match the envelope type.
	ErrTypeMismatch = errors.New("protocol: message type mismatch")
)

var marshalOpts = proto.MarshalOptions{Deterministic: true}

// Wrap builds an envelope for msg with the given type and request id.
// Errors travel only in Envelope.error: Wrap(MessageType_ERROR, id, err) sets that field
// and leaves payload empty.
func Wrap(t agentv1.MessageType, requestID string, msg proto.Message) (*agentv1.Envelope, error) {
	if msg == nil {
		return nil, fmt.Errorf("wrap: nil message: %w", ErrMalformed)
	}
	got, err := typeOf(msg)
	if err != nil {
		return nil, err
	}
	if got != t {
		return nil, fmt.Errorf("wrap type %v, message %v: %w", t, got, ErrTypeMismatch)
	}
	env := &agentv1.Envelope{
		Version:   Version,
		Type:      t,
		RequestId: requestID,
	}
	if t == agentv1.MessageType_ERROR {
		errMsg, ok := msg.(*agentv1.Error)
		if !ok || errMsg == nil {
			return nil, fmt.Errorf("wrap error: %w", ErrTypeMismatch)
		}
		cloned, ok := proto.Clone(errMsg).(*agentv1.Error)
		if !ok {
			return nil, fmt.Errorf("wrap error: %w", ErrMalformed)
		}
		env.Error = cloned
	} else {
		payload, err := marshalOpts.Marshal(msg)
		if err != nil {
			return nil, fmt.Errorf("wrap payload: %w", err)
		}
		env.Payload = payload
	}
	if err := validate(env); err != nil {
		return nil, err
	}
	return env, nil
}

// Marshal validates env and returns its deterministic encoding.
func Marshal(env *agentv1.Envelope) ([]byte, error) {
	if err := validate(env); err != nil {
		return nil, err
	}
	body, err := marshalOpts.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	return frameBody(body)
}

func frameBody(body []byte) ([]byte, error) {
	if len(body) > MaxEnvelopeSize-4 {
		return nil, fmt.Errorf("encoded size %d: %w", len(body)+4, ErrTooLarge)
	}
	//nolint:gosec // G115: len(body) is capped at MaxEnvelopeSize-4 (1 MiB)
	n := uint32(len(body))
	out := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(out, n)
	copy(out[4:], body)
	return out, nil
}

// Unmarshal decodes b into a validated envelope. The size check runs before any proto parse.
func Unmarshal(b []byte) (*agentv1.Envelope, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("empty input: %w", ErrMalformed)
	}
	if len(b) > MaxEnvelopeSize {
		return nil, fmt.Errorf("input size %d: %w", len(b), ErrTooLarge)
	}
	if len(b) < 4 {
		return nil, fmt.Errorf("short input: %w", ErrMalformed)
	}
	n := uint64(binary.BigEndian.Uint32(b[:4]))
	if n > uint64(MaxEnvelopeSize-4) || uint64(4)+n != uint64(len(b)) {
		return nil, fmt.Errorf("framed length %d for %d-byte input: %w", n, len(b), ErrMalformed)
	}
	env := new(agentv1.Envelope)
	if err := proto.Unmarshal(b[4:], env); err != nil {
		return nil, fmt.Errorf("parse envelope: %w", errors.Join(ErrMalformed, err))
	}
	if len(env.ProtoReflect().GetUnknown()) > 0 {
		return nil, fmt.Errorf("unknown envelope fields: %w", ErrMalformed)
	}
	if err := validate(env); err != nil {
		return nil, err
	}
	return env, nil
}

// Open unmarshals env's payload into msg after checking that env.Type matches msg.
// For type ERROR, the structured error is taken from Envelope.error, not from payload.
func Open(env *agentv1.Envelope, msg proto.Message) error {
	if env == nil {
		return fmt.Errorf("open: nil envelope: %w", ErrMalformed)
	}
	if msg == nil {
		return fmt.Errorf("open: nil message: %w", ErrMalformed)
	}
	want, err := typeOf(msg)
	if err != nil {
		return err
	}
	if env.GetType() != want {
		return fmt.Errorf("envelope type %v, message %v: %w", env.GetType(), want, ErrTypeMismatch)
	}
	if want == agentv1.MessageType_ERROR {
		if env.Error == nil {
			return fmt.Errorf("missing error: %w", ErrMalformed)
		}
		out, ok := msg.(*agentv1.Error)
		if !ok || out == nil {
			return fmt.Errorf("open error: %w", ErrTypeMismatch)
		}
		proto.Reset(out)
		proto.Merge(out, env.Error)
		return nil
	}
	if err := proto.Unmarshal(env.GetPayload(), msg); err != nil {
		return fmt.Errorf("parse payload: %w", errors.Join(ErrMalformed, err))
	}
	if err := rejectUnknown(msg.ProtoReflect()); err != nil {
		return err
	}
	return validatePayload(msg)
}

// rejectUnknown fails when m or any nested message it holds has unknown fields.
func rejectUnknown(m protoreflect.Message) error {
	if len(m.GetUnknown()) > 0 {
		return fmt.Errorf("unknown fields in %s: %w", m.Descriptor().FullName(), ErrMalformed)
	}
	var err error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}
		if fd.IsList() {
			for i, n := 0, v.List().Len(); i < n && err == nil; i++ {
				err = rejectUnknown(v.List().Get(i).Message())
			}
			return err == nil
		}
		err = rejectUnknown(v.Message())
		return err == nil
	})
	return err
}

func validate(env *agentv1.Envelope) error {
	if env == nil {
		return fmt.Errorf("nil envelope: %w", ErrMalformed)
	}
	if env.GetVersion() != Version {
		return fmt.Errorf("version %d, expected %d: %w", env.GetVersion(), Version, ErrMalformed)
	}
	if err := validateType(env.GetType()); err != nil {
		return err
	}
	id := env.GetRequestId()
	if n := len(id); n < 1 || n > MaxRequestIDLen {
		return fmt.Errorf("request id length %d: %w", n, ErrMalformed)
	}
	if env.Error != nil {
		if env.GetType() != agentv1.MessageType_ERROR {
			return fmt.Errorf("error set on type %v: %w", env.GetType(), ErrMalformed)
		}
		if len(env.Error.ProtoReflect().GetUnknown()) > 0 {
			return fmt.Errorf("unknown error fields: %w", ErrMalformed)
		}
		if err := validateCode(env.Error.GetCode()); err != nil {
			return err
		}
		if len(env.Error.GetDetail()) > MaxErrorDetailLen {
			return fmt.Errorf("error detail length %d: %w", len(env.Error.GetDetail()), ErrMalformed)
		}
	} else if env.GetType() == agentv1.MessageType_ERROR {
		return fmt.Errorf("error type missing error: %w", ErrMalformed)
	}
	return nil
}

func validatePayload(msg proto.Message) error {
	switch m := msg.(type) {
	case *agentv1.EnrollResponse:
		return validateEnrollResult(m.GetResult())
	case *agentv1.Deploy:
		return validateDeploy(m)
	case *agentv1.SwitchRelease:
		return validateSwitchRelease(m)
	case *agentv1.Rollback:
		return validateRollback(m)
	case *agentv1.RunPreDeploy:
		return validateRunPreDeploy(m)
	case *agentv1.CommandResult:
		return validateCommandResult(m)
	case *agentv1.Build:
		return validateBuild(m)
	case *agentv1.StopApp:
		return validateStopApp(m)
	case *agentv1.RemoveApp:
		return validateRemoveApp(m)
	case *agentv1.Heartbeat:
		return validateHeartbeat(m)
	case *agentv1.UpdateOffer:
		return validateUpdateOffer(m)
	case *agentv1.UpdateReport:
		return validateUpdateReport(m)
	default:
		return nil
	}
}

func validateType(t agentv1.MessageType) error {
	switch t {
	case agentv1.MessageType_ENROLL_REQUEST,
		agentv1.MessageType_ENROLL_RESPONSE,
		agentv1.MessageType_HEARTBEAT,
		agentv1.MessageType_HEARTBEAT_ACK,
		agentv1.MessageType_ERROR,
		agentv1.MessageType_DEPLOY,
		agentv1.MessageType_SWITCH_RELEASE,
		agentv1.MessageType_ROLLBACK,
		agentv1.MessageType_RUN_PRE_DEPLOY,
		agentv1.MessageType_COMMAND_RESULT,
		agentv1.MessageType_BUILD,
		agentv1.MessageType_STOP_APP,
		agentv1.MessageType_REMOVE_APP,
		agentv1.MessageType_UPDATE_OFFER,
		agentv1.MessageType_UPDATE_REPORT:
		return nil
	case agentv1.MessageType_MESSAGE_TYPE_UNSPECIFIED:
		return fmt.Errorf("unspecified message type: %w", ErrMalformed)
	default:
		return fmt.Errorf("type %d: %w", t, ErrUnknownType)
	}
}

func validateCode(c agentv1.ErrorCode) error {
	switch c {
	case agentv1.ErrorCode_INVALID,
		agentv1.ErrorCode_UNAUTHORIZED,
		agentv1.ErrorCode_TOO_LARGE,
		agentv1.ErrorCode_UNKNOWN_TYPE,
		agentv1.ErrorCode_UNAVAILABLE:
		return nil
	case agentv1.ErrorCode_ERROR_CODE_UNSPECIFIED:
		return fmt.Errorf("unspecified error code: %w", ErrMalformed)
	default:
		return fmt.Errorf("code %d: %w", c, ErrUnknownCode)
	}
}

func validateEnrollResult(r agentv1.EnrollResult) error {
	switch r {
	case agentv1.EnrollResult_ACCEPTED,
		agentv1.EnrollResult_TOKEN_INVALID,
		agentv1.EnrollResult_TOKEN_USED:
		return nil
	case agentv1.EnrollResult_ENROLL_RESULT_UNSPECIFIED:
		return fmt.Errorf("unspecified enroll result: %w", ErrMalformed)
	default:
		return fmt.Errorf("enroll result %d: %w", r, ErrMalformed)
	}
}

func typeOf(msg proto.Message) (agentv1.MessageType, error) {
	switch msg.(type) {
	case *agentv1.EnrollRequest:
		return agentv1.MessageType_ENROLL_REQUEST, nil
	case *agentv1.EnrollResponse:
		return agentv1.MessageType_ENROLL_RESPONSE, nil
	case *agentv1.Heartbeat:
		return agentv1.MessageType_HEARTBEAT, nil
	case *agentv1.HeartbeatAck:
		return agentv1.MessageType_HEARTBEAT_ACK, nil
	case *agentv1.Error:
		return agentv1.MessageType_ERROR, nil
	case *agentv1.Deploy:
		return agentv1.MessageType_DEPLOY, nil
	case *agentv1.SwitchRelease:
		return agentv1.MessageType_SWITCH_RELEASE, nil
	case *agentv1.Rollback:
		return agentv1.MessageType_ROLLBACK, nil
	case *agentv1.RunPreDeploy:
		return agentv1.MessageType_RUN_PRE_DEPLOY, nil
	case *agentv1.CommandResult:
		return agentv1.MessageType_COMMAND_RESULT, nil
	case *agentv1.Build:
		return agentv1.MessageType_BUILD, nil
	case *agentv1.StopApp:
		return agentv1.MessageType_STOP_APP, nil
	case *agentv1.RemoveApp:
		return agentv1.MessageType_REMOVE_APP, nil
	case *agentv1.UpdateOffer:
		return agentv1.MessageType_UPDATE_OFFER, nil
	case *agentv1.UpdateReport:
		return agentv1.MessageType_UPDATE_REPORT, nil
	default:
		return agentv1.MessageType_MESSAGE_TYPE_UNSPECIFIED, fmt.Errorf("unsupported message %T: %w", msg, ErrUnknownType)
	}
}
