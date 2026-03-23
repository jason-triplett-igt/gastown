package runtime

import (
	"fmt"
	"testing"
	"time"
)

func TestDeriveLifecycleState(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()

	tests := []struct {
		name    string
		binding *SessionBinding
		alive   bool
		err     error
		want    string
	}{
		{
			name:    "stopping wins",
			binding: &SessionBinding{LifecycleState: SessionLifecycleStopping, UpdatedAt: now},
			alive:   true,
			want:    SessionLifecycleStopping,
		},
		{
			name:    "starting fresh without lookup stays starting",
			binding: &SessionBinding{LifecycleState: SessionLifecycleStarting, UpdatedAt: now},
			err:     fmt.Errorf("lookup unavailable"),
			want:    SessionLifecycleStarting,
		},
		{
			name:    "starting stale without lookup becomes unknown",
			binding: &SessionBinding{LifecycleState: SessionLifecycleStarting, UpdatedAt: now.Add(-2 * SessionLifecycleStartingGrace)},
			err:     fmt.Errorf("lookup unavailable"),
			want:    SessionLifecycleUnknown,
		},
		{
			name:    "alive becomes running",
			binding: &SessionBinding{LifecycleState: SessionLifecycleStarting, UpdatedAt: now},
			alive:   true,
			want:    SessionLifecycleRunning,
		},
		{
			name: "binding with lookup error becomes unknown",
			binding: &SessionBinding{
				LifecycleState: SessionLifecycleRunning,
				UpdatedAt:      now.Add(-10 * time.Second),
			},
			err:  fmt.Errorf("transport failed"),
			want: SessionLifecycleUnknown,
		},
		{
			name: "no binding and error becomes stopped",
			err:  fmt.Errorf("missing"),
			want: SessionLifecycleStopped,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DeriveLifecycleState(tc.binding, tc.alive, tc.err); got != tc.want {
				t.Fatalf("DeriveLifecycleState() = %q, want %q", got, tc.want)
			}
		})
	}
}
