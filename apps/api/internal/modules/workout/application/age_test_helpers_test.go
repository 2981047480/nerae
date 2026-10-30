package application

import (
	"context"
	"database/sql"
	"testing"

	"nerae/api/internal/modules/auth/session"
)

// adultDeclarationVersionForTest reads the version only after authenticating
// the supplied session, mirroring how a CAS caller obtains current state.
func adultDeclarationVersionForTest(t *testing.T, ctx context.Context, db *sql.DB, auth *session.Store, token string) uint64 {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin authenticated age-version read")
	}
	defer tx.Rollback()
	principal, err := auth.Authenticate(ctx, tx, token)
	if err != nil {
		t.Fatal("authenticate age-version read")
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SubjectID == 0 {
		t.Fatal("read authenticated principal for age-version read")
	}
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT age_declaration_version FROM user WHERE id = ?`, details.SubjectID).Scan(&version); err != nil {
		t.Fatal("read authenticated current age declaration version")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("commit authenticated age-version read")
	}
	return version
}

func declareAdultForTest(t *testing.T, ctx context.Context, db *sql.DB, auth *session.Store, token string) {
	t.Helper()
	current := adultDeclarationVersionForTest(t, ctx, db, auth, token)
	next, err := auth.DeclareAdultAtVersion(ctx, db, token, current, true)
	if err != nil || next != current+1 {
		t.Fatal("versioned adult self-declaration failed")
	}
}

func clearAdultForTest(t *testing.T, ctx context.Context, db *sql.DB, auth *session.Store, token string) {
	t.Helper()
	current := adultDeclarationVersionForTest(t, ctx, db, auth, token)
	next, err := auth.ClearAdultDeclarationAtVersion(ctx, db, token, current)
	if err != nil || next != current+1 {
		t.Fatal("versioned adult declaration withdrawal failed")
	}
}
