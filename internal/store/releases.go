package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// enqueueRetries bounds how often EnqueueRelease retries after losing the race
// for a service's queued slot: with N callers one call loses at most N-1 times.
const enqueueRetries = 10

// releaseColumns is every column of releases, qualified for reads and the join.
const releaseColumns = `r.id, r.org_id, r.service_id, r.image_ref, r.git_repository, r.git_commit,
	r.port, r.health_path, r.domains, r.pre_deploy, r.pre_deploy_reversible,
	r.status, r.step, r.request_id, r.image_id, r.repo_digest, r.detail,
	r.created_at, r.finished_at`

// scanRelease reads one row of releaseColumns. An empty pre_deploy is no argv.
func scanRelease(row interface{ Scan(...any) error }) (Release, error) {
	var r Release
	var domains, preDeploy, status, step string
	if err := row.Scan(&r.ID, &r.OrgID, &r.ServiceID, &r.ImageRef, &r.GitRepository, &r.GitCommit,
		&r.Port, &r.HealthPath, &domains, &preDeploy, &r.PreDeployReversible,
		&status, &step, &r.RequestID, &r.ImageID, &r.RepoDigest, &r.Detail,
		&r.CreatedAt, &r.FinishedAt); err != nil {
		return Release{}, fmt.Errorf("scan release: %w", err)
	}
	if err := json.Unmarshal([]byte(domains), &r.Domains); err != nil {
		return Release{}, fmt.Errorf("decode release %s domains: %w", r.ID, err)
	}
	if preDeploy != "" {
		if err := json.Unmarshal([]byte(preDeploy), &r.PreDeploy); err != nil {
			return Release{}, fmt.Errorf("decode release %s pre_deploy argv: %w", r.ID, err)
		}
	}
	r.Status, r.Step = ReleaseStatus(status), ReleaseStep(step)
	return r, nil
}

// encodeArgs renders domains as a JSON array and pre_deploy as a JSON argv;
// none is [] and empty text.
func encodeArgs(domains, preDeploy []string) (string, string, error) {
	d := domains
	if d == nil {
		d = []string{}
	}
	domainsJSON, err := json.Marshal(d)
	if err != nil {
		return "", "", fmt.Errorf("encode release domains: %w", err)
	}
	argv := ""
	if len(preDeploy) > 0 {
		argvJSON, err := json.Marshal(preDeploy)
		if err != nil {
			return "", "", fmt.Errorf("encode release pre_deploy argv: %w", err)
		}
		argv = string(argvJSON)
	}
	return string(domainsJSON), argv, nil
}

// EnqueueRelease implements Store.
func (s *SQLStore) EnqueueRelease(ctx context.Context, orgID string, r Release, now time.Time) (Release, error) {
	id, err := NewID()
	if err != nil {
		return Release{}, err
	}
	domains, preDeploy, err := encodeArgs(r.Domains, r.PreDeploy)
	if err != nil {
		return Release{}, err
	}
	out := Release{
		OrgID: orgID, ID: id, ServiceID: r.ServiceID,
		ImageRef: r.ImageRef, GitRepository: r.GitRepository, GitCommit: r.GitCommit,
		Port: r.Port, HealthPath: r.HealthPath, Domains: r.Domains, PreDeploy: r.PreDeploy,
		PreDeployReversible: r.PreDeployReversible,
		Status:              ReleaseQueued, CreatedAt: formatTime(now),
	}
	var attemptErr error
	for range enqueueRetries {
		if attemptErr = s.enqueueReleaseOnce(ctx, orgID, out, domains, preDeploy); !errors.Is(attemptErr, ErrDuplicate) {
			if attemptErr != nil {
				return Release{}, attemptErr
			}
			return out, nil
		}
	}
	return Release{}, fmt.Errorf("enqueue release: the queued slot of service %s kept changing after %d attempts, retry the deploy: %w",
		r.ServiceID, enqueueRetries, attemptErr)
}

// enqueueReleaseOnce runs one attempt: check the service in the org-bound
// transaction (Postgres referential checks do not apply row-level security),
// supersede the service's queued release and insert r as the new one.
func (s *SQLStore) enqueueReleaseOnce(ctx context.Context, orgID string, r Release, domains, preDeploy string) error {
	return s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		var found string
		selErr := tx.QueryRowContext(ctx,
			s.q("SELECT id FROM services WHERE id = $1 AND org_id = $2"), r.ServiceID, orgID).Scan(&found)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("enqueue release: service %s is not in org %s: %w", r.ServiceID, orgID, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("enqueue release: select service: %w", selErr)
		}
		if _, err := tx.ExecContext(ctx,
			s.q("UPDATE releases SET status = 'superseded', finished_at = $1 WHERE org_id = $2 AND service_id = $3 AND status = 'queued'"),
			r.CreatedAt, orgID, r.ServiceID); err != nil {
			return fmt.Errorf("enqueue release: supersede the queued release: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			s.q(`INSERT INTO releases (id, org_id, service_id, image_ref, git_repository, git_commit,
					port, health_path, domains, pre_deploy, pre_deploy_reversible,
					status, step, request_id, image_id, repo_digest, detail,
					created_at, finished_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`),
			r.ID, r.OrgID, r.ServiceID, r.ImageRef, r.GitRepository, r.GitCommit,
			r.Port, r.HealthPath, domains, preDeploy, r.PreDeployReversible,
			string(r.Status), string(r.Step), r.RequestID, r.ImageID, r.RepoDigest, r.Detail,
			r.CreatedAt, r.FinishedAt); err != nil {
			return s.dupOr(err, "insert release")
		}
		return nil
	})
}

// updateRelease runs one status-change statement in the org-bound transaction.
// Zero changed rows means another org's release or a status the statement does
// not accept; the status read in the same transaction tells which. The single
// UPDATE writes nothing in either case.
func (s *SQLStore) updateRelease(ctx context.Context, orgID, releaseID, what, query string, args ...any) error {
	return s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, s.q(query), args...)
		if err != nil {
			if s.d.IsDuplicate != nil && s.d.IsDuplicate(err) {
				return fmt.Errorf("%s: release %s: another release of its service is running: %w", what, releaseID, ErrReleaseState)
			}
			return fmt.Errorf("%s: %w", what, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("%s: count changed rows: %w", what, err)
		}
		if n > 0 {
			return nil
		}
		var status string
		selErr := tx.QueryRowContext(ctx,
			s.q("SELECT status FROM releases WHERE id = $1 AND org_id = $2"), releaseID, orgID).Scan(&status)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("%s: release %s is not in org %s: %w", what, releaseID, orgID, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("%s: read release status: %w", what, selErr)
		}
		return fmt.Errorf("%s: release %s is %s: %w", what, releaseID, status, ErrReleaseState)
	})
}

// StartRelease implements Store.
func (s *SQLStore) StartRelease(ctx context.Context, orgID, releaseID string) error {
	return s.updateRelease(ctx, orgID, releaseID, "start release",
		"UPDATE releases SET status = 'running' WHERE id = $1 AND org_id = $2 AND status = 'queued'",
		releaseID, orgID)
}

// SetReleaseStep implements Store.
func (s *SQLStore) SetReleaseStep(ctx context.Context, orgID, releaseID string, step ReleaseStep, requestID string) error {
	if !step.Valid() {
		return fmt.Errorf("set release step: step %q is not one of build, deploy, pre_deploy, switch", step)
	}
	return s.updateRelease(ctx, orgID, releaseID, "set release step",
		"UPDATE releases SET step = $1, request_id = $2 WHERE id = $3 AND org_id = $4 AND status = 'running'",
		string(step), requestID, releaseID, orgID)
}

// SetReleaseImage implements Store.
func (s *SQLStore) SetReleaseImage(ctx context.Context, orgID, releaseID, imageID, repoDigest string) error {
	return s.updateRelease(ctx, orgID, releaseID, "set release image",
		"UPDATE releases SET image_id = $1, repo_digest = $2 WHERE id = $3 AND org_id = $4 AND status = 'running'",
		imageID, repoDigest, releaseID, orgID)
}

// FinishRelease implements Store.
func (s *SQLStore) FinishRelease(ctx context.Context, orgID, releaseID string, status ReleaseStatus, detail string, now time.Time) error {
	if status != ReleaseSucceeded && status != ReleaseFailed && status != ReleaseRejected {
		return fmt.Errorf("finish release: status %q is not one of succeeded, failed, rejected", status)
	}
	return s.updateRelease(ctx, orgID, releaseID, "finish release",
		"UPDATE releases SET status = $1, detail = $2, finished_at = $3 WHERE id = $4 AND org_id = $5 AND status IN ('queued', 'running')",
		string(status), detail, formatTime(now), releaseID, orgID)
}

// Release implements Store.
func (s *SQLStore) Release(ctx context.Context, orgID, releaseID string) (Release, error) {
	var out Release
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		r, scanErr := scanRelease(tx.QueryRowContext(ctx,
			s.q("SELECT "+releaseColumns+" FROM releases r WHERE r.id = $1 AND r.org_id = $2"), releaseID, orgID))
		if errors.Is(scanErr, sql.ErrNoRows) {
			return fmt.Errorf("release %s is not in org %s: %w", releaseID, orgID, ErrNotFound)
		}
		if scanErr != nil {
			return fmt.Errorf("select release: %w", scanErr)
		}
		out = r
		return nil
	})
	return out, err
}

// Releases implements Store.
func (s *SQLStore) Releases(ctx context.Context, orgID, serviceID string) ([]Release, error) {
	return s.queryReleases(ctx, orgID,
		"SELECT "+releaseColumns+" FROM releases r WHERE r.org_id = $1 AND r.service_id = $2 ORDER BY r.created_at DESC, r.id DESC LIMIT 50",
		orgID, serviceID)
}

// InFlightReleases implements Store. Both the release and its service carry
// the org predicate, so the join never reads across orgs; the org id is passed
// twice because each placeholder must be used once and in order.
func (s *SQLStore) InFlightReleases(ctx context.Context, orgID, serverID string) ([]Release, error) {
	return s.queryReleases(ctx, orgID,
		`SELECT `+releaseColumns+` FROM releases r JOIN services v ON v.id = r.service_id
			WHERE r.org_id = $1 AND v.org_id = $2 AND v.server_id = $3 AND r.status IN ('queued', 'running')
			ORDER BY r.created_at, r.id`,
		orgID, orgID, serverID)
}

// queryReleases runs a list query in the org-bound transaction.
func (s *SQLStore) queryReleases(ctx context.Context, orgID, query string, args ...any) ([]Release, error) {
	var out []Release
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, s.q(query), args...)
		if err != nil {
			return fmt.Errorf("select releases: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			r, scanErr := scanRelease(rows)
			if scanErr != nil {
				return scanErr
			}
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read releases: %w", err)
		}
		return nil
	})
	return out, err
}
