package middleware

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/session"
)

func isolatedAuthDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_HTTP_AUTH_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_HTTP_AUTH_TEST_DSN through the private MySQL harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	fixtureRoot := os.Getenv("NERAE_HTTP_AUTH_TEST_ROOT")
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_http_auth_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
		fixtureRoot == "" || filepath.Clean(fixtureRoot) != resolved || parent != resolved ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-http-auth-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Fatal("isolated MySQL unavailable")
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAuthenticatedRequestUsesLiveSessionAndTransaction(t *testing.T) {
	db := isolatedAuthDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	codec, err := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("test codec unavailable")
	}
	protector, err := authdomain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	if err != nil {
		t.Fatal("test identity protector unavailable")
	}
	resolver, err := identity.NewResolver(protector)
	if err != nil {
		t.Fatal("test resolver unavailable")
	}
	store, err := session.NewStore(resolver, codec)
	if err != nil {
		t.Fatal("test store unavailable")
	}
	const sessionID = "session-test-0000000000001"
	issued, err := codec.Issue("101", sessionID, 1)
	if err != nil {
		t.Fatal("test token unavailable")
	}
	token := issued.ForResponse()
	claims, err := codec.Verify(token)
	if err != nil {
		t.Fatal("test token invalid")
	}
	now := time.Now().UTC()
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO user (id, status, token_version, identity_version, created_at, updated_at) VALUES (101, 'active', 1, 1, ?, ?)`, []any{now, now}},
		{`INSERT INTO login_identity (id,user_id,kind,provider_scope,subject_enc,subject_lookup_hmac,key_id,state,verified_at,created_at,updated_at) VALUES (?,101,'email',?,?,?,?,'active',?,?,?)`, []any{[]byte("identity-test"), []byte("test"), []byte("synthetic"), bytes.Repeat([]byte{1}, 32), []byte("test-key"), now, now, now}},
		{`INSERT INTO trusted_device (id,user_id,key_fingerprint,public_x,public_y,state,created_at) VALUES (?,101,?,?,?,'active',?)`, []any{[]byte("device-test"), bytes.Repeat([]byte{2}, 32), strings.Repeat("A", 43), strings.Repeat("B", 43), now}},
		{`INSERT INTO auth_session (id,user_id,identity_id,device_id,login_challenge_id,token_version,state,authenticated_at,expires_at,created_at) VALUES (?,101,?,?,?,1,'active',?,?,?)`, []any{[]byte(sessionID), []byte("identity-test"), []byte("device-test"), []byte("challenge-test"), claims.IssuedAt, claims.ExpiresAt, now}},
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement.sql, statement.args...); err != nil {
			t.Fatal("could not create synthetic session fixture")
		}
	}
	r := httptest.NewRequest("GET", "/me/sessions/42", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-User-Id", "202")
	r.Header.Set("X-Role", "coach")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start transaction")
	}
	principal, err := AuthenticateRequest(r, tx, store)
	if err != nil || principal == nil {
		_ = tx.Rollback()
		t.Fatal("valid session rejected")
	}
	details, err := principal.ForTransaction(ctx, tx)
	if err != nil || details.SubjectID != 101 {
		_ = tx.Rollback()
		t.Fatal("headers changed the authenticated subject")
	}
	otherTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("could not start independent transaction")
	}
	if _, err := principal.ForTransaction(ctx, otherTx); err == nil {
		t.Fatal("principal escaped its transaction")
	}
	_ = otherTx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE trusted_device SET state='revoked' WHERE id=?`, []byte("device-test")); err != nil {
		_ = tx.Rollback()
		t.Fatal("test revocation failed")
	}
	if _, err := principal.ForTransaction(ctx, tx); err == nil {
		_ = tx.Rollback()
		t.Fatal("same-transaction revocation was not observed")
	}
	_ = tx.Rollback()
	if _, err := principal.ForTransaction(ctx, tx); err == nil {
		t.Fatal("principal survived rollback")
	}

	bad := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	bad.Header.Set("Authorization", "Bearer "+token+"x")
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start invalid-token transaction")
	}
	if p, err := AuthenticateRequest(bad, tx, store); p != nil || !errors.Is(err, ErrSessionRejected) {
		_ = tx.Rollback()
		t.Fatal("invalid signature accepted")
	}
	_ = tx.Rollback()
	// A live-looking credential must not become a logout merely because the
	// transaction or database lookup is unavailable.
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start unavailable test")
	}
	_ = tx.Rollback()
	if p, err := AuthenticateRequest(r, tx, store); p != nil || !errors.Is(err, ErrAuthUnavailable) || AuthFailureKind(err) != Unavailable {
		t.Fatal("ended transaction was not classified unavailable")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start cancelled lookup test")
	}
	cancelled, cancelQuery := context.WithCancel(ctx)
	cancelQuery()
	if p, err := AuthenticateRequest(r.WithContext(cancelled), tx, store); p != nil || !errors.Is(err, ErrAuthUnavailable) || AuthFailureKind(err) != Unavailable {
		_ = tx.Rollback()
		t.Fatal("cancelled lookup was not classified unavailable")
	}
	_ = tx.Rollback()
	// Exercise revocation in a separate transaction and observe a real row-lock
	// wait before committing the authenticated transaction.
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start lock test")
	}
	principal, err = AuthenticateRequest(r, tx, store)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal("could not authenticate lock test")
	}
	revocation := make(chan error, 1)
	go func() {
		_, err := db.ExecContext(ctx, `UPDATE auth_session SET state='revoked', revoked_at=? WHERE id=?`, now, []byte(sessionID))
		revocation <- err
	}()
	deadline, waiting := time.Now().Add(5*time.Second), 0
	for time.Now().Before(deadline) {
		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits w
			JOIN performance_schema.data_locks l ON l.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID
			WHERE l.OBJECT_SCHEMA=DATABASE() AND l.OBJECT_NAME='auth_session'`).Scan(&waiting)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("could not observe lock wait")
		}
		if waiting > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if waiting == 0 {
		_ = tx.Rollback()
		t.Fatal("revocation did not reach the session lock")
	}
	if _, err := principal.ForTransaction(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("locked session stopped being current")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("could not commit lock test")
	}
	if err := <-revocation; err != nil {
		t.Fatal("separate revocation failed")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not start revoked-session transaction")
	}
	if p, err := AuthenticateRequest(r, tx, store); p != nil || !errors.Is(err, ErrSessionRejected) {
		_ = tx.Rollback()
		t.Fatal("revoked session accepted")
	}
	_ = tx.Rollback()
	if _, err := db.ExecContext(ctx, `UPDATE auth_session SET state='active', revoked_at=NULL WHERE id=?`, []byte(sessionID)); err != nil {
		t.Fatal("could not restore test session")
	}
	for _, tc := range []struct{ disable, restore string }{
		{`UPDATE user SET status='disabled' WHERE id=101`, `UPDATE user SET status='active' WHERE id=101`},
		{`UPDATE user SET token_version=2 WHERE id=101`, `UPDATE user SET token_version=1 WHERE id=101`},
		{`UPDATE login_identity SET state='revoked' WHERE id='identity-test'`, `UPDATE login_identity SET state='active' WHERE id='identity-test'`},
		{`UPDATE trusted_device SET state='revoked' WHERE id='device-test'`, `UPDATE trusted_device SET state='active' WHERE id='device-test'`},
	} {
		if _, err := db.ExecContext(ctx, tc.disable); err != nil {
			t.Fatal("could not disable synthetic auth state")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not start state test")
		}
		if p, err := AuthenticateRequest(r, tx, store); p != nil || !errors.Is(err, ErrSessionRejected) {
			_ = tx.Rollback()
			t.Fatal("revoked authentication state accepted")
		}
		_ = tx.Rollback()
		if _, err := db.ExecContext(ctx, tc.restore); err != nil {
			t.Fatal("could not restore synthetic auth state")
		}
	}
}

func TestAuthFailureKindDoesNotMapUnknownFailuresToLogout(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want ErrorKind
	}{
		{ErrInvalidBearer, Unauthenticated},
		{ErrSessionRejected, Unauthenticated},
		{ErrAuthUnavailable, Unavailable},
		{errors.New("private upstream detail"), InternalError},
		{nil, InternalError},
	} {
		if got := AuthFailureKind(tc.err); got != tc.want {
			t.Fatalf("got %v, want %v", got, tc.want)
		}
	}
}
