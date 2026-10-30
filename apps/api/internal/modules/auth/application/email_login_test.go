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
	"nerae/api/internal/modules/auth/approval"
	authbootstraptest "nerae/api/internal/modules/auth/bootstrap/testsupport"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/repository"
	"nerae/api/internal/modules/auth/session"
)

func emailFlowDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("NERAE_EMAIL_FLOW_TEST_DSN")
	if dsn == "" {
		t.Skip("set NERAE_EMAIL_FLOW_TEST_DSN using the isolated MySQL harness")
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
	if err != nil || statErr != nil || socketErr != nil || cfg.Net != "unix" || cfg.DBName != "nerae_email_flow_test" ||
		cfg.User != "root" || cfg.Passwd != "" || !cfg.ParseTime || cfg.Loc != time.UTC ||
		filepath.Base(socket) != "mysql.sock" || filepath.Dir(resolved) != "/private/tmp" ||
		!strings.HasPrefix(filepath.Base(resolved), "nerae-email-flow-mysql-") ||
		!info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Mode()&os.ModeSymlink != 0 || socketInfo.Mode()&os.ModeSocket == 0 {
		t.Fatal("refusing non-isolated MySQL target")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil || db.Ping() != nil {
		t.Fatal("could not open isolated database")
	}
	db.SetMaxOpenConns(6)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func flowDevice(t *testing.T) (*ecdsa.PrivateKey, domain.DevicePublicJWK) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("could not create synthetic device")
	}
	encode := base64.RawURLEncoding.EncodeToString
	return key, domain.DevicePublicJWK{Kty: "EC", Crv: "P-256", X: encode(key.X.FillBytes(make([]byte, 32))), Y: encode(key.Y.FillBytes(make([]byte, 32)))}
}

func flowProof(t *testing.T, key *ecdsa.PrivateKey, id, nonce string) string {
	t.Helper()
	jti, err := domain.GenerateChallengeID()
	if err != nil {
		t.Fatal("could not generate proof identifier")
	}
	now := time.Now().UTC()
	payload, err := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": id,
		"nonce": nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	if err != nil {
		t.Fatal("could not encode synthetic proof")
	}
	encode := base64.RawURLEncoding.EncodeToString
	unsigned := encode([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + encode(payload)
	digest := sha256.Sum256([]byte(unsigned))
	r, signatureS, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal("could not sign synthetic proof")
	}
	return unsigned + "." + encode(append(r.FillBytes(make([]byte, 32)), signatureS.FillBytes(make([]byte, 32))...))
}

func TestEmailLoginTransaction(t *testing.T) {
	db := emailFlowDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	hasher, err := domain.NewEmailCodeHasher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal("hasher initialization failed")
	}
	replay, err := repository.NewProofReplayStore(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal("replay initialization failed")
	}
	protector, err := domain.NewEmailIdentityProtector(bytes.Repeat([]byte{3}, 32), bytes.Repeat([]byte{4}, 32), "email-test-key")
	if err != nil {
		t.Fatal("protector initialization failed")
	}
	challenges, err := challenge.NewStore(hasher, replay, protector, challenge.Policy{ChallengeTTL: 5 * time.Minute,
		MaxAttempts: 3, DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}})
	if err != nil {
		t.Fatal("challenge initialization failed")
	}
	authbootstraptest.InitializeIdentityEpoch(t, db, protector)
	resolver, err := identity.NewResolver(protector)
	if err != nil {
		t.Fatal("resolver initialization failed")
	}
	codec, err := session.NewTokenCodec(bytes.Repeat([]byte{5}, 32), session.TokenPolicy{MaxLifetime: time.Hour})
	if err != nil {
		t.Fatal("codec initialization failed")
	}
	sessions, err := session.NewStore(resolver, codec)
	if err != nil {
		t.Fatal("session initialization failed")
	}
	approvals, err := approval.NewStore(resolver, bytes.Repeat([]byte{6}, 32), "approval-test-key", approval.Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	if err != nil {
		t.Fatal("approval initialization failed")
	}
	initTx, err := db.BeginTx(ctx, nil)
	if err != nil || approvals.InitializeKey(ctx, initTx) != nil || initTx.Commit() != nil {
		t.Fatal("approval key initialization failed")
	}
	login, err := NewEmailLogin(challenges, sessions, approvals)
	if err != nil {
		t.Fatal("login initialization failed")
	}

	issue := func(email string, key *ecdsa.PrivateKey, public domain.DevicePublicJWK) challenge.AttemptInput {
		t.Helper()
		id, err := domain.GenerateChallengeID()
		if err != nil {
			t.Fatal("challenge ID generation failed")
		}
		nonce, err := domain.GenerateChallengeNonce()
		if err != nil {
			t.Fatal("nonce generation failed")
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal("challenge transaction failed")
		}
		defer tx.Rollback()
		if err := challenges.Issue(ctx, tx, challenge.IssueInput{Email: email, ChallengeID: id, Nonce: nonce, Code: "012345", PublicKey: public}); err != nil || tx.Commit() != nil {
			t.Fatal("could not commit synthetic challenge")
		}
		return challenge.AttemptInput{ChallengeID: id, Code: "012345", DeviceProof: flowProof(t, key, id, nonce)}
	}
	firstKey, firstPublic := flowDevice(t)
	newKey, newPublic := flowDevice(t)
	otherKey, otherPublic := flowDevice(t)
	first := issue("person@example.test", firstKey, firstPublic)
	wrong := first
	wrong.Code = "999999"
	out, err := login.VerifyEmail(ctx, db, wrong)
	if !errors.Is(err, ErrRejected) || out.Kind() != "" {
		t.Fatal("wrong code did not reject")
	}
	var attempts int
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id = ?`, first.ChallengeID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("wrong-code attempt was not committed")
	}
	out, err = login.VerifyEmail(ctx, db, first)
	firstToken, ok := out.SessionTokenForResponse()
	if err != nil || !ok || out.Kind() != Authenticated {
		t.Fatal("first device did not get a committed session")
	}
	if _, ok := out.PendingApprovalForResponse(); ok {
		t.Fatal("authenticated result contained approval")
	}
	firstClaims, err := codec.Verify(firstToken)
	if err != nil {
		t.Fatal("first session token invalid")
	}
	if _, err := login.VerifyEmail(ctx, db, first); !errors.Is(err, ErrRejected) {
		t.Fatal("consumed challenge was accepted twice")
	}

	trusted := issue("person@example.test", firstKey, firstPublic)
	trustedResult, err := login.VerifyEmail(ctx, db, trusted)
	trustedToken, ok := trustedResult.SessionTokenForResponse()
	if err != nil || !ok {
		t.Fatal("trusted device login failed")
	}
	trustedClaims, err := codec.Verify(trustedToken)
	if err != nil || trustedClaims.SubjectID != firstClaims.SubjectID || trustedClaims.SessionID == firstClaims.SessionID {
		t.Fatal("trusted login changed subject or reused session")
	}

	pendingInput := issue("person@example.test", newKey, newPublic)
	wrongApprovals, err := approval.NewStore(resolver, bytes.Repeat([]byte{7}, 32), "approval-test-key", approval.Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute})
	if err != nil {
		t.Fatal("wrong-key test setup failed")
	}
	wrongLogin, err := NewEmailLogin(challenges, sessions, wrongApprovals)
	if err != nil {
		t.Fatal("wrong-key coordinator setup failed")
	}
	failed, err := wrongLogin.VerifyEmail(ctx, db, pendingInput)
	if !errors.Is(err, ErrUnavailable) || failed.Kind() != "" {
		t.Fatal("invalid approval epoch was accepted")
	}
	var consumed bool
	if err := db.QueryRowContext(ctx, `SELECT consumed_at IS NOT NULL FROM auth_email_login_challenge WHERE challenge_id = ?`, pendingInput.ChallengeID).Scan(&consumed); err != nil || consumed {
		t.Fatal("failed approval did not roll back challenge")
	}
	pending, err := login.VerifyEmail(ctx, db, pendingInput)
	data, ok := pending.PendingApprovalForResponse()
	if err != nil || !ok || pending.Kind() != ApprovalRequired || data.ComparisonCode == "" {
		t.Fatal("new device did not obtain a committed approval request")
	}
	if token, ok := pending.SessionTokenForResponse(); ok || token != "" {
		t.Fatal("new device received a session before approval")
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id = ?`, pendingInput.ChallengeID).Scan(&count); err != nil || count != 0 {
		t.Fatal("new-device approval created a session")
	}

	other := issue("other@example.test", otherKey, otherPublic)
	otherResult, err := login.VerifyEmail(ctx, db, other)
	otherToken, ok := otherResult.SessionTokenForResponse()
	if err != nil || !ok {
		t.Fatal("other subject login failed")
	}
	otherClaims, err := codec.Verify(otherToken)
	if err != nil || otherClaims.SubjectID == firstClaims.SubjectID {
		t.Fatal("distinct subjects were merged")
	}

	limited := issue("person@example.test", firstKey, firstPublic)
	bad := limited
	bad.Code = "999999"
	for i := 0; i < 3; i++ {
		if result, err := login.VerifyEmail(ctx, db, bad); !errors.Is(err, ErrRejected) || result.Kind() != "" {
			t.Fatal("wrong code did not reject")
		}
	}
	if result, err := login.VerifyEmail(ctx, db, limited); !errors.Is(err, ErrRejected) || result.Kind() != "" {
		t.Fatal("exhausted challenge accepted correct code")
	}
	if err := db.QueryRowContext(ctx, `SELECT attempts FROM auth_email_login_challenge WHERE challenge_id = ?`, limited.ChallengeID).Scan(&attempts); err != nil || attempts != 3 {
		t.Fatal("attempt limit not persisted")
	}

	concurrentKey, concurrentPublic := flowDevice(t)
	concurrent := issue("concurrent@example.test", concurrentKey, concurrentPublic)
	type result struct {
		outcome Outcome
		err     error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { outcome, err := login.VerifyEmail(ctx, db, concurrent); results <- result{outcome, err} }()
	}
	successes, rejected := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.err == nil && r.outcome.Kind() == Authenticated {
			successes++
		} else if errors.Is(r.err, ErrRejected) {
			rejected++
		} else {
			t.Fatal("concurrent challenge produced unexpected result")
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatal("concurrent challenge was not single-use")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_session WHERE login_challenge_id = ?`, concurrent.ChallengeID).Scan(&count); err != nil || count != 1 {
		t.Fatal("concurrent challenge created multiple sessions")
	}

	for _, value := range []any{out, pending, login} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive result was serializable")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Info("test", "value", value)
		formatted := fmt.Sprintf("%v %+v %#v %s", value, value, value, log.String())
		if strings.Contains(formatted, firstToken) || strings.Contains(formatted, data.Secret) || strings.Contains(formatted, pendingInput.DeviceProof) {
			t.Fatal("credential leaked through formatting or logs")
		}
	}
}

func TestEmailLoginRejectsMissingDependencies(t *testing.T) {
	if _, err := NewEmailLogin(nil, nil, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing stores accepted")
	}
	if result, err := (*EmailLogin)(nil).VerifyEmail(context.Background(), nil, challenge.AttemptInput{}); !errors.Is(err, ErrUnavailable) || result.Kind() != "" {
		t.Fatal("missing dependencies accepted")
	}
	if _, ok := (Outcome{}).SessionTokenForResponse(); ok {
		t.Fatal("zero outcome exposed a token")
	}
	if _, ok := (Outcome{}).PendingApprovalForResponse(); ok {
		t.Fatal("zero outcome exposed an approval")
	}
}
