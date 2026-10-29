package challenge

import (
	"context"
	"database/sql"
)

// ChallengeIDForTransaction exposes the server-bound challenge identifier for
// a uniqueness constraint. It is not an authentication credential: callers
// must still accept and recheck the opaque VerifiedEmail in the same live tx.
func (v *VerifiedEmail) ChallengeIDForTransaction(ctx context.Context, tx *sql.Tx) (string, error) {
	if _, err := v.ForTransaction(ctx, tx); err != nil || v.expectation.Purpose != "email_login" || !opaque(v.expectation.ResourceID, 22, 128) {
		return "", ErrChallengeStore
	}
	return v.expectation.ResourceID, nil
}
