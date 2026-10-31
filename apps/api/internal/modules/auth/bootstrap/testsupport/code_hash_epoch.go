package testsupport

import (
	"context"
	"database/sql"
	"time"

	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/codeepoch"
	"nerae/api/internal/modules/auth/domain"
)

// InitializeEmailCodeHashEpoch initializes the identity, code-derivation, and
// code-hash epochs in one transaction. The callback must use the real
// derivation store configured for the fixture and leave its epoch lock held.
func InitializeEmailCodeHashEpoch(t testFatal, db *sql.DB, protector *domain.EmailIdentityProtector,
	hasher *domain.EmailCodeHasher, hashKeyID string,
	initializeDerivation func(context.Context, *sql.Tx) error) {
	t.Helper()
	if db == nil || protector == nil || hasher == nil || initializeDerivation == nil {
		t.Fatal("invalid email code hash epoch fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	identityInitializer, err := bootstrap.NewIdentityEpochInitializer(protector)
	if err != nil {
		t.Fatal("could not create identity epoch initializer")
	}
	hashEpoch, err := codeepoch.New(hasher, hashKeyID)
	if err != nil {
		t.Fatal("could not create code hash epoch initializer")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin email code epoch initialization")
	}
	defer tx.Rollback()
	if err := identityInitializer.Initialize(ctx, tx); err != nil {
		t.Fatal("could not initialize synthetic identity epoch")
	}
	if err := initializeDerivation(ctx, tx); err != nil {
		t.Fatal("could not initialize synthetic derivation epoch")
	}
	if err := hashEpoch.Initialize(ctx, tx); err != nil {
		t.Fatal("could not initialize synthetic code hash epoch")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("could not commit email code epoch initialization")
	}
}
