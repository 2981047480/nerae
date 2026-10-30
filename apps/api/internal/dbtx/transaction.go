// Package dbtx defines the minimum database transaction capabilities used by
// repositories. Transaction lifecycle stays with the application service.
package dbtx

import (
	"context"
	"database/sql"
)

type Row interface{ Scan(dest ...any) error }

type ReadTransaction interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) Row
}

type Transaction interface {
	ReadTransaction
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type sqlView struct{ tx *sql.Tx }

// Wrap exposes queries and writes from an existing SQL transaction without
// exposing its Commit or Rollback methods. The source must be a live transaction;
// this ordinary view does not enforce an SQL allowlist. A nil source stays nil.
func Wrap(tx *sql.Tx) Transaction {
	if tx == nil {
		return nil
	}
	return sqlView{tx: tx}
}

func (v sqlView) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return v.tx.ExecContext(ctx, query, args...)
}
func (v sqlView) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return v.tx.QueryContext(ctx, query, args...)
}
func (v sqlView) QueryRowContext(ctx context.Context, query string, args ...any) Row {
	return v.tx.QueryRowContext(ctx, query, args...)
}
