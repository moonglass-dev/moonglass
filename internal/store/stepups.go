package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// stepUpColumns is every column of step_ups that reads back. Each nullable
// marker column is coalesced to empty text, and confirm_hash is never selected.
const stepUpColumns = `org_id, id, user_id, token_id, session_id, operation,
	project_id, environment_id, input_digest, created_at, expires_at,
	COALESCE(signed_in_at, ''), COALESCE(approved_at, ''),
	COALESCE(declined_at, ''), COALESCE(used_at, '')`

// scanStepUp reads one row of stepUpColumns.
func scanStepUp(row interface{ Scan(...any) error }) (StepUp, error) {
	var su StepUp
	if err := row.Scan(&su.OrgID, &su.ID, &su.UserID, &su.TokenID, &su.SessionID,
		&su.Operation, &su.ProjectID, &su.EnvironmentID, &su.InputDigest,
		&su.CreatedAt, &su.ExpiresAt, &su.SignedInAt, &su.ApprovedAt,
		&su.DeclinedAt, &su.UsedAt); err != nil {
		return StepUp{}, fmt.Errorf("scan step-up: %w", err)
	}
	return su, nil
}

// CreateStepUp implements Store. Every binding rule is checked before any SQL
// runs, so a refused step-up writes no row. The stored row is built from the
// input's binding fields only: the caller's org, id, times and markers are
// dropped, and confirm_hash and the markers stay NULL.
func (s *SQLStore) CreateStepUp(ctx context.Context, orgID string, su StepUp, now, expires time.Time) (StepUp, error) {
	if su.UserID == "" {
		return StepUp{}, errors.New("create step-up: user id is empty: a step-up binds one user")
	}
	if su.Operation == "" {
		return StepUp{}, errors.New("create step-up: operation is empty: a step-up binds one operation")
	}
	if su.InputDigest == "" {
		return StepUp{}, errors.New("create step-up: input digest is empty: a step-up binds one input digest")
	}
	if (su.TokenID == "") == (su.SessionID == "") {
		got := "neither"
		if su.TokenID != "" {
			got = "both"
		}
		return StepUp{}, fmt.Errorf("create step-up: exactly one of token id and session id must be set, got %s", got)
	}
	id, err := NewID()
	if err != nil {
		return StepUp{}, err
	}
	out := StepUp{
		OrgID:         orgID,
		ID:            id,
		UserID:        su.UserID,
		TokenID:       su.TokenID,
		SessionID:     su.SessionID,
		Operation:     su.Operation,
		ProjectID:     su.ProjectID,
		EnvironmentID: su.EnvironmentID,
		InputDigest:   su.InputDigest,
		CreatedAt:     formatTime(now),
		ExpiresAt:     formatTime(expires),
	}
	err = s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		if _, insErr := tx.ExecContext(ctx, s.q(`INSERT INTO step_ups (org_id, id, user_id, token_id,
				session_id, operation, project_id, environment_id, input_digest, created_at, expires_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`),
			out.OrgID, out.ID, out.UserID, out.TokenID, out.SessionID, out.Operation,
			out.ProjectID, out.EnvironmentID, out.InputDigest, out.CreatedAt, out.ExpiresAt); insErr != nil {
			return s.dupOr(insErr, "insert step-up")
		}
		return nil
	})
	if err != nil {
		return StepUp{}, err
	}
	return out, nil
}

// StepUp implements Store. The row is read inside the org-bound transaction
// with org_id in the WHERE, so another org's step-up is ErrNotFound.
func (s *SQLStore) StepUp(ctx context.Context, orgID, id string) (StepUp, error) {
	var out StepUp
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		su, scanErr := scanStepUp(tx.QueryRowContext(ctx,
			s.q("SELECT "+stepUpColumns+" FROM step_ups WHERE id = $1 AND org_id = $2"), id, orgID))
		if errors.Is(scanErr, sql.ErrNoRows) {
			return fmt.Errorf("step-up %s is not in org %s: %w", id, orgID, ErrNotFound)
		}
		if scanErr != nil {
			return scanErr
		}
		out = su
		return nil
	})
	if err != nil {
		return StepUp{}, err
	}
	return out, nil
}
