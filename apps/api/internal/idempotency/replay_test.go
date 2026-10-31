package idempotency

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func createReplayableResult(t *testing.T, f *idemFixture, actor idemActor, req Request, ref, body, event string,
	authorize Authorize) (Result, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	first, err := f.execute(actor, req, authorize, func(ctx context.Context, tx Transaction, principal Actor) (Reply, error) {
		calls.Add(1)
		if _, err := tx.ExecContext(ctx, testInsertEffectSQL, event, principal.SubjectID(), req.Method, req.Path, []byte("synthetic-business-write")); err != nil {
			return Reply{}, ErrUnavailable
		}
		return Reply{Status: 201, Body: []byte(body), ResultRef: ref}, nil
	})
	if err != nil || first.Replayed {
		t.Fatal("initial synthetic write did not commit")
	}
	return first, calls
}

func (f *idemFixture) replay(actor idemActor, req Request, authorize Authorize) (Result, error) {
	return f.store.ReplayExisting(f.ctx, f.db, actor.token, req, authorize)
}

func TestActorSessionIDTracksTheVerifiedSessionAndRevocationBlocksReplay(t *testing.T) {
	f := newIdemFixture(t)
	firstActor := f.actor(t)
	secondSession := f.newSessionFor(t, firstActor)
	if secondSession.subjectID != firstActor.subjectID || secondSession.sessionID == firstActor.sessionID {
		t.Fatal("fixture did not create two distinct authenticated sessions for one subject")
	}
	req := idemRequest("POST", "/synthetic/session-bound/replay", "SessionBoundReplay-01", []byte(`{"v":1}`))
	event := "session-bound-replay-effect"
	var firstObserved []string
	authorizeFirst := func(_ context.Context, tx ReadTransaction, actor Actor, _ string, _ int) error {
		if tx == nil || actor.SubjectID() != firstActor.subjectID || actor.SessionID() != firstActor.sessionID {
			return ErrForbidden
		}
		firstObserved = append(firstObserved, actor.SessionID())
		return nil
	}
	mutatedSession := ""
	created, err := f.execute(firstActor, req, authorizeFirst, func(ctx context.Context, tx Transaction, actor Actor) (Reply, error) {
		mutatedSession = actor.SessionID()
		if _, err := tx.ExecContext(ctx, testInsertEffectSQL, event, actor.SubjectID(), req.Method, req.Path, []byte("synthetic")); err != nil {
			return Reply{}, ErrUnavailable
		}
		return Reply{Status: 201, Body: []byte(`{"ok":true}`), ResultRef: "resource:session-bound"}, nil
	})
	if err != nil || created.Replayed || mutatedSession != firstActor.sessionID || len(firstObserved) < 2 {
		t.Fatal("Execute did not expose the validated original session through every callback")
	}
	for _, id := range firstObserved {
		if id != firstActor.sessionID {
			t.Fatal("Execute changed the authenticated session between authorization checks")
		}
	}

	var replayObserved []string
	authorizeSecondSession := func(_ context.Context, tx ReadTransaction, actor Actor, _ string, _ int) error {
		if tx == nil || actor.SubjectID() != secondSession.subjectID || actor.SessionID() != secondSession.sessionID {
			return ErrForbidden
		}
		replayObserved = append(replayObserved, actor.SessionID())
		return nil
	}
	replayed, err := f.replay(secondSession, req, authorizeSecondSession)
	if err != nil || !replayed.Replayed || replayed.ResultRef != created.ResultRef || !bytes.Equal(replayed.Body, created.Body) || len(replayObserved) < 2 {
		t.Fatal("ReplayExisting did not bind authorization to the exact authenticated session")
	}
	for _, id := range replayObserved {
		if id != secondSession.sessionID {
			t.Fatal("ReplayExisting changed the authenticated session between authorization checks")
		}
	}

	if _, err := f.db.ExecContext(f.ctx, `UPDATE auth_session SET state='revoked', revoked_at=UTC_TIMESTAMP(6) WHERE id=? AND user_id=?`,
		[]byte(secondSession.sessionID), secondSession.subjectID); err != nil {
		t.Fatal("could not revoke the synthetic replay session")
	}
	result, err := f.replay(secondSession, req, authorizeSecondSession)
	if !errors.Is(err, ErrAuthentication) || result.Body != nil || result.ResultRef != "" {
		t.Fatal("revoked authenticated session received an idempotent response")
	}
	if _, err := f.execute(secondSession, req, authorizeSecondSession, func(context.Context, Transaction, Actor) (Reply, error) {
		t.Fatal("revoked session reached mutation callback")
		return Reply{}, nil
	}); !errors.Is(err, ErrAuthentication) {
		t.Fatal("revoked session was allowed to execute an idempotent operation")
	}
	var effects int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, event).Scan(&effects); err != nil || effects != 1 {
		t.Fatal("session revocation caused a second business mutation")
	}
}

func TestReplayExistingReturnsOnlyCurrentCommittedReceipt(t *testing.T) {
	f := newIdemFixture(t)
	actor := f.actor(t)
	req := idemRequest("POST", "/synthetic/replay-existing/exact", "ReplayExistingKey-01", []byte(`{"b":2,"a":1}`))
	first, calls := createReplayableResult(t, f, actor, req, "resource:replay-existing", `{"ok":true,"version":1}`, "replay-existing-effect", f.authorize(actor))
	var beforeExpiry string
	if err := f.db.QueryRowContext(f.ctx, `SELECT DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM transport_idempotency_record
		WHERE actor_id=? AND method=? AND route=? AND request_key=?`, actor.subjectID, req.Method, req.Path, []byte(req.Key)).Scan(&beforeExpiry); err != nil {
		t.Fatal("could not read committed receipt expiry")
	}
	var replayed [2]Result
	var replayErrors [2]error
	var wait sync.WaitGroup
	wait.Add(len(replayed))
	for i := range replayed {
		go func(index int) {
			defer wait.Done()
			replayed[index], replayErrors[index] = f.replay(actor, req, f.authorize(actor))
		}(i)
	}
	wait.Wait()
	for i, got := range replayed {
		if replayErrors[i] != nil || !got.Replayed || got.Status != first.Status || got.ResultRef != first.ResultRef ||
			!got.ExpiresAt.Equal(first.ExpiresAt) || !bytes.Equal(got.Body, first.Body) {
			t.Fatal("concurrent replay did not return the exact committed receipt")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("replay caused a second mutation")
	}
	var effects int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, "replay-existing-effect").Scan(&effects); err != nil || effects != 1 {
		t.Fatal("replay changed business rows")
	}
	var afterExpiry string
	if err := f.db.QueryRowContext(f.ctx, `SELECT DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM transport_idempotency_record
		WHERE actor_id=? AND method=? AND route=? AND request_key=?`, actor.subjectID, req.Method, req.Path, []byte(req.Key)).Scan(&afterExpiry); err != nil || beforeExpiry != afterExpiry {
		t.Fatal("ReplayExisting refreshed or changed the saved receipt")
	}
}

func TestReplayExistingMissingExpiredAndProcessingFailClosed(t *testing.T) {
	f := newIdemFixture(t)
	actor := f.actor(t)
	missing := idemRequest("POST", "/synthetic/replay-existing/missing", "ReplayMissingKey-01", []byte(`{"v":1}`))
	if result, err := f.replay(actor, missing, f.authorize(actor)); !errors.Is(err, ErrReplayUnavailable) || result.Body != nil || result.ResultRef != "" {
		t.Fatal("missing receipt was not reported as unavailable")
	}

	processing := idemRequest("POST", "/synthetic/replay-existing/processing", "ReplayProcessingKey-01", []byte(`{"v":2}`))
	normalized, err := normalizeRequest(processing)
	if err != nil {
		t.Fatal("processing request normalization failed")
	}
	claim := bytes.Repeat([]byte{0x5a}, 32)
	created := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO transport_idempotency_record
		(actor_id,method,route,request_key,request_hmac,claim_token,state,created_at,expires_at)
		VALUES (?,?,?,?,?,?,'processing',?,?)`, actor.subjectID, normalized.Method, normalized.Path, []byte(normalized.Key),
		f.store.digest(actor.subjectID, normalized), claim, sqlTime(created), sqlTime(created.Add(f.store.ttl))); err != nil {
		t.Fatal("could not create synthetic in-progress receipt")
	}
	if result, err := f.replay(actor, processing, f.authorize(actor)); !errors.Is(err, ErrInProgress) || result.Body != nil || result.ResultRef != "" {
		t.Fatal("processing entry returned a response or was treated as missing")
	}

	expiredReq := idemRequest("POST", "/synthetic/replay-existing/expired", "ReplayExpiredKey-01", []byte(`{"v":3}`))
	first, calls := createReplayableResult(t, f, actor, expiredReq, "resource:expired-receipt", `{"value":"old"}`, "replay-expired-effect", f.authorize(actor))
	oldNow := f.store.now
	f.store.now = func() time.Time { return first.ExpiresAt.Add(time.Second) }
	result, replayErr := f.replay(actor, expiredReq, f.authorize(actor))
	f.store.now = oldNow
	if !errors.Is(replayErr, ErrReplayUnavailable) || result.Body != nil || result.ResultRef != "" || calls.Load() != 1 {
		t.Fatal("expired receipt was refreshed, replayed, or rerun")
	}
	var effects int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, "replay-expired-effect").Scan(&effects); err != nil || effects != 1 {
		t.Fatal("expired replay performed a new business write")
	}
}

func TestReplayExistingConflictsIsActorScopedAndReauthorizes(t *testing.T) {
	f := newIdemFixture(t)
	actor := f.actor(t)
	other := f.actor(t)
	req := idemRequest("POST", "/synthetic/replay-existing/conflict", "ReplayConflictKey-01", []byte(`{"v":1}`))
	first, calls := createReplayableResult(t, f, actor, req, "resource:replay-conflict", `{"secret":"cached"}`, "replay-conflict-effect", f.authorize(actor))
	changed := req
	changed.Body = []byte(`{"v":2}`)
	if result, err := f.replay(actor, changed, f.authorize(actor)); !errors.Is(err, ErrConflict) || result.Body != nil {
		t.Fatal("same scope with changed request did not conflict without a body")
	}
	changedQuery := req
	changedQuery.RawQuery = "page=2"
	if result, err := f.replay(actor, changedQuery, f.authorize(actor)); !errors.Is(err, ErrConflict) || result.Body != nil {
		t.Fatal("same key with changed query did not conflict without a body")
	}
	changedMethod := req
	changedMethod.Method = "PUT"
	if result, err := f.replay(actor, changedMethod, f.authorize(actor)); !errors.Is(err, ErrReplayUnavailable) || result.Body != nil {
		t.Fatal("a different method read another method's result")
	}
	otherPath := req
	otherPath.Path = "/synthetic/replay-existing/other-path"
	if result, err := f.replay(actor, otherPath, f.authorize(actor)); !errors.Is(err, ErrReplayUnavailable) || result.Body != nil {
		t.Fatal("a different canonical path read another path's result")
	}
	if result, err := f.replay(other, req, f.authorize(other)); !errors.Is(err, ErrReplayUnavailable) || result.Body != nil {
		t.Fatal("another actor received or detected the first actor's receipt")
	}

	const resource = "resource:replay-revocable"
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO idempotency_test_resource(resource_id,owner_id,revoked) VALUES(?,?,FALSE)`, []byte(resource), actor.subjectID); err != nil {
		t.Fatal("could not create synthetic authorized resource")
	}
	authorizeResource := func(ctx context.Context, tx ReadTransaction, principal Actor, ref string, _ int) error {
		if tx == nil || principal.SubjectID() != actor.subjectID {
			return ErrForbidden
		}
		if ref == "" {
			return nil
		}
		var owner uint64
		var revoked bool
		if err := tx.QueryRowContext(ctx, testLockResourceSQL, []byte(ref)).Scan(&owner, &revoked); err != nil {
			return ErrUnavailable
		}
		if owner != principal.SubjectID() || revoked {
			return ErrForbidden
		}
		return nil
	}
	resourceReq := idemRequest("POST", "/synthetic/replay-existing/revoked-resource", "ReplayRevokeKey-01", []byte(`{"private":true}`))
	resourceFirst, resourceCalls := createReplayableResult(t, f, actor, resourceReq, resource, `{"secret":"must-not-return"}`, "replay-revoke-effect", authorizeResource)
	if _, err := f.db.ExecContext(f.ctx, `UPDATE idempotency_test_resource SET revoked=TRUE WHERE resource_id=?`, []byte(resource)); err != nil {
		t.Fatal("could not revoke synthetic authorization")
	}
	if result, err := f.replay(actor, resourceReq, authorizeResource); !errors.Is(err, ErrForbidden) || result.Body != nil || result.ResultRef != "" {
		t.Fatal("revoked resource replay disclosed its cached response")
	}
	if calls.Load() != 1 || resourceCalls.Load() != 1 || first.Body == nil || resourceFirst.Body == nil {
		t.Fatal("replay authorization path changed prior business execution")
	}
}

func TestReplayExistingRejectsCorruptionAndExpiryAfterLockWait(t *testing.T) {
	f := newIdemFixture(t)
	actor := f.actor(t)
	corruptReq := idemRequest("POST", "/synthetic/replay-existing/corrupt", "ReplayCorruptKey-01", []byte(`{"v":1}`))
	first, calls := createReplayableResult(t, f, actor, corruptReq, "resource:corrupt-receipt", `{"secret":"ciphertext"}`, "replay-corrupt-effect", f.authorize(actor))
	var ciphertext []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT response_enc FROM transport_idempotency_record WHERE actor_id=? AND method=? AND route=? AND request_key=?`,
		actor.subjectID, corruptReq.Method, corruptReq.Path, []byte(corruptReq.Key)).Scan(&ciphertext); err != nil || len(ciphertext) < 1 {
		t.Fatal("could not read encrypted synthetic result")
	}
	ciphertext[0] ^= 0xff
	if _, err := f.db.ExecContext(f.ctx, `UPDATE transport_idempotency_record SET response_enc=? WHERE actor_id=? AND method=? AND route=? AND request_key=?`,
		ciphertext, actor.subjectID, corruptReq.Method, corruptReq.Path, []byte(corruptReq.Key)); err != nil {
		t.Fatal("could not corrupt synthetic receipt")
	}
	if result, err := f.replay(actor, corruptReq, f.authorize(actor)); !errors.Is(err, ErrUnavailable) || result.Body != nil || result.ResultRef != "" || calls.Load() != 1 {
		t.Fatal("tampered receipt returned cached content or executed new work")
	}

	waitReq := idemRequest("POST", "/synthetic/replay-existing/lock-expiry", "ReplayLockExpiryKey-01", []byte(`{"v":2}`))
	first, calls = createReplayableResult(t, f, actor, waitReq, "resource:lock-expiry", `{"value":"still-private"}`, "replay-lock-expiry-effect", f.authorize(actor))
	var beforeExpiry string
	if err := f.db.QueryRowContext(f.ctx, `SELECT DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM transport_idempotency_record
		WHERE actor_id=? AND method=? AND route=? AND request_key=?`, actor.subjectID, waitReq.Method, waitReq.Path, []byte(waitReq.Key)).Scan(&beforeExpiry); err != nil {
		t.Fatal("could not capture expiry before lock wait")
	}
	lockTx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal("could not begin receipt lock transaction")
	}
	var lockedKey []byte
	if err := lockTx.QueryRowContext(f.ctx, `SELECT request_key FROM transport_idempotency_record WHERE actor_id=? AND method=? AND route=? AND request_key=? FOR UPDATE`,
		actor.subjectID, waitReq.Method, waitReq.Path, []byte(waitReq.Key)).Scan(&lockedKey); err != nil {
		_ = lockTx.Rollback()
		t.Fatal("could not lock receipt for expiry race")
	}
	var fakeNow atomic.Int64
	fakeNow.Store(first.ExpiresAt.Add(-time.Second).UnixNano())
	oldNow := f.store.now
	f.store.now = func() time.Time { return time.Unix(0, fakeNow.Load()).UTC() }
	finished := make(chan struct {
		result Result
		err    error
	}, 1)
	go func() {
		result, err := f.replay(actor, waitReq, f.authorize(actor))
		finished <- struct {
			result Result
			err    error
		}{result, err}
	}()
	waitForMySQLLockWait(t, f)
	fakeNow.Store(first.ExpiresAt.Add(time.Second).UnixNano())
	if err := lockTx.Commit(); err != nil {
		f.store.now = oldNow
		t.Fatal("could not release receipt lock")
	}
	outcome := <-finished
	f.store.now = oldNow
	if !errors.Is(outcome.err, ErrReplayUnavailable) || outcome.result.Body != nil || outcome.result.ResultRef != "" || calls.Load() != 1 {
		t.Fatal("receipt expiring during lock wait was replayed or rerun")
	}
	var afterExpiry string
	if err := f.db.QueryRowContext(f.ctx, `SELECT DATE_FORMAT(expires_at,'%Y-%m-%d %H:%i:%s.%f') FROM transport_idempotency_record
		WHERE actor_id=? AND method=? AND route=? AND request_key=?`, actor.subjectID, waitReq.Method, waitReq.Path, []byte(waitReq.Key)).Scan(&afterExpiry); err != nil || beforeExpiry != afterExpiry {
		t.Fatal("expired replay refreshed its existing receipt")
	}
	var effects int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, "replay-lock-expiry-effect").Scan(&effects); err != nil || effects != 1 {
		t.Fatal("expired lock-wait replay performed a business write")
	}
}

func TestReplayExistingCommitOutcomeUnknownNeverReleasesBody(t *testing.T) {
	f := newIdemFixture(t)
	actor := f.actor(t)
	req := idemRequest("POST", "/synthetic/replay-existing/commit-unknown", "ReplayCommitUnknown-01", []byte(`{"v":1}`))
	_, calls := createReplayableResult(t, f, actor, req, "resource:commit-unknown", `{"secret":"cached"}`, "replay-commit-unknown-effect", f.authorize(actor))
	ctx, cancel := context.WithCancel(f.ctx)
	oldNow := f.store.now
	clockReads := 0
	f.store.now = func() time.Time {
		clockReads++
		if clockReads == 2 {
			cancel()
		}
		return oldNow()
	}
	result, err := f.store.ReplayExisting(ctx, f.db, actor.token, req, f.authorize(actor))
	f.store.now = oldNow
	cancel()
	if !errors.Is(err, ErrOutcomeUnknown) || len(result.Body) != 0 || result.ResultRef != "" || calls.Load() != 1 {
		t.Fatal("uncertain read transaction commit released a cached response or ran a mutation")
	}
	var effects int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM idempotency_test_effect WHERE event_id=?`, "replay-commit-unknown-effect").Scan(&effects); err != nil || effects != 1 {
		t.Fatal("commit-unknown recovery changed synthetic business rows")
	}
}
