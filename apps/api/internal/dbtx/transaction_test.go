package dbtx

import (
	"database/sql"
	"testing"
)

func TestWrapKeepsLifecycleWithOwner(t *testing.T) {
	if Wrap(nil) != nil {
		t.Fatal("nil transaction became a usable view")
	}
	view := Wrap(&sql.Tx{})
	if view == nil {
		t.Fatal("non-nil transaction lost its view")
	}
	if _, ok := any(view).(*sql.Tx); ok {
		t.Fatal("view exposed the underlying transaction")
	}
	if _, ok := any(view).(interface{ Commit() error }); ok {
		t.Fatal("view exposed commit")
	}
	if _, ok := any(view).(interface{ Rollback() error }); ok {
		t.Fatal("view exposed rollback")
	}
}
