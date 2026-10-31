package approvalreceipt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

const (
	testSessionID = "session_0123456789abcdefgh"
	testToken     = "abcdefghijklmnopqrstuvwxabcdefghijklmnopqrstuvwx"
)

func fixedDatabaseTime(t *testing.T, tx *sql.Tx, value time.Time) DatabaseUTC {
	t.Helper()
	now := DatabaseUTC{at: value, tx: tx}
	if !validDatabaseUTC(now) {
		t.Fatal("test database time is not canonical UTC microseconds")
	}
	return now
}

func testDatabaseTx(t *testing.T, raw string) (*sql.DB, *sql.Tx, DatabaseUTC) {
	return testDatabaseTxValues(t, raw)
}

func testDatabaseTxValues(t *testing.T, values ...string) (*sql.DB, *sql.Tx, DatabaseUTC) {
	t.Helper()
	db := sql.OpenDB(clockConnector{values: append([]string(nil), values...)})
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		_ = db.Close()
		t.Fatal("could not begin synthetic tx")
	}
	now, err := ReadDatabaseUTC(context.Background(), tx)
	if err != nil {
		_ = tx.Rollback()
		_ = db.Close()
		t.Fatal("could not read synthetic DB time")
	}
	t.Cleanup(func() {
		_ = tx.Rollback()
		_ = db.Close()
	})
	return db, tx, now
}

func testCrypto(t *testing.T) *Crypto {
	t.Helper()
	c, err := New(KeyConfig{
		IdempotencyHMACKey: bytes.Repeat([]byte{0x11}, 32),
		RequestHMACKey:     bytes.Repeat([]byte{0x22}, 32),
		AES256GCMKey:       bytes.Repeat([]byte{0x33}, 32),
	})
	if err != nil {
		t.Fatal("New failed")
	}
	return c
}

func validRequest() Request {
	return Request{
		IdempotencyKey: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 24)),
		ApprovalID:     "approval_0123456789abcdefgh",
		ApprovalSecret: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x55}, 32)),
		DeviceProof:    compactProof(),
	}
}

func compactProof() string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"ES256","typ":"JWT"}`)) + "." + enc([]byte(`{"purpose":"approval_exchange"}`)) + "." + enc(bytes.Repeat([]byte{0x66}, 64))
}

func makeTestContext(t *testing.T, c *Crypto, req Request) (Binding, Context, DatabaseUTC, *sql.DB, *sql.Tx) {
	t.Helper()
	b, err := c.Summarize(req)
	if err != nil {
		t.Fatal("Summarize failed")
	}
	subjectScope, err := c.SubjectScope("101")
	if err != nil {
		t.Fatal("SubjectScope failed")
	}
	approvalScope, err := c.ApprovalScope(req.ApprovalID)
	if err != nil {
		t.Fatal("ApprovalScope failed")
	}
	expires := time.Date(2026, 10, 31, 12, 30, 15, 123456000, time.UTC)
	ctx, err := c.NewContext("approval-key-v1", subjectScope, approvalScope, b, resultSession, testSessionID, expires)
	if err != nil {
		t.Fatal("NewContext failed")
	}
	db, tx, now := testDatabaseTx(t, "2026-10-31 12:00:00.000000")
	return b, ctx, now, db, tx
}

type testSessionVerifier struct {
	subjectID        string
	sessionID        string
	tokenExpiresAt   time.Time
	sessionExpiresAt time.Time
	token            string
	err              error
}

type clockConnector struct {
	value  string
	err    error
	values []string
}

func (c clockConnector) Connect(context.Context) (driver.Conn, error) {
	return &clockConn{value: c.value, err: c.err, values: append([]string(nil), c.values...)}, nil
}
func (c clockConnector) Driver() driver.Driver { return clockDriver{connector: c} }

type clockDriver struct{ connector clockConnector }

func (d clockDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type clockConn struct {
	value  string
	err    error
	values []string
	index  int
}

func (c *clockConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unsupported") }
func (c *clockConn) Close() error                        { return nil }
func (c *clockConn) Begin() (driver.Tx, error)           { return clockTx{}, nil }
func (c *clockConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return clockTx{}, nil
}
func (c *clockConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if query != databaseNowSQL || c.err != nil {
		if c.err != nil {
			return nil, c.err
		}
		return nil, errors.New("unexpected query")
	}
	value := c.value
	if len(c.values) != 0 {
		index := c.index
		if index >= len(c.values) {
			index = len(c.values) - 1
		}
		value = c.values[index]
		c.index++
	}
	return &clockRows{value: value}, nil
}

type clockTx struct{}

func (clockTx) Commit() error   { return nil }
func (clockTx) Rollback() error { return nil }

type clockRows struct {
	value string
	read  bool
}

func (clockRows) Columns() []string { return []string{"db_now"} }
func (clockRows) Close() error      { return nil }
func (r *clockRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = r.value
	return nil
}

func TestReadDatabaseUTCRetrievesExactUTCClock(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		bad   bool
	}{
		{"valid", "2026-10-31 12:00:00.123456", false},
		{"short precision", "2026-10-31 12:00:00.123", true},
		{"invalid", "not-a-time", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(clockConnector{value: tc.value})
			defer db.Close()
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			now, err := ReadDatabaseUTC(context.Background(), tx)
			_ = tx.Rollback()
			if tc.bad {
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatal("invalid database time accepted")
				}
				return
			}
			want := time.Date(2026, 10, 31, 12, 0, 0, 123456000, time.UTC)
			if err != nil || !now.at.Equal(want) || now.at.Location() != time.UTC || now.tx != tx {
				t.Fatal("database UTC timestamp was not preserved")
			}
		})
	}
}

func (v testSessionVerifier) VerifyCurrentSession(ctx context.Context, tx *sql.Tx, token string) (string, string, time.Time, time.Time, error) {
	if ctx == nil || tx == nil || token != v.token {
		return "", "", time.Time{}, time.Time{}, errors.New("verification failed")
	}
	return v.subjectID, v.sessionID, v.tokenExpiresAt, v.sessionExpiresAt, v.err
}

func testVerifier() testSessionVerifier {
	return testSessionVerifier{subjectID: "101", sessionID: testSessionID,
		tokenExpiresAt:   time.Date(2026, 10, 31, 13, 0, 0, 0, time.UTC),
		sessionExpiresAt: time.Date(2026, 10, 31, 13, 0, 0, 0, time.UTC), token: testToken}
}

func TestScopesAreDerivedAndDomainSeparated(t *testing.T) {
	c := testCrypto(t)
	subject, err := c.SubjectScope("101")
	if err != nil {
		t.Fatal(err)
	}
	if subject.digest == sha256.Sum256([]byte("101")) {
		t.Fatal("subject scope is an enumerable raw hash")
	}
	if again, _ := c.SubjectScope("101"); again != subject {
		t.Fatal("subject scope is not deterministic")
	}
	if other, _ := c.SubjectScope("102"); other == subject {
		t.Fatal("different subject derived the same scope")
	}
	approval, err := c.ApprovalScope("approval_0123456789abcdefgh")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := c.ApprovalScope("approval_0123456789abcdefgh"); again != approval {
		t.Fatal("approval scope is not deterministic")
	}
	if other, _ := c.ApprovalScope("approval_1123456789abcdefgh"); other == approval {
		t.Fatal("different approval derived the same scope")
	}
	if _, err := c.SubjectScope("01"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("noncanonical subject ID accepted")
	}
	if _, err := c.SubjectScope("0"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("zero subject ID accepted")
	}
	if _, err := c.ApprovalScope("short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("invalid approval ID accepted")
	}
}

func TestSummarizeSeparatesKeyAndRequestBinding(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	base, err := c.Summarize(req)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.Summarize(req); err != nil || again != base {
		t.Fatal("same request did not produce stable binding")
	}
	changedKey := req
	changedKey.IdempotencyKey = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x45}, 24))
	keyOnly, err := c.Summarize(changedKey)
	if err != nil || keyOnly.idempotencyHMAC == base.idempotencyHMAC || keyOnly.requestHMAC != base.requestHMAC {
		t.Fatal("idempotency-key binding was not independent")
	}
	for name, mutate := range map[string]func(*Request){
		"approval id": func(r *Request) { r.ApprovalID += "x" },
		"secret": func(r *Request) {
			r.ApprovalSecret = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x57}, 32))
		},
		"proof": func(r *Request) { r.DeviceProof = strings.Replace(r.DeviceProof, "eyJ", "eXJ", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := req
			mutate(&changed)
			got, err := c.Summarize(changed)
			if err != nil || got.requestHMAC == base.requestHMAC {
				t.Fatal("request field did not change request digest")
			}
		})
	}
}

func TestSealOpenChecksSessionBindingExpiryAndAAD(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	_, receiptContext, now, _, tx := makeTestContext(t, c, req)
	verifier := testVerifier()
	sealed, err := c.Seal(context.Background(), receiptContext, testToken, now, tx, verifier)
	if err != nil {
		t.Fatal("Seal failed")
	}
	opened, err := c.Open(context.Background(), receiptContext, sealed, now, tx, verifier)
	if err != nil || string(opened.Bytes()) != testToken {
		t.Fatal("Open did not recover verified token")
	}
	mutations := map[string]func(*Context){
		"subject scope":  func(c *Context) { c.subjectScope.digest[0] ^= 1 },
		"approval scope": func(c *Context) { c.approvalScope.digest[0] ^= 1 },
		"idempotency":    func(c *Context) { c.binding.idempotencyHMAC[0] ^= 1 },
		"request":        func(c *Context) { c.binding.requestHMAC[0] ^= 1 },
		"approval id":    func(c *Context) { c.binding.approvalID += "x" },
		"key id":         func(c *Context) { c.keyID = "approval-key-v2" },
		"result type":    func(c *Context) { c.resultType = "other" },
		"reference":      func(c *Context) { c.reference = "session_1123456789abcdefgh" },
		"expiry":         func(c *Context) { c.expiresAt = c.expiresAt.Add(time.Microsecond) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := receiptContext
			mutate(&altered)
			if _, err := c.Open(context.Background(), altered, sealed, now, tx, verifier); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatal("AAD change was accepted")
			}
		})
	}
	modified := sealed.Bytes()
	modified[len(modified)-1] ^= 1
	parsed, err := ParseSealed(modified)
	if err != nil {
		t.Fatal("framing-valid modified blob should parse before authentication")
	}
	if _, err := c.Open(context.Background(), receiptContext, parsed, now, tx, verifier); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("modified ciphertext authenticated")
	}
	late := fixedDatabaseTime(t, tx, receiptContext.expiresAt)
	if _, err := c.Open(context.Background(), receiptContext, sealed, late, tx, verifier); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("expired receipt opened")
	}
	if _, err := c.Seal(context.Background(), receiptContext, testToken, late, tx, verifier); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("expired receipt was sealed")
	}
}

func TestSessionVerifierMustBindTokenToContext(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	_, receiptContext, now, db, tx := makeTestContext(t, c, req)
	for name, verifier := range map[string]testSessionVerifier{
		"different subject": func() testSessionVerifier { v := testVerifier(); v.subjectID = "102"; return v }(),
		"different session": func() testSessionVerifier { v := testVerifier(); v.sessionID = "session_1123456789abcdefgh"; return v }(),
		"session expiry too soon": func() testSessionVerifier {
			v := testVerifier()
			v.sessionExpiresAt = receiptContext.expiresAt.Add(-time.Microsecond)
			return v
		}(),
		"token and database expiry differ": func() testSessionVerifier {
			v := testVerifier()
			v.sessionExpiresAt = v.sessionExpiresAt.Add(time.Second)
			return v
		}(),
		"inactive or unverified": func() testSessionVerifier {
			v := testVerifier()
			v.err = errors.New("private verifier detail")
			return v
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.Seal(context.Background(), receiptContext, testToken, now, tx, verifier); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatal("mismatched/unverified session token was sealed")
			}
		})
	}
	sealed, err := c.Seal(context.Background(), receiptContext, testToken, now, tx, testVerifier())
	if err != nil {
		t.Fatal(err)
	}
	wrong := testVerifier()
	wrong.sessionID = "session_1123456789abcdefgh"
	if _, err := c.Open(context.Background(), receiptContext, sealed, now, tx, wrong); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("Open released token after current-session verification failed")
	}
	otherTx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer otherTx.Rollback()
	if _, err := c.Open(context.Background(), receiptContext, sealed, now, otherTx, testVerifier()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("DB clock from another transaction was accepted")
	}
}

func TestSealAndOpenRecheckDBClockAfterVerifier(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	_, receiptContext, _, _, tx := makeTestContext(t, c, req)
	verifier := testVerifier()
	before := "2026-10-31 12:00:00.000000"
	afterExpiry := "2026-10-31 12:30:15.123457"
	dbSeal, txSeal, nowSeal := testDatabaseTxValues(t, before, afterExpiry)
	defer dbSeal.Close()
	if _, err := c.Seal(context.Background(), receiptContext, testToken, nowSeal, txSeal, verifier); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("receipt expiring during session verification was sealed")
	}
	sealed, err := c.Seal(context.Background(), receiptContext, testToken, fixedDatabaseTime(t, tx, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)), tx, verifier)
	if err != nil {
		t.Fatal("could not produce valid receipt for open boundary test")
	}
	dbOpen, txOpen, nowOpen := testDatabaseTxValues(t, before, afterExpiry)
	defer dbOpen.Close()
	if _, err := c.Open(context.Background(), receiptContext, sealed, nowOpen, txOpen, verifier); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("receipt expiring during session verification was opened")
	}
}

func TestValidationAndBounds(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	for name, mutate := range map[string]func(*Request){
		"short id":         func(r *Request) { r.ApprovalID = "short" },
		"noncanonical key": func(r *Request) { r.IdempotencyKey += "=" },
		"short key":        func(r *Request) { r.IdempotencyKey = base64.RawURLEncoding.EncodeToString([]byte("tiny")) },
		"bad secret":       func(r *Request) { r.ApprovalSecret = strings.Repeat("A", 42) },
		"proof malformed":  func(r *Request) { r.DeviceProof = "not-a-jws" },
		"proof long":       func(r *Request) { r.DeviceProof = strings.Repeat("A", maxProofBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := req
			mutate(&bad)
			if _, err := c.Summarize(bad); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid request accepted")
			}
		})
	}
	binding, _, _, _, tx := makeTestContext(t, c, req)
	subjectScope, _ := c.SubjectScope("101")
	approvalScope, _ := c.ApprovalScope(req.ApprovalID)
	future := time.Date(2026, 10, 31, 13, 0, 0, 0, time.UTC)
	for name, args := range map[string]struct {
		key      string
		subject  SubjectScope
		approval ApprovalScope
		typ      string
		ref      string
		expires  time.Time
	}{
		"zero subject":      {"approval-key-v1", SubjectScope{}, approvalScope, resultSession, testSessionID, future},
		"zero approval":     {"approval-key-v1", subjectScope, ApprovalScope{}, resultSession, testSessionID, future},
		"bad key id":        {"bad/key", subjectScope, approvalScope, resultSession, testSessionID, future},
		"wrong type":        {"approval-key-v1", subjectScope, approvalScope, "pending", testSessionID, future},
		"bad reference":     {"approval-key-v1", subjectScope, approvalScope, resultSession, "short", future},
		"nanosecond expiry": {"approval-key-v1", subjectScope, approvalScope, resultSession, testSessionID, time.Date(2026, 10, 31, 12, 30, 15, 123456789, time.UTC)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := c.NewContext(args.key, args.subject, args.approval, binding, args.typ, args.ref, args.expires); !errors.Is(err, ErrInvalidInput) {
				t.Fatal("invalid context accepted")
			}
		})
	}
	wrongApproval, _ := c.ApprovalScope("approval_1123456789abcdefgh")
	if _, err := c.NewContext("approval-key-v1", subjectScope, wrongApproval, binding, resultSession, testSessionID, future); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("scope from another approval was accepted")
	}
	for _, badTime := range []time.Time{
		{}, time.Date(2026, 10, 31, 12, 0, 0, 123456789, time.UTC), time.Date(2026, 10, 31, 12, 0, 0, 0, time.FixedZone("not-utc", 3600)),
	} {
		if validDatabaseUTC(DatabaseUTC{at: badTime, tx: tx}) {
			t.Fatal("untrusted/invalid DB time representation accepted")
		}
	}
	_, ctx, now, _, tx := makeTestContext(t, c, req)
	verifier := testVerifier()
	maxToken := strings.Repeat("a", maxPlaintext)
	verifier.token = maxToken
	sealed, err := c.Seal(context.Background(), ctx, maxToken, now, tx, verifier)
	if err != nil {
		t.Fatal("maximum bounded token rejected")
	}
	if _, err := ParseSealed(sealed.Bytes()); err != nil {
		t.Fatal("maximum valid sealed blob rejected")
	}
	tooLarge := strings.Repeat("a", maxPlaintext+1)
	verifier.token = tooLarge
	if _, err := c.Seal(context.Background(), ctx, tooLarge, now, tx, verifier); !errors.Is(err, ErrInvalidInput) {
		t.Fatal("oversized token accepted")
	}
	for _, blob := range [][]byte{nil, []byte{1}, append([]byte{2}, bytes.Repeat([]byte{0}, 28)...), append([]byte{1}, bytes.Repeat([]byte{0}, 1+gcmNonceSize+gcmTagSize+maxPlaintext)...)} {
		if _, err := ParseSealed(blob); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatal("malformed framing accepted")
		}
	}
}

func TestKeyConfigurationEpochAndRandomFailure(t *testing.T) {
	base := KeyConfig{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)}
	for name, cfg := range map[string]KeyConfig{
		"short":     {bytes.Repeat([]byte{1}, 31), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)},
		"zero":      {make([]byte, 32), bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{3}, 32)},
		"duplicate": {bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{3}, 32)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(cfg); !errors.Is(err, ErrConfiguration) {
				t.Fatal("bad key configuration accepted")
			}
		})
	}
	c, err := New(base)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := c.EpochTag("approval-key-v1")
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := c.EpochTag("approval-key-v1"); again != tag {
		t.Fatal("epoch tag was unstable")
	}
	if changed, _ := c.EpochTag("approval-key-v2"); changed == tag {
		t.Fatal("keyID not bound to epoch")
	}
	for _, bad := range []string{"", "bad/key", strings.Repeat("a", 65), "é"} {
		if _, err := c.EpochTag(bad); !errors.Is(err, ErrConfiguration) {
			t.Fatal("invalid keyID accepted")
		}
	}
	_, receiptContext, now, _, tx := makeTestContext(t, c, validRequest())
	c.random = failingReader{}
	if _, err := c.Seal(context.Background(), receiptContext, testToken, now, tx, testVerifier()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("random source failure did not fail closed")
	}
	var zero Crypto
	if _, err := zero.EpochTag("approval-key-v1"); !errors.Is(err, ErrConfiguration) {
		t.Fatal("zero crypto accepted")
	}
	if _, err := zero.Open(context.Background(), receiptContext, Sealed{}, now, tx, testVerifier()); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatal("zero crypto opened receipt")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("private entropy failure") }

func TestNonceUniquenessAndKeySeparation(t *testing.T) {
	baseConfig := KeyConfig{bytes.Repeat([]byte{0x11}, 32), bytes.Repeat([]byte{0x22}, 32), bytes.Repeat([]byte{0x33}, 32)}
	base, err := New(baseConfig)
	if err != nil {
		t.Fatal(err)
	}
	req := validRequest()
	baseBinding, baseContext, now, _, tx := makeTestContext(t, base, req)
	verifier := testVerifier()
	first, err := base.Seal(context.Background(), baseContext, testToken, now, tx, verifier)
	if err != nil {
		t.Fatal(err)
	}
	second, err := base.Seal(context.Background(), baseContext, testToken, now, tx, verifier)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Bytes()[1:1+gcmNonceSize], second.Bytes()[1:1+gcmNonceSize]) {
		t.Fatal("consecutive seals reused nonce")
	}
	for _, role := range []string{"idempotency", "request", "aead"} {
		t.Run(role, func(t *testing.T) {
			cfg := KeyConfig{append([]byte(nil), baseConfig.IdempotencyHMACKey...), append([]byte(nil), baseConfig.RequestHMACKey...), append([]byte(nil), baseConfig.AES256GCMKey...)}
			switch role {
			case "idempotency":
				cfg.IdempotencyHMACKey[0] ^= 1
			case "request":
				cfg.RequestHMACKey[0] ^= 1
			case "aead":
				cfg.AES256GCMKey[0] ^= 1
			}
			other, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			otherBinding, err := other.Summarize(req)
			if err != nil {
				t.Fatal(err)
			}
			if role == "idempotency" && (otherBinding.idempotencyHMAC == baseBinding.idempotencyHMAC || otherBinding.requestHMAC != baseBinding.requestHMAC) {
				t.Fatal("idempotency key did not have independent effect")
			}
			if role == "request" && (otherBinding.idempotencyHMAC != baseBinding.idempotencyHMAC || otherBinding.requestHMAC == baseBinding.requestHMAC) {
				t.Fatal("request key did not have independent effect")
			}
			if role == "aead" {
				if otherBinding != baseBinding {
					t.Fatal("AEAD key changed request digests")
				}
				if _, err := other.Open(context.Background(), baseContext, first, now, tx, verifier); !errors.Is(err, ErrInvalidReceipt) {
					t.Fatal("different AEAD key opened ciphertext")
				}
			}
			if tagA, _ := base.EpochTag("approval-key-v1"); func() bool { tagB, _ := other.EpochTag("approval-key-v1"); return tagA == tagB }() {
				t.Fatal("key change was not reflected in epoch")
			}
		})
	}
}

func TestSensitiveValuesStayRedacted(t *testing.T) {
	c := testCrypto(t)
	req := validRequest()
	b, receiptContext, now, _, tx := makeTestContext(t, c, req)
	sealed, err := c.Seal(context.Background(), receiptContext, testToken, now, tx, testVerifier())
	if err != nil {
		t.Fatal(err)
	}
	plain, err := c.Open(context.Background(), receiptContext, sealed, now, tx, testVerifier())
	if err != nil {
		t.Fatal(err)
	}
	subjectScope, _ := c.SubjectScope("101")
	approvalScope, _ := c.ApprovalScope(req.ApprovalID)
	databaseNow := now
	values := []any{KeyConfig{[]byte("secret-key-marker"), []byte("secret-key-two"), []byte("secret-key-three")}, req, b,
		receiptContext, sealed, plain, c, Crypto{}, subjectScope, approvalScope, databaseNow}
	for _, value := range values {
		formatted := fmt.Sprintf("%v %+v %#v", value, value, value)
		for _, secret := range []string{req.IdempotencyKey, req.ApprovalID, req.ApprovalSecret, req.DeviceProof, testToken, "secret-key-marker"} {
			if strings.Contains(formatted, secret) {
				t.Fatal("formatting exposed sensitive input")
			}
		}
		if _, err := json.Marshal(value); err == nil {
			t.Fatal("sensitive value was JSON serializable")
		}
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		logger.Info("value", "v", value)
		for _, secret := range []string{req.IdempotencyKey, req.ApprovalSecret, req.DeviceProof, testToken} {
			if strings.Contains(buf.String(), secret) {
				t.Fatal("slog exposed sensitive input")
			}
		}
	}
}
