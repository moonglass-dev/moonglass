package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Dialect holds what differs between backends. Queries are written once with
// $n placeholders, each used once and in order.
type Dialect struct {
	// SetOrg binds the transaction to orgID for row-level security. Nil on
	// backends without RLS.
	SetOrg func(ctx context.Context, tx *sql.Tx, orgID string) error
	// IsDuplicate reports a unique-constraint violation.
	IsDuplicate func(err error) bool
	// Rebind rewrites $n placeholders for the driver.
	Rebind func(query string) string
}

// SQLStore implements Store over database/sql. The backend packages construct it.
type SQLStore struct {
	db *sql.DB
	d  Dialect
}

// NewSQLStore wraps db, which must already be migrated.
func NewSQLStore(db *sql.DB, d Dialect) *SQLStore {
	return &SQLStore{db: db, d: d}
}

// Close closes the database handle.
func (s *SQLStore) Close() error { return s.db.Close() }

func (s *SQLStore) q(query string) string {
	if s.d.Rebind == nil {
		return query
	}
	return s.d.Rebind(query)
}

// inOrg runs fn in a transaction bound to orgID.
func (s *SQLStore) inOrg(ctx context.Context, orgID string, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if s.d.SetOrg != nil {
		if err := s.d.SetOrg(ctx, tx, orgID); err != nil {
			return fmt.Errorf("bind transaction to org: %w", err)
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *SQLStore) dupOr(err error, what string) error {
	if s.d.IsDuplicate != nil && s.d.IsDuplicate(err) {
		return fmt.Errorf("%s: %w", what, ErrDuplicate)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// CreateOrg implements Store.
func (s *SQLStore) CreateOrg(ctx context.Context) (string, error) {
	id, err := NewID()
	if err != nil {
		return "", err
	}
	err = s.inOrg(ctx, id, func(tx *sql.Tx) error {
		if _, insErr := tx.ExecContext(ctx, s.q("INSERT INTO orgs (id, org_id, created_at) VALUES ($1, $2, $3)"),
			id, id, formatTime(time.Now())); insErr != nil {
			return fmt.Errorf("insert org: %w", insErr)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// UserForIdentity implements Store.
func (s *SQLStore) UserForIdentity(ctx context.Context, identity string) (string, error) {
	if identity == "" {
		return "", errors.New("user for identity: identity is empty: pass provider:subject")
	}
	id, err := NewID()
	if err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx,
		s.q("INSERT INTO user_identities (identity, user_id) VALUES ($1, $2) ON CONFLICT (identity) DO NOTHING"),
		identity, id); err != nil {
		return "", fmt.Errorf("mint user for identity: %w", err)
	}
	var userID string
	if err := s.db.QueryRowContext(ctx, s.q("SELECT user_id FROM user_identities WHERE identity = $1"), identity).Scan(&userID); err != nil {
		return "", fmt.Errorf("read user for identity: %w", err)
	}
	return userID, nil
}

// AddMembership implements Store. The memberships row and the user_orgs
// lookup row are written in one transaction.
func (s *SQLStore) AddMembership(ctx context.Context, orgID, userID string, role Role) error {
	if !role.Valid() {
		return fmt.Errorf("add membership: role %q is not one of owner, admin, member, viewer", role)
	}
	return s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			s.q("INSERT INTO memberships (org_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)"),
			orgID, userID, string(role), formatTime(time.Now())); err != nil {
			return s.dupOr(err, "insert membership")
		}
		if _, err := tx.ExecContext(ctx,
			s.q("INSERT INTO user_orgs (user_id, org_id) VALUES ($1, $2)"), userID, orgID); err != nil {
			return s.dupOr(err, "insert user org")
		}
		return nil
	})
}

// Memberships implements Store.
func (s *SQLStore) Memberships(ctx context.Context, orgID string) ([]Membership, error) {
	var out []Membership
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, s.q("SELECT org_id, user_id, role FROM memberships WHERE org_id = $1 ORDER BY user_id"), orgID)
		if err != nil {
			return fmt.Errorf("select memberships: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var m Membership
			var role string
			if err := rows.Scan(&m.OrgID, &m.UserID, &role); err != nil {
				return fmt.Errorf("scan membership: %w", err)
			}
			m.Role = Role(role)
			out = append(out, m)
		}
		return rows.Err()
	})
	return out, err
}

// UserOrgs implements Store. It reads a lookup table, so no org is bound.
func (s *SQLStore) UserOrgs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, s.q("SELECT org_id FROM user_orgs WHERE user_id = $1 ORDER BY org_id"), userID)
	if err != nil {
		return nil, fmt.Errorf("select user orgs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var org string
		if err := rows.Scan(&org); err != nil {
			return nil, fmt.Errorf("scan user org: %w", err)
		}
		out = append(out, org)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select user orgs: %w", err)
	}
	return out, nil
}

// AddServer implements Store. The signing public key is stored only in
// agent_keys; the age recipient only on servers.
func (s *SQLStore) AddServer(ctx context.Context, orgID, publicKey, ageRecipient string) (Server, error) {
	if ageRecipient == "" {
		return Server{}, errors.New("add server: agent age recipient is empty: pass the age1… recipient the agent sent at enrolment")
	}
	id, err := NewID()
	if err != nil {
		return Server{}, err
	}
	srv := Server{OrgID: orgID, ID: id, PublicKey: publicKey, AgeRecipient: ageRecipient, CreatedAt: formatTime(time.Now())}
	err = s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		if _, insErr := tx.ExecContext(ctx, s.q("INSERT INTO servers (id, org_id, created_at, agent_age_recipient) VALUES ($1, $2, $3, $4)"),
			srv.ID, orgID, srv.CreatedAt, srv.AgeRecipient); insErr != nil {
			return s.dupOr(insErr, "insert server")
		}
		if _, insErr := tx.ExecContext(ctx, s.q("INSERT INTO agent_keys (public_key, server_id, org_id) VALUES ($1, $2, $3)"),
			publicKey, srv.ID, orgID); insErr != nil {
			return s.dupOr(insErr, "insert agent key")
		}
		return nil
	})
	if err != nil {
		return Server{}, err
	}
	return srv, nil
}

// ServerByPublicKey implements Store.
func (s *SQLStore) ServerByPublicKey(ctx context.Context, publicKey string) (Server, error) {
	srv := Server{PublicKey: publicKey}
	err := s.db.QueryRowContext(ctx,
		s.q("SELECT org_id, server_id FROM agent_keys WHERE public_key = $1 AND revoked_at IS NULL"), publicKey).
		Scan(&srv.OrgID, &srv.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, fmt.Errorf("server by public key: %w", ErrNotFound)
	}
	if err != nil {
		return Server{}, fmt.Errorf("select agent key: %w", err)
	}
	err = s.inOrg(ctx, srv.OrgID, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, s.q("SELECT created_at, agent_age_recipient FROM servers WHERE id = $1 AND org_id = $2"), srv.ID, srv.OrgID).
			Scan(&srv.CreatedAt, &srv.AgeRecipient)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, fmt.Errorf("server by public key: %w", ErrNotFound)
	}
	if err != nil {
		return Server{}, fmt.Errorf("select server: %w", err)
	}
	return srv, nil
}

// Servers implements Store.
func (s *SQLStore) Servers(ctx context.Context, orgID string) ([]Server, error) {
	var out []Server
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, s.q(`SELECT s.id, s.org_id, s.created_at, k.public_key, s.agent_age_recipient
			FROM servers s JOIN agent_keys k ON k.server_id = s.id
			WHERE s.org_id = $1 AND k.revoked_at IS NULL ORDER BY s.id`), orgID)
		if err != nil {
			return fmt.Errorf("select servers: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var srv Server
			if err := rows.Scan(&srv.ID, &srv.OrgID, &srv.CreatedAt, &srv.PublicKey, &srv.AgeRecipient); err != nil {
				return fmt.Errorf("scan server: %w", err)
			}
			out = append(out, srv)
		}
		return rows.Err()
	})
	return out, err
}

// AddEnrolmentToken implements Store.
func (s *SQLStore) AddEnrolmentToken(ctx context.Context, orgID, tokenHash string, expires time.Time) error {
	if _, err := s.db.ExecContext(ctx,
		s.q("INSERT INTO enrolment_token_hashes (token_hash, org_id, expires_at) VALUES ($1, $2, $3)"),
		tokenHash, orgID, formatTime(expires)); err != nil {
		return s.dupOr(err, "insert enrolment token")
	}
	return nil
}

// UseEnrolmentToken implements Store. The update is one statement, so a token
// is used at most once even under concurrent callers.
func (s *SQLStore) UseEnrolmentToken(ctx context.Context, tokenHash string, now time.Time) (string, error) {
	var orgID string
	stamp := formatTime(now)
	err := s.db.QueryRowContext(ctx, s.q(`UPDATE enrolment_token_hashes SET used_at = $1
		WHERE token_hash = $2 AND used_at IS NULL AND expires_at > $3 RETURNING org_id`),
		stamp, tokenHash, stamp).Scan(&orgID)
	if err == nil {
		return orgID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("use enrolment token: %w", err)
	}
	var used sql.NullString
	err = s.db.QueryRowContext(ctx, s.q("SELECT used_at FROM enrolment_token_hashes WHERE token_hash = $1"), tokenHash).Scan(&used)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("use enrolment token: %w", ErrNotFound)
	case err != nil:
		return "", fmt.Errorf("read enrolment token: %w", err)
	case used.Valid:
		return "", fmt.Errorf("use enrolment token: %w", ErrTokenUsed)
	}
	return "", fmt.Errorf("use enrolment token: %w", ErrTokenExpired)
}

// EnrolmentTokens implements Store.
func (s *SQLStore) EnrolmentTokens(ctx context.Context, orgID string) ([]TokenInfo, error) {
	rows, err := s.db.QueryContext(ctx,
		s.q("SELECT org_id, expires_at, used_at IS NOT NULL FROM enrolment_token_hashes WHERE org_id = $1 ORDER BY expires_at"), orgID)
	if err != nil {
		return nil, fmt.Errorf("select enrolment tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []TokenInfo
	for rows.Next() {
		var t TokenInfo
		if err := rows.Scan(&t.OrgID, &t.ExpiresAt, &t.Used); err != nil {
			return nil, fmt.Errorf("scan enrolment token: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("select enrolment tokens: %w", err)
	}
	return out, nil
}

// RebindQuestion rewrites $n placeholders to ?, for drivers that use them.
// Each placeholder must appear once and in order.
func RebindQuestion(query string) string {
	var b strings.Builder
	for i := 0; i < len(query); i++ {
		if query[i] != '$' {
			b.WriteByte(query[i])
			continue
		}
		b.WriteByte('?')
		for i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9' {
			i++
		}
	}
	return b.String()
}
