package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExternalOwnerRequestRoundTrip(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := enqueueExternalOwnerRequest(ctx, townRoot, binding, ExternalOwnerRequestKindAsk, "hello")
	if err != nil {
		t.Fatalf("enqueueExternalOwnerRequest() error = %v", err)
	}
	paths, err := NextExternalOwnerRequests(townRoot, "hq-mayor")
	if err != nil {
		t.Fatalf("NextExternalOwnerRequests() error = %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("NextExternalOwnerRequests() len = %d, want 1", len(paths))
	}
	claimed, err := ClaimExternalOwnerRequest(paths[0])
	if err != nil {
		t.Fatalf("ClaimExternalOwnerRequest() error = %v", err)
	}
	parsed, err := ReadExternalOwnerRequest(claimed)
	if err != nil {
		t.Fatalf("ReadExternalOwnerRequest() error = %v", err)
	}
	if parsed.ID != request.ID || parsed.Message != "hello" {
		t.Fatalf("parsed request = %#v, want id=%q message=hello", parsed, request.ID)
	}
	response := ExternalCopilotOwnerResponse{ID: request.ID, Content: "DONE", CreatedAt: time.Now().UTC()}
	if err := WriteExternalOwnerResponse(townRoot, "hq-mayor", response); err != nil {
		t.Fatalf("WriteExternalOwnerResponse() error = %v", err)
	}
	got, err := waitForExternalOwnerResponse(context.Background(), townRoot, binding, request.ID)
	if err != nil {
		t.Fatalf("waitForExternalOwnerResponse() error = %v", err)
	}
	if got.Content != "DONE" {
		t.Fatalf("response content = %q, want DONE", got.Content)
	}
	if err := RemoveExternalOwnerRequest(claimed); err != nil {
		t.Fatalf("RemoveExternalOwnerRequest() error = %v", err)
	}
	if _, err := filepath.Abs(ExternalOwnerDir(townRoot, "hq-mayor")); err != nil {
		t.Fatalf("ExternalOwnerDir() invalid path: %v", err)
	}
}

func TestDiscoverExternalOwnerFromPersistedBinding(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{
		SessionName:      "hq-mayor",
		RuntimeSessionID: "runtime-123",
		Metadata:         OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 999999),
	}
	if err := WriteExternalOwnerStatus(townRoot, "hq-mayor", ExternalCopilotOwnerStatus{OwnerPID: 999999, RuntimeSessionID: "runtime-123", UpdatedAt: time.Now().Add(-ExternalOwnerHeartbeatWindow * 2)}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}
	discovery, err := DiscoverExternalOwner(townRoot, binding)
	if err != nil {
		t.Fatalf("DiscoverExternalOwner() error = %v", err)
	}
	if discovery.OwnerAlive {
		t.Fatal("OwnerAlive = true, want false for dead pid")
	}
	if !discovery.Recoverable {
		t.Fatal("Recoverable = false, want true")
	}
	if !discovery.NeedsRecovery {
		t.Fatal("NeedsRecovery = false, want true")
	}
	if discovery.Reason == "" {
		t.Fatal("Reason = empty, want explanation")
	}
}

func TestMarkExternalOwnerRecoveredClearsOwnerPid(t *testing.T) {
	t.Parallel()
	store := NewFileSessionBindingStore(t.TempDir())
	binding := SessionBinding{
		IssueID:          "hq-mayor",
		Role:             "mayor",
		AgentName:        "mayor",
		SessionName:      "hq-mayor",
		RuntimeSessionID: "runtime-123",
		Metadata:         OwnerBindingMetadata("/tmp/owner", 1234),
		LifecycleState:   SessionLifecycleStarting,
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil || loaded == nil {
		t.Fatalf("Load() = %v, %v", loaded, err)
	}
	if err := MarkExternalOwnerRecovered(context.Background(), store, loaded); err != nil {
		t.Fatalf("MarkExternalOwnerRecovered() error = %v", err)
	}
	updated, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil || updated == nil {
		t.Fatalf("Load() updated = %v, %v", updated, err)
	}
	if _, ok := updated.Metadata[ExternalOwnerPIDMetadataKey]; ok {
		t.Fatalf("owner pid metadata still present: %#v", updated.Metadata)
	}
	if _, ok := updated.Metadata[ExternalOwnerModeMetadataKey]; ok {
		t.Fatalf("owner mode metadata still present: %#v", updated.Metadata)
	}
	if updated.LifecycleState != SessionLifecycleStopped {
		t.Fatalf("LifecycleState = %q, want %q", updated.LifecycleState, SessionLifecycleStopped)
	}
}

func TestWaitForExternalOwnerResponseIgnoresIncompleteResponseFile(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	requestID := "req-test"
	if err := os.MkdirAll(filepath.Join(ExternalOwnerDir(townRoot, "hq-mayor"), "responses"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	incomplete := []byte("{\n  \"id\": \"req-test\",\n  \"created_at\": \"2026-03-24T00:00:00Z\"\n}\n")
	responsePath := filepath.Join(ExternalOwnerDir(townRoot, "hq-mayor"), "responses", requestID+".json")
	if err := os.WriteFile(responsePath, incomplete, 0o644); err != nil {
		t.Fatalf("WriteFile(incomplete) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := WriteExternalOwnerResponse(townRoot, "hq-mayor", ExternalCopilotOwnerResponse{ID: requestID, Content: "DONE", CreatedAt: time.Now().UTC()}); err != nil {
			t.Errorf("WriteExternalOwnerResponse() error = %v", err)
		}
	}()
	response, err := waitForExternalOwnerResponse(ctx, townRoot, binding, requestID)
	if err != nil {
		t.Fatalf("waitForExternalOwnerResponse() error = %v", err)
	}
	if response.Content != "DONE" {
		t.Fatalf("response.Content = %q, want DONE", response.Content)
	}
}
