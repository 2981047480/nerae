package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/dbtx"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/challenge"
	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	issuancetestsupport "nerae/api/internal/modules/auth/issuance/testsupport"
	authrepo "nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/application"
	"nerae/api/internal/modules/workout/repository"
)

func recordingV2HTTPDB(t *testing.T) *sql.DB {
	t.Helper()
	return recordingV2HTTPDBNamed(t, "NERAE_RECORDING_V2_HTTP_TEST_DSN", "nerae_recording_v2_http_test")
}
func recordingV2HTTPDBNamed(t *testing.T, envName, database string) *sql.DB {
	t.Helper()
	if (envName != "NERAE_RECORDING_V2_HTTP_TEST_DSN" || database != "nerae_recording_v2_http_test") &&
		(envName != "NERAE_RECORDING_V2_RESTORE_TEST_DSN" || database != "nerae_recording_v2_restore_test") {
		t.Fatal("refusing unrecognized synthetic database")
	}
	dsn := os.Getenv(envName)
	if dsn == "" {
		t.Skip("set NERAE_RECORDING_V2_HTTP_TEST_DSN using isolated MySQL")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	tmp, tmpErr := filepath.EvalSymlinks(os.TempDir())
	info, statErr := os.Lstat(parent)
	sock, sockErr := os.Lstat(socket)
	if err != nil || tmpErr != nil || statErr != nil || sockErr != nil || cfg.Net != "unix" || cfg.DBName != database || cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != tmp || !strings.HasPrefix(filepath.Base(resolved), "nerae-workout-session-mysql-") || !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || sock.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Fatal("isolated database unavailable")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRecordingV2RestoredBackup(t *testing.T) {
	db := recordingV2HTTPDBNamed(t, "NERAE_RECORDING_V2_RESTORE_TEST_DSN", "nerae_recording_v2_restore_test")
	ctx := context.Background()
	var sessionID, owner uint64
	if err := db.QueryRowContext(ctx, `SELECT id, user_id FROM workout_session WHERE record_format = 2 LIMIT 1`).Scan(&sessionID, &owner); err != nil {
		t.Fatal("restored synthetic session missing")
	}
	store, err := repository.NewRecordingV2Store(bytes.Repeat([]byte{6}, 32), "workout-test-key")
	if err != nil {
		t.Fatal("restored synthetic key rejected")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("restored database transaction unavailable")
	}
	snapshot, err := store.ReadFreeV2Tx(ctx, dbtx.Wrap(tx), owner, sessionID)
	_ = tx.Rollback()
	if err != nil || snapshot.Version != 7 || len(snapshot.Recording.Sets) != 1 || len(snapshot.Recording.Removed) != 1 {
		t.Fatal("restored recording or removed snapshots differ")
	}
	set := snapshot.Recording.Sets["record-001"]
	if set.Measurement.WeightKg == nil || *set.Measurement.WeightKg != "50" {
		t.Fatal("restored encrypted measurement differs")
	}
	var fences int
	if db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_recording_v2_fence WHERE user_id = ?`, owner).Scan(&fences) != nil || fences < 1 {
		t.Fatal("restored request-key fences missing")
	}
}
func TestRecordingV2HTTPFlow(t *testing.T) {
	db := recordingV2HTTPDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	hasher, _ := authdomain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := authrepo.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	identityProtector, _ := authdomain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	challenges, err := challenge.NewStore(hasher, replay, identityProtector, "fixture-email-code-hash-v1", challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3, DeviceProof: authdomain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge setup")
	}
	issuancetestsupport.InitializeEmailCodeHashEpoch(t, db, identityProtector, hasher)
	resolver, _ := identity.NewResolver(identityProtector)
	codec, _ := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	auth, _ := session.NewStore(resolver, codec)
	names, _ := repository.NewExerciseNameProtector(bytes.Repeat([]byte{6}, 32), "workout-test-key")
	store, err := repository.NewRecordingV2Store(bytes.Repeat([]byte{6}, 32), "workout-test-key")
	if err != nil {
		t.Fatal(err)
	}
	executor, err := application.NewRecordingV2Executor(auth, bytes.Repeat([]byte{17}, 32), bytes.Repeat([]byte{18}, 32), "write-test-idempotency", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if names.InitializeExerciseKey(ctx, tx) != nil || executor.InitializeKey(ctx, tx) != nil || tx.Commit() != nil {
		t.Fatal("initialize key epochs")
	}
	service, err := application.NewRecordingV2Service(auth, store)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := middleware.NewSchemaValidator(4<<20, 100000)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRecordingV2Handler(db, service, store, executor, validator)
	if err != nil {
		t.Fatal(err)
	}
	login := func(label string) string {
		t.Helper()
		key, public := readTestDevice(t)
		challengeID, _ := authdomain.GenerateChallengeID()
		nonce, _ := authdomain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = challenges.Issue(ctx, tx, challenge.IssueInput{Email: label + "@example.test", ChallengeID: challengeID, Nonce: nonce, Code: "012345", PublicKey: public})
		if err != nil || tx.Commit() != nil {
			t.Fatal("issue synthetic challenge")
		}
		proof := readTestProof(t, key, challengeID, nonce)
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: challengeID, Code: "012345", DeviceProof: proof})
		if err != nil || !attempt.Verified {
			t.Fatal("verify synthetic challenge")
		}
		issued, err := auth.IssueEmail(ctx, tx, attempt.Proof)
		if err != nil || tx.Commit() != nil {
			t.Fatal("issue synthetic session")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("release synthetic session")
		}
		declareAdultForTest(t, ctx, db, auth, token)
		return token
	}
	owner, other := login("v2-http-owner"), login("v2-http-other")
	request := func(method, path, body, token, key string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	date := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	createBody := fmt.Sprintf(`{"source":"free","date":"%s","timeZone":"UTC","clientEventId":"http-event-001","mode":"after_workout"}`, date)
	created := request(http.MethodPost, "/me/recordings", createBody, owner, "create-key-001")
	if created.Code != 201 {
		t.Fatalf("create status %d body %s", created.Code, created.Body.String())
	}
	var ref struct {
		ID      string `json:"id"`
		Version uint64 `json:"version"`
	}
	if json.Unmarshal(created.Body.Bytes(), &ref) != nil || ref.ID == "" || ref.Version != 1 {
		t.Fatal("bad create receipt")
	}
	path := "/me/recordings/" + ref.ID
	legacyRecords, err := repository.NewRecordStore(names)
	if err != nil {
		t.Fatal(err)
	}
	legacyService, err := application.New(auth, repository.NewSessionStore(), legacyRecords)
	if err != nil {
		t.Fatal(err)
	}
	legacyExecutor, err := application.NewWorkoutExecutor(auth, bytes.Repeat([]byte{17}, 32), bytes.Repeat([]byte{18}, 32), "write-test-idempotency", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	legacyRead, err := NewReadHandler(db, legacyService, validator)
	if err != nil {
		t.Fatal(err)
	}
	legacyWrite, err := NewWriteHandler(db, legacyService, validator, legacyExecutor)
	if err != nil {
		t.Fatal(err)
	}
	oldRequest := func(h http.Handler, method, target, body, token, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body)).WithContext(ctx)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	oldDetail := oldRequest(legacyRead, http.MethodGet, "/me/sessions/"+ref.ID, "", owner, "")
	if oldDetail.Code != 409 || !strings.Contains(oldDetail.Body.String(), "CLIENT_UPGRADE_REQUIRED") {
		t.Fatal("legacy detail did not prompt upgrade")
	}
	if got := oldRequest(legacyRead, http.MethodGet, "/me/sessions", "", owner, ""); got.Code != 409 || !strings.Contains(got.Body.String(), "CLIENT_UPGRADE_REQUIRED") {
		t.Fatal("legacy list silently exposed partial history")
	}
	if got := oldRequest(legacyRead, http.MethodGet, "/me/sessions/"+ref.ID, "", other, ""); got.Code != 404 {
		t.Fatal("legacy detail leaked another owner's record")
	}
	legacyBody := `{"expectedVersion":1,"exerciseEntryKeys":[],"deleteRecordKeys":["record-old"]}`
	if got := oldRequest(legacyWrite, http.MethodPost, "/me/sessions/"+ref.ID+"/sets", legacyBody, owner, "legacy-key-01"); got.Code != 409 || !strings.Contains(got.Body.String(), "CLIENT_UPGRADE_REQUIRED") {
		t.Fatalf("legacy write did not prompt upgrade: %d", got.Code)
	}
	batch := `{"expectedVersion":1,"addEntries":[{"exerciseEntryKey":"entry-0001","exerciseName":"深蹲"}],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","setIndex":1,"confirmed":false,"measurement":{"method":"total","weightKg":"45","reps":8}},{"recordKey":"record-002","exerciseEntryKey":"entry-0001","setIndex":2,"confirmed":false,"measurement":{"method":"total"}}],"exerciseEntryKeys":["entry-0001"]}`
	saved := request(http.MethodPost, path+"/sets", batch, owner, "sets-key-0001")
	if saved.Code != 200 {
		t.Fatalf("save status %d body %s", saved.Code, saved.Body.String())
	}
	replayed := request(http.MethodPost, path+"/sets/replay", batch, owner, "sets-key-0001")
	if replayed.Code != 200 || replayed.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(saved.Body.Bytes(), replayed.Body.Bytes()) {
		t.Fatal("replay did not recover original receipt")
	}
	stale := request(http.MethodPost, path+"/sets", batch, owner, "sets-key-stale")
	if stale.Code != 412 {
		t.Fatalf("stale write status %d", stale.Code)
	}
	detail := request(http.MethodGet, path, "", owner, "")
	if detail.Code != 200 {
		t.Fatalf("detail status %d body %s", detail.Code, detail.Body.String())
	}
	var before struct {
		Version uint64           `json:"version"`
		Sets    []map[string]any `json:"sets"`
		Entries []struct {
			Volume struct {
				CalculatedKg string `json:"calculatedKg"`
				FullSets     int    `json:"fullSets"`
				ExcludedSets int    `json:"excludedSets"`
			} `json:"volume"`
		} `json:"entries"`
	}
	if json.Unmarshal(detail.Body.Bytes(), &before) != nil || before.Version != 2 || len(before.Sets) != 2 {
		t.Fatal("detail lost sets")
	}
	if len(before.Entries) != 1 || before.Entries[0].Volume.CalculatedKg != "360.00" || before.Entries[0].Volume.FullSets != 1 || before.Entries[0].Volume.ExcludedSets != 1 {
		t.Fatal("detail did not report calculated and excluded set volumes")
	}
	blank := before.Sets[1]["measurement"].(map[string]any)
	if _, ok := blank["weightKg"]; ok {
		t.Fatal("blank weight became zero")
	}
	if request(http.MethodGet, path, "", other, "").Code != 404 {
		t.Fatal("other account read draft")
	}
	if request(http.MethodGet, path, "", "", "").Code != 401 {
		t.Fatal("anonymous read draft")
	}
	finished := request(http.MethodPost, path+"/finish", `{"expectedVersion":2}`, owner, "finish-key-01")
	if finished.Code != 200 {
		t.Fatalf("finish status %d body %s", finished.Code, finished.Body.String())
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM transport_idempotency_record WHERE request_key = ?`, []byte("finish-key-01")); err != nil {
		t.Fatal("purge synthetic finish receipt")
	}
	if got := request(http.MethodPost, path+"/finish/replay", `{"expectedVersion":2}`, owner, "finish-key-01"); got.Code != 503 {
		t.Fatalf("expired replay status %d", got.Code)
	}
	if got := request(http.MethodPost, path+"/finish", `{"expectedVersion":3}`, owner, "finish-key-01"); got.Code != 409 {
		t.Fatalf("expired finish key reused: %d", got.Code)
	}
	remove := `{"expectedVersion":3,"removeRecordKeys":["record-001","record-002"],"exerciseEntryKeys":["entry-0001"]}`
	if got := request(http.MethodPost, path+"/sets", remove, owner, "remove-key-01"); got.Code != 200 {
		t.Fatalf("remove status %d body %s", got.Code, got.Body.String())
	}
	detail = request(http.MethodGet, path, "", owner, "")
	var removed struct {
		Status  string `json:"status"`
		Sets    []any  `json:"sets"`
		Removed []any  `json:"removedSets"`
	}
	if detail.Code != 200 || json.Unmarshal(detail.Body.Bytes(), &removed) != nil || removed.Status != "in_progress" || len(removed.Sets) != 0 || len(removed.Removed) != 2 {
		t.Fatal("removing final groups lost recovery or draft status")
	}
	restore := `{"expectedVersion":4,"restoreRecordKeys":["record-002"],"exerciseEntryKeys":["entry-0001"]}`
	if got := request(http.MethodPost, path+"/sets", restore, owner, "restore-key-1"); got.Code != 200 {
		t.Fatalf("restore status %d body %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, path+"/finish", `{"expectedVersion":5}`, owner, "finish-key-02"); got.Code != 200 {
		t.Fatal("blank group cannot be completed")
	}
	invalid := `{"expectedVersion":6,"addEntries":[{"exerciseEntryKey":"entry-0002","exerciseName":"\ud800"}],"exerciseEntryKeys":["entry-0001","entry-0002"]}`
	if got := request(http.MethodPost, path+"/sets", invalid, owner, "invalid-key-1"); got.Code != 422 {
		t.Fatalf("invalid unicode status %d", got.Code)
	}
	offline := `{"expectedVersion":6,"restoreRecordKeys":["record-001"],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-0001","setIndex":1,"confirmed":false,"measurement":{"method":"total","weightKg":"50","reps":8}},{"recordKey":"record-002","exerciseEntryKey":"entry-0001","setIndex":2,"confirmed":false,"measurement":{"method":"total","weightKg":"14"}}],"removeRecords":[{"recordKey":"record-002","removedPosition":2}],"exerciseEntryKeys":["entry-0001"]}`
	if got := request(http.MethodPost, path+"/sets", offline, owner, "offline-key-01"); got.Code != 200 {
		t.Fatalf("offline batch status %d body %s", got.Code, got.Body.String())
	}
	detail = request(http.MethodGet, path, "", owner, "")
	var offlineResult struct {
		Sets []struct {
			RecordKey   string         `json:"recordKey"`
			Measurement map[string]any `json:"measurement"`
		} `json:"sets"`
		Removed []struct {
			Set struct {
				RecordKey   string         `json:"recordKey"`
				Measurement map[string]any `json:"measurement"`
			} `json:"set"`
			Position int `json:"removedPosition"`
		} `json:"removedSets"`
	}
	if detail.Code != 200 || json.Unmarshal(detail.Body.Bytes(), &offlineResult) != nil || len(offlineResult.Sets) != 1 || len(offlineResult.Removed) != 1 || offlineResult.Sets[0].Measurement["weightKg"] != "50" || offlineResult.Removed[0].Set.Measurement["weightKg"] != "14" || offlineResult.Removed[0].Position != 2 {
		t.Fatal("offline edit and removal did not survive together")
	}
	// A date change shares the same transaction/version as all group changes.
	earlier := time.Now().UTC().AddDate(0, 0, -3).Format("2006-01-02")
	changedDate := fmt.Sprintf(`{"expectedVersion":7,"date":"%s","exerciseEntryKeys":["entry-0001"]}`, earlier)
	if got := request(http.MethodPost, path+"/sets", changedDate, other, "date-other-01"); got.Code != 403 {
		t.Fatalf("other owner date write status %d", got.Code)
	}
	future := time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02")
	invalidDate := fmt.Sprintf(`{"expectedVersion":7,"date":"%s","exerciseEntryKeys":["entry-0001"]}`, future)
	if got := request(http.MethodPost, path+"/sets", invalidDate, owner, "date-future-1"); got.Code != 422 {
		t.Fatalf("future date status %d", got.Code)
	}
	if got := request(http.MethodPost, path+"/sets", changedDate, owner, "date-write-01"); got.Code != 200 {
		t.Fatalf("date change status %d", got.Code)
	}
	if got := request(http.MethodPost, path+"/sets/replay", changedDate, owner, "date-write-01"); got.Code != 200 || got.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("date request did not replay its original result")
	}
	if got := request(http.MethodPost, path+"/sets", changedDate, owner, "date-stale-01"); got.Code != 412 {
		t.Fatalf("stale date change status %d", got.Code)
	}
	dateDetail := request(http.MethodGet, path, "", owner, "")
	var afterDate struct {
		Date    string `json:"date"`
		Version uint64 `json:"version"`
		Sets    []any  `json:"sets"`
		Removed []any  `json:"removedSets"`
	}
	if dateDetail.Code != 200 || json.Unmarshal(dateDetail.Body.Bytes(), &afterDate) != nil || afterDate.Date != earlier || afterDate.Version != 8 || len(afterDate.Sets) != 1 || len(afterDate.Removed) != 1 {
		t.Fatal("date change lost groups or did not advance the version")
	}
	unchangedDate := fmt.Sprintf(`{"expectedVersion":8,"date":"%s","exerciseEntryKeys":["entry-0001"]}`, earlier)
	unchanged := request(http.MethodPost, path+"/sets", unchangedDate, owner, "date-noop-001")
	var unchangedReceipt struct {
		Version uint64 `json:"version"`
	}
	if unchanged.Code != 200 || json.Unmarshal(unchanged.Body.Bytes(), &unchangedReceipt) != nil || unchangedReceipt.Version != 8 {
		t.Fatal("unchanged date advanced the version")
	}
	list := request(http.MethodGet, "/me/recordings?page=1&pageSize=10", "", owner, "")
	if list.Code != 200 {
		t.Fatalf("list status %d body %s", list.Code, list.Body.String())
	}
	// A second recording uses the same exercise in two separated segments.
	// Read it through HTTP and the encrypted database, not only the projection helper.
	repeatedCreate := fmt.Sprintf(`{"source":"free","date":"%s","timeZone":"UTC","clientEventId":"http-event-002","mode":"after_workout"}`, date)
	created = request(http.MethodPost, "/me/recordings", repeatedCreate, owner, "create-key-002")
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &ref) != nil {
		t.Fatal("create repeated-action recording")
	}
	repeatedPath := "/me/recordings/" + ref.ID
	repeatedBatch := `{"expectedVersion":1,"addEntries":[{"exerciseEntryKey":"repeat-001","exerciseName":"深蹲"},{"exerciseEntryKey":"repeat-002","exerciseName":"卧推"},{"exerciseEntryKey":"repeat-003","exerciseName":"深蹲"}],"sets":[{"recordKey":"repeat-set-001","exerciseEntryKey":"repeat-001","setIndex":1,"confirmed":false,"measurement":{"method":"total","weightKg":"40","reps":8}},{"recordKey":"repeat-set-002","exerciseEntryKey":"repeat-002","setIndex":1,"confirmed":false,"measurement":{"method":"total","weightKg":"30","reps":10}},{"recordKey":"repeat-set-003","exerciseEntryKey":"repeat-003","setIndex":1,"confirmed":false,"measurement":{"method":"total","weightKg":"50","reps":6}},{"recordKey":"repeat-set-004","exerciseEntryKey":"repeat-003","setIndex":2,"confirmed":false,"measurement":{"method":"total","weightKg":"50"}}],"exerciseEntryKeys":["repeat-001","repeat-002","repeat-003"]}`
	if got := request(http.MethodPost, repeatedPath+"/sets", repeatedBatch, owner, "repeat-key-01"); got.Code != 200 {
		t.Fatalf("save repeated action status %d", got.Code)
	}
	repeatedDetail := request(http.MethodGet, repeatedPath, "", owner, "")
	var actionResult struct {
		ActionVolumes []struct {
			Keys   []string `json:"exerciseEntryKeys"`
			Volume struct {
				CalculatedKg string `json:"calculatedKg"`
				FullSets     int    `json:"fullSets"`
				ExcludedSets int    `json:"excludedSets"`
			} `json:"volume"`
		} `json:"actionVolumes"`
	}
	if repeatedDetail.Code != 200 || json.Unmarshal(repeatedDetail.Body.Bytes(), &actionResult) != nil || len(actionResult.ActionVolumes) != 2 {
		t.Fatal("action totals absent from HTTP detail")
	}
	squatTotal := actionResult.ActionVolumes[0]
	if len(squatTotal.Keys) != 2 || squatTotal.Keys[0] != "repeat-001" || squatTotal.Keys[1] != "repeat-003" ||
		squatTotal.Volume.CalculatedKg != "620.00" || squatTotal.Volume.FullSets != 2 || squatTotal.Volume.ExcludedSets != 1 {
		t.Fatal("HTTP action total included a missing contribution or lost a segment")
	}
}
