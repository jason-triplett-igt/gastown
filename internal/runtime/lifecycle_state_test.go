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

func TestRuntimeSessionPayloadFailsClosedToMinimalFields(t *testing.T) {
	payload := RuntimeSessionPayload("gt-witness", "", "witness", "", "", map[string]interface{}{
		"alive": true,
		"ready": false,
		"busy":  true,
	})
	if payload["session"] != "gt-witness" {
		t.Fatalf("payload = %#v, want session", payload)
	}
	if payload["role"] != "witness" {
		t.Fatalf("payload = %#v, want role", payload)
	}
	if _, ok := payload["runtime_session_id"]; ok {
		t.Fatalf("payload = %#v, should omit empty runtime_session_id", payload)
	}
	if _, ok := payload["issue"]; ok {
		t.Fatalf("payload = %#v, should omit empty issue", payload)
	}
	if _, ok := payload["provider"]; ok {
		t.Fatalf("payload = %#v, should omit empty provider", payload)
	}
	if payload["alive"] != true || payload["ready"] != false || payload["busy"] != true {
		t.Fatalf("payload = %#v, want lifecycle extras preserved", payload)
	}
}

func TestDeriveLifecycleStateRunningWithoutLookupStaysStoppedWhenDead(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	binding := &SessionBinding{LifecycleState: SessionLifecycleRunning, UpdatedAt: now}
	if got := DeriveLifecycleState(binding, false, nil); got != SessionLifecycleStopped {
		t.Fatalf("DeriveLifecycleState() = %q, want %q", got, SessionLifecycleStopped)
	}
}

func TestRuntimeSessionPayloadIncludesLatestStatusFields(t *testing.T) {
	payload := RuntimeSessionPayload("slotmachine-run", "runtime-777", "polecat", "slotmachine-910", "copilot", map[string]interface{}{
		"alive": true,
		"ready": true,
		"busy":  false,
	})
	if payload["session"] != "slotmachine-run" || payload["runtime_session_id"] != "runtime-777" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload["role"] != "polecat" || payload["issue"] != "slotmachine-910" || payload["provider"] != "copilot" {
		t.Fatalf("payload = %#v", payload)
	}
	if payload["alive"] != true || payload["ready"] != true || payload["busy"] != false {
		t.Fatalf("payload = %#v", payload)
	}
}
