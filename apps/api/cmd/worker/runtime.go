package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"log/slog"
	"time"

	"github.com/go-sql-driver/mysql"
	"nerae/api/internal/config"
	"nerae/api/internal/modules/auth/bootstrap"
	"nerae/api/internal/modules/auth/codeepoch"
	"nerae/api/internal/modules/auth/domain"
	"nerae/api/internal/modules/auth/issuance"
	authmail "nerae/api/internal/modules/auth/mail"
	"nerae/api/internal/modules/auth/mailworker"
)

var (
	errWorkerStartup = errors.New("worker startup unavailable")
	errWorkerRun     = errors.New("worker stopped unexpectedly")
)

type runtime struct {
	db         *sql.DB
	worker     *mailworker.Worker
	outbox     *issuance.Outbox
	identities *domain.EmailIdentityProtector
	hasher     *domain.EmailCodeHasher
	hasherID   string
	deriver    *issuance.Deriver
}

func (runtime) String() string               { return "WorkerRuntime{redacted}" }
func (runtime) GoString() string             { return "WorkerRuntime{redacted}" }
func (runtime) MarshalJSON() ([]byte, error) { return nil, errWorkerStartup }
func (runtime) LogValue() slog.Value         { return slog.StringValue("WorkerRuntime{redacted}") }

func (r *runtime) close() {
	if r != nil && r.db != nil {
		_ = r.db.Close()
	}
}

func newRuntime(ctx context.Context, cfg config.Config, secrets workerSecrets, logger *slog.Logger) (*runtime, error) {
	if ctx == nil || !secrets.valid() || logger == nil || cfg.MySQLDSN == "" {
		return nil, errWorkerStartup
	}
	if !validWorkerDSN(cfg.MySQLDSN) {
		return nil, errWorkerStartup
	}
	identityIndex, err := decodeWorkerKey(secrets.EmailIdentityIndexKey)
	if err != nil {
		return nil, errWorkerStartup
	}
	identityEncryption, err := decodeWorkerKey(secrets.EmailIdentityEncryptionKey)
	if err != nil {
		return nil, errWorkerStartup
	}
	codeHash, err := decodeWorkerKey(secrets.EmailCodeHashKey)
	if err != nil {
		return nil, errWorkerStartup
	}
	codeDerivation, err := decodeWorkerKey(secrets.EmailCodeDerivationKey)
	if err != nil {
		return nil, errWorkerStartup
	}
	identities, err := domain.NewEmailIdentityProtector(identityIndex, identityEncryption, secrets.EmailIdentityKeyID)
	if err != nil {
		return nil, errWorkerStartup
	}
	hasher, err := domain.NewEmailCodeHasher(codeHash)
	if err != nil {
		return nil, errWorkerStartup
	}
	deriver, err := issuance.NewDeriver(codeDerivation)
	if err != nil {
		return nil, errWorkerStartup
	}
	mailer, err := authmail.NewSMTPMailer(secrets.smtpConfig())
	if err != nil {
		return nil, errWorkerStartup
	}
	outbox, err := issuance.NewOutbox(deriver, secrets.EmailCodeKeyID, hasher, secrets.EmailCodeHashKeyID, identities, mailer,
		time.Duration(secrets.LeaseMS)*time.Millisecond)
	if err != nil {
		return nil, errWorkerStartup
	}
	db, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		return nil, errWorkerStartup
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(5 * time.Minute)
	closeOnFailure := true
	defer func() {
		if closeOnFailure {
			_ = db.Close()
		}
	}()
	startupTimeout := time.Duration(secrets.StartupTimeoutMS) * time.Millisecond
	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	if err := db.PingContext(startupCtx); err != nil {
		return nil, errWorkerStartup
	}
	tx, err := db.BeginTx(startupCtx, nil)
	if err != nil {
		return nil, errWorkerStartup
	}
	defer tx.Rollback()
	initializer, err := bootstrap.NewIdentityEpochInitializer(identities)
	if err != nil || initializer.Initialize(startupCtx, tx) != nil {
		return nil, errWorkerStartup
	}
	if err := ensureCodeEpochBootstrapSafe(startupCtx, tx); err != nil {
		return nil, errWorkerStartup
	}
	if err := outbox.InitializeCodeKey(startupCtx, tx); err != nil {
		return nil, errWorkerStartup
	}
	// Keep startup epoch acquisition ordered like challenge writes: identity,
	// code derivation, then code hash. The caller owns this single transaction.
	hashEpoch, err := codeepoch.New(hasher, secrets.EmailCodeHashKeyID)
	if err != nil || hashEpoch.Initialize(startupCtx, tx) != nil {
		return nil, errWorkerStartup
	}
	if err := tx.Commit(); err != nil {
		// Unknown commit outcome is not retried. The next process start will
		// verify the epoch row against the same configured keys.
		return nil, errWorkerStartup
	}
	worker, err := mailworker.New(secrets.workerConfig(outbox, db, logger))
	if err != nil {
		return nil, errWorkerStartup
	}
	closeOnFailure = false
	return &runtime{db: db, worker: worker, outbox: outbox, identities: identities, hasher: hasher, hasherID: secrets.EmailCodeHashKeyID, deriver: deriver}, nil
}

func validWorkerDSN(value string) bool {
	dsn, err := mysql.ParseDSN(value)
	if err != nil || dsn.User == "" || dsn.DBName == "" || !dsn.ParseTime || dsn.Loc == nil || dsn.Loc.String() != "UTC" {
		return false
	}
	if dsn.Net == "unix" {
		return true
	}
	// Production TCP MySQL connections must use the driver's verified system-
	// trust TLS mode. Named custom configurations are not accepted here because
	// their SkipVerify/ServerName settings cannot be proven by parsing a DSN.
	return dsn.Net == "tcp" && dsn.TLSConfig == "true"
}

func ensureCodeEpochBootstrapSafe(ctx context.Context, tx *sql.Tx) error {
	if ctx == nil || tx == nil {
		return errWorkerStartup
	}
	var slot int
	err := tx.QueryRowContext(ctx, `SELECT slot FROM auth_email_code_key_epoch WHERE slot=1 FOR UPDATE`).Scan(&slot)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return errWorkerStartup
	}
	// If a code-key epoch disappeared after challenge history existed, do not
	// silently bind a new key to that history. The identity epoch row is already
	// locked by IdentityEpochInitializer, which serializes this check against
	// challenge creation's identity→code epoch lock order.
	for _, statement := range []string{
		`SELECT 1 FROM auth_email_login_slot LIMIT 1 FOR UPDATE`,
		`SELECT 1 FROM auth_email_login_challenge LIMIT 1 FOR UPDATE`,
		`SELECT 1 FROM auth_email_outbox LIMIT 1 FOR UPDATE`,
	} {
		var found int
		err := tx.QueryRowContext(ctx, statement).Scan(&found)
		if err == nil || !errors.Is(err, sql.ErrNoRows) {
			return errWorkerStartup
		}
	}
	return nil
}

func decodeWorkerKey(value string) ([]byte, error) {
	key, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != value {
		return nil, errWorkerConfig
	}
	return key, nil
}

func run(ctx context.Context, logger *slog.Logger) error {
	if ctx == nil || logger == nil {
		return errWorkerStartup
	}
	cfg, err := config.Load(config.Worker)
	if err != nil {
		return errWorkerStartup
	}
	secrets, err := loadWorkerSecrets()
	if err != nil {
		return errWorkerStartup
	}
	r, err := newRuntime(ctx, cfg, secrets, logger)
	if err != nil {
		return errWorkerStartup
	}
	defer r.close()
	logger.Info("email worker started")
	if err := r.worker.Run(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return errWorkerRun
	}
	logger.Info("email worker stopped")
	return nil
}
