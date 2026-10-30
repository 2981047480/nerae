package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nerae/api/internal/idempotency"
	"nerae/api/internal/middleware"
	"nerae/api/internal/modules/auth/challenge"
	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	authrepo "nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/application"
	"nerae/api/internal/modules/workout/repository"
)

func TestWorkoutHTTPWrite(t *testing.T) {
	db := readTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	hasher, err := authdomain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal("email hasher setup failed")
	}
	replay, err := authrepo.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal("proof replay setup failed")
	}
	protector, err := authdomain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	if err != nil {
		t.Fatal("email protector setup failed")
	}
	challenges, err := challenge.NewStore(hasher, replay, protector, challenge.Policy{
		ChallengeTTL: 5 * time.Minute, MaxAttempts: 3,
		DeviceProof: authdomain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second},
	})
	if err != nil {
		t.Fatal("challenge store setup failed")
	}
	resolver, err := identity.NewResolver(protector)
	if err != nil {
		t.Fatal("identity resolver setup failed")
	}
	codec, err := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("token codec setup failed")
	}
	auth, err := session.NewStore(resolver, codec)
	if err != nil {
		t.Fatal("session store setup failed")
	}
	nameProtector, err := repository.NewExerciseNameProtector(bytes.Repeat([]byte{6}, 32), "workout-test-key")
	if err != nil {
		t.Fatal("exercise protector setup failed")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("exercise key transaction failed")
	}
	if err := nameProtector.InitializeExerciseKey(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("exercise key initialization failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("exercise key commit failed")
	}
	service, err := application.New(auth, repository.NewSessionStore(), mustRecordStore(t, nameProtector))
	if err != nil {
		t.Fatal("workout service setup failed")
	}
	validator, err := middleware.NewSchemaValidator(4<<20, 100000)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	executor, err := application.NewWorkoutExecutor(auth, bytes.Repeat([]byte{17}, 32), bytes.Repeat([]byte{18}, 32), "write-test-idempotency", time.Hour)
	if err != nil {
		t.Fatal("workout executor setup failed")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("idempotency key transaction failed")
	}
	if err := executor.InitializeKey(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatal("idempotency key initialization failed")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal("idempotency key commit failed")
	}
	writeHandler, err := NewWriteHandler(db, service, validator, executor)
	if err != nil {
		t.Fatal("write handler setup failed")
	}
	readHandler, err := NewReadHandler(db, service, validator)
	if err != nil {
		t.Fatal("read handler setup failed")
	}
	combined := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			readHandler.ServeHTTP(w, r)
			return
		}
		writeHandler.ServeHTTP(w, r)
	})
	server := httptest.NewTLSServer(combined)
	defer server.Close()
	client := server.Client()
	client.Timeout = 8 * time.Second

	login := func(label string, key *ecdsa.PrivateKey, public authdomain.DevicePublicJWK) string {
		t.Helper()
		challengeID, err := authdomain.GenerateChallengeID()
		if err != nil {
			t.Fatal("challenge ID generation failed")
		}
		nonce, err := authdomain.GenerateChallengeNonce()
		if err != nil {
			t.Fatal("challenge nonce generation failed")
		}
		email := fmt.Sprintf("%s@example.test", label)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("challenge transaction failed")
		}
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: challengeID, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil {
			_ = tx.Rollback()
			t.Fatal("challenge issue failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("challenge issue commit failed")
		}
		proof := readTestProof(t, key, challengeID, nonce)
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("login transaction failed")
		}
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: challengeID, Code: "012345", DeviceProof: proof})
		if err != nil || !attempt.Verified {
			_ = tx.Rollback()
			t.Fatalf("challenge verification failed (verified=%t, err=%v)", attempt.Verified, err)
		}
		issued, err := auth.IssueEmail(ctx, tx, attempt.Proof)
		if err != nil {
			_ = tx.Rollback()
			t.Fatal("session issue failed")
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("login commit failed")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil || token == "" {
			t.Fatal("session release failed")
		}
		return token
	}
	keyA, publicA := readTestDevice(t)
	keyB, publicB := readTestDevice(t)
	tokenA := login("write-a", keyA, publicA)
	tokenAAgain := login("write-a", keyA, publicA)
	tokenB := login("write-b", keyB, publicB)
	claimsA, err := codec.Verify(tokenA)
	if err != nil {
		t.Fatal("synthetic session A token verification failed")
	}
	claimsAAgain, err := codec.Verify(tokenAAgain)
	if err != nil || claimsAAgain.SubjectID != claimsA.SubjectID {
		t.Fatal("second synthetic session did not resolve to the same subject")
	}
	claimsB, err := codec.Verify(tokenB)
	if err != nil || claimsB.SubjectID == claimsA.SubjectID {
		t.Fatal("synthetic session B did not resolve to a distinct subject")
	}

	type httpResult struct {
		status  int
		headers http.Header
		body    []byte
	}
	request := func(method, path string, body []byte, token, key string) httpResult {
		t.Helper()
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+path, reader)
		if err != nil {
			t.Fatal("request construction failed")
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("local HTTPS request failed")
		}
		defer response.Body.Close()
		responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal("HTTP response read failed")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("status %d response was cacheable", response.StatusCode)
		}
		if bytes.Contains(responseBody, []byte(tokenA)) || bytes.Contains(responseBody, []byte(tokenAAgain)) || bytes.Contains(responseBody, []byte(tokenB)) {
			t.Fatal("HTTP response leaked a bearer token")
		}
		return httpResult{status: response.StatusCode, headers: response.Header.Clone(), body: responseBody}
	}
	assertRequestID := func(result httpResult) string {
		t.Helper()
		var payload struct {
			RequestID string `json:"requestId"`
		}
		if err := json.Unmarshal(result.body, &payload); err != nil || payload.RequestID == "" || result.headers.Get("X-Request-ID") != payload.RequestID {
			t.Fatalf("error request ID did not match body: %s / %s", result.headers.Get("X-Request-ID"), result.body)
		}
		return payload.RequestID
	}

	date := time.Now().UTC().Format("2006-01-02")
	createBody := []byte(fmt.Sprintf(`{"source":"free","date":%q,"timeZone":"UTC","clientEventId":"http-write-event-001"}`, date))
	if got := request(http.MethodPost, "/me/sessions", createBody, "", "create-key-0001"); got.status != http.StatusUnauthorized {
		t.Fatalf("missing bearer returned %d, want 401", got.status)
	}
	if got := request(http.MethodPost, "/me/sessions", createBody, tokenA, "create-key-0001"); got.status != http.StatusForbidden {
		t.Fatalf("undeclared age returned %d, want 403", got.status)
	}
	if err := auth.DeclareAdult(ctx, db, tokenA, true); err != nil {
		t.Fatal("adult declaration A failed")
	}
	if err := auth.DeclareAdult(ctx, db, tokenB, true); err != nil {
		t.Fatal("adult declaration B failed")
	}
	if got := request(http.MethodPost, "/me/sessions", []byte(`{"source":"free"`), tokenA, "malformed-key-0001"); got.status != http.StatusUnprocessableEntity {
		t.Fatalf("malformed JSON returned %d, want 422", got.status)
	}

	planBody := []byte(fmt.Sprintf(`{"source":"plan","date":%q,"timeZone":"UTC","planId":"101","planVersion":1,"planDayId":"101","clientEventId":"http-plan-event-001"}`, date))
	if got := request(http.MethodPost, "/me/sessions", planBody, tokenA, "plan-key-0001"); got.status != http.StatusServiceUnavailable {
		t.Fatalf("valid unsupported plan create returned %d, want 503", got.status)
	}

	created := request(http.MethodPost, "/me/sessions", createBody, tokenA, "create-key-0001")
	if created.status != http.StatusCreated || created.headers.Get("Idempotency-Key-Expires-At") == "" || created.headers.Get("X-Request-ID") == "" {
		t.Fatalf("free creation failed or omitted success headers: %d %v %s", created.status, created.headers, created.body)
	}
	expires, err := time.Parse(time.RFC3339Nano, created.headers.Get("Idempotency-Key-Expires-At"))
	if err != nil || !expires.After(time.Now()) {
		t.Fatal("creation idempotency expiry header was invalid or stale")
	}
	var createdSession struct {
		ID            string `json:"id"`
		ClientEventID string `json:"clientEventId"`
		Status        string `json:"status"`
		Version       int    `json:"version"`
	}
	if err := json.Unmarshal(created.body, &createdSession); err != nil || createdSession.ID == "" || createdSession.ClientEventID != "http-write-event-001" || createdSession.Status != "in_progress" || createdSession.Version != 1 {
		t.Fatalf("created detail was incomplete: %s", created.body)
	}
	createReplay := request(http.MethodPost, "/me/sessions", createBody, tokenA, "create-key-0001")
	if createReplay.status != http.StatusCreated || !bytes.Equal(createReplay.body, created.body) || createReplay.headers.Get("Idempotency-Key-Expires-At") != created.headers.Get("Idempotency-Key-Expires-At") {
		t.Fatal("create idempotency replay did not return the original result")
	}

	var sessionID uint64
	if _, err := fmt.Sscan(createdSession.ID, &sessionID); err != nil || sessionID == 0 {
		t.Fatal("created session ID was not numeric")
	}
	setsPath := "/me/sessions/" + createdSession.ID + "/sets"
	setBody := []byte(`{"expectedVersion":1,"exerciseEntryKeys":["write-entry-001"],"sets":[{"recordKey":"write-record-001","exerciseEntryKey":"write-entry-001","exerciseName":"卧推","kind":"weight","setIndex":1,"weightKg":42.50,"reps":8,"rpe":7.5,"completed":true}]}`)
	saved := request(http.MethodPost, setsPath, setBody, tokenA, "sets-key-0001")
	if saved.status != http.StatusOK || saved.headers.Get("Idempotency-Key-Expires-At") == "" {
		t.Fatalf("set batch save failed: %d %s", saved.status, saved.body)
	}
	var savedDetail struct {
		ID                string   `json:"id"`
		Status            string   `json:"status"`
		Version           int      `json:"version"`
		ExerciseEntryKeys []string `json:"exerciseEntryKeys"`
		Sets              []struct {
			RecordKey string      `json:"recordKey"`
			WeightKg  json.Number `json:"weightKg"`
			RPE       json.Number `json:"rpe"`
			Completed bool        `json:"completed"`
			Name      string      `json:"resolvedExerciseName"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(saved.body, &savedDetail); err != nil || savedDetail.ID != createdSession.ID || savedDetail.Version != 2 || savedDetail.Status != "in_progress" ||
		len(savedDetail.ExerciseEntryKeys) != 1 || savedDetail.ExerciseEntryKeys[0] != "write-entry-001" || len(savedDetail.Sets) != 1 ||
		savedDetail.Sets[0].RecordKey != "write-record-001" || savedDetail.Sets[0].WeightKg.String() != "42.50" || savedDetail.Sets[0].RPE.String() != "7.5" || !savedDetail.Sets[0].Completed || savedDetail.Sets[0].Name != "卧推" {
		t.Fatalf("saved response lost schema data or decimal precision: %s", saved.body)
	}
	savedReplay := request(http.MethodPost, setsPath, setBody, tokenA, "sets-key-0001")
	if savedReplay.status != http.StatusOK || !bytes.Equal(savedReplay.body, saved.body) || savedReplay.headers.Get("Idempotency-Key-Expires-At") != saved.headers.Get("Idempotency-Key-Expires-At") {
		t.Fatal("identical set request did not replay the original response")
	}

	otherBody := []byte(strings.Replace(string(setBody), "42.50", "43.00", 1))
	conflict := request(http.MethodPost, setsPath, otherBody, tokenA, "sets-key-0001")
	if conflict.status != http.StatusConflict || bytes.Contains(conflict.body, []byte("write-record-001")) || assertRequestID(conflict) == "" {
		t.Fatalf("same key with another request did not return a safe 409: %d %s", conflict.status, conflict.body)
	}

	staleBody := []byte(`{"expectedVersion":1,"exerciseEntryKeys":["write-entry-001"],"deleteRecordKeys":["write-record-001"]}`)
	stale := request(http.MethodPost, setsPath, staleBody, tokenA, "stale-key-0001")
	if stale.status != http.StatusPreconditionFailed || !strings.Contains(string(stale.body), `"code":"VERSION_CONFLICT"`) {
		t.Fatalf("stale version returned %d, want 412: %s", stale.status, stale.body)
	}
	staleID := assertRequestID(stale)
	staleReplay := request(http.MethodPost, setsPath, staleBody, tokenA, "stale-key-0001")
	if staleReplay.status != http.StatusPreconditionFailed || !bytes.Equal(staleReplay.body, stale.body) || staleReplay.headers.Get("X-Request-ID") != staleID {
		t.Fatal("cached 412 did not preserve exact body and request ID")
	}

	duplicateJSON := []byte(`{"expectedVersion":2,"expectedVersion":2,"exerciseEntryKeys":["write-entry-001"],"sets":[{"recordKey":"write-record-002","exerciseEntryKey":"write-entry-001","exerciseName":"卧推","kind":"weight","setIndex":2,"weightKg":10,"reps":5}]}`)
	if got := request(http.MethodPost, setsPath, duplicateJSON, tokenA, "duplicate-key-0001"); got.status != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate JSON key returned %d, want 422", got.status)
	}
	precisionBody := []byte(strings.Replace(string(setBody), `"expectedVersion":1`, `"expectedVersion":2`, 1))
	precisionBody = []byte(strings.Replace(string(precisionBody), "42.50", "42.501", 1))
	if got := request(http.MethodPost, setsPath, precisionBody, tokenA, "precision-key-0001"); got.status != http.StatusUnprocessableEntity {
		t.Fatalf("three decimal places returned %d, want 422", got.status)
	}
	orderBody := []byte(strings.Replace(string(setBody), `"expectedVersion":1`, `"expectedVersion":2`, 1))
	orderBody = []byte(strings.Replace(string(orderBody), `"exerciseEntryKeys":["write-entry-001"]`, `"exerciseEntryKeys":["other-entry-001"]`, 1))
	if got := request(http.MethodPost, setsPath, orderBody, tokenA, "order-key-0001"); got.status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid complete order returned %d, want 422", got.status)
	}

	catalogBody := []byte(`{"expectedVersion":2,"exerciseEntryKeys":["write-entry-001"],"sets":[{"recordKey":"write-record-002","exerciseEntryKey":"write-entry-001","exerciseId":"101","kind":"bodyweight","setIndex":2,"reps":5}]}`)
	if got := request(http.MethodPost, setsPath, catalogBody, tokenA, "catalog-key-0001"); got.status != http.StatusServiceUnavailable {
		t.Fatalf("valid unsupported catalog action returned %d, want 503", got.status)
	}
	if got := request(http.MethodPost, setsPath, setBody, tokenB, "sets-key-0001"); got.status != http.StatusForbidden || bytes.Contains(got.body, []byte("write-record-001")) || bytes.Contains(got.body, []byte("42.50")) {
		t.Fatalf("another subject received cached response or non-generic result: %d %s", got.status, got.body)
	}

	finishPath := "/me/sessions/" + createdSession.ID + "/finish"
	optionalFinish := []byte(`{"expectedVersion":2,"durationMin":null}`)
	if got := request(http.MethodPost, finishPath, optionalFinish, tokenA, "finish-extra-0001"); got.status != http.StatusServiceUnavailable {
		t.Fatalf("unsupported finish field returned %d, want 503", got.status)
	}
	finishBody := []byte(`{"expectedVersion":2}`)
	finished := request(http.MethodPost, finishPath, finishBody, tokenA, "finish-key-0001")
	if finished.status != http.StatusOK || finished.headers.Get("Idempotency-Key-Expires-At") == "" {
		t.Fatalf("finish failed or omitted expiry header: %d %s", finished.status, finished.body)
	}
	var finishedDetail struct {
		Status  string `json:"status"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(finished.body, &finishedDetail); err != nil || finishedDetail.Status != "completed" || finishedDetail.Version != 3 {
		t.Fatalf("finish response was not a complete confirmation: %s", finished.body)
	}
	finishReplay := request(http.MethodPost, finishPath, finishBody, tokenA, "finish-key-0001")
	if finishReplay.status != http.StatusOK || !bytes.Equal(finishReplay.body, finished.body) {
		t.Fatal("finish replay did not return the original response")
	}

	readback := request(http.MethodGet, "/me/sessions/"+createdSession.ID, nil, tokenA, "")
	if readback.status != http.StatusOK {
		t.Fatalf("real HTTP readback returned %d: %s", readback.status, readback.body)
	}
	var readDetail struct {
		ID            string `json:"id"`
		ClientEventID string `json:"clientEventId"`
		Status        string `json:"status"`
		Version       int    `json:"version"`
		Sets          []struct {
			Weight json.Number `json:"weightKg"`
			Key    string      `json:"recordKey"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(readback.body, &readDetail); err != nil || readDetail.ID != createdSession.ID || readDetail.ClientEventID != "http-write-event-001" ||
		readDetail.Status != "completed" || readDetail.Version != 3 || len(readDetail.Sets) != 1 || readDetail.Sets[0].Key != "write-record-001" || readDetail.Sets[0].Weight.String() != "42.50" {
		t.Fatalf("HTTP readback differed from acknowledged write: %s", readback.body)
	}

	// Exercise exact JSON-number parsing through the real HTTP boundary. Invalid
	// values must fail before persisting anything; the subsequent valid request
	// uses the original version and complete entry order.
	numericCreateBody := []byte(fmt.Sprintf(`{"source":"free","date":%q,"timeZone":"UTC","clientEventId":"http-numeric-event-001"}`, date))
	numericCreated := request(http.MethodPost, "/me/sessions", numericCreateBody, tokenA, "numeric-create-key-001")
	if numericCreated.status != http.StatusCreated {
		t.Fatalf("numeric test session create returned %d: %s", numericCreated.status, numericCreated.body)
	}
	var numericSession struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(numericCreated.body, &numericSession); err != nil || numericSession.ID == "" {
		t.Fatalf("numeric test session response was invalid: %s", numericCreated.body)
	}
	numericSetsPath := "/me/sessions/" + numericSession.ID + "/sets"
	numericBody := []byte(`{"expectedVersion":1e0,"exerciseEntryKeys":["numeric-entry-001"],"sets":[{"recordKey":"numeric-record-001","exerciseEntryKey":"numeric-entry-001","exerciseName":"划船","kind":"weight","setIndex":1.0,"weightKg":4.250e1,"reps":8e0,"rpe":75e-1}]}`)
	invalidNumericBodies := []struct {
		name string
		body []byte
	}{
		{"excess precision", []byte(strings.Replace(string(numericBody), "4.250e1", "4.2501e1", 1))},
		{"three decimal places with trailing zero", []byte(strings.Replace(string(numericBody), "4.250e1", "42.500", 1))},
		{"three decimal places after exponent", []byte(strings.Replace(string(numericBody), "4.250e1", "4.2500e1", 1))},
		{"non-integer", []byte(strings.Replace(string(numericBody), "8e0", "8.5e0", 1))},
		{"uint64 overflow", []byte(strings.Replace(string(numericBody), `"setIndex":1.0`, `"setIndex":18446744073709551616`, 1))},
		{"extreme exponent", []byte(strings.Replace(string(numericBody), "4.250e1", "1e99999999999999999999999999999999999999", 1))},
	}
	for index, invalid := range invalidNumericBodies {
		result := request(http.MethodPost, numericSetsPath, invalid.body, tokenA, fmt.Sprintf("numeric-invalid-key-%04d", index+1))
		if result.status != http.StatusUnprocessableEntity {
			t.Fatalf("%s JSON number returned %d, want 422: %s", invalid.name, result.status, result.body)
		}
		unchanged := request(http.MethodGet, "/me/sessions/"+numericSession.ID, nil, tokenA, "")
		var snapshot struct {
			Version int               `json:"version"`
			Sets    []json.RawMessage `json:"sets"`
		}
		if unchanged.status != http.StatusOK || json.Unmarshal(unchanged.body, &snapshot) != nil || snapshot.Version != 1 || len(snapshot.Sets) != 0 {
			t.Fatalf("%s JSON number changed persisted session state: %s", invalid.name, unchanged.body)
		}
	}
	numericSaved := request(http.MethodPost, numericSetsPath, numericBody, tokenA, "numeric-valid-key-0001")
	if numericSaved.status != http.StatusOK {
		t.Fatalf("exact integral/exponent JSON numbers returned %d: %s", numericSaved.status, numericSaved.body)
	}
	var numericDetail struct {
		Version int `json:"version"`
		Sets    []struct {
			Index     int         `json:"setIndex"`
			Weight    json.Number `json:"weightKg"`
			Reps      json.Number `json:"reps"`
			RPE       json.Number `json:"rpe"`
			Completed bool        `json:"completed"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(numericSaved.body, &numericDetail); err != nil || numericDetail.Version != 2 || len(numericDetail.Sets) != 1 || numericDetail.Sets[0].Index != 1 ||
		!equalJSONNumber(numericDetail.Sets[0].Weight, "42.50") || !equalJSONNumber(numericDetail.Sets[0].Reps, "8") || !equalJSONNumber(numericDetail.Sets[0].RPE, "7.5") || !numericDetail.Sets[0].Completed {
		t.Fatalf("exponent JSON values were not preserved exactly: %s", numericSaved.body)
	}
	if os.Getenv("NERAE_WORKOUT_WRITE_CLIENT_INTEROP") == "1" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			t.Fatal("could not locate interop test")
		}
		root, err := filepath.Abs(filepath.Join(workingDirectory, "../../../../../.."))
		if err != nil {
			t.Fatal("could not locate repository root")
		}
		certificate := filepath.Join(t.TempDir(), "local-test-ca.pem")
		parsedCertificate, err := x509.ParseCertificate(server.Certificate().Raw)
		if err != nil {
			t.Fatal("could not parse local TLS certificate")
		}
		if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: parsedCertificate.Raw}), 0600); err != nil {
			t.Fatal("could not prepare local TLS trust")
		}
		command := exec.CommandContext(ctx, "/opt/homebrew/bin/node", filepath.Join(root, "docs/evidence/A04/A04-09/client_write_interop.mjs"))
		command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+certificate, "NODE_TLS_REJECT_UNAUTHORIZED=1",
			"NERAE_INTEROP_URL="+server.URL, "NERAE_INTEROP_CLIENT_DIR="+filepath.Join(root, "apps/client/src/features/training"),
			"NERAE_INTEROP_SUBJECT_A="+claimsA.SubjectID, "NERAE_INTEROP_TOKEN_A="+tokenA,
			"NERAE_INTEROP_TOKEN_AGAIN="+tokenAAgain, "NERAE_INTEROP_SUBJECT_B="+claimsB.SubjectID,
			"NERAE_INTEROP_TOKEN_B="+tokenB, "NERAE_INTEROP_DATE="+date)
		output, err := command.CombinedOutput()
		if err != nil {
			redacted := bytes.ReplaceAll(output, []byte(tokenA), []byte("[redacted]"))
			redacted = bytes.ReplaceAll(redacted, []byte(tokenAAgain), []byte("[redacted]"))
			redacted = bytes.ReplaceAll(redacted, []byte(tokenB), []byte("[redacted]"))
			t.Fatalf("real client adapter interoperability failed: %s", redacted)
		}
		if !bytes.Contains(output, []byte("PASS: actual client adapter")) {
			t.Fatal("client interoperability did not run")
		}
		t.Log("PASS: actual client adapter created, saved, replayed, finished and restored over isolated TLS HTTP")
	}
}

func TestWriteResultRevalidatesCachedBodies(t *testing.T) {
	validator, err := middleware.NewSchemaValidator(4<<20, 100000)
	if err != nil {
		t.Fatal("schema validator setup failed")
	}
	handler := &writeHandler{validator: validator}
	id, err := middleware.NewRequestID()
	if err != nil {
		t.Fatal("request ID setup failed")
	}
	marker := "legacy-private-cached-body"
	invalid := idempotency.Result{Status: http.StatusCreated, Body: []byte(`{"id":"` + marker + `"}`), Replayed: true, ExpiresAt: time.Now().Add(time.Hour)}
	recorder := httptest.NewRecorder()
	handler.writeResult(recorder, id, invalid)
	if recorder.Code != http.StatusServiceUnavailable || bytes.Contains(recorder.Body.Bytes(), []byte(marker)) ||
		!bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"SERVICE_UNAVAILABLE"`)) || recorder.Header().Get("Cache-Control") != "no-store" || recorder.Header().Get("Idempotency-Key-Expires-At") != "" {
		t.Fatalf("invalid cached schema was returned instead of a generic 503: %d %s", recorder.Code, recorder.Body.Bytes())
	}
	var failure struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil || failure.RequestID == "" || recorder.Header().Get("X-Request-ID") != failure.RequestID {
		t.Fatalf("generic 503 request ID was inconsistent: %v", recorder.Header())
	}

	validBody := []byte(`{"id":"1","date":"2026-10-30","timeZone":"UTC","status":"in_progress","source":"free","version":1,"createdAt":"2026-10-30T00:00:00Z","updatedAt":"2026-10-30T00:00:00Z","recordedAt":null,"clientEventId":"cached-event-001","sets":[],"exerciseEntryKeys":[]}`)
	expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	valid := idempotency.Result{Status: http.StatusCreated, Body: validBody, ResultRef: "1", ExpiresAt: expiresAt, Replayed: true}
	validRecorder := httptest.NewRecorder()
	handler.writeResult(validRecorder, id, valid)
	if validRecorder.Code != http.StatusCreated || !bytes.Equal(validRecorder.Body.Bytes(), validBody) || validRecorder.Header().Get("Cache-Control") != "no-store" ||
		validRecorder.Header().Get("Idempotency-Key-Expires-At") != expiresAt.Format(time.RFC3339Nano) {
		t.Fatalf("valid cached response was not returned byte-for-byte: %d %s", validRecorder.Code, validRecorder.Body.Bytes())
	}
}

func mustRecordStore(t *testing.T, protector *repository.ExerciseNameProtector) *repository.RecordStore {
	t.Helper()
	store, err := repository.NewRecordStore(protector)
	if err != nil {
		t.Fatal("record store setup failed")
	}
	return store
}

func equalJSONNumber(actual json.Number, expected string) bool {
	actualRat, ok := new(big.Rat).SetString(actual.String())
	if !ok {
		return false
	}
	expectedRat, ok := new(big.Rat).SetString(expected)
	return ok && actualRat.Cmp(expectedRat) == 0
}
