// Package store is the control-plane storage interface (ADR-0002). Only this
// package and its subpackages write SQL.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// Role is a membership role (ADR-0006).
type Role string

// The four roles of ADR-0006.
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	RoleViewer Role = "viewer"
)

// Valid reports whether r is one of the four ADR-0006 roles.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleMember, RoleViewer:
		return true
	}
	return false
}

// Errors returned by Store methods. Test them with errors.Is.
var (
	ErrNotFound     = errors.New("store: not found")
	ErrDuplicate    = errors.New("store: already exists")
	ErrTokenUsed    = errors.New("store: enrolment token already used")
	ErrTokenExpired = errors.New("store: enrolment token expired")
)

// Membership is one user's role in one org.
type Membership struct {
	OrgID  string
	UserID string
	Role   Role
}

// Server is a registered server and its agent public key.
type Server struct {
	OrgID     string
	ID        string
	PublicKey string
	CreatedAt string
}

// TokenInfo describes an enrolment token without revealing its hash.
type TokenInfo struct {
	OrgID     string
	ExpiresAt string
	Used      bool
}

// Scope is one token scope row; "*" matches anything (ADR-0006 decision 3).
type Scope struct {
	Project     string
	Environment string
	Operation   string
}

// Token is an API token without its secret: the secret is stored only as a
// hash. Revoked is read from the lookup row.
type Token struct {
	ID        string
	OrgID     string
	UserID    string
	Name      string
	Last4     string
	ExpiresAt string
	Scopes    []Scope
	Revoked   bool
}

// AuditEvent is one hash-chained audit row. PrevHash is empty for Seq 1.
type AuditEvent struct {
	OrgID         string
	ID            string
	At            string
	PrincipalKind string
	UserID        string
	TokenID       string
	Operation     string
	Outcome       string
	Reason        string
	PrevHash      string
	Hash          string
	Seq           int64
}

// Store is the storage interface both backends implement.
type Store interface {
	Close() error
	// CreateOrg creates an org and returns its id.
	CreateOrg(ctx context.Context) (orgID string, err error)
	// UserForIdentity returns the user id for an external identity and mints
	// one the first time the identity is seen.
	UserForIdentity(ctx context.Context, identity string) (userID string, err error)
	AddMembership(ctx context.Context, orgID, userID string, role Role) error
	Memberships(ctx context.Context, orgID string) ([]Membership, error)
	UserOrgs(ctx context.Context, userID string) ([]string, error)
	AddServer(ctx context.Context, orgID, publicKey string) (Server, error)
	ServerByPublicKey(ctx context.Context, publicKey string) (Server, error)
	Servers(ctx context.Context, orgID string) ([]Server, error)
	// AddEnrolmentToken stores tokenHash (see HashToken); the caller chooses expires.
	AddEnrolmentToken(ctx context.Context, orgID, tokenHash string, expires time.Time) error
	// UseEnrolmentToken marks the token used and returns its org. It returns
	// ErrNotFound, ErrTokenUsed or ErrTokenExpired when the token cannot be used.
	UseEnrolmentToken(ctx context.Context, tokenHash string, now time.Time) (orgID string, err error)
	EnrolmentTokens(ctx context.Context, orgID string) ([]TokenInfo, error)
	// AddToken stores t, its scope rows and tokenHash (see HashToken).
	AddToken(ctx context.Context, orgID string, t Token, tokenHash string) error
	// TokenByHash finds a token by the hash of its text and returns it with
	// its holder's role. It returns ErrNotFound when no token or membership matches.
	TokenByHash(ctx context.Context, tokenHash string) (Token, Role, error)
	// AppendAudit adds the event that seal builds for the next sequence number
	// and the previous hash, in one transaction so the chain stays serial.
	AppendAudit(ctx context.Context, orgID string, seal func(prevHash string, seq int64) AuditEvent) error
	// AuditEvents returns the org's events in sequence order.
	AuditEvents(ctx context.Context, orgID string) ([]AuditEvent, error)
}

// HashToken returns the hex SHA-256 of a bearer token. Only this value is stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewID returns 16 random bytes as 32 lowercase hex characters.
func NewID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// formatTime renders a timestamp as UTC RFC3339 text.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}
