package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"moonglass.dev/moonglass/internal/store"
)

// eventHash is SHA-256 over the 13 hashed fields of an event, in this fixed
// order: prev_hash, org_id, seq, id, at, principal_kind, user_id, token_id,
// session_id, step_up_id, operation, outcome, reason. The body is a JSON array,
// so no field can bleed into another, and its first element is the previous
// hash, which chains the events.
func eventHash(e store.AuditEvent) string {
	body, err := json.Marshal([]any{e.PrevHash, e.OrgID, e.Seq, e.ID, e.At, e.PrincipalKind, e.UserID, e.TokenID, e.SessionID, e.StepUpID, e.Operation, e.Outcome, e.Reason})
	if err != nil {
		panic("registry: audit body is not encodable: " + err.Error())
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func sealEvent(e store.AuditEvent) store.AuditEvent {
	e.Hash = eventHash(e)
	return e
}

// VerifyAudit checks a chain read in sequence order: it starts at sequence 1
// with an empty previous hash, each event links to the hash before it, and each
// hash covers its own body. The error names the first bad sequence number.
func VerifyAudit(events []store.AuditEvent) error {
	prev := ""
	for i, e := range events {
		want := int64(i + 1)
		switch {
		case e.Seq != want:
			return fmt.Errorf("audit chain broken at position %d: event has sequence %d, want %d (an event was removed or reordered)", want, e.Seq, want)
		case e.PrevHash != prev:
			return fmt.Errorf("audit chain broken at sequence %d: previous hash %q does not match the hash of event %d", e.Seq, e.PrevHash, e.Seq-1)
		case e.Hash != eventHash(e):
			return fmt.Errorf("audit chain broken at sequence %d: hash does not cover the event body (the event was altered)", e.Seq)
		}
		prev = e.Hash
	}
	return nil
}

// VerifyAudit reads the org's events and verifies the chain.
func (r *Registry) VerifyAudit(ctx context.Context, orgID string) error {
	events, err := r.st.AuditEvents(ctx, orgID)
	if err != nil {
		return fmt.Errorf("read audit events for org %s: %w", orgID, err)
	}
	return VerifyAudit(events)
}
