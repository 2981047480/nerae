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
	"reflect"
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
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_session WHERE user_id = ?`, claimsA.SubjectID).Scan(&count); err != nil || count != 0 {
		t.Fatal("age-rejected workout left data")
	}
	// These are explicit 18+ self-declarations through the authenticated auth
	// API, not independent verification of either subject's actual age.
	if err := auth.DeclareAdult(ctx, db, tokenA, true); err != nil {
		t.Fatal("could not declare adult status for first test subject")
	}
	if err := auth.DeclareAdult(ctx, db, tokenB, true); err != nil {
		t.Fatal("could not declare adult status for second test subject")
	}
	created, err := service.CreateFree(ctx, db, tokenA, input)
	if err != nil || created.ID == 0 || created.Version != 1 {
		t.Fatal("authenticated subject could not create workout")
	}
	first := domain.Set{RecordKey: "record-001", ExerciseEntryKey: "entry-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "自定义动作"},
		Index: 1, Kind: domain.Weight, WeightKg: "42.5", Reps: 8}
	second := first
	second.RecordKey, second.Index, second.WeightKg = "record-002", 2, "45"
	if v, changed, err := service.SaveSets(ctx, db, tokenA, created.ID, 1, domain.Batch{Sets: []domain.Set{first, second}}, []string{"entry-001"}); err != nil || !changed || v != 2 {
		t.Fatal("authenticated subject could not save sets")
	}
	snapshot, err := service.ReadFree(ctx, db, tokenAgain, created.ID)
	if err != nil || snapshot.Version != 2 || snapshot.ClientEventID != input.ClientEventID || len(snapshot.Sets) != 2 || snapshot.Sets[first.RecordKey].WeightKg != "42.50" || snapshot.Sets[first.RecordKey].Exercise.Name != first.Exercise.Name {
		t.Fatal("another session did not recover the same persisted workout")
	}
	sameSubjectSnapshot, err := service.ReadFree(ctx, db, tokenA, created.ID)
	if err != nil || sameSubjectSnapshot.ClientEventID != snapshot.ClientEventID {
		t.Fatal("two authenticated sessions for the same subject did not recover the persisted event ID")
	}
	if _, err := service.ReadFree(ctx, db, tokenB, created.ID); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("different subject read a private workout")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_session SET client_event_id = ? WHERE id = ?`, []byte{0xff, 'x', 'x', 'x', 'x', 'x', 'x', 'x'}, created.ID); err != nil {
		t.Fatal("could not corrupt isolated event ID fixture")
	}
	if _, err := service.ReadFree(ctx, db, tokenA, created.ID); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("invalid stored event ID was accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE workout_session SET client_event_id = ? WHERE id = ?`, []byte(input.ClientEventID), created.ID); err != nil {
		t.Fatal("could not restore isolated event ID fixture")
	}
	if _, _, err := service.SaveSets(ctx, db, tokenB, created.ID, 2, domain.Batch{Sets: []domain.Set{first}}, []string{"entry-001"}); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("different subject modified a private workout")
	}
	modified := first
	modified.WeightKg = "50"
	if v, changed, err := service.SaveSets(ctx, db, tokenAgain, created.ID, 2, domain.Batch{Sets: []domain.Set{modified}}, []string{"entry-001"}); err != nil || !changed || v != 3 {
		t.Fatal("second session could not modify the same workout")
	}
	if _, _, err := service.SaveSets(ctx, db, tokenA, created.ID, 2, domain.Batch{Sets: []domain.Set{first}}, []string{"entry-001"}); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatal("stale session overwrote a newer workout")
	}
	snapshot, err = service.ReadFree(ctx, db, tokenA, created.ID)
	if err != nil || snapshot.Version != 3 || snapshot.Sets[first.RecordKey].WeightKg != "50.00" {
		t.Fatal("stale write changed the persisted workout")
	}
	// Independently exercise the ordered path using the same two authenticated
	// sessions. The second device approval is covered in its own test harness.
	orderedInput := input
	orderedInput.ClientEventID = "ordered-integration-event"
	ordered, err := service.CreateFree(ctx, db, tokenA, orderedInput)
	if err != nil {
		t.Fatal("could not create ordered workout")
	}
	otherEntry := second
	otherEntry.ExerciseEntryKey = "entry-002"
	otherEntry.Exercise.Name = "自定义卧推"
	otherEntry.Index = 1
	wanted := []string{"entry-002", "entry-001"}
	if v, changed, err := service.SaveOrderedSets(ctx, db, tokenA, ordered.ID, 1, domain.Batch{Sets: []domain.Set{first, otherEntry}}, wanted); err != nil || !changed || v != 2 {
		t.Fatal("authenticated ordered save failed")
	}
	orderedSnapshot, err := service.ReadOrderedFree(ctx, db, tokenAgain, ordered.ID)
	if err != nil || orderedSnapshot.Version != 2 || orderedSnapshot.ClientEventID != orderedInput.ClientEventID || orderedSnapshot.ClientEventID == snapshot.ClientEventID || !reflect.DeepEqual(orderedSnapshot.ExerciseEntryKeys, wanted) || len(orderedSnapshot.Sets) != 2 {
		t.Fatal("another session did not recover ordered groups")
	}
	if err := auth.ClearAdultDeclaration(ctx, db, tokenA); err != nil {
		t.Fatal("could not clear the authenticated subject's adult self-declaration")
	}
	if _, err := service.ReadOrderedFree(ctx, db, tokenAgain, ordered.ID); !errors.Is(err, ErrAgeRequired) {
		t.Fatal("existing session read workout after adult self-declaration was cleared")
	}
	deniedUpdate := first
	deniedUpdate.WeightKg = "60"
	if _, _, err := service.SaveOrderedSets(ctx, db, tokenA, ordered.ID, 2, domain.Batch{Sets: []domain.Set{deniedUpdate}}, wanted); !errors.Is(err, ErrAgeRequired) {
		t.Fatal("existing session wrote workout after adult self-declaration was cleared")
	}
	var withheldVersion uint64
	var withheldStatus string
	var withheldSetCount int
	var withheldWeight string
	var withheldOrderCount int
	if err := db.QueryRowContext(ctx, `SELECT version, status FROM workout_session WHERE id = ?`, ordered.ID).
		Scan(&withheldVersion, &withheldStatus); err != nil || withheldVersion != 2 || withheldStatus != string(domain.InProgress) {
		t.Fatalf("age-rejected access changed the ordered session: err=%v version=%d status=%q", err, withheldVersion, withheldStatus)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), SUM(weight_kg) FROM set_record WHERE session_id = ?`, ordered.ID).
		Scan(&withheldSetCount, &withheldWeight); err != nil || withheldSetCount != 2 || withheldWeight != "87.50" {
		t.Fatalf("age-rejected access changed saved measurements: err=%v count=%d weight=%q", err, withheldSetCount, withheldWeight)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workout_exercise_order WHERE session_id = ?`, ordered.ID).
		Scan(&withheldOrderCount); err != nil || withheldOrderCount != 2 {
		t.Fatalf("age-rejected access changed saved order: err=%v count=%d", err, withheldOrderCount)
	}
	if err := auth.DeclareAdult(ctx, db, tokenA, true); err != nil {
		t.Fatal("could not explicitly re-declare adult status for the test subject")
	}
	orderedSnapshot, err = service.ReadOrderedFree(ctx, db, tokenAgain, ordered.ID)
	if err != nil || orderedSnapshot.Version != 2 || !reflect.DeepEqual(orderedSnapshot.ExerciseEntryKeys, wanted) || len(orderedSnapshot.Sets) != 2 {
		t.Fatal("existing session did not recover workout read after explicit re-declaration")
	}
	if _, err := service.ReadOrderedFree(ctx, db, tokenB, ordered.ID); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("different subject read ordered workout")
	}
	if _, _, err := service.SaveOrderedSets(ctx, db, tokenB, ordered.ID, 2, domain.Batch{}, wanted); !errors.Is(err, repository.ErrUnavailable) {
		t.Fatal("different subject changed ordered workout")
	}
	reversed := []string{"entry-001", "entry-002"}
	if v, changed, err := service.SaveOrderedSets(ctx, db, tokenAgain, ordered.ID, 2, domain.Batch{}, reversed); err != nil || !changed || v != 3 {
		t.Fatal("authenticated order-only update failed")
	}
	if _, _, err := service.SaveOrderedSets(ctx, db, tokenA, ordered.ID, 2, domain.Batch{}, wanted); !errors.Is(err, repository.ErrVersionConflict) {
		t.Fatal("stale ordered version was accepted")
	}
	if v, err := service.CompleteOrderedFree(ctx, db, tokenA, ordered.ID, 3); err != nil || v != 4 {
		t.Fatal("ordered completion failed")
	}
	orderedSnapshot, err = service.ReadOrderedFree(ctx, db, tokenAgain, ordered.ID)
	if err != nil || orderedSnapshot.Version != 4 || orderedSnapshot.Status != domain.Completed || !reflect.DeepEqual(orderedSnapshot.ExerciseEntryKeys, reversed) {
		t.Fatal("ordered completion did not preserve order")
	}
	orderedAttempt := first
	orderedAttempt.WeightKg = "60"
	err = service.inTransaction(ctx, db, tokenA, func(tx *sql.Tx, userID uint64) error {
		if _, _, err := records.ApplyOrderedBatch(ctx, tx, userID, ordered.ID, 4, domain.Batch{Sets: []domain.Set{orderedAttempt}}, wanted); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, userID)
		return err
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("ordered transaction did not conservatively reject revoked device")
	}
	orderedSnapshot, err = service.ReadOrderedFree(ctx, db, tokenA, ordered.ID)
	if err != nil || orderedSnapshot.Version != 4 || orderedSnapshot.Sets[first.RecordKey].WeightKg != "42.50" || !reflect.DeepEqual(orderedSnapshot.ExerciseEntryKeys, reversed) {
		t.Fatal("authorization failure did not roll back groups and order")
	}
	// Simulate a revocation inside the same transaction after a group update.
	// The service's final authorization check must roll both changes back.
	attempted := first
	attempted.WeightKg = "60"
	err = service.inTransaction(ctx, db, tokenA, func(tx *sql.Tx, userID uint64) error {
		if _, _, err := records.ApplyOrderedBatch(ctx, tx, userID, created.ID, 3, domain.Batch{Sets: []domain.Set{attempted}}, []string{"entry-001"}); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE trusted_device SET state = 'revoked' WHERE user_id = ?`, userID)
		return err
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("same-transaction revocation was not conservatively rejected")
	}
	snapshot, err = service.ReadFree(ctx, db, tokenA, created.ID)
	if err != nil || snapshot.Version != 3 || snapshot.Sets[first.RecordKey].WeightKg != "50.00" {
		t.Fatal("authorization failure did not roll back the workout write")
	}
	// A failed commit-time recheck must never be translated to logout, and
	// the earlier write must not survive the cancelled transaction.
	cancelCtx, cancelWork := context.WithCancel(ctx)
	err = service.inTransaction(cancelCtx, db, tokenA, func(tx *sql.Tx, userID uint64) error {
		if _, _, err := records.ApplyOrderedBatch(cancelCtx, tx, userID, created.ID, 3, domain.Batch{Sets: []domain.Set{attempted}}, []string{"entry-001"}); err != nil {
			return err
		}
		cancelWork()
		return nil
	})
	cancelWork()
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("failed authorization recheck was not classified unavailable")
	}
	snapshot, err = service.ReadFree(ctx, db, tokenA, created.ID)
	if err != nil || snapshot.Version != 3 || snapshot.Sets[first.RecordKey].WeightKg != "50.00" {
		t.Fatal("cancelled authorization recheck did not roll back the workout")
	}
	// A change to the age marker in the same transaction must also roll back.
	err = service.inTransaction(ctx, db, tokenA, func(tx *sql.Tx, userID uint64) error {
		if _, _, err := records.ApplyOrderedBatch(ctx, tx, userID, created.ID, 3, domain.Batch{Sets: []domain.Set{attempted}}, []string{"entry-001"}); err != nil {
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
		if strings.Contains(output, first.Exercise.Name) || strings.Contains(output, "50.00") || strings.Contains(output, input.ClientEventID) {
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
