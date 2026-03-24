package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExternalOwnerSendRequestRoundTrip(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := enqueueExternalOwnerRequest(ctx, townRoot, binding, ExternalOwnerRequestKindSend, "hello")
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
	if parsed.ID != request.ID || parsed.Message != "hello" || parsed.Kind != ExternalOwnerRequestKindSend {
		t.Fatalf("parsed request = %#v, want id=%q kind=send message=hello", parsed, request.ID)
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

func TestExternalOwnerAskRequestReturnsContent(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := enqueueExternalOwnerRequest(ctx, townRoot, binding, ExternalOwnerRequestKindAsk, "what now?")
	if err != nil {
		t.Fatalf("enqueueExternalOwnerRequest() error = %v", err)
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = WriteExternalOwnerResponse(townRoot, "hq-mayor", ExternalCopilotOwnerResponse{ID: request.ID, Content: "DONE", CreatedAt: time.Now().UTC()})
	}()
	resp, err := waitForExternalOwnerResponse(ctx, townRoot, binding, request.ID)
	if err != nil {
		t.Fatalf("waitForExternalOwnerResponse() error = %v", err)
	}
	if resp.Content != "DONE" {
		t.Fatalf("Content = %q, want DONE", resp.Content)
	}
}

func TestExternalOwnerRequestClaimIsAtomic(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	request, err := enqueueExternalOwnerRequest(context.Background(), townRoot, binding, ExternalOwnerRequestKindAsk, "hello")
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
	if _, err := ClaimExternalOwnerRequest(paths[0]); err == nil {
		t.Fatal("second ClaimExternalOwnerRequest() error = nil, want failure")
	}
	if !strings.Contains(claimed, ".claimed-") {
		t.Fatalf("claimed path = %q, want .claimed-<id> suffix", claimed)
	}
	parsed, err := ReadExternalOwnerRequest(claimed)
	if err != nil {
		t.Fatalf("ReadExternalOwnerRequest() error = %v", err)
	}
	if parsed.ID != request.ID {
		t.Fatalf("parsed.ID = %q, want %q", parsed.ID, request.ID)
	}
}

func TestExternalOwnerRejectsUnsupportedRequestKind(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	_, err := enqueueExternalOwnerRequest(context.Background(), townRoot, binding, "dance", "hello")
	if err == nil {
		t.Fatal("enqueueExternalOwnerRequest() error = nil, want unsupported kind error")
	}
	if !strings.Contains(err.Error(), "unsupported owner request kind") {
		t.Fatalf("error = %v, want unsupported owner request kind", err)
	}
}

func TestExternalOwnerProcessesRequestsFIFO(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	first, err := enqueueExternalOwnerRequest(context.Background(), townRoot, binding, ExternalOwnerRequestKindSend, "first")
	if err != nil {
		t.Fatalf("enqueue first error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := enqueueExternalOwnerRequest(context.Background(), townRoot, binding, ExternalOwnerRequestKindAsk, "second")
	if err != nil {
		t.Fatalf("enqueue second error = %v", err)
	}
	paths, err := NextExternalOwnerRequests(townRoot, "hq-mayor")
	if err != nil {
		t.Fatalf("NextExternalOwnerRequests() error = %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("NextExternalOwnerRequests() len = %d, want 2", len(paths))
	}
	firstParsed, err := ReadExternalOwnerRequest(paths[0])
	if err != nil {
		t.Fatalf("ReadExternalOwnerRequest(first) error = %v", err)
	}
	secondParsed, err := ReadExternalOwnerRequest(paths[1])
	if err != nil {
		t.Fatalf("ReadExternalOwnerRequest(second) error = %v", err)
	}
	if firstParsed.ID != first.ID || secondParsed.ID != second.ID {
		t.Fatalf("request order = %#v %#v, want first then second", firstParsed, secondParsed)
	}
	if firstParsed.Message != "first" || secondParsed.Message != "second" {
		t.Fatalf("request messages = %#v %#v", firstParsed, secondParsed)
	}
}

func TestExternalOwnerAskRequestReturnsStructuredError(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := enqueueExternalOwnerRequest(ctx, townRoot, binding, ExternalOwnerRequestKindAsk, "what now?")
	if err != nil {
		t.Fatalf("enqueueExternalOwnerRequest() error = %v", err)
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = WriteExternalOwnerResponse(townRoot, "hq-mayor", ExternalCopilotOwnerResponse{ID: request.ID, Error: "tool failed", CreatedAt: time.Now().UTC()})
	}()
	resp, err := waitForExternalOwnerResponse(ctx, townRoot, binding, request.ID)
	if err != nil {
		t.Fatalf("waitForExternalOwnerResponse() error = %v", err)
	}
	if resp.Error != "tool failed" {
		t.Fatalf("Error = %q, want tool failed", resp.Error)
	}
	if resp.Content != "" {
		t.Fatalf("Content = %q, want empty", resp.Content)
	}
}

func TestExternalOwnerClaimedAndRemovedRequestsAreNotRequeued(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	binding := &SessionBinding{SessionName: "hq-mayor", Metadata: OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234)}
	_, err := enqueueExternalOwnerRequest(context.Background(), townRoot, binding, ExternalOwnerRequestKindSend, "first")
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
	remainder, err := NextExternalOwnerRequests(townRoot, "hq-mayor")
	if err != nil {
		t.Fatalf("NextExternalOwnerRequests() after claim error = %v", err)
	}
	if len(remainder) != 0 {
		t.Fatalf("NextExternalOwnerRequests() after claim = %#v, want none", remainder)
	}
	if err := RemoveExternalOwnerRequest(claimed); err != nil {
		t.Fatalf("RemoveExternalOwnerRequest() error = %v", err)
	}
	remainder, err = NextExternalOwnerRequests(townRoot, "hq-mayor")
	if err != nil {
		t.Fatalf("NextExternalOwnerRequests() after remove error = %v", err)
	}
	if len(remainder) != 0 {
		t.Fatalf("NextExternalOwnerRequests() after remove = %#v, want none", remainder)
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

func TestExternalOwnerResetDoesNotLoseRuntimeBinding(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	store := NewFileSessionBindingStore(townRoot)
	binding := SessionBinding{
		IssueID:          "hq-mayor",
		Role:             "mayor",
		AgentName:        "mayor",
		SessionName:      "hq-mayor",
		RuntimeSessionID: "runtime-123",
		WorkDir:          filepath.Join(townRoot, "mayor"),
		Metadata:         OwnerBindingMetadata(ExternalOwnerDir(townRoot, "hq-mayor"), 1234),
		LifecycleState:   SessionLifecycleRunning,
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := WriteExternalOwnerStatus(townRoot, "hq-mayor", ExternalCopilotOwnerStatus{OwnerPID: 1234, RuntimeSessionID: "runtime-123", UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}
	if _, err := enqueueExternalOwnerRequest(context.Background(), townRoot, &binding, ExternalOwnerRequestKindAsk, "hello"); err != nil {
		t.Fatalf("enqueueExternalOwnerRequest() error = %v", err)
	}
	if err := WriteExternalOwnerResponse(townRoot, "hq-mayor", ExternalCopilotOwnerResponse{ID: "done", Content: "DONE", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteExternalOwnerResponse() error = %v", err)
	}

	if err := ResetExternalOwnerState(townRoot, "hq-mayor"); err != nil {
		t.Fatalf("ResetExternalOwnerState() error = %v", err)
	}
	if _, err := ReadExternalOwnerStatus(townRoot, "hq-mayor"); err == nil {
		t.Fatal("ReadExternalOwnerStatus() error = nil, want status removed")
	}
	requests, err := NextExternalOwnerRequests(townRoot, "hq-mayor")
	if err != nil {
		t.Fatalf("NextExternalOwnerRequests() error = %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("requests = %#v, want none", requests)
	}
	entries, err := os.ReadDir(filepath.Join(ExternalOwnerDir(townRoot, "hq-mayor"), "responses"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir(responses) error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("response entries = %d, want 0", len(entries))
	}
	loaded, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil || loaded == nil {
		t.Fatalf("Load() = %v, %v", loaded, err)
	}
	if loaded.RuntimeSessionID != "runtime-123" {
		t.Fatalf("RuntimeSessionID = %q, want runtime-123", loaded.RuntimeSessionID)
	}
	if !IsExternalOwnerBinding(loaded) {
		t.Fatalf("binding metadata = %#v, want owner-managed binding to remain persisted", loaded.Metadata)
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

func TestWaitForExternalOwnerReadyReturnsStatusWhenRuntimeSessionIDAppears(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	sessionName := "hq-mayor"
	pid := os.Getpid()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = WriteExternalOwnerStatus(townRoot, sessionName, ExternalCopilotOwnerStatus{
			OwnerPID:         pid,
			RuntimeSessionID: "runtime-123",
			UpdatedAt:        time.Now().UTC(),
		})
	}()

	status, err := waitForExternalOwnerReady(ctx, townRoot, sessionName, pid)
	if err != nil {
		t.Fatalf("waitForExternalOwnerReady() error = %v", err)
	}
	if status.RuntimeSessionID != "runtime-123" {
		t.Fatalf("RuntimeSessionID = %q, want runtime-123", status.RuntimeSessionID)
	}
	if status.OwnerPID != pid {
		t.Fatalf("OwnerPID = %d, want %d", status.OwnerPID, pid)
	}
}

func TestWaitForExternalOwnerReadyReturnsContextErrorWhenStatusNeverArrives(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	_, err := waitForExternalOwnerReady(ctx, t.TempDir(), "hq-mayor", os.Getpid())
	if err == nil {
		t.Fatal("waitForExternalOwnerReady() error = nil, want context deadline exceeded")
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("waitForExternalOwnerReady() error = %v, want context deadline exceeded", err)
	}
}

func TestWaitForExternalOwnerReadyFailsWhenProcessExitsBeforeReady(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := waitForExternalOwnerReady(ctx, t.TempDir(), "hq-mayor", 999999)
	if err == nil {
		t.Fatal("waitForExternalOwnerReady() error = nil, want early exit error")
	}
	if !strings.Contains(err.Error(), "external owner process exited before becoming ready") {
		t.Fatalf("waitForExternalOwnerReady() error = %v, want early exit message", err)
	}
}

func TestReadExternalOwnerConfigRoundTrip(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	cfg := ExternalCopilotOwnerConfig{
		IssueID:         "slotmachine-910.3.7",
		Role:            "witness",
		RigName:         "gastown",
		AgentName:       "watch",
		SessionName:     "gt-witness-review",
		TownRoot:        townRoot,
		WorkDir:         filepath.Join(townRoot, "gastown"),
		RequestedModel:  "claude-sonnet-4.6",
		ReasoningEffort: "high",
		ResumeSessionID: "runtime-456",
		Metadata: map[string]string{
			"session_kind": "review",
		},
	}
	if err := WriteExternalOwnerConfig(cfg); err != nil {
		t.Fatalf("WriteExternalOwnerConfig() error = %v", err)
	}
	got, err := ReadExternalOwnerConfig(ExternalOwnerConfigPath(townRoot, cfg.SessionName))
	if err != nil {
		t.Fatalf("ReadExternalOwnerConfig() error = %v", err)
	}
	if got.ResumeSessionID != "runtime-456" {
		t.Fatalf("ResumeSessionID = %q, want runtime-456", got.ResumeSessionID)
	}
	if got.RequestedModel != "claude-sonnet-4.6" {
		t.Fatalf("RequestedModel = %q, want claude-sonnet-4.6", got.RequestedModel)
	}
	if got.Metadata["session_kind"] != "review" {
		t.Fatalf("Metadata = %#v, want session_kind=review", got.Metadata)
	}
	if got.TownRoot != townRoot {
		t.Fatalf("TownRoot = %q, want %q", got.TownRoot, townRoot)
	}
	if got.WorkDir != filepath.Join(townRoot, "gastown") {
		t.Fatalf("WorkDir = %q", got.WorkDir)
	}
	if _, err := os.Stat(ExternalOwnerConfigPath(townRoot, cfg.SessionName)); err != nil {
		t.Fatalf("owner config file missing: %v", err)
	}
	if _, err := filepath.Abs(ExternalOwnerConfigPath(townRoot, cfg.SessionName)); err != nil {
		t.Fatalf("ExternalOwnerConfigPath() invalid: %v", err)
	}
	if got.Role != "witness" || got.AgentName != "watch" {
		t.Fatalf("config identity = %#v", got)
	}
	if got.SessionName != "gt-witness-review" {
		t.Fatalf("SessionName = %q, want gt-witness-review", got.SessionName)
	}
	if got.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", got.ReasoningEffort)
	}
	if got.IssueID == "" {
		t.Fatal("IssueID = empty, want persisted value")
	}
	if got.IssueID != "slotmachine-910.3.7" {
		t.Fatalf("IssueID = %q, want slotmachine-910.3.7", got.IssueID)
	}
	if got.RigName != "gastown" {
		t.Fatalf("RigName = %q, want gastown", got.RigName)
	}
}
