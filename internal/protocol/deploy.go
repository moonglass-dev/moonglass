package protocol

import (
	"fmt"
	"strings"
	"unicode"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

const (
	maxIDLen    = 64
	maxImageLen = 512
	maxArgs     = 32
	maxArgLen   = 1024
)

func validateID(field, s string) error {
	if n := len(s); n < 1 || n > maxIDLen {
		return fmt.Errorf("%s length %d, expected 1-%d bytes: %w", field, n, maxIDLen, ErrMalformed)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
		if !ok {
			return fmt.Errorf("%s has byte %#x at %d, expected only [A-Za-z0-9._-]: %w", field, c, i, ErrMalformed)
		}
	}
	return nil
}

func validateImage(s string) error {
	if n := len(s); n < 1 || n > maxImageLen {
		return fmt.Errorf("image length %d, expected 1-%d bytes: %w", n, maxImageLen, ErrMalformed)
	}
	if s[0] == '-' {
		return fmt.Errorf("image starts with '-', expected an image reference: %w", ErrMalformed)
	}
	if i := strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }); i >= 0 {
		return fmt.Errorf("image has whitespace or control character at %d, expected none: %w", i, ErrMalformed)
	}
	return nil
}

func validateArgs(args []string) error {
	if n := len(args); n < 1 || n > maxArgs {
		return fmt.Errorf("args count %d, expected 1-%d: %w", n, maxArgs, ErrMalformed)
	}
	for i, a := range args {
		if n := len(a); n < 1 || n > maxArgLen {
			return fmt.Errorf("args[%d] length %d, expected 1-%d bytes: %w", i, n, maxArgLen, ErrMalformed)
		}
		if strings.IndexByte(a, 0) >= 0 {
			return fmt.Errorf("args[%d] contains NUL, expected none: %w", i, ErrMalformed)
		}
	}
	return nil
}

func validateDeploy(m *agentv1.Deploy) error {
	if err := validateID("app_id", m.GetAppId()); err != nil {
		return err
	}
	if err := validateID("release_id", m.GetReleaseId()); err != nil {
		return err
	}
	return validateImage(m.GetImage())
}

func validateSwitchRelease(m *agentv1.SwitchRelease) error {
	if err := validateID("app_id", m.GetAppId()); err != nil {
		return err
	}
	return validateID("release_id", m.GetReleaseId())
}

func validateRollback(m *agentv1.Rollback) error {
	return validateID("app_id", m.GetAppId())
}

func validateRunPreDeploy(m *agentv1.RunPreDeploy) error {
	if err := validateID("app_id", m.GetAppId()); err != nil {
		return err
	}
	if err := validateID("release_id", m.GetReleaseId()); err != nil {
		return err
	}
	return validateArgs(m.GetArgs())
}
