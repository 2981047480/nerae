package session

import (
	"errors"
	"testing"
)

func TestClassifyCurrentSessionRowDoesNotHideCorruptState(t *testing.T) {
	for _, test := range []struct {
		name  string
		state string
		live  bool
		want  error
	}{
		{name: "revoked", state: "revoked", live: false, want: ErrInvalidSession},
		{name: "active expired", state: "active", live: false, want: ErrInvalidSession},
		{name: "active live no-op", state: "active", live: true, want: ErrSessionUnavailable},
		{name: "unknown state", state: "corrupt", live: false, want: ErrSessionUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := classifyCurrentSessionRow(test.state, test.live); !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}
