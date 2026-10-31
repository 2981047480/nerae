// Package testsupport bootstraps synthetic auth epochs for integration tests.
package testsupport

import (
	"bytes"
	"context"
	"database/sql"

	bootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
)

const HashKeyID = "fixture-email-code-hash-v1"

// InitializeEmailCodeHashEpoch uses the real code issuer to bind a synthetic
// derivation-key epoch before initializing the verifier-key epoch.
func InitializeEmailCodeHashEpoch(t interface {
	Helper()
	Fatal(args ...any)
}, db *sql.DB, identities *domain.EmailIdentityProtector, hasher *domain.EmailCodeHasher) {
	t.Helper()
	deriver, err := issuance.NewDeriver(bytes.Repeat([]byte{0x6d}, 32))
	if err != nil {
		t.Fatal("synthetic code deriver setup failed")
	}
	outbox, err := issuance.NewEnqueueOutbox(deriver, "fixture-code-derive-v1", hasher, HashKeyID, identities)
	if err != nil {
		t.Fatal("synthetic code outbox setup failed")
	}
	bootstraptest.InitializeEmailCodeHashEpoch(t, db, identities, hasher, HashKeyID,
		func(ctx context.Context, tx *sql.Tx) error { return outbox.InitializeCodeKey(ctx, tx) })
}
