package main

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
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	api "nerae/api/internal/gen"
	"nerae/api/internal/modules/auth/approval"
	"nerae/api/internal/modules/auth/challenge"
	"nerae/api/internal/modules/auth/domain"
	authidentity "nerae/api/internal/modules/auth/identity"
	"nerae/api/internal/modules/auth/issuance"
	"nerae/api/internal/modules/auth/ratelimit"
	authsession "nerae/api/internal/modules/auth/session"
	"nerae/api/internal/modules/auth/trustedproxy"
	workoutapp "nerae/api/internal/modules/workout/application"
	workoutdomain "nerae/api/internal/modules/workout/domain"
	workoutrepo "nerae/api/internal/modules/workout/repository"
)

func businessFixture() (businessKeys, businessPolicy) {
	key := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	keys := businessKeys{
		identityIndex: key(1), identityCipher: key(2), codeHash: key(3), codeDerive: key(4),
		proofReplay: key(5), sessionToken: key(6), approval: key(7),
		challengeIdempotency: key(8), challengeRequest: key(9), challengeCipher: key(10),
		loginIdempotency: key(11), loginRequest: key(12), loginCipher: key(13),
		exchangeIdempotency: key(22), exchangeRequest: key(23), exchangeCipher: key(24), exchangeID: "assembly-exchange-v1",
		rateEmail: key(14), rateDevice: key(15), rateIP: key(16), rateGlobal: key(17),
		workoutName: key(18), workoutHash: key(19), workoutCipher: key(20),
		identityID: "assembly-identity-v1", codeID: "assembly-code-v1", codeHashID: "assembly-code-hash-v1", approvalID: "assembly-approval-v1",
		challengeID: "assembly-challenge-v1", loginID: "assembly-login-v1", workoutNameID: "assembly-name-v1", workoutID: "assembly-workout-v1",
	}
	dim := ratelimit.DimensionPolicy{Limit: 1000, Window: time.Hour}
	limit := ratelimit.Policy{Email: dim, Device: dim, IP: dim, Global: dim}
	return keys, businessPolicy{
		challenge: challenge.Policy{ChallengeTTL: 5 * time.Minute, MaxAttempts: 3,
			DeviceProof: domain.ProofPolicy{MaxLifetime: 5 * time.Minute, ClockSkew: 30 * time.Second}},
		rate:               ratelimit.Config{IPv6PrefixBits: 64, EmailChallenge: limit, EmailLogin: limit},
		approvalRate:       ratelimit.ApprovalDecisionPolicy{Subject: dim, Approval: ratelimit.DimensionPolicy{Limit: 3, Window: time.Hour}, IP: dim, Global: dim},
		exchangeRate:       ratelimit.ApprovalExchangePolicy{Capability: dim, IP: dim, Global: dim},
		challengeRetention: time.Hour, loginRetention: 2 * time.Minute, sessionLifetime: time.Hour,
		exchangeRetention: 2 * time.Minute, workoutRetention: 24 * time.Hour, approval: approval.Policy{Lifetime: 5 * time.Minute, ReauthWindow: time.Minute}, codeDigits: 6,
	}
}

func TestBusinessKeysRejectMissingReusedAndDisclosure(t *testing.T) {
	keys, _ := businessFixture()
	if !keys.distinctKeys() {
		t.Fatal("valid fixture keys rejected")
	}
	for _, replace := range []func(*businessKeys){
		func(k *businessKeys) { k.rateEmail = nil },
		func(k *businessKeys) { k.workoutCipher = make([]byte, 32) },
		func(k *businessKeys) { k.codeHash = append([]byte(nil), k.sessionToken...) },
		func(k *businessKeys) { k.exchangeCipher = nil },
		func(k *businessKeys) { k.exchangeRequest = append([]byte(nil), k.loginRequest...) },
	} {
		bad := keys
		replace(&bad)
		if bad.distinctKeys() {
			t.Fatal("missing, zero or reused key accepted")
		}
	}
	if fmt.Sprint(keys) != "BusinessKeys{redacted}" {
		t.Fatal("keys exposed by formatting")
	}
	if _, err := json.Marshal(keys); err == nil {
		t.Fatal("keys exposed by JSON")
	}
	if _, err := buildBusinessHandler(nil, nil, nil, keys, businessPolicy{}, nil); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}

// This test runs only through a private socket-only MySQL/Redis harness. All
// identities, device keys and code derivation keys are synthetic.
func TestBusinessAssemblyPrivateServices(t *testing.T) {
	dsn, socket := os.Getenv("NERAE_API_ASSEMBLY_TEST_DSN"), os.Getenv("NERAE_API_ASSEMBLY_REDIS_SOCKET")
	if dsn == "" || socket == "" {
		t.Skip("private socket-only services not configured")
	}
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil || parsed.Net != "unix" || parsed.DBName != "nerae_api_assembly_test" {
		t.Fatal("test database must use the dedicated private assembly schema over a Unix socket")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal("test database unavailable")
	}
	defer db.Close()
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DialTimeout: time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	keys, policy := businessFixture()
	proxies := []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}
	// A late epoch mismatch must roll back all earlier epoch initialization.
	foreignName, err := workoutrepo.NewExerciseNameProtector(bytes.Repeat([]byte{97}, 32), "other-name-v1")
	if err != nil {
		t.Fatal("foreign workout key setup failed")
	}
	setup, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("foreign workout epoch setup failed")
	}
	if err := foreignName.InitializeExerciseKey(ctx, setup); err != nil {
		_ = setup.Rollback()
		t.Fatal("foreign workout epoch setup failed")
	}
	if err := setup.Commit(); err != nil {
		t.Fatal("foreign workout epoch setup failed")
	}
	if _, err := buildBusinessHandler(ctx, db, rdb, keys, policy, proxies); err == nil {
		t.Fatal("late workout epoch mismatch accepted")
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM auth_email_identity_key_epoch`,
		`SELECT COUNT(*) FROM auth_email_code_key_epoch`,
		`SELECT COUNT(*) FROM auth_email_code_hash_key_epoch`,
		`SELECT COUNT(*) FROM auth_email_challenge_request_key_epoch`,
		`SELECT COUNT(*) FROM auth_email_login_receipt_key_epoch`,
		`SELECT COUNT(*) FROM auth_approval_key_epoch WHERE key_id IS NOT NULL`,
		`SELECT COUNT(*) FROM auth_approval_exchange_receipt_key_epoch`,
		`SELECT COUNT(*) FROM auth_approval_poll_key_epoch`,
		`SELECT COUNT(*) FROM transport_idempotency_key_epoch WHERE key_id IS NOT NULL`,
	} {
		var count int
		if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed startup left an earlier epoch initialized")
		}
	}
	// This reset is confined to the freshly created synthetic test database;
	// real key recovery is a separate, controlled deployment operation.
	if _, err := db.ExecContext(ctx, `UPDATE workout_crypto_key_epoch SET key_id=NULL,key_tag=NULL WHERE slot=1`); err != nil {
		t.Fatal("synthetic workout epoch reset failed")
	}
	handler, err := buildBusinessHandler(ctx, db, rdb, keys, policy, proxies)
	if err != nil {
		t.Fatal("business assembly failed")
	}
	if _, err := buildBusinessHandler(ctx, db, rdb, keys, policy, proxies); err != nil {
		t.Fatal("same-key restart failed")
	}
	unsafePolicy := policy
	unsafePolicy.approvalRate.Approval.Limit = 11
	if _, err := buildBusinessHandler(ctx, db, rdb, keys, unsafePolicy, proxies); err == nil {
		t.Fatal("approval attempt limit above the safety ceiling was accepted")
	}
	unsafePolicy = policy
	unsafePolicy.approvalRate.Approval.Window = time.Second
	if _, err := buildBusinessHandler(ctx, db, rdb, keys, unsafePolicy, proxies); err == nil {
		t.Fatal("approval limit window shorter than the approval lifetime was accepted")
	}
	unsafePolicy = policy
	unsafePolicy.exchangeRate.Capability.Limit = 0
	if _, err := buildBusinessHandler(ctx, db, rdb, keys, unsafePolicy, proxies); err == nil {
		t.Fatal("missing approval exchange capability limit was accepted")
	}
	unsafePolicy = policy
	unsafePolicy.exchangeRetention = 0
	if _, err := buildBusinessHandler(ctx, db, rdb, keys, unsafePolicy, proxies); err == nil {
		t.Fatal("missing approval exchange receipt retention was accepted")
	}
	wrongExchangeKey := keys
	wrongExchangeKey.exchangeCipher = bytes.Repeat([]byte{98}, 32)
	if _, err := buildBusinessHandler(ctx, db, rdb, wrongExchangeKey, policy, proxies); err == nil {
		t.Fatal("approval exchange epoch mismatch was accepted")
	}
	// Emulate a live workout transaction which already holds the idempotency
	// epoch. Once startup has locked the earlier approval epoch, it must wait
	// for idempotency before locking workout crypto, allowing the write to take
	// its next lock and finish without the reverse-order cycle.
	hold, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("live-write lock transaction failed")
	}
	var slot int
	if err := hold.QueryRowContext(ctx, `SELECT slot FROM transport_idempotency_key_epoch WHERE slot=1 FOR SHARE`).Scan(&slot); err != nil {
		_ = hold.Rollback()
		t.Fatal("live-write idempotency lock failed")
	}
	started := make(chan error, 1)
	go func() {
		_, err := buildBusinessHandler(ctx, db, rdb, keys, policy, proxies)
		started <- err
	}()
	locked := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		probe, err := db.BeginTx(ctx, nil)
		if err != nil {
			_ = hold.Rollback()
			t.Fatal("startup lock probe failed")
		}
		err = probe.QueryRowContext(ctx, `SELECT slot FROM auth_approval_key_epoch WHERE slot=1 FOR UPDATE NOWAIT`).Scan(&slot)
		_ = probe.Rollback()
		var mysqlErr *mysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 3572 {
			locked = true
			break
		}
		if err != nil {
			_ = hold.Rollback()
			t.Fatal("startup approval lock probe was unavailable")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !locked {
		_ = hold.Rollback()
		t.Fatal("startup did not reach the expected held epoch")
	}
	if err := hold.QueryRowContext(ctx, `SELECT slot FROM workout_crypto_key_epoch WHERE slot=1 FOR SHARE NOWAIT`).Scan(&slot); err != nil {
		_ = hold.Rollback()
		t.Fatal("startup locked workout crypto before idempotency")
	}
	if err := hold.Commit(); err != nil {
		t.Fatal("live-write lock transaction did not finish")
	}
	if err := <-started; err != nil {
		t.Fatal("startup did not finish after the live write released its locks")
	}
	wrong := keys
	wrong.codeDerive = bytes.Repeat([]byte{97}, 32)
	if _, err := buildBusinessHandler(ctx, db, rdb, wrong, policy, proxies); err == nil {
		t.Fatal("mismatched derivation epoch accepted")
	}
	if _, err := buildBusinessHandler(ctx, db, rdb, keys, policy, []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}); err == nil {
		t.Fatal("untrusted proxy range accepted")
	}

	device, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("device fixture creation failed")
	}
	enc := base64.RawURLEncoding.EncodeToString
	body, _ := json.Marshal(map[string]any{"email": "assembly@example.test", "devicePublicKey": map[string]string{
		"kty": "EC", "crv": "P-256", "x": enc(device.X.FillBytes(make([]byte, 32))), "y": enc(device.Y.FillBytes(make([]byte, 32))),
	}})
	requestKey := func() string {
		b := make([]byte, 24)
		if _, e := rand.Read(b); e != nil {
			t.Fatal("key creation failed")
		}
		return enc(b)
	}
	proxyCredential := bytes.Repeat([]byte{25}, 32)
	guarded := proxyBoundary(proxyCredential, handler)
	request := func(method, path string, body []byte, token, key, peer, source string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
		r.RemoteAddr = peer
		r.Header.Set(proxyCredentialHeader, enc(proxyCredential))
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if source != "" {
			r.Header.Set(trustedproxy.SourceIPHeader, source)
		}
		w := httptest.NewRecorder()
		guarded.ServeHTTP(w, r)
		return w
	}
	peer, source := "127.0.0.1:12345", "203.0.113.23"
	challengeKey := requestKey()
	untrusted := request("POST", "/auth/email/challenges", body, "", challengeKey, "198.51.100.1:12345", source)
	if untrusted.Code != http.StatusServiceUnavailable {
		t.Fatalf("untrusted proxy returned %d", untrusted.Code)
	}
	issued := request("POST", "/auth/email/challenges", body, "", challengeKey, peer, source)
	var challengeResponse api.EmailLoginChallenge
	if issued.Code != http.StatusAccepted || json.Unmarshal(issued.Body.Bytes(), &challengeResponse) != nil || challengeResponse.ChallengeId == "" || challengeResponse.Nonce == "" {
		t.Fatalf("challenge status %d", issued.Code)
	}
	replayed := request("POST", "/auth/email/challenges", body, "", challengeKey, peer, source)
	if replayed.Code != http.StatusAccepted || !bytes.Equal(issued.Body.Bytes(), replayed.Body.Bytes()) {
		t.Fatal("challenge replay changed result")
	}
	var queued int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM auth_email_outbox WHERE challenge_id=?`, []byte(challengeResponse.ChallengeId)).Scan(&queued); err != nil || queued != 1 {
		t.Fatal("challenge did not queue exactly once")
	}
	deriver, _ := issuance.NewDeriver(keys.codeDerive)
	code, err := deriver.Derive(string(challengeResponse.ChallengeId), "email_login", 6)
	if err != nil {
		t.Fatal("synthetic code derivation failed")
	}
	jti := requestKey()
	now := time.Now().UTC()
	claims, _ := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": challengeResponse.ChallengeId,
		"nonce": challengeResponse.Nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": jti})
	unsigned := enc([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + enc(claims)
	digest := sha256.Sum256([]byte(unsigned))
	sigR, sigS, err := ecdsa.Sign(rand.Reader, device, digest[:])
	if err != nil {
		t.Fatal("device fixture signing failed")
	}
	proof := unsigned + "." + enc(append(sigR.FillBytes(make([]byte, 32)), sigS.FillBytes(make([]byte, 32))...))
	loginBody, _ := json.Marshal(map[string]string{"challengeId": string(challengeResponse.ChallengeId), "code": code, "deviceProof": proof})
	login := request("POST", "/auth/email-login", loginBody, "", requestKey(), peer, source)
	var success api.PhoneAuthSuccess
	if login.Code != http.StatusOK || json.Unmarshal(login.Body.Bytes(), &success) != nil || success.Token == "" || success.User.Id == "" {
		t.Fatalf("login status %d", login.Code)
	}
	token := success.Token
	// Exercise library is tested through its authenticated application boundary
	// only; it is deliberately not mounted as a public HTTP route yet.
	personalStore, err := workoutrepo.NewPersonalExerciseStore(keys.workoutName, keys.workoutNameID)
	if err != nil {
		t.Fatal("personal exercise store setup failed")
	}
	personalEpoch, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("personal exercise epoch transaction failed")
	}
	if err := personalStore.InitializeKey(ctx, personalEpoch); err != nil {
		personalEpoch.Rollback()
		t.Fatal("personal exercise epoch failed")
	}
	if err := personalEpoch.Commit(); err != nil {
		t.Fatal("personal exercise epoch commit failed")
	}
	personalIdentity, err := domain.NewEmailIdentityProtector(keys.identityIndex, keys.identityCipher, keys.identityID)
	if err != nil {
		t.Fatal("personal exercise identity setup failed")
	}
	personalResolver, err := authidentity.NewResolver(personalIdentity)
	if err != nil {
		t.Fatal("personal exercise resolver setup failed")
	}
	personalCodec, err := authsession.NewTokenCodec(keys.sessionToken, authsession.TokenPolicy{MaxLifetime: policy.sessionLifetime})
	if err != nil {
		t.Fatal("personal exercise token setup failed")
	}
	personalSessions, err := authsession.NewStore(personalResolver, personalCodec)
	if err != nil {
		t.Fatal("personal exercise session setup failed")
	}
	personalService, err := workoutapp.NewPersonalExerciseService(personalSessions, personalStore)
	if err != nil {
		t.Fatal("personal exercise service setup failed")
	}
	personal := workoutdomain.PersonalExercise{ID: "97ab0210c56f48ae930de1629b761f09", Name: "划船"}
	if _, _, err := personalService.PutOwn(ctx, db, token, personal, 0); !errors.Is(err, workoutapp.ErrAgeRequired) {
		t.Fatal("personal exercise allowed a user without age verification")
	}
	secondDevice, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("second synthetic device setup failed")
	}
	secondBody, _ := json.Marshal(map[string]any{"email": "assembly@example.test", "devicePublicKey": map[string]string{
		"kty": "EC", "crv": "P-256", "x": enc(secondDevice.X.FillBytes(make([]byte, 32))), "y": enc(secondDevice.Y.FillBytes(make([]byte, 32))),
	}})
	secondIssued := request("POST", "/auth/email/challenges", secondBody, "", requestKey(), peer, source)
	var secondChallenge api.EmailLoginChallenge
	if secondIssued.Code != http.StatusAccepted || json.Unmarshal(secondIssued.Body.Bytes(), &secondChallenge) != nil || secondChallenge.ChallengeId == "" {
		t.Fatal("assembled new-device challenge failed")
	}
	secondCode, err := deriver.Derive(string(secondChallenge.ChallengeId), "email_login", 6)
	if err != nil {
		t.Fatal("second synthetic code derivation failed")
	}
	issuedAt := time.Now().UTC()
	secondClaims, _ := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "email_login", "resourceId": secondChallenge.ChallengeId,
		"nonce": secondChallenge.Nonce, "iat": issuedAt.Unix(), "exp": issuedAt.Add(time.Minute).Unix(), "jti": requestKey()})
	secondUnsigned := enc([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + enc(secondClaims)
	secondDigest := sha256.Sum256([]byte(secondUnsigned))
	secondR, secondS, err := ecdsa.Sign(rand.Reader, secondDevice, secondDigest[:])
	if err != nil {
		t.Fatal("second synthetic device signing failed")
	}
	secondProof := secondUnsigned + "." + enc(append(secondR.FillBytes(make([]byte, 32)), secondS.FillBytes(make([]byte, 32))...))
	secondLoginBody, _ := json.Marshal(map[string]string{"challengeId": string(secondChallenge.ChallengeId), "code": secondCode, "deviceProof": secondProof})
	secondLogin := request("POST", "/auth/email-login", secondLoginBody, "", requestKey(), peer, source)
	var pending api.PhoneAuthApprovalRequired
	if secondLogin.Code != http.StatusOK || json.Unmarshal(secondLogin.Body.Bytes(), &pending) != nil || string(pending.Status) != "approval_required" || pending.ApprovalId == "" || pending.ApprovalSecret == "" || pending.Nonce == "" {
		t.Fatal("assembled new-device login did not require approval")
	}
	exchangePath := "/auth/login-approvals/" + string(pending.ApprovalId) + "/exchange"
	exchangeBody := func() []byte {
		t.Helper()
		now := time.Now().UTC()
		claims, _ := json.Marshal(map[string]any{"aud": "nerae-auth-device", "purpose": "approval_exchange", "resourceId": pending.ApprovalId,
			"nonce": pending.Nonce, "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "jti": requestKey()})
		unsigned := enc([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + enc(claims)
		digest := sha256.Sum256([]byte(unsigned))
		r, s, err := ecdsa.Sign(rand.Reader, secondDevice, digest[:])
		if err != nil {
			t.Fatal("approval exchange signing failed")
		}
		proof := unsigned + "." + enc(append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...))
		body, _ := json.Marshal(map[string]string{"approvalSecret": string(pending.ApprovalSecret), "deviceProof": proof})
		return body
	}
	pendingBody, pendingKey := exchangeBody(), requestKey()
	if badIP := request("POST", exchangePath, pendingBody, "", pendingKey, "198.51.100.1:12345", source); badIP.Code != http.StatusServiceUnavailable {
		t.Fatal("approval exchange accepted an untrusted proxy")
	}
	firstPoll := request("POST", exchangePath, pendingBody, "", pendingKey, peer, source)
	if firstPoll.Code != http.StatusOK || firstPoll.Body.String() != `{"status":"pending"}` || firstPoll.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("assembled pending approval poll failed: %d", firstPoll.Code)
	}
	decisionPath := "/me/login-approvals/" + string(pending.ApprovalId) + "/decision"
	decisionBody, _ := json.Marshal(map[string]any{"decision": "approve", "comparisonCode": pending.ComparisonCode, "confirmed": true})
	decisionKey := requestKey()
	if badIP := request("POST", decisionPath, decisionBody, token, decisionKey, "198.51.100.1:12345", source); badIP.Code != http.StatusServiceUnavailable {
		t.Fatal("decision accepted an untrusted proxy")
	}
	decision := request("POST", decisionPath, decisionBody, token, decisionKey, peer, source)
	if decision.Code != http.StatusOK || decision.Body.String() != `{"status":"approved"}` || decision.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("assembled decision failed: %d", decision.Code)
	}
	decisionReplay := request("POST", decisionPath, decisionBody, token, decisionKey, peer, source)
	if decisionReplay.Code != http.StatusOK || decisionReplay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(decision.Body.Bytes(), decisionReplay.Body.Bytes()) {
		t.Fatal("assembled decision did not replay its committed receipt")
	}
	pendingReplay := request("POST", exchangePath, pendingBody, "", pendingKey, peer, source)
	if pendingReplay.Code != http.StatusOK || !bytes.Equal(pendingReplay.Body.Bytes(), firstPoll.Body.Bytes()) {
		t.Fatal("assembled approval poll did not preserve the original pending snapshot")
	}
	approvedBody, approvedKey := exchangeBody(), requestKey()
	exchanged := request("POST", exchangePath, approvedBody, "", approvedKey, peer, source)
	var secondSuccess api.PhoneAuthSuccess
	if exchanged.Code != http.StatusOK || json.Unmarshal(exchanged.Body.Bytes(), &secondSuccess) != nil || secondSuccess.Token == "" || secondSuccess.User.Id != success.User.Id {
		t.Fatalf("assembled new-device exchange failed: %d", exchanged.Code)
	}
	exchangeReplay := request("POST", exchangePath, approvedBody, "", approvedKey, peer, source)
	var replaySuccess api.PhoneAuthSuccess
	if exchangeReplay.Code != http.StatusOK || json.Unmarshal(exchangeReplay.Body.Bytes(), &replaySuccess) != nil || replaySuccess.Token != secondSuccess.Token {
		t.Fatal("assembled new-device exchange did not recover the same session")
	}
	age := request("POST", "/me/age-verify", []byte(`{"isAdult":true,"expectedVersion":1}`), token, "", peer, source)
	if age.Code != http.StatusOK {
		t.Fatalf("age status %d", age.Code)
	}
	createdPersonal, changed, err := personalService.PutOwn(ctx, db, token, personal, 0)
	if err != nil || !changed || createdPersonal.Version != 1 {
		t.Fatal("authenticated personal exercise create failed")
	}
	otherPersonal, err := personalService.ReadOwn(ctx, db, secondSuccess.Token, personal.ID)
	if err != nil || otherPersonal.Name != personal.Name || otherPersonal.ID != personal.ID {
		t.Fatal("approved second device could not read the same personal action")
	}
	personal.Name = "坐姿划船"
	renamedPersonal, changed, err := personalService.PutOwn(ctx, db, secondSuccess.Token, personal, 1)
	if err != nil || !changed || renamedPersonal.Version != 2 {
		t.Fatal("approved device could not rename personal action")
	}
	ownAgain, err := personalService.ReadOwn(ctx, db, token, personal.ID)
	if err != nil || ownAgain.ID != personal.ID || ownAgain.Name != personal.Name {
		t.Fatal("renamed action did not keep its identity")
	}
	personalHistory, err := personalService.ReadOwnVersion(ctx, db, token, personal.ID, 1)
	if err != nil || personalHistory.ID != personal.ID || personalHistory.Name != "划船" {
		t.Fatal("renaming changed the personal action historical snapshot")
	}
	personalPage, err := personalService.ListOwn(ctx, db, secondSuccess.Token, "划船", "", 20)
	if err != nil || len(personalPage.Items) != 1 || personalPage.Items[0].ID != personal.ID {
		t.Fatal("approved device could not search personal action")
	}
	if _, err := personalService.ReadOwn(ctx, db, "invalid", personal.ID); !errors.Is(err, workoutapp.ErrAuthentication) {
		t.Fatal("personal action accepted an unauthenticated session")
	}
	t.Log("PASS: approved devices read historical personal exercise names")
	t.Log("PASS: approved synthetic devices share an age-gated personal exercise identity")
	create := []byte(fmt.Sprintf(`{"source":"free","date":%q,"timeZone":"UTC","clientEventId":"api-assembly-event-001"}`, time.Now().UTC().Format("2006-01-02")))
	createKey := requestKey()
	created := request("POST", "/me/sessions", create, token, createKey, peer, source)
	var record struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if created.Code != http.StatusCreated || created.Header().Get("Idempotency-Key-Expires-At") == "" || json.Unmarshal(created.Body.Bytes(), &record) != nil || record.ID == "" || record.Version != 1 {
		t.Fatalf("create status %d", created.Code)
	}
	again := request("POST", "/me/sessions", create, token, createKey, peer, source)
	if again.Code != http.StatusCreated || !bytes.Equal(created.Body.Bytes(), again.Body.Bytes()) {
		t.Fatal("workout create replay changed result")
	}
	sets := []byte(`{"expectedVersion":1,"exerciseEntryKeys":["entry-001"],"sets":[{"recordKey":"record-001","exerciseEntryKey":"entry-001","exerciseName":"卧推","kind":"weight","setIndex":1,"weightKg":42.50,"reps":8,"completed":true}]}`)
	secondRead := request("GET", "/me/sessions/"+record.ID, nil, secondSuccess.Token, "", peer, source)
	if secondRead.Code != http.StatusOK {
		t.Fatalf("second device could not read the created workout: %d", secondRead.Code)
	}
	saved := request("POST", "/me/sessions/"+record.ID+"/sets", sets, secondSuccess.Token, requestKey(), peer, source)
	if saved.Code != http.StatusOK {
		t.Fatalf("set write status %d", saved.Code)
	}
	read := request("GET", "/me/sessions/"+record.ID, nil, token, "", peer, source)
	if read.Code != http.StatusOK || !bytes.Contains(read.Body.Bytes(), []byte("record-001")) {
		t.Fatalf("read status %d", read.Code)
	}
	anonymous := request("GET", "/me/sessions/"+record.ID, nil, "", "", peer, source)
	if anonymous.Code != http.StatusUnauthorized || bytes.Contains(anonymous.Body.Bytes(), []byte("record-001")) {
		t.Fatal("anonymous record access not denied")
	}
	// Exercise the current recording contract using the two separately approved
	// synthetic devices, not only the legacy session route above.
	v2Create := []byte(fmt.Sprintf(`{"source":"free","date":%q,"timeZone":"UTC","clientEventId":"api-assembly-v2-event-01","mode":"after_workout"}`, time.Now().UTC().Format("2006-01-02")))
	v2Created := request("POST", "/me/recordings", v2Create, token, requestKey(), peer, source)
	var v2 struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
	}
	if v2Created.Code != http.StatusCreated || json.Unmarshal(v2Created.Body.Bytes(), &v2) != nil || v2.ID == "" || v2.Version != 1 {
		t.Fatalf("v2 create status %d", v2Created.Code)
	}
	v2Path := "/me/recordings/" + v2.ID
	v2Batch := []byte(`{"expectedVersion":1,"addEntries":[{"exerciseEntryKey":"v2-entry-001","exerciseName":"深蹲"}],"sets":[{"recordKey":"v2-record-001","exerciseEntryKey":"v2-entry-001","setIndex":1,"confirmed":false,"measurement":{"method":"total","weightKg":"45","reps":8}},{"recordKey":"v2-record-002","exerciseEntryKey":"v2-entry-001","setIndex":2,"confirmed":false,"measurement":{"method":"total"}}],"exerciseEntryKeys":["v2-entry-001"]}`)
	if got := request("POST", v2Path+"/sets", v2Batch, secondSuccess.Token, requestKey(), peer, source); got.Code != http.StatusOK {
		t.Fatalf("v2 second-device write status %d", got.Code)
	}
	v2Add := []byte(`{"expectedVersion":2,"sets":[{"recordKey":"v2-record-003","exerciseEntryKey":"v2-entry-001","setIndex":3,"confirmed":false,"measurement":{"method":"total","weightKg":"50","reps":6}}],"exerciseEntryKeys":["v2-entry-001"]}`)
	if got := request("POST", v2Path+"/sets", v2Add, secondSuccess.Token, requestKey(), peer, source); got.Code != http.StatusOK {
		t.Fatalf("v2 added group status %d", got.Code)
	}
	if got := request("POST", v2Path+"/sets", v2Add, token, requestKey(), peer, source); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("v2 stale write status %d", got.Code)
	}
	if got := request("POST", v2Path+"/finish", []byte(`{"expectedVersion":3}`), token, requestKey(), peer, source); got.Code != http.StatusOK {
		t.Fatalf("v2 finish status %d", got.Code)
	}
	v2Read := request("GET", v2Path, nil, secondSuccess.Token, "", peer, source)
	var v2Detail struct {
		Version int    `json:"version"`
		Status  string `json:"status"`
		Sets    []struct {
			Measurement map[string]any `json:"measurement"`
		} `json:"sets"`
		ActionVolumes []struct {
			Volume struct {
				CalculatedKg string `json:"calculatedKg"`
				ExcludedSets int    `json:"excludedSets"`
			} `json:"volume"`
		} `json:"actionVolumes"`
	}
	if v2Read.Code != http.StatusOK || json.Unmarshal(v2Read.Body.Bytes(), &v2Detail) != nil || v2Detail.Version != 4 ||
		v2Detail.Status != "completed" || len(v2Detail.Sets) != 3 || len(v2Detail.ActionVolumes) != 1 ||
		v2Detail.ActionVolumes[0].Volume.CalculatedKg != "660.00" || v2Detail.ActionVolumes[0].Volume.ExcludedSets != 1 {
		t.Fatal("v2 two-device read lost a group, completion or calculated total")
	}
	if _, exists := v2Detail.Sets[1].Measurement["weightKg"]; exists {
		t.Fatal("v2 blank group was converted to zero")
	}
	if got := request("GET", v2Path, nil, "", "", peer, source); got.Code != http.StatusUnauthorized {
		t.Fatalf("v2 anonymous status %d", got.Code)
	}
	t.Log("PASS: approved synthetic devices share v2 groups, completion and missing values")
	t.Log("PASS: assembled handler challenge -> synthetic derived code login -> age -> workout create/set/read")
	t.Log("PASS: assembled new-device decision uses trusted IP, shared idempotency epoch and authorized replay")
	t.Log("PASS: assembled new-device exchange and shared workout through proxy boundary")
}
