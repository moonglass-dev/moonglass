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

// ReleaseStatus is the queue state of one release.
type ReleaseStatus string

// The six release statuses.
const (
	ReleaseQueued     ReleaseStatus = "queued"
	ReleaseRunning    ReleaseStatus = "running"
	ReleaseSucceeded  ReleaseStatus = "succeeded"
	ReleaseFailed     ReleaseStatus = "failed"
	ReleaseRejected   ReleaseStatus = "rejected"
	ReleaseSuperseded ReleaseStatus = "superseded"
)

// ReleaseStep is the command in flight on a running release.
type ReleaseStep string

// The four release steps.
const (
	StepBuild     ReleaseStep = "build"
	StepDeploy    ReleaseStep = "deploy"
	StepPreDeploy ReleaseStep = "pre_deploy"
	StepSwitch    ReleaseStep = "switch"
)

// Valid reports whether s is one of the four release steps.
func (s ReleaseStep) Valid() bool {
	switch s {
	case StepBuild, StepDeploy, StepPreDeploy, StepSwitch:
		return true
	}
	return false
}

// Errors returned by Store methods. Test them with errors.Is.
var (
	ErrNotFound      = errors.New("store: not found")
	ErrDuplicate     = errors.New("store: already exists")
	ErrInvalidName   = errors.New("store: invalid name")
	ErrTokenUsed     = errors.New("store: token already used")
	ErrTokenExpired  = errors.New("store: token expired")
	ErrReleaseState  = errors.New("store: release is not in a status that allows this")
	ErrRollbackState = errors.New("store: rollback is not in a status that allows this")
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

// Project is one project of an org.
type Project struct {
	OrgID     string
	ID        string
	Name      string
	CreatedAt string
}

// Environment is one environment of a project.
type Environment struct {
	OrgID     string
	ID        string
	ProjectID string
	Name      string
	CreatedAt string
}

// Service is one service of an environment, on exactly one server.
// CurrentReleaseID and PreviousReleaseID copy the app state of the latest
// CommandResult; both are empty when there is no such release.
type Service struct {
	OrgID             string
	ID                string
	ProjectID         string
	EnvironmentID     string
	Name              string
	ServerID          string
	CurrentReleaseID  string
	PreviousReleaseID string
	CreatedAt         string
}

// Release is one queued, running or finished deploy of a service. Domains is
// the host list the release answers on; PreDeploy is the argv run before the
// switch, or nil for none. The fields are ordered so the garbage collector
// scans the fewest bytes.
type Release struct {
	OrgID         string
	ID            string
	ServiceID     string
	ImageRef      string
	GitRepository string
	GitCommit     string
	HealthPath    string
	Status        ReleaseStatus
	Step          ReleaseStep
	RequestID     string
	ImageID       string
	RepoDigest    string
	Detail        string
	CreatedAt     string
	FinishedAt    string
	Domains       []string
	PreDeploy     []string
	Port          int
	// PreDeployReversible is last so the struct packs without padding.
	PreDeployReversible bool
}

// Rollback is one rollback of a service, from its start to its final status.
// Its Status is running, succeeded, failed or rejected, from ReleaseStatus;
// FinishedAt is empty until the rollback is final.
type Rollback struct {
	OrgID      string
	ID         string
	ServiceID  string
	RequestID  string
	Status     ReleaseStatus
	Detail     string
	CreatedAt  string
	FinishedAt string
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

// Session is one sign-in session without its bearer: the bearer is stored only
// as HashToken in session_hashes, so this type has no hash field.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt string
}

// LinkToken is one link token without its bearer: the bearer is stored only as
// its HashToken in link_token_hashes, so this type has no hash field.
type LinkToken struct {
	ID        string
	UserID    string
	ExpiresAt string
}

// StepUp is one step-up request. It binds one user, one credential (exactly one
// of TokenID and SessionID), one operation, one target and one input digest. An
// unset marker column reads back as empty text. There is no confirmation-hash
// field: this ticket never reads or writes confirm_hash.
type StepUp struct {
	OrgID         string
	ID            string
	UserID        string
	TokenID       string
	SessionID     string
	Operation     string
	ProjectID     string
	EnvironmentID string
	InputDigest   string
	CreatedAt     string
	ExpiresAt     string
	SignedInAt    string
	ApprovedAt    string
	DeclinedAt    string
	UsedAt        string
}

// AuditEvent is one hash-chained audit row. PrevHash is empty for Seq 1.
type AuditEvent struct {
	OrgID         string
	ID            string
	At            string
	PrincipalKind string
	UserID        string
	TokenID       string
	SessionID     string
	StepUpID      string
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
	// CreateProject returns ErrInvalidName for a name outside the name format
	// and ErrDuplicate when the org already has that name.
	CreateProject(ctx context.Context, orgID, name string) (Project, error)
	// Projects returns the org's projects ordered by name.
	Projects(ctx context.Context, orgID string) ([]Project, error)
	// CreateEnvironment returns ErrInvalidName for a name outside the name
	// format, ErrNotFound when projectID is not in orgID, and ErrDuplicate for
	// a repeated name in the project.
	CreateEnvironment(ctx context.Context, orgID, projectID, name string) (Environment, error)
	// Environments returns the project's environments ordered by name.
	Environments(ctx context.Context, orgID, projectID string) ([]Environment, error)
	// CreateService returns ErrInvalidName for a name outside the name format,
	// ErrNotFound when environmentID or serverID is not in orgID, and
	// ErrDuplicate for a repeated name in the environment.
	CreateService(ctx context.Context, orgID, environmentID, name, serverID string) (Service, error)
	// Services returns the environment's services ordered by name.
	Services(ctx context.Context, orgID, environmentID string) ([]Service, error)
	// ResolveTarget resolves project and environment names to their ids in the
	// org. An empty environment resolves the project only and returns an empty
	// environment id. ErrNotFound when either name is not the org's.
	ResolveTarget(ctx context.Context, orgID, project, environment string) (projectID, environmentID string, err error)
	// EnqueueRelease queues r on its service, superseding the queued release in
	// the same transaction and retrying a lost race; r's status, step, ids,
	// detail and times are ignored, and ErrNotFound means the service is not
	// the org's.
	EnqueueRelease(ctx context.Context, orgID string, r Release, now time.Time) (Release, error)
	// StartRelease moves a queued release to running; ErrNotFound for another
	// org's release, ErrReleaseState when it is not queued or one runs.
	StartRelease(ctx context.Context, orgID, releaseID string) error
	// SetReleaseStep records the step in flight and its request id on a running
	// release; ErrNotFound, ErrReleaseState when not running, and an error
	// naming the four steps for an unknown one.
	SetReleaseStep(ctx context.Context, orgID, releaseID string, step ReleaseStep, requestID string) error
	// SetReleaseImage records what the agent's CommandResult reported on a
	// running release; ErrNotFound, ErrReleaseState when not running.
	SetReleaseImage(ctx context.Context, orgID, releaseID, imageID, repoDigest string) error
	// FinishRelease ends a queued or running release as succeeded, failed or
	// rejected with detail and finished_at now; ErrNotFound, ErrReleaseState
	// when final, and an error naming the three final statuses otherwise.
	FinishRelease(ctx context.Context, orgID, releaseID string, status ReleaseStatus, detail string, now time.Time) error
	// Release returns one release of the org. ErrNotFound when it is not the
	// org's.
	Release(ctx context.Context, orgID, releaseID string) (Release, error)
	// Releases returns the service's releases newest first, at most 50.
	Releases(ctx context.Context, orgID, serviceID string) ([]Release, error)
	// InFlightReleases returns the queued and running releases of the org's
	// services on serverID, oldest first. Another org's server returns none.
	InFlightReleases(ctx context.Context, orgID, serverID string) ([]Release, error)
	// ServiceByName returns the service of that name in the environment.
	// ErrNotFound when no service of that name is in that environment of the org.
	ServiceByName(ctx context.Context, orgID, environmentID, name string) (Service, error)
	// SetServiceReleases writes both release ids of one service in one UPDATE.
	// An empty previousReleaseID is stored as NULL. The ids are copied, never
	// interpreted. ErrNotFound when the service is not the org's.
	SetServiceReleases(ctx context.Context, orgID, serviceID, currentReleaseID, previousReleaseID string) error
	// StartRollback records a running rollback for a service of the org at now
	// and returns the stored row. ErrNotFound when the service is not the org's.
	StartRollback(ctx context.Context, orgID, serviceID, requestID string, now time.Time) (Rollback, error)
	// FinishRollback ends a running rollback as succeeded, failed or rejected
	// with detail and finished_at now, and returns the finished row. ErrNotFound
	// when it is not the org's, ErrRollbackState when it is already final, and
	// an error naming the three final statuses otherwise.
	FinishRollback(ctx context.Context, orgID, rollbackID string, status ReleaseStatus, detail string, now time.Time) (Rollback, error)
	// InFlightRollbacks returns the running rollbacks of the org's services on
	// serverID, oldest first. Another org's server returns none.
	InFlightRollbacks(ctx context.Context, orgID, serverID string) ([]Rollback, error)
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
	// AddSession stores a session of userID under sessionHash (see HashToken)
	// and returns it; a repeated hash is ErrDuplicate.
	AddSession(ctx context.Context, userID, sessionHash string, expires time.Time) (Session, error)
	// SessionByHash returns the session of sessionHash while it is live at now.
	// It returns ErrNotFound for an unknown, revoked or expired session.
	SessionByHash(ctx context.Context, sessionHash string, now time.Time) (Session, error)
	// RevokeSession stamps revoked_at on one of userID's sessions. It returns
	// nil for a session already revoked, and ErrNotFound when no session with
	// that id belongs to userID.
	RevokeSession(ctx context.Context, userID, sessionID string, now time.Time) error
	// AddLinkToken stores tokenHash (see HashToken) for userID and returns the
	// link token; a repeated hash is ErrDuplicate.
	AddLinkToken(ctx context.Context, userID, tokenHash string, expires time.Time) (LinkToken, error)
	// UseLinkToken marks a link token used and returns it. It returns
	// ErrNotFound, ErrTokenUsed or ErrTokenExpired when the token cannot be used.
	UseLinkToken(ctx context.Context, linkID string, now time.Time) (LinkToken, error)
	// CreateStepUp stores one step-up in the org, bound to one user, one
	// credential, one operation, one target and one input digest, with
	// created_at now and expires_at the given expiry. The caller's org, id,
	// times and markers are ignored. An empty user, operation or digest, or
	// credentials that are both set or both empty, are refused before any SQL
	// runs.
	CreateStepUp(ctx context.Context, orgID string, su StepUp, now, expires time.Time) (StepUp, error)
	// StepUp returns one step-up of the org. ErrNotFound when it is not the
	// org's.
	StepUp(ctx context.Context, orgID, id string) (StepUp, error)
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
