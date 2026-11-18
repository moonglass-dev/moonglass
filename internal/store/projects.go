package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// CreateProject implements Store.
func (s *SQLStore) CreateProject(ctx context.Context, orgID, name string) (Project, error) {
	if err := checkName("project", name); err != nil {
		return Project{}, err
	}
	id, err := NewID()
	if err != nil {
		return Project{}, err
	}
	p := Project{OrgID: orgID, ID: id, Name: name, CreatedAt: formatTime(time.Now())}
	err = s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		if _, insErr := tx.ExecContext(ctx,
			s.q("INSERT INTO projects (id, org_id, name, created_at) VALUES ($1, $2, $3, $4)"),
			p.ID, orgID, name, p.CreatedAt); insErr != nil {
			return s.dupOr(insErr, "insert project")
		}
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	return p, nil
}

// Projects implements Store.
func (s *SQLStore) Projects(ctx context.Context, orgID string) ([]Project, error) {
	var out []Project
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			s.q("SELECT org_id, id, name, created_at FROM projects WHERE org_id = $1 ORDER BY name"), orgID)
		if err != nil {
			return fmt.Errorf("select projects: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var p Project
			if err := rows.Scan(&p.OrgID, &p.ID, &p.Name, &p.CreatedAt); err != nil {
				return fmt.Errorf("scan project: %w", err)
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// CreateEnvironment implements Store. The project is read inside the org-bound
// transaction, so a project of another org is reported as ErrNotFound.
func (s *SQLStore) CreateEnvironment(ctx context.Context, orgID, projectID, name string) (Environment, error) {
	if err := checkName("environment", name); err != nil {
		return Environment{}, err
	}
	id, err := NewID()
	if err != nil {
		return Environment{}, err
	}
	e := Environment{OrgID: orgID, ID: id, ProjectID: projectID, Name: name, CreatedAt: formatTime(time.Now())}
	err = s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		var found string
		selErr := tx.QueryRowContext(ctx,
			s.q("SELECT id FROM projects WHERE id = $1 AND org_id = $2"), projectID, orgID).Scan(&found)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("create environment: project %s is not in org %s: %w", projectID, orgID, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("select project: %w", selErr)
		}
		if _, insErr := tx.ExecContext(ctx,
			s.q("INSERT INTO environments (id, org_id, project_id, name, created_at) VALUES ($1, $2, $3, $4, $5)"),
			e.ID, orgID, projectID, name, e.CreatedAt); insErr != nil {
			return s.dupOr(insErr, "insert environment")
		}
		return nil
	})
	if err != nil {
		return Environment{}, err
	}
	return e, nil
}

// Environments implements Store.
func (s *SQLStore) Environments(ctx context.Context, orgID, projectID string) ([]Environment, error) {
	var out []Environment
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			s.q("SELECT org_id, id, project_id, name, created_at FROM environments WHERE org_id = $1 AND project_id = $2 ORDER BY name"),
			orgID, projectID)
		if err != nil {
			return fmt.Errorf("select environments: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var e Environment
			if err := rows.Scan(&e.OrgID, &e.ID, &e.ProjectID, &e.Name, &e.CreatedAt); err != nil {
				return fmt.Errorf("scan environment: %w", err)
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// CreateService implements Store. The environment and the server are read
// inside the org-bound transaction, so a parent of another org is reported as
// ErrNotFound: Postgres referential checks do not apply row-level security.
func (s *SQLStore) CreateService(ctx context.Context, orgID, environmentID, name, serverID string) (Service, error) {
	if err := checkName("service", name); err != nil {
		return Service{}, err
	}
	id, err := NewID()
	if err != nil {
		return Service{}, err
	}
	svc := Service{OrgID: orgID, ID: id, EnvironmentID: environmentID, Name: name, ServerID: serverID, CreatedAt: formatTime(time.Now())}
	err = s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		var projectID string
		selErr := tx.QueryRowContext(ctx,
			s.q("SELECT project_id FROM environments WHERE id = $1 AND org_id = $2"), environmentID, orgID).Scan(&projectID)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("create service: environment %s is not in org %s: %w", environmentID, orgID, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("select environment: %w", selErr)
		}
		var found string
		selErr = tx.QueryRowContext(ctx,
			s.q("SELECT id FROM servers WHERE id = $1 AND org_id = $2"), serverID, orgID).Scan(&found)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("create service: server %s is not in org %s: %w", serverID, orgID, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("select server: %w", selErr)
		}
		svc.ProjectID = projectID
		if _, insErr := tx.ExecContext(ctx,
			s.q(`INSERT INTO services (id, org_id, project_id, environment_id, name, server_id, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`),
			svc.ID, orgID, projectID, environmentID, name, serverID, svc.CreatedAt); insErr != nil {
			return s.dupOr(insErr, "insert service")
		}
		return nil
	})
	if err != nil {
		return Service{}, err
	}
	return svc, nil
}

// Services implements Store.
func (s *SQLStore) Services(ctx context.Context, orgID, environmentID string) ([]Service, error) {
	var out []Service
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			s.q(`SELECT org_id, id, project_id, environment_id, name, server_id, created_at
				FROM services WHERE org_id = $1 AND environment_id = $2 ORDER BY name`),
			orgID, environmentID)
		if err != nil {
			return fmt.Errorf("select services: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var svc Service
			if err := rows.Scan(&svc.OrgID, &svc.ID, &svc.ProjectID, &svc.EnvironmentID, &svc.Name, &svc.ServerID, &svc.CreatedAt); err != nil {
				return fmt.Errorf("scan service: %w", err)
			}
			out = append(out, svc)
		}
		return rows.Err()
	})
	return out, err
}

// ResolveTarget implements Store.
func (s *SQLStore) ResolveTarget(ctx context.Context, orgID, project, environment string) (string, string, error) {
	var projectID, environmentID string
	err := s.inOrg(ctx, orgID, func(tx *sql.Tx) error {
		selErr := tx.QueryRowContext(ctx,
			s.q("SELECT id FROM projects WHERE org_id = $1 AND name = $2"), orgID, project).Scan(&projectID)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("resolve target: project %q not found: %w", project, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("select project: %w", selErr)
		}
		if environment == "" {
			return nil
		}
		selErr = tx.QueryRowContext(ctx,
			s.q("SELECT id FROM environments WHERE org_id = $1 AND project_id = $2 AND name = $3"),
			orgID, projectID, environment).Scan(&environmentID)
		if errors.Is(selErr, sql.ErrNoRows) {
			return fmt.Errorf("resolve target: environment %q not found in project %q: %w", environment, project, ErrNotFound)
		}
		if selErr != nil {
			return fmt.Errorf("select environment: %w", selErr)
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return projectID, environmentID, nil
}
