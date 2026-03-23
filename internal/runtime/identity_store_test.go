package runtime

import (
	"context"
	"testing"
	"time"
)

func TestFileSessionBindingStoreRoundTrip(t *testing.T) {
	t.Parallel()
	store := NewFileSessionBindingStore(t.TempDir())
	binding := SessionBinding{
		IssueID:          "slotmachine-910.1.3",
		Role:             "polecat",
		RigName:          "gastown",
		AgentName:        "dealer",
		Provider:         "copilot",
		SessionName:      "gt-dealer",
		RuntimeSessionID: "copilot-session-123",
		WorkDir:          "/tmp/worktree",
		Metadata:         map[string]string{"scope": "builder"},
	}

	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got == nil {
		t.Fatal("Load() = nil, want binding")
	}
	if got.RuntimeSessionID != binding.RuntimeSessionID {
		t.Fatalf("RuntimeSessionID = %q, want %q", got.RuntimeSessionID, binding.RuntimeSessionID)
	}
	if got.SessionName != binding.SessionName {
		t.Fatalf("SessionName = %q, want %q", got.SessionName, binding.SessionName)
	}
	if got.Metadata["scope"] != "builder" {
		t.Fatalf("Metadata = %#v, want scope=builder", got.Metadata)
	}

	if err := store.Delete(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	got, err = store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil {
		t.Fatalf("Load() after delete error = %v", err)
	}
	if got != nil {
		t.Fatalf("Load() after delete = %#v, want nil", got)
	}
}

func TestBindingKeySanitizesComponents(t *testing.T) {
	t.Parallel()
	got := bindingKey("GT-123/abc", "Polecat", "My Rig", "dealer@one")
	want := "gt-123-abc--polecat--my-rig--dealer-one"
	if got != want {
		t.Fatalf("bindingKey() = %q, want %q", got, want)
	}
}

func TestFileSessionBindingStoreLoadsLatestMatchWithoutIssueID(t *testing.T) {
	t.Parallel()
	store := NewFileSessionBindingStore(t.TempDir())
	older := SessionBinding{
		IssueID:          "slotmachine-100",
		Role:             "polecat",
		RigName:          "gastown",
		AgentName:        "dealer",
		SessionName:      "old-session",
		RuntimeSessionID: "runtime-old",
		UpdatedAt:        time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC),
	}
	newer := SessionBinding{
		IssueID:          "slotmachine-200",
		Role:             "polecat",
		RigName:          "gastown",
		AgentName:        "dealer",
		SessionName:      "new-session",
		RuntimeSessionID: "runtime-new",
		UpdatedAt:        time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
	}
	if err := store.Save(context.Background(), older); err != nil {
		t.Fatalf("Save() older error = %v", err)
	}
	if err := store.Save(context.Background(), newer); err != nil {
		t.Fatalf("Save() newer error = %v", err)
	}
	got, err := store.Load(context.Background(), "", "polecat", "gastown", "dealer")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got == nil || got.RuntimeSessionID != "runtime-new" {
		t.Fatalf("Load() = %#v, want latest binding", got)
	}
}

func TestFileSessionBindingStoreListFiltersAndSorts(t *testing.T) {
	t.Parallel()
	store := NewFileSessionBindingStore(t.TempDir())
	bindings := []SessionBinding{
		{IssueID: "slotmachine-100", Role: "polecat", RigName: "gastown", AgentName: "alpha", SessionName: "alpha", UpdatedAt: time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)},
		{IssueID: "slotmachine-200", Role: "witness", RigName: "gastown", AgentName: "witness", SessionName: "witness", UpdatedAt: time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC)},
		{IssueID: "slotmachine-300", Role: "polecat", RigName: "gastown", AgentName: "beta", SessionName: "beta", UpdatedAt: time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC)},
	}
	for _, binding := range bindings {
		if err := store.Save(context.Background(), binding); err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}
	got, err := store.List(context.Background(), "polecat", "gastown")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	if got[0].AgentName != "beta" || got[1].AgentName != "alpha" {
		t.Fatalf("List() = %#v, want sorted polecat bindings", got)
	}
}
