package protocol

import (
	"fmt"
	"strings"
	"unicode"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

const (
	maxIDLen         = 64
	maxImageLen      = 512
	maxArgs          = 32
	maxArgLen        = 1024
	maxPort          = 65535
	maxHealthPathLen = 256
	maxDomains       = 16
	maxDomainLen     = 253
	maxLabelLen      = 63
	maxDetailLen     = 4096
	sha256HexLen     = 64
	nameChars        = "abcdefghijklmnopqrstuvwxyz0123456789-"
	pathChars        = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._~/-"
	hexChars         = "0123456789abcdef"
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

func validateImage(field, s string) error {
	if n := len(s); n < 1 || n > maxImageLen {
		return fmt.Errorf("%s length %d, expected 1-%d bytes: %w", field, n, maxImageLen, ErrMalformed)
	}
	if s[0] == '-' {
		return fmt.Errorf("%s starts with '-', expected an image reference: %w", field, ErrMalformed)
	}
	if i := strings.IndexFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }); i >= 0 {
		return fmt.Errorf("%s has whitespace or control character at %d, expected none: %w", field, i, ErrMalformed)
	}
	return nil
}

// validateName checks a name or DNS label: 1-63 bytes of [a-z0-9-], letters or digits at both ends.
func validateName(field, s string) error {
	if n := len(s); n < 1 || n > maxLabelLen {
		return fmt.Errorf("%s length %d, expected 1-%d bytes: %w", field, n, maxLabelLen, ErrMalformed)
	}
	if strings.Trim(s, nameChars) != "" {
		return fmt.Errorf("%s %q, expected only [a-z0-9-]: %w", field, s, ErrMalformed)
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return fmt.Errorf("%s %q starts or ends with '-', expected a letter or digit: %w", field, s, ErrMalformed)
	}
	return nil
}

// validateSHA256 checks a "sha256:" prefix followed by exactly 64 lowercase hex characters.
func validateSHA256(field, s string) error {
	digest, ok := strings.CutPrefix(s, "sha256:")
	if !ok {
		return fmt.Errorf("%s %q has no \"sha256:\" prefix, expected sha256:<64 lowercase hex>: %w", field, s, ErrMalformed)
	}
	if n := len(digest); n != sha256HexLen {
		return fmt.Errorf("%s %q has %d hex characters, expected %d: %w", field, s, n, sha256HexLen, ErrMalformed)
	}
	if strings.Trim(digest, hexChars) != "" {
		return fmt.Errorf("%s %q has a byte outside lowercase hex: %w", field, s, ErrMalformed)
	}
	return nil
}

// validateRepoDigest checks <repository>@sha256:<64 lowercase hex> under the image rule.
func validateRepoDigest(s string) error {
	if err := validateImage("repo_digest", s); err != nil {
		return err
	}
	i := strings.LastIndexByte(s, '@')
	if i < 1 {
		return fmt.Errorf("repo_digest %q has no non-empty repository before '@', expected <repository>@sha256:<64 hex>: %w", s, ErrMalformed)
	}
	return validateSHA256("repo_digest", s[i+1:])
}

// validateDomain checks one lowercase DNS name of at least two labels.
func validateDomain(i int, d string) error {
	field := fmt.Sprintf("domains[%d]", i)
	if n := len(d); n < 1 || n > maxDomainLen {
		return fmt.Errorf("%s length %d, expected 1-%d bytes: %w", field, n, maxDomainLen, ErrMalformed)
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return fmt.Errorf("%s %q has %d labels, expected at least 2: %w", field, d, len(labels), ErrMalformed)
	}
	for _, label := range labels {
		if err := validateName(field, label); err != nil {
			return err
		}
	}
	return nil
}

// validateServiceSettings checks port, health_path and domains, which need a port.
func validateServiceSettings(m *agentv1.Deploy) error {
	port := m.GetPort()
	if port > maxPort {
		return fmt.Errorf("port %d, expected 0 or 1-%d: %w", port, maxPort, ErrMalformed)
	}
	if p := m.GetHealthPath(); p != "" {
		if port == 0 {
			return fmt.Errorf("health_path %q set with port 0, expected a port: %w", p, ErrMalformed)
		}
		if n := len(p); n < 1 || n > maxHealthPathLen {
			return fmt.Errorf("health_path length %d, expected 1-%d bytes: %w", n, maxHealthPathLen, ErrMalformed)
		}
		if !strings.HasPrefix(p, "/") {
			return fmt.Errorf("health_path %q does not start with '/', expected an HTTP path: %w", p, ErrMalformed)
		}
		if strings.Trim(p, pathChars) != "" {
			return fmt.Errorf("health_path %q, expected only [A-Za-z0-9._~/-]: %w", p, ErrMalformed)
		}
	}
	domains := m.GetDomains()
	if n := len(domains); n > maxDomains {
		return fmt.Errorf("domains count %d, expected 0-%d: %w", n, maxDomains, ErrMalformed)
	}
	seen := make(map[string]int, len(domains))
	for i, d := range domains {
		if port == 0 {
			return fmt.Errorf("domains[%d] %q set with port 0, expected a port: %w", i, d, ErrMalformed)
		}
		if first, ok := seen[d]; ok {
			return fmt.Errorf("domains[%d] %q duplicates domains[%d]: %w", i, d, first, ErrMalformed)
		}
		seen[d] = i
		if err := validateDomain(i, d); err != nil {
			return err
		}
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
	if err := validateImage("image", m.GetImage()); err != nil {
		return err
	}
	if err := validateName("project", m.GetProject()); err != nil {
		return err
	}
	if err := validateName("environment", m.GetEnvironment()); err != nil {
		return err
	}
	if err := validateName("service", m.GetService()); err != nil {
		return err
	}
	return validateServiceSettings(m)
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

func validateStopApp(m *agentv1.StopApp) error {
	return validateID("app_id", m.GetAppId())
}

func validateRemoveApp(m *agentv1.RemoveApp) error {
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

func validateCommandResult(m *agentv1.CommandResult) error {
	switch m.GetOutcome() {
	case agentv1.CommandOutcome_SUCCEEDED,
		agentv1.CommandOutcome_FAILED,
		agentv1.CommandOutcome_REJECTED:
	case agentv1.CommandOutcome_COMMAND_OUTCOME_UNSPECIFIED:
		return fmt.Errorf("outcome unspecified, expected SUCCEEDED, FAILED or REJECTED: %w", ErrMalformed)
	default:
		return fmt.Errorf("outcome %d, expected SUCCEEDED, FAILED or REJECTED: %w", m.GetOutcome(), ErrMalformed)
	}
	if n := len(m.GetDetail()); n > maxDetailLen {
		return fmt.Errorf("detail length %d, expected 0-%d bytes: %w", n, maxDetailLen, ErrMalformed)
	}
	if m.GetOutcome() == agentv1.CommandOutcome_SUCCEEDED && m.GetDetail() != "" {
		return fmt.Errorf("detail length %d on outcome SUCCEEDED, expected empty: %w", len(m.GetDetail()), ErrMalformed)
	}
	if m.GetApp() == nil {
		return fmt.Errorf("app missing, expected the app's release state: %w", ErrMalformed)
	}
	if err := validateAppState(m.GetApp()); err != nil {
		return err
	}
	if s := m.GetImageId(); s != "" {
		if err := validateSHA256("image_id", s); err != nil {
			return err
		}
	}
	if s := m.GetRepoDigest(); s != "" {
		if err := validateRepoDigest(s); err != nil {
			return err
		}
	}
	return nil
}

func validateAppState(a *agentv1.AppState) error {
	for _, f := range []struct{ field, value string }{
		{"app.current_release_id", a.GetCurrentReleaseId()},
		{"app.previous_release_id", a.GetPreviousReleaseId()},
		{"app.candidate_release_id", a.GetCandidateReleaseId()},
	} {
		if f.value == "" {
			continue
		}
		if err := validateID(f.field, f.value); err != nil {
			return err
		}
	}
	switch a.GetPhase() {
	case agentv1.ReleasePhase_NONE, agentv1.ReleasePhase_PENDING, agentv1.ReleasePhase_READY:
		return nil
	case agentv1.ReleasePhase_RELEASE_PHASE_UNSPECIFIED:
		return fmt.Errorf("app.phase unspecified, expected NONE, PENDING or READY: %w", ErrMalformed)
	default:
		return fmt.Errorf("app.phase %d, expected NONE, PENDING or READY: %w", a.GetPhase(), ErrMalformed)
	}
}
