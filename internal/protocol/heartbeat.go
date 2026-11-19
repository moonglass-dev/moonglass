package protocol

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

const (
	maxDeployKeyLen = 256
	deployKeyPrefix = "ssh-ed25519 "
	deployKeySuffix = " moonglass-deploy"
	base64Chars     = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
)

// validateHeartbeat checks the server reports and the update channel.
// agent_version has no rule.
func validateHeartbeat(m *agentv1.Heartbeat) error {
	if err := validateUpdateChannel(m.GetUpdateChannel()); err != nil {
		return err
	}
	if disk := m.GetDisk(); disk != nil {
		if err := validateDiskReport(disk); err != nil {
			return err
		}
	}
	if key := m.GetDeployKey(); key != "" {
		return validateDeployKey(key)
	}
	return nil
}

// validateUpdateChannel checks the channel byte for byte: empty, STABLE or
// CANARY, so no case-folded or padded value reaches the control plane.
func validateUpdateChannel(s string) error {
	switch s {
	case "", "STABLE", "CANARY":
		return nil
	default:
		return fmt.Errorf("update_channel %s, expected empty, STABLE or CANARY: %w", quoteOrLen(s), ErrMalformed)
	}
}

// validateDiskReport checks the two filesystem pairs and the level. The level is
// not derived from the byte counts: hysteresis means one sample cannot give it.
func validateDiskReport(d *agentv1.DiskReport) error {
	if free, total := d.GetDataRootFreeBytes(), d.GetDataRootTotalBytes(); free > total {
		return fmt.Errorf("disk.data_root_free_bytes %d exceeds disk.data_root_total_bytes %d: %w", free, total, ErrMalformed)
	}
	if free, total := d.GetAgentDirFreeBytes(), d.GetAgentDirTotalBytes(); free > total {
		return fmt.Errorf("disk.agent_dir_free_bytes %d exceeds disk.agent_dir_total_bytes %d: %w", free, total, ErrMalformed)
	}
	switch d.GetLevel() {
	case agentv1.DiskLevel_OK, agentv1.DiskLevel_LOW, agentv1.DiskLevel_CRITICAL:
		return nil
	case agentv1.DiskLevel_DISK_LEVEL_UNSPECIFIED:
		return fmt.Errorf("disk.level unspecified, expected OK, LOW or CRITICAL: %w", ErrMalformed)
	default:
		return fmt.Errorf("disk.level %d, expected OK, LOW or CRITICAL: %w", d.GetLevel(), ErrMalformed)
	}
}

// validateDeployKey checks the shape of one OpenSSH line, never that the base64
// decodes to a well-formed Ed25519 wire key.
func validateDeployKey(s string) error {
	if n := len(s); n > maxDeployKeyLen {
		return fmt.Errorf("deploy_key length %d, expected at most %d bytes: %w", n, maxDeployKeyLen, ErrMalformed)
	}
	rest, ok := strings.CutPrefix(s, deployKeyPrefix)
	if !ok {
		return fmt.Errorf("deploy_key %q has no %q prefix, expected ssh-ed25519 <base64> moonglass-deploy: %w", s, deployKeyPrefix, ErrMalformed)
	}
	b64, ok := strings.CutSuffix(rest, deployKeySuffix)
	if !ok {
		return fmt.Errorf("deploy_key %q has no %q suffix, expected ssh-ed25519 <base64> moonglass-deploy: %w", s, deployKeySuffix, ErrMalformed)
	}
	if b64 == "" {
		return fmt.Errorf("deploy_key has empty base64, expected the public key: %w", ErrMalformed)
	}
	// The alphabet check runs before decoding: Go's decoder skips CR and LF even
	// in strict mode, so it would accept a newline inside the base64.
	if strings.Trim(b64, base64Chars) != "" {
		return fmt.Errorf("deploy_key base64 %q has a byte outside the standard alphabet: %w", b64, ErrMalformed)
	}
	if _, err := base64.StdEncoding.Strict().DecodeString(b64); err != nil {
		return fmt.Errorf("deploy_key base64 %q is not padded standard base64: %w", b64, errors.Join(ErrMalformed, err))
	}
	return nil
}
