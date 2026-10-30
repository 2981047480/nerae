package idempotency

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
)

type idemActor struct {
	token     string
	subjectID uint64
	sessionID string
}

const testInsertEffectSQL = `INSERT INTO idempotency_test_effect (event_id,actor_id,method,route,payload) VALUES (?,?,?,?,?)`
const testLockResourceSQL = `SELECT owner_id, revoked FROM idempotency_test_resource WHERE resource_id = ? FOR UPDATE`

type idemFixture struct {
	db         *sql.DB
	ctx        context.Context
	cancel     context.CancelFunc
	store      *Store
	sessions   *session.Store
	challenges *challenge.Store
	codec      *session.TokenCodec
	nextEmail  atomic.Uint64
}

func openIdempotencyDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_IDEMPOTENCY_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_IDEMPOTENCY_TEST_DSN using the isolated MySQL harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated idempotency DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" ||
		cfg.DBName != "nerae_idempotency_test" || cfg.User != "root" || cfg.Passwd != "" ||
		!cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" ||
		filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-idempotency-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 ||
		socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("cannot open isolated idempotency database")
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal("cannot connect to isolated idempotency database")
	}
	return db
}

func newIdemFixture(t *testing.T) *idemFixture {
	t.Helper()
	db := openIdempotencyDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	f := &idemFixture{db: db, ctx: ctx, cancel: cancel}
	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatal("could not initialize test code hasher")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{0x22}, 32))
	if err != nil {
		t.Fatal("could not initialize test proof replay store")
	}
	protector, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{0x33}, 32), bytes.Repeat([]byte{0x44}, 32), "test-email-key")
	if err != nil {
		t.Fatal("could not initialize test identity protector")
	}
	f.challenges, err = challenge.NewStore(hasher, replay, protector, challenge.Policy{
		ChallengeTTL: 5 * time.Minute, MaxAttempts: 3,
		DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second},
	})
	if err != nil {
		t.Fatal("could not initialize test challenge store")
	}
	resolver, err := identity.NewResolver(protector)
	if err != nil {
		t.Fatal("could not initialize test identity resolver")
	}
	f.codec, err = session.NewTokenCodec(bytes.Repeat([]byte{0x55}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("could not initialize test token codec")
	}
	f.sessions, err = session.NewStore(resolver, f.codec)
	if err != nil {
		t.Fatal("could not initialize test session store")
	}
	f.store, err = NewStore(f.sessions, bytes.Repeat([]byte{0x66}, 32), bytes.Repeat([]byte{0x77}, 32),
		"test-idempotency-key-v1", 30*time.Minute, StatementPolicy{
			Reads:  []string{testLockResourceSQL},
			Writes: []string{testInsertEffectSQL},
		})
	if err != nil {
		t.Fatal("could not initialize idempotency store")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin key initialization")
	}
	if err := f.store.InitializeKey(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("could not initialize idempotency key epoch")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("could not commit key initialization")
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS idempotency_test_effect (
		event_id VARBINARY(128) NOT NULL PRIMARY KEY,
		actor_id BIGINT UNSIGNED NOT NULL,
		method VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		route VARBINARY(256) NOT NULL,
		payload VARBINARY(2048) NOT NULL
	) ENGINE=InnoDB`)
	if err != nil {
		t.Fatal("could not create synthetic business table")
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS idempotency_test_resource (
		resource_id VARBINARY(128) NOT NULL PRIMARY KEY,
		owner_id BIGINT UNSIGNED NOT NULL,
		revoked BOOLEAN NOT NULL DEFAULT FALSE
	) ENGINE=InnoDB`)
	if err != nil {
		t.Fatal("could not create synthetic resource table")
	}
	return f
}

func idemDevice(t *testing.T) (*ecdsa.PrivateKey, domain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("could not create synthetic device key")
	}
	encode := base64.RawURLEncoding.EncodeToString
	return key, domain.DevicePublicJWK{Kty: "EC", Crv: "P-256",
		X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
}

func idemProof(t *testing.T, key *ecdsa.PrivateKey, id, nonce string) string {
	t.Helper()
	jti, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("could not generate test proof ID")
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login",
		"resourceId": id, "nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("could not encode test device proof")
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("could not sign test device proof")
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return unsigned + "." + encode(signature)
}

func (f *idemFixture) actor(t *testing.T) idemActor {
	t.Helper()
	key, public := idemDevice(t)
	id, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("could not generate challenge ID")
	}
	nonce, err := domain.GenerateChallengeNonce()
	if err != nil {
		t.Fatal("could not generate challenge nonce")
	}
	email := fmt.Sprintf("idempotency-%d@example.test", f.nextEmail.Add(1))
	issueTx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("could not begin challenge issuance")
	}
	if err := f.challenges.Issue(f.ctx, issueTx, challenge.IssueInput{
		Email: email, ChallengeID: id, Nonce: nonce, Code: "482951", PublicKey: public,
	}); err != nil {
		_ = issueTx.Rollback()
		t.Fatal("could not issue synthetic challenge")
	}
	if err := issueTx.Commit(); err != nil {
		t.Fatal("could not commit synthetic challenge")
	}
	loginTx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("could not begin login transaction")
	}
	attempt, err := f.challenges.Attempt(f.ctx, loginTx, challenge.AttemptInput{
		ChallengeID: id, Code: "482951", DeviceProof: idemProof(t, key, id, nonce),
	})
	if err != nil || !attempt.Verified || attempt.Proof == nil {
		_ = loginTx.Rollback()
		t.Fatal("could not verify synthetic email challenge")
	}
	issued, err := f.sessions.IssueEmail(f.ctx, loginTx, attempt.Proof)
	if err != nil {
		_ = loginTx.Rollback()
		t.Fatal("could not issue synthetic authenticated session")
	}
	if err := loginTx.Commit(); err != nil {
		t.Fatal("could not commit synthetic session")
	}
	token, err := issued.AfterCommit(f.ctx, f.db)
	if err != nil || token == "" {
		t.Fatal("synthetic session did not become durable")
	}
	claims, err := f.codec.Verify(token)
	if err != nil {
		t.Fatal("could not verify synthetic session token")
	}
	var subjectID uint64
	if _, err := fmt.Sscan(claims.SubjectID, &subjectID); err != nil || subjectID == 0 {
		t.Fatal("synthetic session had no authenticated subject")
	}
	return idemActor{token: token, subjectID: subjectID, sessionID: claims.SessionID}
}

func (f *idemFixture) authorize(actor idemActor) Authorize {
	return func(ctx context.Context, tx ReadTransaction, principal Actor, _ string) error {
		if tx == nil || principal.SubjectID() != actor.subjectID {
			return ErrForbidden
		}
		return nil
	}
}

func (f *idemFixture) execute(actor idemActor, req Request, auth Authorize, mutate Mutate) (Result, error) {
	return f.store.Execute(f.ctx, f.db, actor.token, req, auth, mutate)
}

func idemRequest(method, path, key string, body []byte) Request {
	return Request{Method: method, Path: path, Key: key, Body: append([]byte(nil), body...)}
}

func waitForMySQLLockWait(t *testing.T, f *idemFixture) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var waits int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM performance_schema.data_lock_waits`).Scan(&waits); err == nil && waits > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("competing request did not reach a real MySQL lock wait")
}

func checkRedaction(t *testing.T, secrets []string, values ...any) {
	t.Helper()
	for _, value := range values {
		text := fmt.Sprintf("%+v %#v %v", value, value, value)
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Fatal("idempotency data leaked through formatting")
			}
			if strings.Contains(text, "Body:") || strings.Contains(text, "Key:") {
				t.Fatal("idempotency body or key appeared in formatted output")
			}
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		for _, secret := range secrets {
			if bytes.Contains(log.Bytes(), []byte(secret)) || bytes.Contains(log.Bytes(), []byte(base64.StdEncoding.EncodeToString([]byte(secret)))) {
				t.Fatal("idempotency data leaked through structured logging")
			}
		}
		encoded, err := json.Marshal(value)
		if err == nil {
			for _, secret := range secrets {
				if bytes.Contains(encoded, []byte(secret)) || bytes.Contains(encoded, []byte(base64.StdEncoding.EncodeToString([]byte(secret)))) {
					t.Fatal("idempotency data leaked through JSON")
				}
			}
		}
	}
}

func TestAuthenticatedIdempotencyTransactions(t *testing.T) {
	f := newIdemFixture(t)
	actor := f.actor(t)
	other := f.actor(t)

	t.Run("purge deletes only expired terminal rows within batch limit", func(t *testing.T) {
		defer func() { f.store.now = time.Now }()
		purgeActor := f.actor(t)
		baseTime := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
		f.store.now = func() time.Time { return baseTime }
		okReq := idemRequest("POST", "/synthetic/purge/succeeded", "PurgeSuccess-01", []byte(`{"ok":true}`))
		if _, err := f.execute(purgeActor, okReq, f.authorize(purgeActor), func(context.Context, Transaction, Actor) (Reply, error) {
			return Reply{Status: 201, Body: []byte(`{"ok":true}`), ResultRef: "scope:user"}, nil
		}); err != nil {
			t.Fatal("could not create succeeded purge fixture")
		}
		failedReq := idemRequest("POST", "/synthetic/purge/failed", "PurgeFailure-01", []byte(`{"bad":true}`))
		if _, err := f.execute(purgeActor, failedReq, f.authorize(purgeActor), func(context.Context, Transaction, Actor) (Reply, error) {
			return Reply{Status: 422, Body: []byte(`{"error":"invalid"}`), ResultRef: "scope:user"}, nil
		}); err != nil {
			t.Fatal("could not create failed purge fixture")
		}
		processingKey := []byte("ProcessingKey-01")
		claim := bytes.Repeat([]byte{0x91}, 32)
		requestMAC := bytes.Repeat([]byte{0x82}, 32)
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO transport_idempotency_record
			(actor_id,method,route,request_key,request_hmac,claim_token,state,created_at,expires_at)
			VALUES (?,?,?,?,?,?,'processing',?,?)`, purgeActor.subjectID, []byte("POST"), []byte("/synthetic/purge/processing"),
			processingKey, requestMAC, claim, baseTime.Add(-time.Hour), baseTime.Add(-time.Minute)); err != nil {
			t.Fatal("could not create processing purge fixture")
		}
		f.store.now = func() time.Time { return baseTime.Add(time.Hour) }
		removed, err := f.store.PurgeExpired(f.ctx, f.db, 1)
		if err != nil || removed != 1 {
			t.Fatal("purge did not honor its one-row batch limit")
		}
		var total, processing, terminal int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM transport_idempotency_record WHERE actor_id=? AND route LIKE '/synthetic/purge/%'`, purgeActor.subjectID).Scan(&total); err != nil {
			t.Fatal("could not count purge fixtures")
		}
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM transport_idempotency_record WHERE actor_id=? AND state='processing' AND request_key=?`, purgeActor.subjectID, processingKey).Scan(&processing); err != nil {
			t.Fatal("could not verify processing retention")
		}
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM transport_idempotency_record WHERE actor_id=? AND state IN ('succeeded','failed') AND route LIKE '/synthetic/purge/%'`, purgeActor.subjectID).Scan(&terminal); err != nil {
			t.Fatal("could not verify terminal records")
		}
		if total != 2 || processing != 1 || terminal != 1 {
			t.Fatalf("purge row state mismatch: total=%d processing=%d terminal=%d deleted=%d", total, processing, terminal, removed)
		}
		removed, err = f.store.PurgeExpired(f.ctx, f.db, 10)
		if err != nil || removed != 1 {
			t.Fatal("purge did not remove the remaining expired terminal row")
		}
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM transport_idempotency_record WHERE actor_id=? AND route LIKE '/synthetic/purge/%'`, purgeActor.subjectID).Scan(&total); err != nil || total != 1 {
			t.Fatal("processing state was not retained after terminal purge")
		}
	})

	t.Run("purge waits for expired-key refresh and preserves renewed result", func(t *testing.T) {
		defer func() { f.store.now = time.Now }()
		purgeActor := f.actor(t)
		req := idemRequest("POST", "/synthetic/purge/refresh-during-delete", "PurgeRefreshKey-01", []byte(`{"refresh":true}`))
		baseTime := time.Date(2090, 2, 3, 4, 5, 6, 0, time.UTC)
		f.store.now = func() time.Time { return baseTime }
		initial, err := f.execute(purgeActor, req, f.authorize(purgeActor), func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			if _, err := tx.ExecContext(ctx, testInsertEffectSQL, "purge-refresh-initial", p.SubjectID(), req.Method, req.Path, []byte("initial")); err != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 201, Body: []byte(`{"revision":"initial"}`), ResultRef: "scope:user"}, nil
		})
		if err != nil || initial.Replayed {
			t.Fatal("could not create initial terminal result")
		}
		now := baseTime.Add(31 * time.Minute)
		f.store.now = func() time.Time { return now }
		entered, release := make(chan struct{}), make(chan struct{})
		mutations := atomic.Int32{}
		mutate := func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			if mutations.Add(1) != 1 {
				t.Error("expired same-key retry reran unexpectedly")
			}
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return Reply{}, ErrUnavailable
			}
			if _, err := tx.ExecContext(ctx, testInsertEffectSQL, "purge-refresh-new", p.SubjectID(), req.Method, req.Path, []byte("refreshed")); err != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 200, Body: []byte(`{"revision":"refreshed"}`), ResultRef: "scope:user"}, nil
		}
		type outcome struct {
			result Result
			err    error
		}
		refreshedDone := make(chan outcome, 1)
		go func() {
			result, err := f.execute(purgeActor, req, f.authorize(purgeActor), mutate)
			refreshedDone <- outcome{result: result, err: err}
		}()
		select {
		case <-entered:
		case <-time.After(8 * time.Second):
			t.Fatal("expired key was not reclaimed for a new transaction")
		}
		purgeDone := make(chan struct {
			removed int64
			err     error
		}, 1)
		purgeStarted := make(chan struct{})
		go func() {
			close(purgeStarted)
			removed, err := f.store.PurgeExpired(f.ctx, f.db, 100)
			purgeDone <- struct {
				removed int64
				err     error
			}{removed: removed, err: err}
		}()
		<-purgeStarted
		waitForMySQLLockWait(t, f)
		close(release)
		refreshed := <-refreshedDone
		if refreshed.err != nil || refreshed.result.Status != 200 || refreshed.result.Replayed ||
			!refreshed.result.ExpiresAt.After(initial.ExpiresAt) || !bytes.Equal(refreshed.result.Body, []byte(`{"revision":"refreshed"}`)) {
			t.Fatal("expired-key transaction did not commit a refreshed result")
		}
		purged := <-purgeDone
		if purged.err != nil || purged.removed != 0 {
			t.Fatal("purge removed the result whose expiration was refreshed under lock")
		}
		replay, err := f.execute(purgeActor, req, f.authorize(purgeActor), func(context.Context, Transaction, Actor) (Reply, error) {
			t.Error("replay unexpectedly reexecuted after purge race")
			return Reply{}, ErrUnavailable
		})
		if err != nil || !replay.Replayed || replay.Status != refreshed.result.Status || replay.ResultRef != refreshed.result.ResultRef ||
			!bytes.Equal(replay.Body, refreshed.result.Body) || mutations.Load() != 1 {
			t.Fatal("same-key replay did not recover the renewed result")
		}
	})

	t.Run("unregistered SQL is rejected before reaching MySQL", func(t *testing.T) {
		req := idemRequest("POST", "/synthetic/sql-policy", "SQLPolicyKey-01", []byte(`{"ok":true}`))
		var callbacks atomic.Int32
		authorize := func(ctx context.Context, tx ReadTransaction, p Actor, _ string) error {
			callbacks.Add(1)
			var one int
			if err := tx.QueryRowContext(ctx, "SELECT 1").Scan(&one); !errors.Is(err, ErrInvalid) {
				t.Error("unregistered SELECT was not rejected by the exact read policy")
			}
			if rows, err := tx.QueryContext(ctx, "SELECT GET_LOCK(?, 0)", "never-acquire"); !errors.Is(err, ErrInvalid) {
				if rows != nil {
					_ = rows.Close()
				}
				t.Error("unregistered GET_LOCK was not rejected")
			}
			if rows, err := tx.QueryContext(ctx, "SELECT 1 INTO OUTFILE '/tmp/nerae-idempotency-test-must-not-exist'"); !errors.Is(err, ErrInvalid) {
				if rows != nil {
					_ = rows.Close()
				}
				t.Error("unregistered INTO OUTFILE was not rejected")
			}
			return nil
		}
		result, err := f.execute(actor, req, authorize, func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			callbacks.Add(1)
			if _, err := tx.ExecContext(ctx, "UPDATE idempotency_test_effect SET payload=? WHERE event_id=?", []byte("x"), []byte("none")); !errors.Is(err, ErrInvalid) {
				t.Error("unregistered UPDATE was not rejected")
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_test_effect WHERE event_id=?", []byte("none")); !errors.Is(err, ErrInvalid) {
				t.Error("unregistered DELETE was not rejected")
			}
			if _, err := tx.ExecContext(ctx, testInsertEffectSQL, "policy-effect", p.SubjectID(), req.Method, req.Path, []byte("allowed")); err != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 201, Body: []byte(`{"ok":true}`), ResultRef: "scope:user"}, nil
		})
		if err != nil || result.Status != 201 || callbacks.Load() != 3 {
			t.Fatal("registered statement could not execute after rejected SQL")
		}
	})

	t.Run("resource authorization lock serializes revocation and protects replay", func(t *testing.T) {
		const resourceID = "resource-locked-for-idempotency-test"
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO idempotency_test_resource (resource_id,owner_id,revoked) VALUES (?,?,FALSE)`,
			[]byte(resourceID), actor.subjectID); err != nil {
			t.Fatal("could not create synthetic authorization resource")
		}
		req := idemRequest("POST", "/synthetic/resources/"+resourceID, "ResourceLockKey-01", []byte(`{"change":true}`))
		authorize := func(ctx context.Context, tx ReadTransaction, p Actor, ref string) error {
			if p.SubjectID() != actor.subjectID || (ref != "" && ref != resourceID) {
				return ErrForbidden
			}
			var owner uint64
			var revoked bool
			if err := tx.QueryRowContext(ctx, testLockResourceSQL, []byte(resourceID)).Scan(&owner, &revoked); err != nil {
				return ErrUnavailable
			}
			if owner != p.SubjectID() || revoked {
				return ErrForbidden
			}
			return nil
		}
		entered, release := make(chan struct{}), make(chan struct{})
		mutations := atomic.Int32{}
		mutate := func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			if mutations.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return Reply{}, ErrUnavailable
				}
			}
			if _, err := tx.ExecContext(ctx, testInsertEffectSQL, "resource-lock-effect", p.SubjectID(), req.Method, req.Path, []byte("changed")); err != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 200, Body: []byte(`{"changed":true}`), ResultRef: resourceID}, nil
		}
		type outcome struct {
			result Result
			err    error
		}
		finished := make(chan outcome, 1)
		go func() {
			result, err := f.execute(actor, req, authorize, mutate)
			finished <- outcome{result: result, err: err}
		}()
		select {
		case <-entered:
		case <-time.After(8 * time.Second):
			t.Fatal("authorized mutation did not acquire resource lock")
		}
		revoked := make(chan error, 1)
		revokerStarted := make(chan struct{})
		go func() {
			close(revokerStarted)
			_, err := f.db.ExecContext(f.ctx, `UPDATE idempotency_test_resource SET revoked=TRUE WHERE resource_id=?`, []byte(resourceID))
			revoked <- err
		}()
		<-revokerStarted
		waitForMySQLLockWait(t, f)
		close(release)
		first := <-finished
		if first.err != nil || first.result.Status != 200 || first.result.Replayed || !bytes.Equal(first.result.Body, []byte(`{"changed":true}`)) {
			t.Fatal("authorized mutation did not finish before revocation")
		}
		if err := <-revoked; err != nil {
			t.Fatal("resource revocation did not commit after the authorization lock released")
		}
		replay, err := f.execute(actor, req, authorize, func(context.Context, Transaction, Actor) (Reply, error) {
			t.Error("revoked resource replay reached mutation callback")
			return Reply{}, ErrUnavailable
		})
		if !errors.Is(err, ErrForbidden) || replay.Status != 0 || len(replay.Body) != 0 || replay.ResultRef != "" || mutations.Load() != 1 {
			t.Fatal("revoked resource replay disclosed the cached body")
		}
	})

	t.Run("same actor method route and key commit once concurrently and replay", func(t *testing.T) {
		req := idemRequest("POST", "/synthetic/resources/1", "ConcurrentKey-01", []byte(`{"secret":"test-secret-request"}`))
		reply := Reply{Status: 201, Body: []byte(`{"secret":"test-secret-response"}`), ResultRef: "resource:concurrent-1"}
		var calls atomic.Int32
		entered, release := make(chan struct{}), make(chan struct{})
		mutate := func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			if _, ok := any(tx).(*sql.Tx); ok {
				t.Error("callback received the raw sql transaction")
			}
			if _, ok := any(tx).(interface{ Commit() error }); ok {
				t.Error("callback can commit the executor transaction")
			}
			if _, ok := any(tx).(interface{ Rollback() error }); ok {
				t.Error("callback can roll back the executor transaction")
			}
			if p.SubjectID() != actor.subjectID || p.AgeVerified() {
				t.Error("callback actor was not derived from the authenticated session")
			}
			if _, err := tx.ExecContext(ctx, "COMMIT"); err == nil {
				t.Error("callback COMMIT was not rejected")
			}
			if _, err := tx.ExecContext(ctx, "DELETE FROM idempotency_test_effect; DELETE FROM idempotency_test_effect"); err == nil {
				t.Error("callback multi-statement SQL was not rejected")
			}
			if calls.Add(1) == 1 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return Reply{}, ErrUnavailable
				}
			}
			_, err := tx.ExecContext(ctx, testInsertEffectSQL,
				"concurrent-effect", p.SubjectID(), req.Method, req.Path, []byte("synthetic-effect"))
			if err != nil {
				return Reply{}, ErrUnavailable
			}
			return reply, nil
		}
		type outcome struct {
			result Result
			err    error
		}
		outcomes := make(chan outcome, 2)
		go func() { r, e := f.execute(actor, req, f.authorize(actor), mutate); outcomes <- outcome{r, e} }()
		select {
		case <-entered:
		case <-time.After(8 * time.Second):
			t.Fatal("first mutation did not start")
		}
		started := make(chan struct{})
		go func() {
			close(started)
			r, e := f.execute(actor, req, f.authorize(actor), mutate)
			outcomes <- outcome{r, e}
		}()
		<-started
		waitForMySQLLockWait(t, f)
		close(release)
		a, b := <-outcomes, <-outcomes
		if a.err != nil || b.err != nil {
			t.Fatal("concurrent request failed")
		}
		if a.result.Status != reply.Status || b.result.Status != reply.Status ||
			!bytes.Equal(a.result.Body, reply.Body) || !bytes.Equal(b.result.Body, reply.Body) ||
			a.result.ResultRef != reply.ResultRef || b.result.ResultRef != reply.ResultRef ||
			a.result.ExpiresAt.IsZero() || !a.result.ExpiresAt.Equal(b.result.ExpiresAt) ||
			a.result.Replayed == b.result.Replayed || calls.Load() != 1 {
			t.Fatal("concurrent replay was not a single committed result")
		}
		var count int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, "concurrent-effect").Scan(&count); err != nil || count != 1 {
			t.Fatal("business mutation did not commit exactly once")
		}
		var requestHMAC, encrypted []byte
		err := f.db.QueryRowContext(f.ctx, `SELECT request_hmac,response_enc FROM transport_idempotency_record
			WHERE actor_id=? AND method=? AND route=? AND request_key=?`,
			actor.subjectID, req.Method, req.Path, []byte(req.Key)).Scan(&requestHMAC, &encrypted)
		if err != nil || len(requestHMAC) != sha256.Size || len(encrypted) == 0 {
			t.Fatal("HMAC or encrypted result was not persisted")
		}
		plain := sha256.Sum256(req.Body)
		if bytes.Equal(requestHMAC, plain[:]) || bytes.Equal(requestHMAC, []byte(hex.EncodeToString(plain[:]))) {
			t.Fatal("request digest was not keyed")
		}
		if bytes.Contains(encrypted, []byte("test-secret-response")) {
			t.Fatal("response body was persisted in plaintext")
		}
		checkRedaction(t, []string{actor.token, req.Key, "test-secret-request", "test-secret-response"}, req, reply, a.result)
	})

	t.Run("same key with different body conflicts", func(t *testing.T) {
		req := idemRequest("POST", "/synthetic/conflict", "ConflictKey-01", []byte(`{"z":2,"a":1}`))
		var calls atomic.Int32
		mutate := func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			calls.Add(1)
			_, err := tx.ExecContext(ctx, testInsertEffectSQL,
				"conflict-effect", actor.subjectID, req.Method, req.Path, []byte("effect"))
			if err != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 201, Body: []byte(`{"result":"first"}`), ResultRef: "resource:conflict"}, nil
		}
		if _, err := f.execute(actor, req, f.authorize(actor), mutate); err != nil {
			t.Fatal("initial request failed")
		}
		equivalent := req
		equivalent.Body = []byte(`{"a":1,"z":2}`)
		original, err := f.execute(actor, equivalent, f.authorize(actor), mutate)
		if err != nil || !original.Replayed || original.Status != 201 || !bytes.Equal(original.Body, []byte(`{"result":"first"}`)) || calls.Load() != 1 {
			t.Fatal("semantically identical JSON did not replay the original result")
		}
		changed := req
		changed.Body = []byte(`{"value":2}`)
		result, err := f.execute(actor, changed, f.authorize(actor), mutate)
		if !errors.Is(err, ErrConflict) || result.Status != 0 || len(result.Body) != 0 || calls.Load() != 1 {
			t.Fatal("same key different body was not rejected")
		}
		changedQuery := req
		changedQuery.RawQuery = "page=2"
		result, err = f.execute(actor, changedQuery, f.authorize(actor), mutate)
		if !errors.Is(err, ErrConflict) || result.Status != 0 || len(result.Body) != 0 || calls.Load() != 1 {
			t.Fatal("same key different query was not rejected")
		}
	})

	t.Run("actor method concrete path and key case are isolated", func(t *testing.T) {
		var calls atomic.Int32
		run := func(who idemActor, method, path, key, event string) {
			t.Helper()
			req := idemRequest(method, path, key, []byte(`{"same":true}`))
			result, err := f.execute(who, req, f.authorize(who), func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
				calls.Add(1)
				_, e := tx.ExecContext(ctx, testInsertEffectSQL,
					event, p.SubjectID(), method, path, []byte("effect"))
				if e != nil {
					return Reply{}, ErrUnavailable
				}
				return Reply{Status: 200, Body: []byte(`{"ok":true}`), ResultRef: "resource:" + event}, nil
			})
			if err != nil || result.Replayed {
				t.Fatal("distinct idempotency scope was not executed")
			}
		}
		run(actor, "POST", "/synthetic/scope", "CaseKey-0001", "scope-a")
		run(other, "POST", "/synthetic/scope", "CaseKey-0001", "scope-b")
		run(actor, "POST", "/synthetic/other-path", "CaseKey-0001", "scope-path")
		run(actor, "PUT", "/synthetic/scope", "CaseKey-0001", "scope-method")
		run(actor, "POST", "/synthetic/scope", "casekey-0001", "scope-case")
		if calls.Load() != 5 {
			t.Fatal("principal/method/path/key bytes did not isolate records")
		}
	})

	t.Run("callback failure rolls back and same request can retry", func(t *testing.T) {
		req := idemRequest("POST", "/synthetic/rollback", "RollbackKey-01", []byte(`{"value":"retry"}`))
		var calls atomic.Int32
		_, err := f.execute(actor, req, f.authorize(actor), func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			if calls.Add(1) == 1 {
				_, e := tx.ExecContext(ctx, testInsertEffectSQL,
					"rollback-effect", actor.subjectID, req.Method, req.Path, []byte("effect"))
				if e != nil {
					return Reply{}, ErrUnavailable
				}
				return Reply{}, errors.New("synthetic transaction failure")
			}
			_, e := tx.ExecContext(ctx, testInsertEffectSQL,
				"rollback-effect", p.SubjectID(), req.Method, req.Path, []byte("effect"))
			if e != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 201, Body: []byte(`{"result":"retried"}`), ResultRef: "resource:retry"}, nil
		})
		if err == nil {
			t.Fatal("business failure did not abort")
		}
		var effects, records int
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, "rollback-effect").Scan(&effects); err != nil || effects != 0 {
			t.Fatal("business effect survived rollback")
		}
		if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM transport_idempotency_record WHERE actor_id=? AND method=? AND route=? AND request_key=?`,
			actor.subjectID, req.Method, req.Path, []byte(req.Key)).Scan(&records); err != nil || records != 0 {
			t.Fatal("idempotency state survived failed transaction")
		}
		result, err := f.execute(actor, req, f.authorize(actor), func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			calls.Add(1)
			_, e := tx.ExecContext(ctx, testInsertEffectSQL,
				"rollback-effect", p.SubjectID(), req.Method, req.Path, []byte("effect"))
			if e != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 201, Body: []byte(`{"result":"retried"}`), ResultRef: "resource:retry"}, nil
		})
		if err != nil || result.Replayed || calls.Load() != 2 {
			t.Fatal("retry after full rollback did not execute")
		}
	})

	t.Run("deterministic business failures are encrypted and replayed", func(t *testing.T) {
		for _, status := range []int{409, 412, 422} {
			t.Run(fmt.Sprint(status), func(t *testing.T) {
				req := idemRequest("POST", fmt.Sprintf("/synthetic/failure/%d", status),
					fmt.Sprintf("FailureKey-%03d", status), []byte(`{"value":"stable-failure"}`))
				event := fmt.Sprintf("failure-effect-%d", status)
				responseBody := []byte(fmt.Sprintf(`{"error":"business-%d"}`, status))
				var calls atomic.Int32
				mutate := func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
					calls.Add(1)
					if _, err := tx.ExecContext(ctx, testInsertEffectSQL,
						event, p.SubjectID(), req.Method, req.Path, []byte("must-rollback")); err != nil {
						return Reply{}, ErrUnavailable
					}
					return Reply{Status: status, Body: responseBody, ResultRef: "scope:user"}, nil
				}
				first, err := f.execute(actor, req, f.authorize(actor), mutate)
				if err != nil || first.Status != status || first.Replayed || first.ResultRef != "scope:user" || !bytes.Equal(first.Body, responseBody) {
					t.Fatal("deterministic business failure was not returned")
				}
				replay, err := f.execute(actor, req, f.authorize(actor), mutate)
				if err != nil || replay.Status != status || !replay.Replayed || replay.ResultRef != "scope:user" || !bytes.Equal(replay.Body, responseBody) || calls.Load() != 1 {
					t.Fatal("deterministic business failure was not replayed exactly")
				}
				var effects int
				if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, event).Scan(&effects); err != nil || effects != 0 {
					t.Fatal("business write survived deterministic failure savepoint rollback")
				}
				var state string
				var encrypted []byte
				if err := f.db.QueryRowContext(f.ctx, `SELECT state,response_enc FROM transport_idempotency_record WHERE actor_id=? AND method=? AND route=? AND request_key=?`,
					actor.subjectID, req.Method, req.Path, []byte(req.Key)).Scan(&state, &encrypted); err != nil || state != "failed" || len(encrypted) == 0 || bytes.Contains(encrypted, responseBody) {
					t.Fatal("deterministic failure was not stored as an encrypted failed result")
				}
			})
		}
	})

	t.Run("lost response is recovered by same key", func(t *testing.T) {
		req := idemRequest("POST", "/synthetic/lost-response", "LostReplyKey-01", []byte(`{"durable":true}`))
		var calls atomic.Int32
		mutate := func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
			calls.Add(1)
			_, e := tx.ExecContext(ctx, testInsertEffectSQL,
				"lost-effect", p.SubjectID(), req.Method, req.Path, []byte("effect"))
			if e != nil {
				return Reply{}, ErrUnavailable
			}
			return Reply{Status: 201, Body: []byte(`{"result":"recovered"}`), ResultRef: "resource:lost"}, nil
		}
		first, err := f.execute(actor, req, f.authorize(actor), mutate)
		if err != nil {
			t.Fatal("initial operation failed")
		}
		_ = first
		replay, err := f.execute(actor, req, f.authorize(actor), mutate)
		if err != nil || !replay.Replayed || replay.Status != first.Status || !bytes.Equal(replay.Body, first.Body) || replay.ResultRef != first.ResultRef || calls.Load() != 1 {
			t.Fatal("retry did not recover the committed response")
		}
	})

	t.Run("replay reauthorizes resource and active session", func(t *testing.T) {
		req := idemRequest("POST", "/synthetic/revoked-resource", "RevokeKey-001", []byte(`{"private":true}`))
		var allowed atomic.Bool
		allowed.Store(true)
		var mutations atomic.Int32
		authorize := func(ctx context.Context, tx ReadTransaction, p Actor, ref string) error {
			if p.SubjectID() != actor.subjectID || !allowed.Load() {
				return ErrForbidden
			}
			if ref != "" && ref != "resource:private" {
				return ErrForbidden
			}
			return nil
		}
		mutate := func(context.Context, Transaction, Actor) (Reply, error) {
			mutations.Add(1)
			return Reply{Status: 201, Body: []byte(`{"private":"test-secret-response"}`), ResultRef: "resource:private"}, nil
		}
		first, err := f.execute(actor, req, authorize, mutate)
		if err != nil || first.Status != 201 {
			t.Fatal("initial authorized write failed")
		}
		allowed.Store(false)
		denied, err := f.execute(actor, req, authorize, mutate)
		if !errors.Is(err, ErrForbidden) || denied.Status != 0 || len(denied.Body) != 0 || denied.ResultRef != "" || mutations.Load() != 1 {
			t.Fatal("unauthorized replay returned cached body")
		}
		sessionReq := idemRequest("POST", "/synthetic/revoked-session", "SessionKey-001", []byte(`{"private":true}`))
		if _, err := f.execute(other, sessionReq, f.authorize(other), func(context.Context, Transaction, Actor) (Reply, error) {
			return Reply{Status: 201, Body: []byte(`{"result":"private"}`), ResultRef: "resource:private"}, nil
		}); err != nil {
			t.Fatal("session revocation setup write failed")
		}
		if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_session SET state='revoked',revoked_at=UTC_TIMESTAMP(6) WHERE id=?`, []byte(other.sessionID)); err != nil {
			t.Fatal("could not revoke synthetic session")
		}
		replayed, err := f.execute(other, sessionReq, f.authorize(other), func(context.Context, Transaction, Actor) (Reply, error) {
			t.Fatal("revoked session reached business callback")
			return Reply{}, ErrUnavailable
		})
		if !errors.Is(err, ErrAuthentication) || replayed.Status != 0 || len(replayed.Body) != 0 || replayed.ResultRef != "" {
			t.Fatal("revoked session received cached response")
		}
	})

	t.Run("key validation honors case and UTF-8 character bounds", func(t *testing.T) {
		var calls atomic.Int32
		run := func(key, event string) {
			t.Helper()
			req := idemRequest("POST", "/synthetic/key-bounds", key, []byte(`{"same":true}`))
			result, err := f.execute(actor, req, f.authorize(actor), func(ctx context.Context, tx Transaction, p Actor) (Reply, error) {
				calls.Add(1)
				_, e := tx.ExecContext(ctx, testInsertEffectSQL,
					event, p.SubjectID(), req.Method, req.Path, []byte("effect"))
				if e != nil {
					return Reply{}, ErrUnavailable
				}
				return Reply{Status: 200, Body: []byte(`{"ok":true}`), ResultRef: "resource:" + event}, nil
			})
			if err != nil || result.Replayed {
				t.Fatal("valid key rejected or unexpectedly replayed")
			}
		}
		run("CaseKey-0001", "case-upper")
		run("casekey-0001", "case-lower")
		run(strings.Repeat("a", 8), "minimum-key")
		run(strings.Repeat("b", 128), "maximum-key")
		run(strings.Repeat("界", 128), "unicode-maximum-key")
		if calls.Load() != 5 {
			t.Fatal("case or key length semantics are incorrect")
		}
		for _, key := range []string{strings.Repeat("a", 7), strings.Repeat("b", 129)} {
			req := idemRequest("POST", "/synthetic/invalid-key", key, []byte("{}"))
			called := false
			result, err := f.execute(actor, req, f.authorize(actor), func(context.Context, Transaction, Actor) (Reply, error) {
				called = true
				return Reply{Status: 200}, nil
			})
			if !errors.Is(err, ErrInvalid) || called || result.Status != 0 || len(result.Body) != 0 {
				t.Fatal("invalid key reached callback")
			}
		}
	})

	t.Run("missing token never invokes callbacks", func(t *testing.T) {
		req := idemRequest("POST", "/synthetic/no-token", "NoTokenKey-01", []byte("{}"))
		var callbacks atomic.Int32
		result, err := f.store.Execute(f.ctx, f.db, "", req,
			func(context.Context, ReadTransaction, Actor, string) error { callbacks.Add(1); return nil },
			func(context.Context, Transaction, Actor) (Reply, error) {
				callbacks.Add(1)
				return Reply{Status: 200}, nil
			})
		if !errors.Is(err, ErrAuthentication) || callbacks.Load() != 0 || result.Status != 0 || len(result.Body) != 0 {
			t.Fatal("missing token executed or returned a result")
		}
	})
}
