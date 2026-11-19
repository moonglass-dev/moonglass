package protocol

import (
	"fmt"
	"slices"
	"strings"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

const maxQuotedLen = 64

// quoteOrLen quotes s for an error, or gives its length when it is too long to
// echo. Errors stay bounded whatever the input holds.
func quoteOrLen(s string) string {
	if len(s) > maxQuotedLen {
		return fmt.Sprintf("%d bytes", len(s))
	}
	return fmt.Sprintf("%q", s)
}

// validateReleaseVersion checks vMAJOR.MINOR.PATCH: three numeric identifiers
// with no leading zero unless the identifier is exactly "0". Nothing is parsed
// to an integer, so an identifier wider than uint64 is still checked as bytes.
func validateReleaseVersion(field, s string) error {
	fail := func() error {
		return fmt.Errorf("%s %s, expected vMAJOR.MINOR.PATCH: %w", field, quoteOrLen(s), ErrMalformed)
	}
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return fail()
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return fail()
	}
	for _, part := range parts {
		if !validReleaseNumber(part) {
			return fail()
		}
	}
	return nil
}

// validReleaseNumber checks one numeric identifier of a release version.
func validReleaseNumber(s string) bool {
	if s == "" {
		return false
	}
	if len(s) > 1 && s[0] == '0' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func validateUpdateOffer(m *agentv1.UpdateOffer) error {
	return validateReleaseVersion("version", m.GetVersion())
}

// updateReasons returns the reasons an outcome allows, or nil when the outcome
// is not one of the six. The zero outcome is not one of them.
func updateReasons(o agentv1.UpdateOutcome) []agentv1.UpdateReason {
	switch o {
	case agentv1.UpdateOutcome_UPDATE_OUTCOME_UNSPECIFIED:
		return nil
	case agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED:
		return []agentv1.UpdateReason{
			agentv1.UpdateReason_UPDATE_REASON_NOT_NEWER,
			agentv1.UpdateReason_UPDATE_REASON_NOT_ON_CHANNEL,
			agentv1.UpdateReason_UPDATE_REASON_REVERTED_BEFORE,
			agentv1.UpdateReason_UPDATE_REASON_TRIAL_IN_PROGRESS,
			agentv1.UpdateReason_UPDATE_REASON_POINTER,
		}
	case agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED:
		return []agentv1.UpdateReason{
			agentv1.UpdateReason_UPDATE_REASON_DISK,
			agentv1.UpdateReason_UPDATE_REASON_DOWNLOAD,
			agentv1.UpdateReason_UPDATE_REASON_TOO_LARGE,
			agentv1.UpdateReason_UPDATE_REASON_SIGNATURE,
			agentv1.UpdateReason_UPDATE_REASON_CHECKSUM,
			agentv1.UpdateReason_UPDATE_REASON_POINTER_MISMATCH,
			agentv1.UpdateReason_UPDATE_REASON_WRITE,
		}
	case agentv1.UpdateOutcome_UPDATE_OUTCOME_SWITCHING, agentv1.UpdateOutcome_UPDATE_OUTCOME_PASSED:
		return []agentv1.UpdateReason{agentv1.UpdateReason_UPDATE_REASON_UNSPECIFIED}
	case agentv1.UpdateOutcome_UPDATE_OUTCOME_REVERTED, agentv1.UpdateOutcome_UPDATE_OUTCOME_CHECK_FAILED:
		return []agentv1.UpdateReason{
			agentv1.UpdateReason_UPDATE_REASON_RESTARTED,
			agentv1.UpdateReason_UPDATE_REASON_NOT_CONNECTED,
			agentv1.UpdateReason_UPDATE_REASON_EXE_MISMATCH,
			agentv1.UpdateReason_UPDATE_REASON_PROXY,
			agentv1.UpdateReason_UPDATE_REASON_DEADLINE,
		}
	}
	return nil
}

// validateUpdateReport checks the version, the outcome, the reason its outcome
// allows, the sha256 shape that outcome needs and proxy_restarted.
func validateUpdateReport(m *agentv1.UpdateReport) error {
	if err := validateReleaseVersion("version", m.GetVersion()); err != nil {
		return err
	}
	outcome := m.GetOutcome()
	reasons := updateReasons(outcome)
	if reasons == nil {
		return fmt.Errorf("outcome %d, expected one of REJECTED, VERIFY_FAILED, SWITCHING, PASSED, REVERTED, CHECK_FAILED: %w", outcome, ErrMalformed)
	}
	reason := m.GetReason()
	if _, ok := agentv1.UpdateReason_name[int32(reason)]; !ok {
		return fmt.Errorf("reason %d unknown: %w", reason, ErrMalformed)
	}
	if !slices.Contains(reasons, reason) {
		return fmt.Errorf("reason %v with outcome %v, expected one of %v: %w", reason, outcome, reasons, ErrMalformed)
	}
	sha := m.GetSha256()
	if outcome == agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED {
		if sha != "" {
			return fmt.Errorf("sha256 must be empty with outcome REJECTED, got %d bytes: %w", len(sha), ErrMalformed)
		}
	} else if len(sha) != sha256HexLen || strings.Trim(sha, hexChars) != "" {
		return fmt.Errorf("sha256 %s, expected 64 lowercase hex characters: %w", quoteOrLen(sha), ErrMalformed)
	}
	noProxyRestart := outcome == agentv1.UpdateOutcome_UPDATE_OUTCOME_REJECTED ||
		outcome == agentv1.UpdateOutcome_UPDATE_OUTCOME_VERIFY_FAILED
	if m.GetProxyRestarted() && noProxyRestart {
		return fmt.Errorf("proxy_restarted true with outcome %v, expected false: %w", outcome, ErrMalformed)
	}
	return nil
}
