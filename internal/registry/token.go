package registry

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"moonglass.dev/moonglass/internal/store"
)

const (
	tokenPrefix    = "mg_"
	tokenSecretLen = 52
)

var tokenEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewToken returns "mg_" and 256 bits from crypto/rand as 52 lowercase base32 characters.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return tokenPrefix + strings.ToLower(tokenEncoding.EncodeToString(b)), nil
}

// MintToken creates an owner token and returns its text, which is shown once:
// only its hash, name and last four characters are stored. Owner tokens carry
// exactly one scope row, "* * *"; any other scope is rejected.
func MintToken(ctx context.Context, st store.Store, orgID, userID, name string, scopes []Scope, expires, now time.Time) (string, error) {
	if len(scopes) != 1 || scopes[0] != (Scope{Project: "*", Environment: "*", Operation: "*"}) {
		return "", fmt.Errorf("mint token: an owner token has exactly one scope row, \"* * *\", got %v", scopes)
	}
	if name == "" {
		return "", errors.New("mint token: name is empty: name the token after where it will be used")
	}
	if !expires.After(now) {
		return "", fmt.Errorf("mint token: expiry %s is not after now (%s): pick a later expiry", expires.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	text, err := NewToken()
	if err != nil {
		return "", err
	}
	id, err := store.NewID()
	if err != nil {
		return "", fmt.Errorf("mint token: %w", err)
	}
	t := store.Token{
		ID: id, OrgID: orgID, UserID: userID, Name: name, Last4: text[len(text)-4:],
		ExpiresAt: expires.UTC().Format(time.RFC3339), Scopes: scopes,
	}
	if err := st.AddToken(ctx, orgID, t, store.HashToken(text)); err != nil {
		return "", fmt.Errorf("mint token: %w", err)
	}
	return text, nil
}

// Authenticate resolves a bearer token to its principal. A malformed, unknown,
// expired or revoked token returns ErrUnauthenticated.
func (r *Registry) Authenticate(ctx context.Context, bearer string) (Principal, error) {
	if !strings.HasPrefix(bearer, tokenPrefix) || len(bearer) != len(tokenPrefix)+tokenSecretLen {
		return Principal{}, fmt.Errorf("%w: expected an mg_ token of %d characters", ErrUnauthenticated, len(tokenPrefix)+tokenSecretLen)
	}
	t, role, err := r.st.TokenByHash(ctx, store.HashToken(bearer))
	if errors.Is(err, store.ErrNotFound) {
		return Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate token: %w", err)
	}
	expires, err := time.Parse(time.RFC3339, t.ExpiresAt)
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate token %s: stored expiry %q is not RFC 3339: %w", t.ID, t.ExpiresAt, err)
	}
	if t.Revoked || !r.now().Before(expires) {
		return Principal{}, ErrUnauthenticated
	}
	return Principal{OrgID: t.OrgID, UserID: t.UserID, TokenID: t.ID, Role: role, Scopes: t.Scopes}, nil
}
