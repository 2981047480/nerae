package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	issuancetestsupport "nerae/api/internal/modules/auth/issuance/testsupport"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/modules/auth/challenge"
	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	authrepo "nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

type workoutIdempotencyFixture struct {
	db       *sql.DB
	ctx      context.Context
	auth     *session.Store
	executor *WorkoutExecutor
	service  *Service
	tokenA   string
	tokenB   string
}

type workoutIdempotencyResponse struct {
	ID            string `json:"id"`
	Version       uint64 `json:"version"`
	Status        string `json:"status"`
	ClientEventID string `json:"clientEventId"`
}

func newWorkoutIdempotencyFixture(t *testing.T) *workoutIdempotencyFixture {
	t.Helper()
	db := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	hasher, err := authdomain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal("could not initialize synthetic email challenge hasher")
	}
	replay, err := authrepo.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal("could not initialize synthetic proof replay store")
	}
	protector, err := authdomain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	if err != nil {
		t.Fatal("could not initialize synthetic identity protector")
	}
	challenges, err := challenge.NewStore(hasher, replay, protector, "fixture-email-code-hash-v1", challenge.Policy{
		ChallengeTTL: 5 * time.Minute,
		MaxAttempts:  3,
		DeviceProof:  authdomain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second},
	})
	if err != nil {
		t.Fatal("could not initialize synthetic challenge store")
	}
	issuancetestsupport.InitializeEmailCodeHashEpoch(t, db, protector, hasher)
	resolver, err := identity.NewResolver(protector)
	if err != nil {
		t.Fatal("could not initialize synthetic identity resolver")
	}
	codec, err := session.NewTokenCodec(bytes.Repeat([]byte{0x55}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("could not initialize test session codec")
	}
	auth, err := session.NewStore(resolver, codec)
	if err != nil {
		t.Fatal("could not initialize test session store")
	}
	nameProtector, err := repository.NewExerciseNameProtector(bytes.Repeat([]byte{0x66}, 32), "a04-08-workout-test")
	if err != nil {
		t.Fatal("could not initialize workout encryption key")
	}
	initWorkoutTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin workout key initialization")
	}
	if err := nameProtector.InitializeExerciseKey(ctx, initWorkoutTx); err != nil {
		_ = initWorkoutTx.Rollback()
		t.Fatal("could not initialize workout key epoch")
	}
	if err := initWorkoutTx.Commit(); err != nil {
		t.Fatal("could not commit workout key epoch")
	}

	executor, err := NewWorkoutExecutor(auth, bytes.Repeat([]byte{0x77}, 32), bytes.Repeat([]byte{0x88}, 32),
		"a04-08-idempotency-test-v1", 30*time.Minute)
	if err != nil {
		t.Fatal("could not initialize workout idempotency store")
	}
	initIdempotencyTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("could not begin idempotency key initialization")
	}
	if err := executor.InitializeKey(ctx, initIdempotencyTx); err != nil {
		_ = initIdempotencyTx.Rollback()
		t.Fatal("could not initialize idempotency key epoch")
	}
	if err := initIdempotencyTx.Commit(); err != nil {
		t.Fatal("could not commit idempotency key epoch")
	}
	records, err := repository.NewRecordStore(nameProtector)
	if err != nil {
		t.Fatal("could not initialize workout record store")
	}
	service, err := New(auth, repository.NewSessionStore(), records)
	if err != nil {
		t.Fatal("could not initialize workout service")
	}

	login := func(email string) string {
		t.Helper()
		device, public := integrationDevice(t)
		challengeID, err := authdomain.GenerateChallengeID()
		if err != nil {
			t.Fatal("could not generate synthetic login challenge")
		}
		nonce, err := authdomain.GenerateChallengeNonce()
		if err != nil {
			t.Fatal("could not generate synthetic login nonce")
		}
		issueTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin synthetic email challenge")
		}
		if err := challenges.Issue(ctx, issueTx, challenge.IssueInput{
			Email: email, ChallengeID: challengeID, Nonce: nonce, Code: "123456", PublicKey: public,
		}); err != nil {
			_ = issueTx.Rollback()
			t.Fatal("could not issue synthetic email challenge")
		}
		if err := issueTx.Commit(); err != nil {
			t.Fatal("could not commit synthetic email challenge")
		}
		proof := integrationProof(t, device, challengeID, nonce)
		attemptTx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin synthetic email verification")
		}
		attempt, err := challenges.Attempt(ctx, attemptTx, challenge.AttemptInput{
			ChallengeID: challengeID, Code: "123456", DeviceProof: proof,
		})
		if err != nil || !attempt.Verified {
			_ = attemptTx.Rollback()
			t.Fatal("could not verify synthetic email challenge")
		}
		issued, err := auth.IssueEmail(ctx, attemptTx, attempt.Proof)
		if err != nil {
			_ = attemptTx.Rollback()
			t.Fatal("could not issue authenticated synthetic session")
		}
		if err := attemptTx.Commit(); err != nil {
			t.Fatal("could not commit authenticated synthetic session")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("could not release committed synthetic session token")
		}
		return token
	}
	tokenA := login("a04-08-a@example.test")
	tokenB := login("a04-08-b@example.test")
	declareAdultForTest(t, ctx, db, auth, tokenA)
	declareAdultForTest(t, ctx, db, auth, tokenB)
	return &workoutIdempotencyFixture{db: db, ctx: ctx, auth: auth, executor: executor, service: service, tokenA: tokenA, tokenB: tokenB}
}

func (f *workoutIdempotencyFixture) executeCreate(token, key, event string, mutate idempotency.Mutate) (idempotency.Result, error) {
	requestBody, _ := json.Marshal(struct {
		Source        string `json:"source"`
		Date          string `json:"date"`
		TimeZone      string `json:"timeZone"`
		ClientEventID string `json:"clientEventId"`
	}{Source: "free", Date: "2026-10-28", TimeZone: "UTC", ClientEventID: event})
	input := repository.FreeSessionInput{Date: "2026-10-28", ClientEventID: event}
	zone := "UTC"
	input.TimeZone = &zone
	request := idempotency.Request{Method: "POST", Path: "/me/sessions", Key: key, Body: requestBody}
	if mutate == nil {
		mutate = func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
			snapshot, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, input)
			if err != nil {
				return idempotency.Reply{}, err
			}
			body, err := json.Marshal(workoutIdempotencyResponse{
				ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID,
			})
			if err != nil {
				return idempotency.Reply{}, err
			}
			return idempotency.Reply{Status: 201, Body: body, ResultRef: WorkoutResultRef(snapshot.ID)}, nil
		}
	}
	return f.executor.ExecuteCreation(f.ctx, f.db, token, request, mutate)
}

func decodeWorkoutResult(t *testing.T, result idempotency.Result) workoutIdempotencyResponse {
	t.Helper()
	var response workoutIdempotencyResponse
	if err := json.Unmarshal(result.Body, &response); err != nil {
		t.Fatalf("result was not a workout response: %v", err)
	}
	return response
}

func TestIdempotentWorkoutTransactions(t *testing.T) {
	f := newWorkoutIdempotencyFixture(t)
	var createCalls atomic.Int32
	createMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		createCalls.Add(1)
		snapshot, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "a04-08-create-event", TimeZone: stringPointer("UTC"),
		})
		if err != nil {
			return idempotency.Reply{}, err
		}
		body, err := json.Marshal(workoutIdempotencyResponse{ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID})
		return idempotency.Reply{Status: 201, Body: body, ResultRef: WorkoutResultRef(snapshot.ID)}, err
	}
	created, err := f.executeCreate(f.tokenA, "create-key-0001", "a04-08-create-event", createMutation)
	if err != nil || created.Status != 201 || created.Replayed || created.ResultRef == "" {
		t.Fatalf("initial create failed: err=%v status=%d replayed=%t", err, created.Status, created.Replayed)
	}
	createdBody := append([]byte(nil), created.Body...)
	createdAgain, err := f.executeCreate(f.tokenA, "create-key-0001", "a04-08-create-event", createMutation)
	if err != nil || !createdAgain.Replayed || createdAgain.Status != created.Status || createdAgain.ResultRef != created.ResultRef || !bytes.Equal(createdAgain.Body, createdBody) || createCalls.Load() != 1 {
		t.Fatalf("same create request did not exactly replay once: err=%v calls=%d", err, createCalls.Load())
	}

	conflictRequest := idempotency.Request{Method: "POST", Path: "/me/sessions", Key: "create-key-0001", Body: []byte(`{"source":"free","date":"2026-10-27","timeZone":"UTC","clientEventId":"a04-08-create-event"}`)}
	if _, err := f.executor.ExecuteCreation(f.ctx, f.db, f.tokenA, conflictRequest, createMutation); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("same key with a different request was not rejected: %v", err)
	}

	createdResponse := decodeWorkoutResult(t, created)
	target, err := strconv.ParseUint(createdResponse.ID, 10, 64)
	if err != nil || target == 0 {
		t.Fatal("creation response omitted its workout id")
	}

	// A successful response must carry the owned resource reference; a success
	// response with the no-resource sentinel must roll back both records.
	badRefMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		snapshot, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "a04-08-bad-ref", TimeZone: stringPointer("UTC"),
		})
		if err != nil {
			return idempotency.Reply{}, err
		}
		body, _ := json.Marshal(workoutIdempotencyResponse{ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID})
		return idempotency.Reply{Status: 201, Body: body, ResultRef: NoWorkoutResultRef}, nil
	}
	if result, err := f.executeCreate(f.tokenA, "bad-ref-key-01", "a04-08-bad-ref", badRefMutation); err == nil || len(result.Body) != 0 {
		t.Fatal("successful response without an owned-resource reference was returned")
	}
	assertNoEventAndIdempotencyKey(t, f, "a04-08-bad-ref", "bad-ref-key-01")
	badIDMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		snapshot, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "a04-08-bad-id", TimeZone: stringPointer("UTC"),
		})
		if err != nil {
			return idempotency.Reply{}, err
		}
		body, _ := json.Marshal(workoutIdempotencyResponse{ID: strconv.FormatUint(snapshot.ID+1, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID})
		return idempotency.Reply{Status: 201, Body: body, ResultRef: WorkoutResultRef(snapshot.ID)}, nil
	}
	if result, err := f.executeCreate(f.tokenA, "bad-id-key-0001", "a04-08-bad-id", badIDMutation); err == nil || len(result.Body) != 0 {
		t.Fatal("success response with an id different from its authorized reference was returned")
	}
	assertNoEventAndIdempotencyKey(t, f, "a04-08-bad-id", "bad-id-key-0001")
	privateSuccessMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		snapshot, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "a04-08-private-success", TimeZone: stringPointer("UTC"),
		})
		if err != nil {
			return idempotency.Reply{}, err
		}
		body, _ := json.Marshal(map[string]any{
			"id": strconv.FormatUint(snapshot.ID, 10), "version": snapshot.Version,
			"status": string(snapshot.Status), "clientEventId": snapshot.ClientEventID,
			"private": "synthetic-email@example.test",
		})
		return idempotency.Reply{Status: 201, Body: body, ResultRef: WorkoutResultRef(snapshot.ID)}, nil
	}
	if result, err := f.executeCreate(f.tokenA, "private-success-01", "a04-08-private-success", privateSuccessMutation); err == nil || len(result.Body) != 0 {
		t.Fatal("success response with an uncontracted private field was returned")
	}
	assertNoEventAndIdempotencyKey(t, f, "a04-08-private-success", "private-success-01")

	// A deterministic resource-free failure may be replayed only in the fixed
	// public error shape. Arbitrary fields must make the whole transaction fail.
	publicFailure := idempotency.Reply{Status: 422, Body: []byte(`{"code":"VALIDATION_ERROR","message":"填写内容不符合要求","requestId":"00000000000000000000000000000000"}`), ResultRef: NoWorkoutResultRef}
	validFailureMutation := func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
		return publicFailure, nil
	}
	validFailure, err := f.executeCreate(f.tokenA, "public-failure-01", "a04-08-no-op", validFailureMutation)
	if err != nil || validFailure.Status != 422 || validFailure.Replayed {
		t.Fatalf("fixed public validation response was not accepted: err=%v", err)
	}
	validFailureReplay, err := f.executeCreate(f.tokenA, "public-failure-01", "a04-08-no-op", validFailureMutation)
	if err != nil || !validFailureReplay.Replayed || !bytes.Equal(validFailure.Body, validFailureReplay.Body) {
		t.Fatalf("fixed public validation response did not replay exactly: err=%v", err)
	}
	privateFailureMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		if _, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "a04-08-private-failure", TimeZone: stringPointer("UTC"),
		}); err != nil {
			return idempotency.Reply{}, err
		}
		return idempotency.Reply{Status: 422, Body: []byte(`{"code":"VALIDATION_ERROR","message":"填写内容不符合要求","requestId":"00000000000000000000000000000000","private":"synthetic-email@example.test"}`), ResultRef: NoWorkoutResultRef}, nil
	}
	if result, err := f.executeCreate(f.tokenA, "private-failure-01", "a04-08-private-failure", privateFailureMutation); err == nil || len(result.Body) != 0 {
		t.Fatal("resource-free failure with extra private data was returned")
	}
	assertNoEventAndIdempotencyKey(t, f, "a04-08-private-failure", "private-failure-01")

	// Save one actual group through Store.Execute, then cache a genuine stale
	// version rejection and prove it replays without rerunning the mutation.
	set := domain.Set{RecordKey: "set-a04-08-001", ExerciseEntryKey: "entry-a04-08-01",
		Exercise: domain.Exercise{Source: domain.Custom, Name: "合成测试动作"}, Index: 1, Kind: domain.Weight, WeightKg: "40", Reps: 8}
	var saveCalls atomic.Int32
	save := func(version uint64, key string, stale bool) (idempotency.Result, error) {
		type workoutSetRequest struct {
			RecordKey        string `json:"recordKey"`
			ExerciseEntryKey string `json:"exerciseEntryKey"`
			ExerciseName     string `json:"exerciseName"`
			SetIndex         int    `json:"setIndex"`
			Kind             string `json:"kind"`
			WeightKg         string `json:"weightKg"`
			Reps             int    `json:"reps"`
		}
		requestBody, _ := json.Marshal(struct {
			Version uint64              `json:"expectedVersion"`
			Sets    []workoutSetRequest `json:"sets"`
		}{Version: version, Sets: []workoutSetRequest{{
			RecordKey: set.RecordKey, ExerciseEntryKey: set.ExerciseEntryKey,
			ExerciseName: set.Exercise.Name,
			SetIndex:     set.Index, Kind: string(set.Kind), WeightKg: set.WeightKg, Reps: set.Reps,
		}}})
		request := idempotency.Request{Method: "POST", Path: fmt.Sprintf("/me/sessions/%d/sets", target), Key: key, Body: requestBody}
		mutate := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
			saveCalls.Add(1)
			snapshot, err := f.service.SaveFreeForIdempotency(ctx, tx, actor, target, version, domain.Batch{Sets: []domain.Set{set}}, []string{set.ExerciseEntryKey})
			if err != nil {
				if stale && errors.Is(err, repository.ErrVersionConflict) {
					return idempotency.Reply{Status: 412, Body: []byte(`{"code":"VERSION_CONFLICT","message":"记录已更新，请先查看最新内容","requestId":"00000000000000000000000000000000"}`), ResultRef: WorkoutResultRef(target)}, nil
				}
				return idempotency.Reply{}, err
			}
			body, err := json.Marshal(workoutIdempotencyResponse{ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID})
			return idempotency.Reply{Status: 200, Body: body, ResultRef: WorkoutResultRef(target)}, err
		}
		return f.executor.ExecuteExisting(f.ctx, f.db, f.tokenA, target, "sets", request, mutate)
	}
	saved, err := save(1, "save-key-0001", false)
	if err != nil || saved.Status != 200 || saved.Replayed || decodeWorkoutResult(t, saved).Version != 2 {
		t.Fatalf("initial set save failed: err=%v", err)
	}
	stale, err := save(1, "stale-key-0001", true)
	if err != nil || stale.Status != 412 || stale.Replayed {
		t.Fatalf("stale write did not produce a cached 412: err=%v status=%d", err, stale.Status)
	}
	staleBody := append([]byte(nil), stale.Body...)
	staleReplay, err := save(1, "stale-key-0001", true)
	if err != nil || !staleReplay.Replayed || staleReplay.Status != 412 || staleReplay.ResultRef != stale.ResultRef || !bytes.Equal(staleReplay.Body, staleBody) || saveCalls.Load() != 2 {
		t.Fatalf("stale 412 did not replay exactly without rerunning mutation: err=%v calls=%d", err, saveCalls.Load())
	}
	var version uint64
	if err := f.db.QueryRowContext(f.ctx, `SELECT version FROM workout_session WHERE id = ?`, target).Scan(&version); err != nil || version != 2 {
		t.Fatalf("stale rejection changed workout version: err=%v version=%d", err, version)
	}

	// Completion also uses the real executor path and returns the same stored
	// response on retry without inventing any completion payload fields.
	finishRequest := idempotency.Request{Method: "POST", Path: fmt.Sprintf("/me/sessions/%d/finish", target), Key: "finish-key-0001", Body: []byte(`{"expectedVersion":2}`)}
	var finishCalls atomic.Int32
	finishMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		finishCalls.Add(1)
		snapshot, err := f.service.FinishFreeForIdempotency(ctx, tx, actor, target, 2)
		if err != nil {
			return idempotency.Reply{}, err
		}
		body, err := json.Marshal(workoutIdempotencyResponse{ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID})
		return idempotency.Reply{Status: 200, Body: body, ResultRef: WorkoutResultRef(target)}, err
	}
	finish, err := f.executor.ExecuteExisting(f.ctx, f.db, f.tokenA, target, "finish", finishRequest, finishMutation)
	if err != nil || finish.Status != 200 || decodeWorkoutResult(t, finish).Status != string(domain.Completed) {
		t.Fatalf("idempotent finish failed: err=%v", err)
	}
	finishReplay, err := f.executor.ExecuteExisting(f.ctx, f.db, f.tokenA, target, "finish", finishRequest, finishMutation)
	if err != nil || !finishReplay.Replayed || !bytes.Equal(finish.Body, finishReplay.Body) || finishCalls.Load() != 1 {
		t.Fatalf("finish did not replay exactly once: err=%v calls=%d", err, finishCalls.Load())
	}
	if result, err := f.executor.ExecuteExisting(f.ctx, f.db, f.tokenB, target, "finish", finishRequest, func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
		t.Fatal("cross-subject exact-key finish mutation callback ran")
		return idempotency.Reply{}, nil
	}); !errors.Is(err, idempotency.ErrForbidden) || len(result.Body) != 0 || finishCalls.Load() != 1 {
		t.Fatalf("cross-subject exact-key finish returned a cached result or executed: err=%v", err)
	}

	// Another authenticated subject cannot read or receive the cached response
	// for this owned resource, even with a newly chosen key.
	crossRequest := idempotency.Request{Method: "POST", Path: fmt.Sprintf("/me/sessions/%d/sets", target), Key: "cross-user-key01", Body: []byte(`{"expectedVersion":3,"sets":[]}`)}
	if result, err := f.executor.ExecuteExisting(f.ctx, f.db, f.tokenB, target, "sets", crossRequest, func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
		t.Fatal("cross-subject mutation callback ran")
		return idempotency.Reply{}, nil
	}); !errors.Is(err, idempotency.ErrForbidden) || len(result.Body) != 0 {
		t.Fatalf("cross-subject access did not fail closed: err=%v", err)
	}

	// Transient failure after a real business insert must roll back both the
	// workout row and the processing idempotency row, allowing a clean retry.
	failureMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		if _, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "a04-08-rollback", TimeZone: stringPointer("UTC"),
		}); err != nil {
			return idempotency.Reply{}, err
		}
		return idempotency.Reply{}, errors.New("synthetic transient failure after business write")
	}
	if _, err := f.executeCreate(f.tokenA, "rollback-key-0001", "a04-08-rollback", failureMutation); err == nil {
		t.Fatal("transient mutation failure was unexpectedly accepted")
	}
	assertNoEventAndIdempotencyKey(t, f, "a04-08-rollback", "rollback-key-0001")
	retried, err := f.executeCreate(f.tokenA, "rollback-key-0001", "a04-08-rollback", nil)
	if err != nil || retried.Status != 201 || retried.Replayed {
		t.Fatalf("same key was not executable after whole-transaction rollback: err=%v", err)
	}

	// Force overlapping requests for one fresh key. One callback blocks inside
	// the live transaction while the second request reaches the same key.
	var concurrentCalls atomic.Int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	concurrentMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		if concurrentCalls.Add(1) == 1 {
			entered <- struct{}{}
			<-release
		}
		snapshot, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "a04-08-concurrent", TimeZone: stringPointer("UTC"),
		})
		if err != nil {
			return idempotency.Reply{}, err
		}
		body, err := json.Marshal(workoutIdempotencyResponse{ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID})
		return idempotency.Reply{Status: 201, Body: body, ResultRef: WorkoutResultRef(snapshot.ID)}, err
	}
	requestBody, _ := json.Marshal(struct {
		Source   string `json:"source"`
		Date     string `json:"date"`
		TimeZone string `json:"timeZone"`
		Event    string `json:"clientEventId"`
	}{"free", "2026-10-28", "UTC", "a04-08-concurrent"})
	concurrentRequest := idempotency.Request{Method: "POST", Path: "/me/sessions", Key: "concurrent-key-01", Body: requestBody}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	type execution struct {
		result idempotency.Result
		err    error
	}
	results := make(chan execution, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-start
			result, err := f.executor.ExecuteCreation(f.ctx, f.db, f.tokenA, concurrentRequest, concurrentMutation)
			results <- execution{result: result, err: err}
		}()
	}
	<-ready
	<-ready
	close(start)
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("no concurrent mutation entered")
	}
	close(release)
	wg.Wait()
	close(results)
	var firstResult *idempotency.Result
	replayed, firstExecutions := 0, 0
	for item := range results {
		if item.err != nil {
			t.Fatalf("concurrent same-key execution failed: %v", item.err)
		}
		if item.result.Replayed {
			replayed++
		} else {
			firstExecutions++
		}
		if firstResult == nil {
			copy := item.result
			firstResult = &copy
		} else if item.result.Status != firstResult.Status || item.result.ResultRef != firstResult.ResultRef || !bytes.Equal(item.result.Body, firstResult.Body) {
			t.Fatal("concurrent same-key callers received different results")
		}
	}
	if concurrentCalls.Load() != 1 || firstExecutions != 1 || replayed != 1 {
		t.Fatalf("same-key concurrency executed mutation more than once: calls=%d initial=%d replay=%d", concurrentCalls.Load(), firstExecutions, replayed)
	}

	clearAdultForTest(t, f.ctx, f.db, f.auth, f.tokenA)
	if replay, err := f.executeCreate(f.tokenA, "create-key-0001", "a04-08-create-event", createMutation); !errors.Is(err, idempotency.ErrForbidden) || len(replay.Body) != 0 {
		t.Fatalf("adult-declaration withdrawal did not block replay: err=%v", err)
	}
}

func TestWorkoutReplayOnlyApplication(t *testing.T) {
	f := newWorkoutIdempotencyFixture(t)
	createReq := idempotency.Request{Method: "POST", Path: "/me/sessions", Key: "replay-create-001", Body: []byte(`{"source":"free","date":"2026-10-28","timeZone":"UTC","clientEventId":"replay-create-event"}`)}
	var creates atomic.Int32
	createMutation := func(ctx context.Context, tx idempotency.Transaction, actor idempotency.Actor) (idempotency.Reply, error) {
		creates.Add(1)
		snapshot, err := f.service.CreateFreeForIdempotency(ctx, tx, actor, repository.FreeSessionInput{
			Date: "2026-10-28", ClientEventID: "replay-create-event", TimeZone: stringPointer("UTC"),
		})
		if err != nil {
			return idempotency.Reply{}, err
		}
		body, err := json.Marshal(workoutIdempotencyResponse{ID: strconv.FormatUint(snapshot.ID, 10), Version: snapshot.Version, Status: string(snapshot.Status), ClientEventID: snapshot.ClientEventID})
		return idempotency.Reply{Status: 201, Body: body, ResultRef: WorkoutResultRef(snapshot.ID)}, err
	}
	created, err := f.executor.ExecuteCreation(f.ctx, f.db, f.tokenA, createReq, createMutation)
	if err != nil || created.Status != 201 || creates.Load() != 1 {
		t.Fatalf("initial create failed: err=%v", err)
	}
	createdAgain, err := f.executor.ReplayCreation(f.ctx, f.db, f.tokenA, createReq)
	if err != nil || !createdAgain.Replayed || createdAgain.Status != created.Status || createdAgain.ResultRef != created.ResultRef || !bytes.Equal(createdAgain.Body, created.Body) || creates.Load() != 1 {
		t.Fatalf("creation replay did not return the exact stored result without mutation: err=%v", err)
	}

	changed := createReq
	changed.Body = []byte(`{"source":"free","date":"2026-10-27","timeZone":"UTC","clientEventId":"replay-create-event"}`)
	if result, err := f.executor.ReplayCreation(f.ctx, f.db, f.tokenA, changed); !errors.Is(err, idempotency.ErrConflict) || len(result.Body) != 0 {
		t.Fatalf("creation replay did not reject same-key request changes: err=%v", err)
	}
	if _, err := f.executor.ReplayCreation(f.ctx, f.db, f.tokenA, idempotency.Request{Method: "POST", Path: "/me/sessions/1/finish", Key: createReq.Key, Body: createReq.Body}); !errors.Is(err, idempotency.ErrInvalid) {
		t.Fatalf("creation replay accepted a different method path: %v", err)
	}
	if result, err := f.executor.ReplayCreation(f.ctx, f.db, f.tokenB, createReq); !errors.Is(err, idempotency.ErrReplayUnavailable) || len(result.Body) != 0 {
		t.Fatalf("creation replay exposed another actor's receipt: err=%v", err)
	}
	var target uint64
	if err := f.db.QueryRowContext(f.ctx, `SELECT id FROM workout_session WHERE client_event_id = ?`, []byte("replay-create-event")).Scan(&target); err != nil || target == 0 {
		t.Fatal("could not locate synthetic workout for existing-operation replay")
	}

	setReq := idempotency.Request{Method: "POST", Path: fmt.Sprintf("/me/sessions/%d/sets", target), Key: "replay-set-0001", Body: []byte(`{"expectedVersion":0,"sets":[]}`)}
	var setCalls atomic.Int32
	setMutation := func(context.Context, idempotency.Transaction, idempotency.Actor) (idempotency.Reply, error) {
		setCalls.Add(1)
		return idempotency.Reply{Status: 412, Body: []byte(`{"code":"VERSION_CONFLICT","message":"记录已更新，请先查看最新内容","requestId":"00000000000000000000000000000000"}`), ResultRef: WorkoutResultRef(target)}, nil
	}
	setResult, err := f.executor.ExecuteExisting(f.ctx, f.db, f.tokenA, target, "sets", setReq, setMutation)
	if err != nil || setResult.Status != 412 || setCalls.Load() != 1 {
		t.Fatalf("could not create an existing-operation terminal receipt: err=%v", err)
	}
	setReplay, err := f.executor.ReplayExisting(f.ctx, f.db, f.tokenA, target, "sets", setReq)
	if err != nil || !setReplay.Replayed || setReplay.Status != setResult.Status || setReplay.ResultRef != setResult.ResultRef || !bytes.Equal(setReplay.Body, setResult.Body) || setCalls.Load() != 1 {
		t.Fatalf("existing-operation replay differed or executed mutation: err=%v", err)
	}
	changedSet := setReq
	changedSet.Body = []byte(`{"expectedVersion":1,"sets":[]}`)
	if result, err := f.executor.ReplayExisting(f.ctx, f.db, f.tokenA, target, "sets", changedSet); !errors.Is(err, idempotency.ErrConflict) || len(result.Body) != 0 {
		t.Fatalf("existing-operation replay did not reject same-key request changes: err=%v", err)
	}
	if _, err := f.executor.ReplayExisting(f.ctx, f.db, f.tokenA, target, "finish", setReq); !errors.Is(err, idempotency.ErrInvalid) {
		t.Fatalf("existing-operation replay accepted a mismatched operation path: %v", err)
	}
	if result, err := f.executor.ReplayExisting(f.ctx, f.db, f.tokenB, target, "sets", setReq); !errors.Is(err, idempotency.ErrForbidden) || len(result.Body) != 0 {
		t.Fatalf("existing-operation replay did not recheck ownership: err=%v", err)
	}

	if _, err := f.db.ExecContext(f.ctx, `UPDATE transport_idempotency_record SET created_at = UTC_TIMESTAMP(6) - INTERVAL 2 SECOND, expires_at = UTC_TIMESTAMP(6) - INTERVAL 1 SECOND WHERE actor_id = (SELECT user_id FROM workout_session WHERE id = ?) AND method = ? AND route = ? AND request_key = ?`, target, []byte("POST"), []byte(setReq.Path), []byte(setReq.Key)); err != nil {
		t.Fatalf("could not expire synthetic idempotency receipt: %v", err)
	}
	if result, err := f.executor.ReplayExisting(f.ctx, f.db, f.tokenA, target, "sets", setReq); !errors.Is(err, idempotency.ErrReplayUnavailable) || len(result.Body) != 0 || setCalls.Load() != 1 {
		t.Fatalf("expired receipt was not unavailable without mutation: err=%v", err)
	}

	clearAdultForTest(t, f.ctx, f.db, f.auth, f.tokenA)
	if result, err := f.executor.ReplayCreation(f.ctx, f.db, f.tokenA, createReq); !errors.Is(err, idempotency.ErrForbidden) || len(result.Body) != 0 {
		t.Fatalf("creation replay ignored current age authorization: err=%v", err)
	}
	if result, err := f.executor.ReplayExisting(f.ctx, f.db, f.tokenA, target, "sets", setReq); !errors.Is(err, idempotency.ErrForbidden) || len(result.Body) != 0 {
		t.Fatalf("existing-operation replay ignored current age authorization: err=%v", err)
	}
}

func assertNoEventAndIdempotencyKey(t *testing.T, f *workoutIdempotencyFixture, eventID, key string) {
	t.Helper()
	var workoutCount, idemCount int
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM workout_session WHERE client_event_id = ?`, []byte(eventID)).Scan(&workoutCount); err != nil {
		t.Fatal("could not inspect synthetic workout rollback")
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM transport_idempotency_record WHERE request_key = ?`, []byte(key)).Scan(&idemCount); err != nil {
		t.Fatal("could not inspect synthetic idempotency rollback")
	}
	if workoutCount != 0 || idemCount != 0 {
		t.Fatalf("transaction rollback left rows behind: workouts=%d idempotency=%d", workoutCount, idemCount)
	}
}

func stringPointer(value string) *string { return &value }
