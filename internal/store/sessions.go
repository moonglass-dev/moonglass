package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AddSession implements Store. The session row goes into the lookup table,
// which is outside every org and read without app.org_id.
func (s *SQLStore) AddSession(ctx context.Context, userID, sessionHash string, expires time.Time) (Session, error) {
	id, err := NewID()
	if err != nil {
		return Session{}, err
	}
	sess := Session{ID: id, UserID: userID, ExpiresAt: formatTime(expires)}
	if _, err := s.db.ExecContext(ctx,
		s.q("INSERT INTO session_hashes (hash, id, user_id, expires_at) VALUES ($1, $2, $3, $4)"),
		sessionHash, sess.ID, sess.UserID, sess.ExpiresAt); err != nil {
		return Session{}, s.dupOr(err, "insert session")
	}
	return sess, nil
}

// SessionByHash implements Store. The expiry comparison is lexical RFC3339 UTC
// text and strict, so a session is refused at the second it expires.
func (s *SQLStore) SessionByHash(ctx context.Context, sessionHash string, now time.Time) (Session, error) {
	var sess Session
	err := s.db.QueryRowContext(ctx,
		s.q("SELECT id, user_id, expires_at FROM session_hashes WHERE hash = $1 AND revoked_at IS NULL AND expires_at > $2"),
		sessionHash, formatTime(now)).
		Scan(&sess.ID, &sess.UserID, &sess.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session by hash: %w", ErrNotFound)
	}
	if err != nil {
		return Session{}, fmt.Errorf("select session hash: %w", err)
	}
	return sess, nil
}

// RevokeSession implements Store. COALESCE keeps the first revoked_at, so a
// second revoke changes nothing and still matches its row.
func (s *SQLStore) RevokeSession(ctx context.Context, userID, sessionID string, now time.Time) error {
	res, err := s.db.ExecContext(ctx,
		s.q("UPDATE session_hashes SET revoked_at = COALESCE(revoked_at, $1) WHERE id = $2 AND user_id = $3"),
		formatTime(now), sessionID, userID)
	if err != nil {
		return fmt.Errorf("revoke session %s: %w", sessionID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke session %s: count changed rows: %w", sessionID, err)
	}
	if n == 0 {
		return fmt.Errorf("revoke session %s: no session with that id belongs to user %s: %w", sessionID, userID, ErrNotFound)
	}
	return nil
}
