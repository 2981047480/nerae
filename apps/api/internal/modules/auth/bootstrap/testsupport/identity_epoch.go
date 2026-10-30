// Package testsupport contains helpers used by synthetic auth integration tests.
package testsupport

import (
	"context"
	"database/sql"
	"time"

	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/domain"
)

type testFatal interface {
	Helper()
	Fatal(args ...any)
}

// InitializeIdentityEpoch explicitly initializes the synthetic identity key epoch.
// It never reports key material or database values.
func InitializeIdentityEpoch(t testFatal, db *sql.DB, protector *domain.EmailIdentityProtector) {
	t.Helper()
	if db == nil || protector == nil {
		t.Fatal("invalid identity epoch test fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	initializer, err := bootstrap.NewIdentityEpochInitializer(protector)
	if err != nil {
		t.Fatal("could not create identity epoch initializer")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin identity epoch initialization")
	}
	defer tx.Rollback()
	if err := initializer.Initialize(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("could not initialize synthetic identity epoch")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("could not commit synthetic identity epoch")
	}
}
