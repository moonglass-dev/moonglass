package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AddLinkToken implements Store. The link token row goes into the lookup table,
// which is outside every org and read without app.org_id.
func (s *SQLStore) AddLinkToken(ctx context.Context, userID, tokenHash string, expires time.Time) (LinkToken, error) {
	id, err := NewID()
	if err != nil {
		return LinkToken{}, err
	}
	lt := LinkToken{ID: id, UserID: userID, ExpiresAt: formatTime(expires)}
	if _, err := s.db.ExecContext(ctx,
		s.q("INSERT INTO link_token_hashes (hash, id, user_id, expires_at) VALUES ($1, $2, $3, $4)"),
		tokenHash, lt.ID, lt.UserID, lt.ExpiresAt); err != nil {
		return LinkToken{}, s.dupOr(err, "insert link token")
	}
	return lt, nil
}

// UseLinkToken implements Store. The consume is one conditional UPDATE, so a
// link token is used at most once even under concurrent callers. The expiry
// comparison is lexical RFC3339 UTC text and strict, so a token is refused at
// the second it expires. A token that has reached its expiry is marked used at
// its expiry, which makes the expiry final: every later use reports
// ErrTokenExpired, never a token whose window has closed. A token used before
// it expired reports ErrTokenUsed whatever the expiry, because only a real use
// stamps a time before the expiry.
func (s *SQLStore) UseLinkToken(ctx context.Context, linkID string, now time.Time) (LinkToken, error) {
	var lt LinkToken
	stamp := formatTime(now)
	err := s.db.QueryRowContext(ctx, s.q(`UPDATE link_token_hashes SET used_at = $1
		WHERE id = $2 AND used_at IS NULL AND expires_at > $3 RETURNING id, user_id, expires_at`),
		stamp, linkID, stamp).Scan(&lt.ID, &lt.UserID, &lt.ExpiresAt)
	if err == nil {
		return lt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LinkToken{}, fmt.Errorf("use link token %s: %w", linkID, err)
	}
	if _, markErr := s.db.ExecContext(ctx, s.q(`UPDATE link_token_hashes SET used_at = expires_at
		WHERE id = $1 AND used_at IS NULL AND expires_at <= $2`), linkID, stamp); markErr != nil {
		return LinkToken{}, fmt.Errorf("use link token %s: mark expired: %w", linkID, markErr)
	}
	var used sql.NullString
	var expiresAt string
	err = s.db.QueryRowContext(ctx, s.q("SELECT used_at, expires_at FROM link_token_hashes WHERE id = $1"), linkID).
		Scan(&used, &expiresAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return LinkToken{}, fmt.Errorf("use link token %s: %w", linkID, ErrNotFound)
	case err != nil:
		return LinkToken{}, fmt.Errorf("read link token %s: %w", linkID, err)
	case used.Valid && used.String < expiresAt:
		return LinkToken{}, fmt.Errorf("use link token %s: %w", linkID, ErrTokenUsed)
	}
	return LinkToken{}, fmt.Errorf("use link token %s: %w", linkID, ErrTokenExpired)
}
