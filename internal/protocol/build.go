package protocol

import (
	"fmt"
	"strings"

	agentv1 "moonglass.dev/moonglass/internal/protocol/agentv1"
)

const (
	maxOwnerLen  = 39
	maxRepoLen   = 100
	commitHexLen = 40

	ownerChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-"
	repoChars  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
)

// validateBuild checks the identity, the repository and the commit of a build.
func validateBuild(m *agentv1.Build) error {
	if err := validateID("app_id", m.GetAppId()); err != nil {
		return err
	}
	if err := validateID("release_id", m.GetReleaseId()); err != nil {
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
	if err := validateRepository(m.GetRepository()); err != nil {
		return err
	}
	return validateCommit(m.GetCommit())
}

// validateRepository checks <owner>/<repo>, the only repository text that reaches
// the clone URL the agent builds itself.
func validateRepository(s string) error {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return fmt.Errorf("repository %q has %d '/', expected exactly one in <owner>/<repo>: %w", s, len(parts)-1, ErrMalformed)
	}
	owner, repo := parts[0], parts[1]
	if n := len(owner); n < 1 || n > maxOwnerLen {
		return fmt.Errorf("repository owner length %d, expected 1-%d bytes: %w", n, maxOwnerLen, ErrMalformed)
	}
	for i := 0; i < len(owner); i++ {
		if c := owner[i]; !strings.ContainsRune(ownerChars, rune(c)) {
			return fmt.Errorf("repository owner has byte %#x at %d, expected only [A-Za-z0-9-]: %w", c, i, ErrMalformed)
		}
	}
	if owner[0] == '-' {
		return fmt.Errorf("repository owner %q starts with '-', expected a letter or digit: %w", owner, ErrMalformed)
	}
	if n := len(repo); n < 1 || n > maxRepoLen {
		return fmt.Errorf("repository repo length %d, expected 1-%d bytes: %w", n, maxRepoLen, ErrMalformed)
	}
	for i := 0; i < len(repo); i++ {
		if c := repo[i]; !strings.ContainsRune(repoChars, rune(c)) {
			return fmt.Errorf("repository repo has byte %#x at %d, expected only [A-Za-z0-9._-]: %w", c, i, ErrMalformed)
		}
	}
	if repo[0] == '-' || repo[0] == '.' {
		return fmt.Errorf("repository repo %q starts with %q, expected a letter or digit: %w", repo, repo[0], ErrMalformed)
	}
	return nil
}

// validateCommit checks exactly 40 lowercase hex characters: no branch, tag or
// option name can reach git.
func validateCommit(s string) error {
	if n := len(s); n != commitHexLen {
		return fmt.Errorf("commit length %d, expected %d lowercase hex characters: %w", n, commitHexLen, ErrMalformed)
	}
	if strings.Trim(s, hexChars) != "" {
		return fmt.Errorf("commit %q has a byte outside lowercase hex: %w", s, ErrMalformed)
	}
	return nil
}
