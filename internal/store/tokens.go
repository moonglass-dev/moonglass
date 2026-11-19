package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// auditRetries bounds how often AppendAudit retries after losing a race for
// the next sequence number.
const auditRetries = 5

// AddToken implements Store. The token, its scope rows and its lookup row are
// written in one transaction.
func (s *SQLStore) AddToken(ctx context.Context, orgID string, t Token, tokenHash string) error {
	return s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			s.q("INSERT INTO tokens (id, org_id, user_id, name, last4, expires_at, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)"),
			t.ID, orgID, t.UserID, t.Name, t.Last4, t.ExpiresAt, formatTime(time.Now())); err != nil {
			return s.dupOr(err, "insert token")
		}
		for _, sc := range t.Scopes {
			if _, err := tx.ExecContext(ctx,
				s.q("INSERT INTO token_scopes (token_id, org_id, project, environment, operation) VALUES ($1, $2, $3, $4, $5)"),
				t.ID, orgID, sc.Project, sc.Environment, sc.Operation); err != nil {
				return s.dupOr(err, "insert token scope")
			}
		}
		if _, err := tx.ExecContext(ctx,
			s.q("INSERT INTO token_hashes (hash, org_id, token_id) VALUES ($1, $2, $3)"),
			tokenHash, orgID, t.ID); err != nil {
			return s.dupOr(err, "insert token hash")
		}
		return nil
	})
}

// TokenByHash implements Store. It reads the lookup row first, then the token,
// its scope rows and the holder's membership inside the token's org.
func (s *SQLStore) TokenByHash(ctx context.Context, tokenHash string) (Token, Role, error) {
	var t Token
	var revoked sql.NullString
	hashErr := s.db.QueryRowContext(ctx,
		s.q("SELECT org_id, token_id, revoked_at FROM token_hashes WHERE hash = $1"), tokenHash).
		Scan(&t.OrgID, &t.ID, &revoked)
	if errors.Is(hashErr, sql.ErrNoRows) {
		return Token{}, "", fmt.Errorf("token by hash: %w", ErrNotFound)
	}
	if hashErr != nil {
		return Token{}, "", fmt.Errorf("select token hash: %w", hashErr)
	}
	t.Revoked = revoked.Valid
	var role string
	err := s.inOrg(ctx, t.OrgID, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			s.q(`SELECT t.user_id, t.name, t.last4, t.expires_at, m.role
				FROM tokens t JOIN memberships m ON m.org_id = t.org_id AND m.user_id = t.user_id
				WHERE t.id = $1 AND t.org_id = $2`), t.ID, t.OrgID).
			Scan(&t.UserID, &t.Name, &t.Last4, &t.ExpiresAt, &role); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			s.q("SELECT project, environment, operation FROM token_scopes WHERE token_id = $1 AND org_id = $2 ORDER BY project, environment, operation"),
			t.ID, t.OrgID)
		if err != nil {
			return fmt.Errorf("select token scopes: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var sc Scope
			if err := rows.Scan(&sc.Project, &sc.Environment, &sc.Operation); err != nil {
				return fmt.Errorf("scan token scope: %w", err)
			}
			t.Scopes = append(t.Scopes, sc)
		}
		return rows.Err()
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Token{}, "", fmt.Errorf("token by hash: token %s has no membership in its org: %w", t.ID, ErrNotFound)
	}
	if err != nil {
		return Token{}, "", fmt.Errorf("read token: %w", err)
	}
	return t, Role(role), nil
}

// AppendAudit implements Store. A lost race for the sequence number shows up
// as a duplicate key and is retried with a fresh read of the chain head.
func (s *SQLStore) AppendAudit(ctx context.Context, orgID string, seal func(prevHash string, seq int64) AuditEvent) error {
	var err error
	for range auditRetries {
		if err = s.appendAuditOnce(ctx, orgID, seal); !errors.Is(err, ErrDuplicate) {
			return err
		}
	}
	return fmt.Errorf("append audit event: sequence kept colliding after %d attempts: %w", auditRetries, err)
}

func (s *SQLStore) appendAuditOnce(ctx context.Context, orgID string, seal func(prevHash string, seq int64) AuditEvent) error {
	return s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		var seq int64
		var prev string
		err := tx.QueryRowContext(ctx,
			s.q("SELECT seq, hash FROM audit_events WHERE org_id = $1 ORDER BY seq DESC LIMIT 1"), orgID).
			Scan(&seq, &prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read audit chain head: %w", err)
		}
		ev := seal(prev, seq+1)
		if _, err := tx.ExecContext(ctx,
			s.q(`INSERT INTO audit_events (org_id, seq, id, at, principal_kind, user_id, token_id, session_id, step_up_id, operation, outcome, reason, prev_hash, hash)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`),
			orgID, ev.Seq, ev.ID, ev.At, ev.PrincipalKind, ev.UserID, ev.TokenID, ev.SessionID, ev.StepUpID,
			ev.Operation, ev.Outcome, ev.Reason, ev.PrevHash, ev.Hash); err != nil {
			return s.dupOr(err, "insert audit event")
		}
		return nil
	})
}

// AuditEvents implements Store.
func (s *SQLStore) AuditEvents(ctx context.Context, orgID string) ([]AuditEvent, error) {
	var out []AuditEvent
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			s.q(`SELECT org_id, seq, id, at, principal_kind, user_id, token_id, session_id, step_up_id, operation, outcome, reason, prev_hash, hash
				FROM audit_events WHERE org_id = $1 ORDER BY seq`), orgID)
		if err != nil {
			return fmt.Errorf("select audit events: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var ev AuditEvent
			if err := rows.Scan(&ev.OrgID, &ev.Seq, &ev.ID, &ev.At, &ev.PrincipalKind, &ev.UserID, &ev.TokenID,
				&ev.SessionID, &ev.StepUpID, &ev.Operation, &ev.Outcome, &ev.Reason, &ev.PrevHash, &ev.Hash); err != nil {
				return fmt.Errorf("scan audit event: %w", err)
			}
			out = append(out, ev)
		}
		return rows.Err()
	})
	return out, err
}
