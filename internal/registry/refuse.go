package registry

import "errors"

// The refusal kinds a handler answers with. They are distinct from the store's
// sentinels, and internal/api maps them to 400, 404 and 409.
var (
	ErrInvalidInput = errors.New("registry: invalid input")
	ErrNotFound     = errors.New("registry: not found")
	ErrConflict     = errors.New("registry: conflict")
)

// refusal is the error Refuse returns: it matches its kind with errors.Is and
// wraps nothing, so no other error's text reaches the caller.
type refusal struct {
	kind    error
	message string
}

func (e *refusal) Error() string { return e.message }

func (e *refusal) Is(target error) bool { return target == e.kind }

// Refuse builds a handler's refusal. kind is ErrInvalidInput, ErrNotFound or
// ErrConflict; message is written for the caller from the inputs and fixed
// text, never from another error's text.
func Refuse(kind error, message string) error {
	return &refusal{kind: kind, message: message}
}

// handlerReason is the fixed audit reason for a handler error: one per refusal
// kind, and the generic reason for every other error.
func handlerReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return "invalid input"
	case errors.Is(err, ErrNotFound):
		return "not found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	}
	return "the handler returned an error"
}
