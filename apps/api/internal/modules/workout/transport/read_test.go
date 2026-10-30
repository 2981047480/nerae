package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/middleware"
	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	authrepo "nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/application"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

func readTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_WORKOUT_INTEGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_WORKOUT_INTEGRATION_TEST_DSN via isolated MySQL harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_workout_integration_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC || filepath.Base(socket) != "mysql.sock" ||
		filepath.Dir(resolved) != "/private/tmp" || !strings.HasPrefix(filepath.Base(resolved), "nerae-workout-integration-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Fatal("could not open isolated database")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func readTestDevice(t *testing.T) (*ecdsa.PrivateKey, authdomain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("device generation failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	return key, authdomain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
}

func readTestProof(t *testing.T, key *ecdsa.PrivateKey, id, nonce string) string {
	t.Helper()
	jti, err := authdomain.GenerateChallengeID()
	if err != nil {
		t.Fatal("proof identifier failed")
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": id, "nonce": nonce,
		"iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("proof payload failed")
	}
	encode := base64.RawURLEncoding.EncodeToString
	input := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(input))
	r, signatureS, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("proof signing failed")
	}
	return input + "." + encode(append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...))
}

func TestWorkoutHTTPRead(t *testing.T) {
	db := readTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	hasher, _ := authdomain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := authrepo.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	emailProtector, _ := authdomain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	challenges, err := challenge.NewStore(hasher, replay, emailProtector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: authdomain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge setup failed")
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, emailProtector)
	resolver, _ := identity.NewResolver(emailProtector)
	codec, _ := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	auth, _ := session.NewStore(resolver, codec)
	nameProtector, _ := repository.NewExerciseNameProtector(bytes.Repeat([]byte{6}, 32), "workout-test-key")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil || nameProtector.InitializeExerciseKey(ctx, tx) != nil || tx.Commit() != nil {
		t.Fatal("workout key setup failed")
	}
	records, _ := repository.NewRecordStore(nameProtector)
	service, err := application.New(auth, repository.NewSessionStore(), records)
	if err != nil {
		t.Fatal("workout service setup failed")
	}
	validator, err := middleware.NewSchemaValidator(4<<20, 100000)
	if err != nil {
		t.Fatal("schema setup failed")
	}
	handler, err := NewReadHandler(db, service, validator)
	if err != nil {
		t.Fatal("HTTP handler setup failed")
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second
	login := func(email string, key *ecdsa.PrivateKey, public authdomain.DevicePublicJWK) (string, session.TokenClaims) {
		t.Helper()
		id, err := authdomain.GenerateChallengeID()
		if err != nil {
			t.Fatal("challenge ID failed")
		}
		nonce, err := authdomain.GenerateChallengeNonce()
		if err != nil {
			t.Fatal("challenge nonce failed")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("challenge transaction failed")
		}
		defer tx.Rollback()
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("challenge issue failed")
		}
		proof := readTestProof(t, key, id, nonce)
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("login transaction failed")
		}
		defer tx.Rollback()
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: proof})
		if err != nil || !attempt.Verified {
			t.Fatal("challenge verification failed")
		}
		issued, err := auth.IssueEmail(ctx, tx, attempt.Proof)
		if err != nil || tx.Commit() != nil {
			t.Fatal("login commit failed")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("token release failed")
		}
		claims, err := codec.Verify(token)
		if err != nil {
			t.Fatal("token verification failed")
		}
		return token, claims
	}
	keyA, publicA := readTestDevice(t)
	keyB, publicB := readTestDevice(t)
	tokenA, claimsA := login("http-first@example.test", keyA, publicA)
	tokenB, claimsB := login("http-second@example.test", keyB, publicB)
	tokenAgain, claimsAgain := login("http-first@example.test", keyA, publicA)
	if claimsA.SubjectID != claimsAgain.SubjectID || claimsA.SubjectID == claimsB.SubjectID {
		t.Fatal("invalid test subjects")
	}
	get := func(path, token string, forgedSubject string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
		if err != nil {
			t.Fatal("request construction failed")
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if forgedSubject != "" {
			req.Header.Set("X-User-Id", forgedSubject)
		}
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("local HTTP request failed")
		}
		defer response.Body.Close()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if err != nil {
			t.Fatal("HTTP response read failed")
		}
		if token != "" && strings.Contains(string(body), token) {
			t.Fatal("token leaked into HTTP response")
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("response is cacheable")
		}
		return response.StatusCode, response.Header, body
	}
	if status, _, _ := get("/me/sessions", "", ""); status != 401 {
		t.Fatal("missing token was accepted")
	}
	if status, _, _ := get("/me/sessions", tokenA, ""); status != 403 {
		t.Fatal("unverified age was accepted")
	}
	if err := auth.DeclareAdult(ctx, db, tokenA, true); err != nil {
		t.Fatal("first age declaration failed")
	}
	if err := auth.DeclareAdult(ctx, db, tokenB, true); err != nil {
		t.Fatal("second age declaration failed")
	}

	zone := "UTC"
	date := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	created, err := service.CreateFree(ctx, db, tokenA, repository.FreeSessionInput{Date: date, TimeZone: &zone, ClientEventID: "http-event-001"})
	if err != nil {
		t.Fatal("workout creation failed")
	}
	sets := []domain.Set{
		{RecordKey: "http-set-001", ExerciseEntryKey: "http-entry-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "卧推"}, Index: 1, Kind: domain.Weight, WeightKg: "42.50", Reps: 8, RPE: "7.5"},
		{RecordKey: "http-set-003", ExerciseEntryKey: "http-entry-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "卧推"}, Index: 3, Kind: domain.Weight, WeightKg: "45.25", Reps: 6},
		{RecordKey: "http-set-002", ExerciseEntryKey: "http-entry-002", Exercise: domain.Exercise{Source: domain.Custom, Name: "深蹲"}, Index: 1, Kind: domain.Bodyweight, Reps: 10},
	}
	if version, changed, err := service.SaveSets(ctx, db, tokenA, created.ID, 1, domain.Batch{Sets: sets}, []string{"http-entry-002", "http-entry-001"}); err != nil || !changed || version != 2 {
		t.Fatal("group save failed")
	}
	if version, err := service.CompleteOrderedFree(ctx, db, tokenA, created.ID, 2); err != nil || version != 3 {
		t.Fatal("completion failed")
	}

	status, headers, body := get("/me/sessions?page=1&pageSize=20", tokenAgain, claimsB.SubjectID)
	if status != 200 || headers.Get("Content-Type") != "application/json; charset=utf-8" || headers.Get("X-Request-ID") == "" {
		t.Fatal("list response failed")
	}
	var page struct {
		Items []struct {
			ID     string `json:"id"`
			Date   string `json:"date"`
			Status string `json:"status"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &page); err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != fmt.Sprint(created.ID) || page.Items[0].Date != date || page.Items[0].Status != "completed" {
		t.Fatal("list did not project authenticated owner")
	}
	status, _, body = get("/me/sessions/"+fmt.Sprint(created.ID), tokenAgain, "")
	if status != 200 {
		t.Fatal("detail response failed")
	}
	var detail struct {
		ID                string   `json:"id"`
		ClientEventID     string   `json:"clientEventId"`
		Status            string   `json:"status"`
		ExerciseEntryKeys []string `json:"exerciseEntryKeys"`
		Sets              []struct {
			RecordKey string      `json:"recordKey"`
			Index     int         `json:"setIndex"`
			Weight    json.Number `json:"weightKg"`
			Name      string      `json:"resolvedExerciseName"`
		} `json:"sets"`
	}
	if err := json.Unmarshal(body, &detail); err != nil || detail.ClientEventID != "http-event-001" || detail.Status != "completed" ||
		len(detail.ExerciseEntryKeys) != 2 || detail.ExerciseEntryKeys[0] != "http-entry-002" || detail.ExerciseEntryKeys[1] != "http-entry-001" ||
		len(detail.Sets) != 3 || detail.Sets[0].RecordKey != "http-set-002" || detail.Sets[1].RecordKey != "http-set-001" ||
		detail.Sets[1].Weight.String() != "42.50" || detail.Sets[1].Name != "卧推" || detail.Sets[2].Index != 3 || detail.Sets[2].Weight.String() != "45.25" {
		t.Fatal("detail lost identity, order, names or exact decimals")
	}
	status, _, body = get("/me/sessions", tokenB, claimsA.SubjectID)
	if status != 200 || !bytes.Contains(body, []byte(`"items":[]`)) || bytes.Contains(body, []byte("卧推")) {
		t.Fatal("other account list leaked")
	}
	status, _, body = get("/me/sessions/"+fmt.Sprint(created.ID), tokenB, "")
	if status != 404 || !bytes.Contains(body, []byte(`"code":"NOT_FOUND"`)) || bytes.Contains(body, []byte("卧推")) || bytes.Contains(body, []byte("http-event-001")) {
		t.Fatal("other account detail leaked or was not uniformly unavailable")
	}
	status, _, body = get("/me/sessions/"+fmt.Sprint(created.ID+1000), tokenB, "")
	if status != 404 || !bytes.Contains(body, []byte(`"code":"NOT_FOUND"`)) {
		t.Fatal("missing record was not reported consistently")
	}
	if os.Getenv("NERAE_WORKOUT_CLIENT_INTEROP") == "1" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			t.Fatal("could not locate interop test")
		}
		root, err := filepath.Abs(filepath.Join(workingDirectory, "../../../../../.."))
		if err != nil {
			t.Fatal("could not locate interop root")
		}
		certificate := filepath.Join(t.TempDir(), "local-test-ca.pem")
		if err := os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
			t.Fatal("could not prepare isolated TLS trust")
		}
		command := exec.CommandContext(ctx, "/opt/homebrew/bin/node", filepath.Join(root, "docs/evidence/A04/A04-07/client_interop.mjs"))
		command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+certificate, "NODE_TLS_REJECT_UNAUTHORIZED=1",
			"NERAE_INTEROP_URL="+server.URL, "NERAE_INTEROP_CLIENT_DIR="+filepath.Join(root, "apps/client/src/features/training"),
			"NERAE_INTEROP_SUBJECT_A="+claimsA.SubjectID, "NERAE_INTEROP_TOKEN_A="+tokenA,
			"NERAE_INTEROP_SUBJECT_B="+claimsB.SubjectID, "NERAE_INTEROP_TOKEN_B="+tokenB,
			"NERAE_INTEROP_TODAY="+time.Now().UTC().Format("2006-01-02"))
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("real client adapter interoperability failed: %s", output)
		}
		if !bytes.Contains(output, []byte("PASS: real client adapter")) {
			t.Fatal("client interoperability did not run")
		}
	}
	for _, path := range []string{"/me/sessions?page=0", "/me/sessions?pageSize=51", "/me/sessions?page=1&page=2", "/me/sessions?from=2026-02-30", "/me/sessions/0001"} {
		if status, _, _ := get(path, tokenA, ""); status != 422 {
			t.Fatal("invalid input was accepted")
		}
	}
	if err := auth.ClearAdultDeclaration(ctx, db, tokenA); err != nil {
		t.Fatal("age withdrawal failed")
	}
	if status, _, _ := get("/me/sessions/"+fmt.Sprint(created.ID), tokenAgain, ""); status != 403 {
		t.Fatal("withdrawn age declaration read data")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'disabled' WHERE id = ?`, claimsB.SubjectID); err != nil {
		t.Fatal("disable fixture failed")
	}
	if status, _, _ := get("/me/sessions", tokenB, ""); status == 200 {
		t.Fatal("disabled subject accessed list")
	}
	if err := db.Close(); err != nil {
		t.Fatal("could not close isolated test database")
	}
	if status, _, _ := get("/me/sessions/"+fmt.Sprint(created.ID), tokenA, ""); status != 503 {
		t.Fatal("database failure was not reported as unavailable")
	}
}

func TestWorkoutHTTPConstructorRejectsMissingDependencies(t *testing.T) {
	if handler, err := NewReadHandler(nil, nil, nil); handler != nil || !errors.Is(err, middleware.ErrContract) {
		t.Fatal("missing HTTP dependencies accepted")
	}
}
