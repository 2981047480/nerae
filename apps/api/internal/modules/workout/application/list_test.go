package application

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	authdomain "nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	authrepo "nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/workout/domain"
	"nerae/api/internal/modules/workout/repository"
)

func TestWorkoutListIsolationAndPagination(t *testing.T) {
	db := integrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	service, err := New(auth, repository.NewSessionStore(), records)
	if err != nil {
		t.Fatal("workout service setup failed")
	}

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
		proof := integrationProof(t, key, id, nonce)
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
			t.Fatal("session release failed")
		}
		claims, err := codec.Verify(token)
		if err != nil {
			t.Fatal("session token invalid")
		}
		return token, claims
	}
	keyA, publicA := integrationDevice(t)
	keyB, publicB := integrationDevice(t)
	tokenA, claimsA := login("list-first@example.test", keyA, publicA)
	tokenB, claimsB := login("list-second@example.test", keyB, publicB)
	tokenAgain, claimsAgain := login("list-first@example.test", keyA, publicA)
	if claimsA.SubjectID != claimsAgain.SubjectID || claimsA.SubjectID == claimsB.SubjectID {
		t.Fatal("invalid test subjects")
	}
	filter := repository.ListFilter{Page: 1, PageSize: 20}
	if _, err := service.ListFree(ctx, db, tokenA, filter); !errors.Is(err, ErrAgeRequired) {
		t.Fatal("unverified age read a list")
	}
	if err := auth.DeclareAdult(ctx, db, tokenA, true); err != nil {
		t.Fatal("first age declaration failed")
	}
	if err := auth.DeclareAdult(ctx, db, tokenB, true); err != nil {
		t.Fatal("second age declaration failed")
	}

	zone := "UTC"
	today := time.Now().UTC()
	older := today.AddDate(0, 0, -2).Format("2006-01-02")
	recent := today.AddDate(0, 0, -1).Format("2006-01-02")
	create := func(token, date, event string) uint64 {
		t.Helper()
		ref, err := service.CreateFree(ctx, db, token, repository.FreeSessionInput{Date: date, TimeZone: &zone, ClientEventID: event})
		if err != nil {
			t.Fatal("test workout creation failed")
		}
		return ref.ID
	}
	first := create(tokenA, older, "list-event-001")
	second := create(tokenA, recent, "list-event-002")
	third := create(tokenA, recent, "list-event-003")
	other := create(tokenB, recent, "list-event-004")
	set := domain.Set{RecordKey: "list-set-001", ExerciseEntryKey: "list-entry-001", Exercise: domain.Exercise{Source: domain.Custom, Name: "深蹲"}, Index: 1, Kind: domain.Bodyweight, Reps: 8}
	if version, changed, err := service.SaveSets(ctx, db, tokenA, third, 1, domain.Batch{Sets: []domain.Set{set}}, []string{"list-entry-001"}); err != nil || !changed || version != 2 {
		t.Fatal("actual group save failed")
	}
	if version, err := service.CompleteOrderedFree(ctx, db, tokenA, third, 2); err != nil || version != 3 {
		t.Fatal("workout completion failed")
	}

	page, err := service.ListFree(ctx, db, tokenAgain, repository.ListFilter{Page: 1, PageSize: 1})
	if err != nil || page.Total != 3 || page.Page != 1 || page.PageSize != 1 || len(page.Items) != 1 || page.Items[0].ID != third || page.Items[0].Status != domain.Completed || page.Items[0].RecordedAt == nil {
		t.Fatal("first page did not return latest completed own workout")
	}
	next, err := service.ListFree(ctx, db, tokenA, repository.ListFilter{Page: 2, PageSize: 1})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != second {
		t.Fatal("second page did not preserve same-day order")
	}
	last, err := service.ListFree(ctx, db, tokenA, repository.ListFilter{Page: 3, PageSize: 1})
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != first {
		t.Fatal("third page did not preserve date order")
	}
	filtered, err := service.ListFree(ctx, db, tokenA, repository.ListFilter{From: recent, To: recent, Page: 1, PageSize: 20})
	if err != nil || filtered.Total != 2 || len(filtered.Items) != 2 || filtered.Items[0].ID != third || filtered.Items[1].ID != second {
		t.Fatal("date filtering failed")
	}
	empty, err := service.ListFree(ctx, db, tokenA, repository.ListFilter{To: today.AddDate(0, 0, -3).Format("2006-01-02"), Page: 1, PageSize: 20})
	if err != nil || empty.Total != 0 || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatal("empty result was not an empty page")
	}
	far, err := service.ListFree(ctx, db, tokenA, repository.ListFilter{Page: math.MaxUint64, PageSize: 50})
	if err != nil || far.Total != 3 || len(far.Items) != 0 {
		t.Fatal("large page overflowed")
	}
	otherPage, err := service.ListFree(ctx, db, tokenB, filter)
	if err != nil || otherPage.Total != 1 || len(otherPage.Items) != 1 || otherPage.Items[0].ID != other {
		t.Fatal("other subject list crossed ownership")
	}
	for _, invalid := range []repository.ListFilter{
		{Page: 0, PageSize: 1}, {Page: 1, PageSize: 0}, {Page: 1, PageSize: 51},
		{From: "2026-02-30", Page: 1, PageSize: 1}, {To: "not-a-date", Page: 1, PageSize: 1},
		{From: recent, To: older, Page: 1, PageSize: 1},
	} {
		if _, err := service.ListFree(ctx, db, tokenA, invalid); !errors.Is(err, repository.ErrInvalid) {
			t.Fatal("invalid list filter accepted")
		}
	}
	if _, err := service.ListFree(ctx, db, tokenA+"x", filter); !errors.Is(err, ErrAuthentication) {
		t.Fatal("invalid session read list")
	}
	if err := auth.ClearAdultDeclaration(ctx, db, tokenA); err != nil {
		t.Fatal("age declaration removal failed")
	}
	if _, err := service.ListFree(ctx, db, tokenAgain, filter); !errors.Is(err, ErrAgeRequired) {
		t.Fatal("withdrawn age declaration read list")
	}
	if _, err := db.ExecContext(ctx, `UPDATE user SET status = 'disabled' WHERE id = ?`, claimsB.SubjectID); err != nil {
		t.Fatal("fixture disable failed")
	}
	if _, err := service.ListFree(ctx, db, tokenB, filter); err == nil {
		t.Fatal("disabled subject read list")
	}

	for _, value := range []any{page, page.Items[0]} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("internal list result serialized")
		}
		var output bytes.Buffer
		slog.New(slog.NewJSONHandler(&output, nil)).Info("test", "value", value)
		formatted := fmt.Sprintf("%v %+v %#v %s", value, value, value, output.String())
		if strings.Contains(formatted, recent) || strings.Contains(formatted, "list-event-003") {
			t.Fatal("internal list data leaked to logs")
		}
	}
}
