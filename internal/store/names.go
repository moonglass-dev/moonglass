package store

import (
	"fmt"
	"regexp"
)

// namePattern is the protocol's name format (docs/specs/protocol.md): 1 to 63
// bytes of [a-z0-9-], starting and ending with a letter or digit.
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// checkName wraps ErrInvalidName when name is outside the name format.
func checkName(kind, name string) error {
	if namePattern.MatchString(name) {
		return nil
	}
	return fmt.Errorf("%s name %q must be 1 to 63 bytes of [a-z0-9-], starting and ending with a letter or digit: %w",
		kind, name, ErrInvalidName)
}
