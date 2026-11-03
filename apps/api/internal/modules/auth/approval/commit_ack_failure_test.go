package approval

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// commitAckFailureConnector wraps the real isolated MySQL connection. One
// successful COMMIT is deliberately reported as a failure to reproduce a lost
// commit acknowledgement without replacing the database transaction.
type commitAckFailureConnector struct {
	inner driver.Connector
	fail  *atomic.Bool
}

func (c commitAckFailureConnector) Driver() driver.Driver { return c.inner.Driver() }
func (c commitAckFailureConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return commitAckFailureConn{Conn: conn, fail: c.fail}, nil
}

type commitAckFailureConn struct {
	driver.Conn
	fail *atomic.Bool
}

func (c commitAckFailureConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	beginner, ok := c.Conn.(driver.ConnBeginTx)
	if !ok {
		return nil, errors.New("isolated driver does not support transaction options")
	}
	tx, err := beginner.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return commitAckFailureTx{Tx: tx, fail: c.fail}, nil
}

type commitAckFailureTx struct {
	driver.Tx
	fail *atomic.Bool
}

func (t commitAckFailureTx) Commit() error {
	if err := t.Tx.Commit(); err != nil {
		return err
	}
	if t.fail.CompareAndSwap(true, false) {
		return errors.New("synthetic lost commit acknowledgement")
	}
	return nil
}

func isolatedDBWithCommitAckFailure(t *testing.T) (*sql.DB, *atomic.Bool) {
	t.Helper()
	// Validate the DSN and private socket independently, even if a future test
	// calls this helper without first opening the regular isolated connection.
	_ = isolatedDB(t)
	cfg, err := mysql.ParseDSN(os.Getenv("NERAE_APPROVAL_TEST_DSN"))
	if err != nil {
		t.Fatal("could not parse isolated database configuration")
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		t.Fatal("could not create isolated database connector")
	}
	fail := &atomic.Bool{}
	fail.Store(true)
	db := sql.OpenDB(commitAckFailureConnector{inner: connector, fail: fail})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db, fail
}
