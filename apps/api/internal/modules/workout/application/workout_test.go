package application

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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"nerae/api/internal/modules/auth/challenge"
	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	authrepo "nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

func integrationDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_WORKOUT_INTEGRATION_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_WORKOUT_INTEGRATION_TEST_DSN using the isolated MySQL harness")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated integration DSN")
	}
	socket := filepath.Clean(cfg.Addr)
	parent := filepath.Dir(socket)
	resolved, err := filepath.EvalSymlinks(parent)
	info, statErr := os.Lstat(parent)
	socketInfo, socketErr := os.Lstat(socket)
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_workout_integration_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-workout-integration-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("could not open isolated integration database")
	}
	db.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = db.Close() })
	if db.Ping() != nil {
		t.Fatal("could not connect to isolated integration database")
	}
	return db
}

func integrationDevice(t *testing.T) (*ecdsa.PrivateKey, authdomain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("could not generate synthetic device")
	}
	encode := base64.RawURLEncoding.EncodeToString
	return key, authdomain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
}

func integrationProof(t *testing.T, key *ecdsa.PrivateKey, id, nonce string) string {
	t.Helper()
	jti, err := authdomain.GenerateChallengeID()
	if err != nil {
		t.Fatal("could not generate proof ID")
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": id,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("could not create proof")
	}
	encode := base64.RawURLEncoding.EncodeToString
	input := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(input))
	r, signatureS, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("could not sign proof")
	}
	return input + "." + encode(append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...))
}

func TestAuthenticatedWorkoutTransactions(t *testing.T) {
	db := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	hasher, _ := authdomain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	replay, _ := authrepo.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	emailProtector, _ := authdomain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "test-key")
	challenges, err := challenge.NewStore(hasher, replay, emailProtector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: authdomain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("could not initialize challenges")
	}
	resolver, _ := identity.NewResolver(emailProtector)
	codec, _ := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	auth, _ := session.NewStore(resolver, codec)
	nameProtector, _ := repository.NewExerciseNameProtector(bytes.Repeat([]byte{6}, 32), "workout-test-key")
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || nameProtector.InitializeExerciseKey(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("could not initialize test encryption epoch")
	}
	records, _ := repository.NewRecordStore(nameProtector)
	service, err := New(auth, repository.NewSessionStore(), records)
	if err != nil {
		t.Fatal("could not initialize workout service")
	}
	login := func(email string, key *ecdsa.PrivateKey, public authdomain.DevicePublicJWK) (string, session.TokenClaims) {
		t.Helper()
		id, _ := authdomain.GenerateChallengeID()
		nonce, _ := authdomain.GenerateChallengeNonce()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not start challenge")
		}
		defer tx.Rollback()
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("could not create test challenge")
		}
		proof := integrationProof(t, key, id, nonce)
		tx, err = db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("could not begin login transaction")
		}
		defer tx.Rollback()
		attempt, err := challenges.Attempt(ctx, tx, challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: proof})
		if err != nil || !attempt.Verified {
			t.Fatal("could not verify test login")
		}
		issued, err := auth.IssueEmail(ctx, tx, attempt.Proof)
		if err != nil || tx.Commit() != nil {
			t.Fatal("could not commit test session")
		}
		token, err := issued.AfterCommit(ctx, db)
		if err != nil {
			t.Fatal("could not obtain committed test session")
		}
		claims, err := codec.Verify(token)
		if err != nil {
			t.Fatal("could not verify test token")
		}
		return token, claims
	}
	keyA, publicA := integrationDevice(t)
	keyB, publicB := integrationDevice(t)
	tokenA, claimsA := login("first@example.test", keyA, publicA)
	tokenB, claimsB := login("second@example.test", keyB, publicB)
	tokenAgain, claimsAgain := login("first@example.test", keyA, publicA)
	if claimsA.SubjectID != claimsAgain.SubjectID || claimsA.SessionID == claimsAgain.SessionID || claimsA.SubjectID == claimsB.SubjectID {
		t.Fatal("test sessions do not have the intended ownership")
	}
	zone := "UTC"
	input := repository.FreeSessionInput{Date: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"), TimeZone: &zone, ClientEventID: "event-integration"}
	if _, err := service.CreateFree(ctx, db, tokenA, input); !errors.Is(err, ErrAgeRequired) {
		t.Fatal("unverified age could create a workout")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_session`).Scan(&count); err != nil || count != 0 {
		t.Fatal("age-rejected workout left data")
	}
	// This is an isolated database fixture, not a real age-verification flow.
	if _, err := db.ExecContext(ctx, `UPDATE user SET age_verified_at = UTC_TIMESTAMP(6) WHERE id IN (?, ?)`, claimsA.SubjectID, claimsB.SubjectID); err != nil {
		t.Fatal("could not set synthetic age fixtures")
	}
	created, err := service.CreateFree(ctx, db, tokenA, input)
	if err != nil || created.ID == 0 || created.Version != 1 {
		t.Fatal("authenticated subject could not create workout")
	}
	first := domain.Set{RecordKey: "record-001", ExerciseEntryKey: "entry-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "自定义动作"},
		Index: 1, Kind: domain.Weight, WeightKg: "42.5", Reps: 8}
	second := first
	second.RecordKey, second.Index, second.WeightKg = "record-002", 2, "45"
	if v, changed, err := service.SaveSets(ctx, db, tokenA, created.ID, 1, domain.Batch{Sets: []domain.Set{first, second}}); err != nil || !changed || v != 2 {
		t.Fatal("authenticated subject could not save sets")
	}
	snapshot, err := service.ReadFree(ctx, db, tokenAgain, created.ID)
	if err != nil || snapshot.Version != 2 || len(snapshot.Sets) != 2 || snapshot.Sets[first.RecordKey].WeightKg != "42.50" || snapshot.Sets[first.RecordKey].Exercise.Name != first.Exercise.Name {
		t.Fatal("another session did not recover the same persisted workout")
	}
	if _, err := service.ReadFree(ctx, db, tokenB, created.ID); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("different subject read a private workout")
	}
	if _, _, err := service.SaveSets(ctx, db, tokenB, created.ID, 2, domain.Batch{Sets: []domain.Set{first}}); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("different subject modified a private workout")
	}
	modified := first
	modified.WeightKg = "50"
	if v, changed, err := service.SaveSets(ctx, db, tokenAgain, created.ID, 2, domain.Batch{Sets: []domain.Set{modified}}); err != nil || !changed || v != 3 {
		t.Fatal("second session could not modify the same workout")
	}
	if _, _, err := service.SaveSets(ctx, db, tokenA, created.ID, 2, domain.Batch{Sets: []domain.Set{first}}); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatal("stale session overwrote a newer workout")
	}
	snapshot, err = service.ReadFree(ctx, db, tokenA, created.ID)
	if err != nil || snapshot.Version != 3 || snapshot.Sets[first.RecordKey].WeightKg != "50.00" {
		t.Fatal("stale write changed the persisted workout")
	}
	// Simulate a revocation inside the same transaction after a group update.
	// The service's final authorization check must roll both changes back.
	attempted := first
	attempted.WeightKg = "60"
	err = service.inTransaction(ctx, db, tokenA, func(tx *sql.Tx, userID uint64) error {
		if _, _, err := records.ApplyBatch(ctx, tx, userID, created.ID, 3, domain.Batch{Sets: []domain.Set{attempted}}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, userID)
		return err
	})
	if !errors.Is(err, ErrAuthentication) {
		t.Fatal("same-transaction revocation was not rejected")
	}
	snapshot, err = service.ReadFree(ctx, db, tokenA, created.ID)
	if err != nil || snapshot.Version != 3 || snapshot.Sets[first.RecordKey].WeightKg != "50.00" {
		t.Fatal("authorization failure did not roll back the workout write")
	}
	// A change to the age marker in the same transaction must also roll back.
	err = service.inTransaction(ctx, db, tokenA, func(tx *sql.Tx, userID uint64) error {
		if _, _, err := records.ApplyBatch(ctx, tx, userID, created.ID, 3, domain.Batch{Sets: []domain.Set{attempted}}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE user SET age_verified_at = NULL WHERE id = ?`, userID)
		return err
	})
	if !errors.Is(err, ErrAgeRequired) {
		t.Fatal("same-transaction age removal was not rejected")
	}
	snapshot, err = service.ReadFree(ctx, db, tokenA, created.ID)
	if err != nil || snapshot.Version != 3 || snapshot.Sets[first.RecordKey].WeightKg != "50.00" {
		t.Fatal("age rejection did not roll back the workout write")
	}
	for _, value := range []any{snapshot, &snapshot} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive snapshot was JSON-serializable")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		var entry map[string]any
		if json.Unmarshal(log.Bytes(), &entry) != nil {
			t.Fatal("could not parse test log")
		}
		output := fmt.Sprintf("%+v %#v %v", value, value, entry["value"])
		if strings.Contains(output, first.Exercise.Name) || strings.Contains(output, "50.00") {
			t.Fatal("workout content leaked into output")
		}
	}
	if _, err := service.ReadFree(ctx, db, tokenA+"tampered", created.ID); !errors.Is(err, ErrAuthentication) {
		t.Fatal("tampered token was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_session SET time_zone = 'Mars/Olympus' WHERE id = ?`, created.ID); err != nil {
		t.Fatal("could not corrupt isolated timezone fixture")
	}
	if _, err := service.ReadFree(ctx, db, tokenA, created.ID); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("invalid stored timezone was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_session SET time_zone = ? WHERE id = ?`, zone, created.ID); err != nil {
		t.Fatal("could not restore isolated timezone fixture")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_session SET date = DATE_ADD(UTC_DATE(), INTERVAL 1 DAY) WHERE id = ?`, created.ID); err != nil {
		t.Fatal("could not corrupt isolated date fixture")
	}
	if _, err := service.ReadFree(ctx, db, tokenA, created.ID); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("future stored workout date was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_session SET date = ? WHERE id = ?`, input.Date, created.ID); err != nil {
		t.Fatal("could not restore isolated date fixture")
	}
	if _, err := db.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, claimsA.SubjectID); err != nil {
		t.Fatal("could not revoke isolated device")
	}
	if _, err := service.ReadFree(ctx, db, tokenAgain, created.ID); !errors.Is(err, ErrAuthentication) {
		t.Fatal("revoked device retained workout access")
	}
}
