package runtime

import (
	"context"
	"testing"
	"time"
)

func TestReconcileExternalOwnersClearsStaleOwnerBindings(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	store := NewFileSessionBindingStore(townRoot)
	binding := SessionBinding{
		IssueID:          "hq-mayor",
		Role:             "mayor",
		AgentName:        "mayor",
		SessionName:      "hq-mayor",
		RuntimeSessionID: "runtime-123",
		WorkDir:          townRoot + "/mayor",
		Metadata:         OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 999999),
		LifecycleState:   SessionLifecycleRunning,
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	result, err := ReconcileExternalOwners(context.Background(), townRoot)
	if err != nil {
		t.Fatalf("ReconcileExternalOwners() error = %v", err)
	}
	if result.Scanned != 1 || result.StaleCleared != 1 || result.OwnerMissing != 1 {
		t.Fatalf("result = %#v", result)
	}
	updated, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil || updated == nil {
		t.Fatalf("Load() updated = %v, %v", updated, err)
	}
	if IsExternalOwnerBinding(updated) {
		t.Fatalf("binding still marked owner-managed: %#v", updated.Metadata)
	}
}

func TestReconcileExternalOwnersWithRecoverCallsRecoverForMayor(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	store := NewFileSessionBindingStore(townRoot)
	binding := SessionBinding{
		IssueID:          "hq-mayor",
		Role:             "mayor",
		AgentName:        "mayor",
		SessionName:      "hq-mayor",
		RuntimeSessionID: "runtime-123",
		WorkDir:          townRoot + "/mayor",
		Metadata:         OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 999999),
		LifecycleState:   SessionLifecycleRunning,
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	called := false
	result, err := ReconcileExternalOwnersWithRecover(context.Background(), townRoot, func(ctx context.Context, binding SessionBinding) error {
		called = true
		if binding.Role != "mayor" {
			t.Fatalf("binding.Role = %q, want mayor", binding.Role)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReconcileExternalOwnersWithRecover() error = %v", err)
	}
	if !called {
		t.Fatal("recoverFn was not called")
	}
	if result.AutoRecovered != 1 {
		t.Fatalf("AutoRecovered = %d, want 1", result.AutoRecovered)
	}
}

func TestReconcileExternalOwnersWithRecoverCallsRecoverForRigRoles(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	store := NewFileSessionBindingStore(townRoot)
	bindings := []SessionBinding{
		{
			IssueID:          "rig-witness",
			Role:             "witness",
			RigName:          "gastown",
			AgentName:        "witness",
			SessionName:      "gt-rig-gastown-witness",
			RuntimeSessionID: "runtime-witness",
			WorkDir:          townRoot + "/gastown",
			Metadata:         OwnerBindingMetadata(ExternalOwnerDir(townRoot, "gt-rig-gastown-witness"), 999999),
			LifecycleState:   SessionLifecycleRunning,
		},
		{
			IssueID:          "rig-refinery",
			Role:             "refinery",
			RigName:          "gastown",
			AgentName:        "refinery",
			SessionName:      "gt-rig-gastown-refinery",
			RuntimeSessionID: "runtime-refinery",
			WorkDir:          townRoot + "/gastown",
			Metadata:         OwnerBindingMetadata(ExternalOwnerDir(townRoot, "gt-rig-gastown-refinery"), 999999),
			LifecycleState:   SessionLifecycleRunning,
		},
	}
	for _, binding := range bindings {
		if err := store.Save(context.Background(), binding); err != nil {
			t.Fatalf("Save(%s) error = %v", binding.Role, err)
		}
	}
	called := map[string]bool{}
	result, err := ReconcileExternalOwnersWithRecover(context.Background(), townRoot, func(ctx context.Context, binding SessionBinding) error {
		called[binding.Role] = true
		return nil
	})
	if err != nil {
		t.Fatalf("ReconcileExternalOwnersWithRecover() error = %v", err)
	}
	if !called["witness"] || !called["refinery"] {
		t.Fatalf("recoverFn calls = %#v, want witness and refinery", called)
	}
	if result.AutoRecovered != 2 {
		t.Fatalf("AutoRecovered = %d, want 2", result.AutoRecovered)
	}
}

func TestReconcileExternalOwnersPreservesRuntimeSessionIDWhenClearingOwnerMetadata(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	store := NewFileSessionBindingStore(townRoot)
	binding := SessionBinding{
		IssueID:          "rig-witness",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		SessionName:      "gt-rig-gastown-witness",
		RuntimeSessionID: "runtime-witness-123",
		WorkDir:          townRoot + "/gastown",
		Metadata:         OwnerBindingMetadata(ExternalOwnerDir(townRoot, "gt-rig-gastown-witness"), 999999),
		LifecycleState:   SessionLifecycleRunning,
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := ReconcileExternalOwners(context.Background(), townRoot); err != nil {
		t.Fatalf("ReconcileExternalOwners() error = %v", err)
	}
	updated, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil || updated == nil {
		t.Fatalf("Load() updated = %v, %v", updated, err)
	}
	if updated.RuntimeSessionID != "runtime-witness-123" {
		t.Fatalf("RuntimeSessionID = %q, want runtime-witness-123", updated.RuntimeSessionID)
	}
	if updated.LifecycleState != SessionLifecycleStopped {
		t.Fatalf("LifecycleState = %q, want fail-closed stopped state", updated.LifecycleState)
	}
	if updated.Metadata[ExternalOwnerPIDMetadataKey] != "" {
		t.Fatalf("Metadata = %#v, want cleared owner pid", updated.Metadata)
	}
	if IsExternalOwnerBinding(updated) {
		t.Fatalf("binding still marked owner-managed: %#v", updated.Metadata)
	}
	if state := DeriveLifecycleState(updated, false, false, context.DeadlineExceeded); state != SessionLifecycleUnknown {
		t.Fatalf("DeriveLifecycleState() = %q, want %q for errored stopped binding", state, SessionLifecycleUnknown)
	}
	runningBinding := *updated
	runningBinding.LifecycleState = SessionLifecycleRunning
	if state := DeriveLifecycleState(&runningBinding, false, false, context.DeadlineExceeded); state != SessionLifecycleUnknown {
		t.Fatalf("DeriveLifecycleState() running binding with lookup error = %q, want %q", state, SessionLifecycleUnknown)
	}
	updated.UpdatedAt = time.Now().UTC().Add(-2 * SessionLifecycleStartingGrace)
	updated.LifecycleState = SessionLifecycleStarting
	if state := DeriveLifecycleState(updated, false, false, context.DeadlineExceeded); state != SessionLifecycleUnknown {
		t.Fatalf("DeriveLifecycleState() stale starting = %q, want %q", state, SessionLifecycleUnknown)
	}
}
