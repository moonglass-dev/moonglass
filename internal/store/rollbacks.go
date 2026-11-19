package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// rollbackColumns is every column of rollbacks, qualified for the join.
const rollbackColumns = `b.id, b.org_id, b.service_id, b.request_id, b.status,
	b.detail, b.created_at, b.finished_at`

// scanRollback reads one row of rollbackColumns.
func scanRollback(row interface{ Scan(...any) error }) (Rollback, error) {
	var rb Rollback
	var status string
	if err := row.Scan(&rb.ID, &rb.OrgID, &rb.ServiceID, &rb.RequestID, &status,
		&rb.Detail, &rb.CreatedAt, &rb.FinishedAt); err != nil {
		return Rollback{}, fmt.Errorf("scan rollback: %w", err)
	}
	rb.Status = ReleaseStatus(status)
	return rb, nil
}

// StartRollback implements Store. The service is read inside the org-bound
// transaction (Postgres referential checks do not apply row-level security).
func (s *SQLStore) StartRollback(ctx context.Context, orgID, serviceID, requestID string, now time.Time) (Rollback, error) {
	id, err := NewID()
	if err != nil {
		return Rollback{}, err
	}
	rb := Rollback{
		OrgID: orgID, ID: id, ServiceID: serviceID, RequestID: requestID,
		Status: ReleaseRunning, CreatedAt: formatTime(now),
	}
	err = s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		var found string
		selErr := tx.QueryRowContext(ctx,
			s.q("SELECT id FROM services WHERE id = $1 AND org_id = $2"), serviceID, orgID).Scan(&found)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("start rollback: service %s is not in org %s: %w", serviceID, orgID, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("start rollback: select service: %w", selErr)
		}
		if _, insErr := tx.ExecContext(ctx,
			s.q(`INSERT INTO rollbacks (id, org_id, service_id, request_id, status, detail, created_at, finished_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`),
			rb.ID, orgID, serviceID, requestID, string(ReleaseRunning), "", rb.CreatedAt, ""); insErr != nil {
			return s.dupOr(insErr, "insert rollback")
		}
		return nil
	})
	if err != nil {
		return Rollback{}, err
	}
	return rb, nil
}

// FinishRollback implements Store. The update accepts only a running rollback,
// and the status read in the same transaction tells a final rollback
// (ErrRollbackState) from another org's (ErrNotFound). Nothing changes in
// either case.
func (s *SQLStore) FinishRollback(ctx context.Context, orgID, rollbackID string, status ReleaseStatus, detail string, now time.Time) (Rollback, error) {
	if status != ReleaseSucceeded && status != ReleaseFailed && status != ReleaseRejected {
		return Rollback{}, fmt.Errorf("finish rollback: status %q is not one of succeeded, failed, rejected", status)
	}
	var out Rollback
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rb, scanErr := scanRollback(tx.QueryRowContext(ctx,
			s.q(`UPDATE rollbacks SET status = $1, detail = $2, finished_at = $3
				WHERE id = $4 AND org_id = $5 AND status = 'running'
				RETURNING id, org_id, service_id, request_id, status, detail, created_at, finished_at`),
			string(status), detail, formatTime(now), rollbackID, orgID))
		if !errors.Is(scanErr, sql.ErrNoRows) {
			if scanErr != nil {
				return fmt.Errorf("finish rollback: %w", scanErr)
			}
			out = rb
			return nil
		}
		var existing string
		selErr := tx.QueryRowContext(ctx,
			s.q("SELECT status FROM rollbacks WHERE id = $1 AND org_id = $2"), rollbackID, orgID).Scan(&existing)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("finish rollback: rollback %s is not in org %s: %w", rollbackID, orgID, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("finish rollback: read rollback status: %w", selErr)
		}
		return fmt.Errorf("finish rollback: rollback %s is %s: %w", rollbackID, existing, ErrRollbackState)
	})
	if err != nil {
		return Rollback{}, err
	}
	return out, nil
}

// InFlightRollbacks implements Store. Both the rollback and its service carry
// the org predicate, so the join never reads across orgs; the org id is passed
// twice because each placeholder must be used once and in order.
func (s *SQLStore) InFlightRollbacks(ctx context.Context, orgID, serverID string) ([]Rollback, error) {
	var out []Rollback
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			s.q(`SELECT `+rollbackColumns+` FROM rollbacks b JOIN services v ON v.id = b.service_id
				WHERE b.org_id = $1 AND v.org_id = $2 AND v.server_id = $3 AND b.status = 'running'
				ORDER BY b.created_at, b.id`),
			orgID, orgID, serverID)
		if err != nil {
			return fmt.Errorf("select rollbacks: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			rb, scanErr := scanRollback(rows)
			if scanErr != nil {
				return scanErr
			}
			out = append(out, rb)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read rollbacks: %w", err)
		}
		return nil
	})
	return out, err
}
