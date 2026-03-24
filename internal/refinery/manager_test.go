package refinery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/testutil"
	"github.com/steveyegge/gastown/internal/tmux"
)

type fakeRuntimeStarter struct {
	requests  []runtime.SessionLaunchRequest
	lookupReq *runtime.SessionLookupRequest
	session   runtime.ManagedSession
	err       error
}

func (f *fakeRuntimeStarter) Start(_ context.Context, req runtime.SessionLaunchRequest) (runtime.ManagedSession, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

func (f *fakeRuntimeStarter) Lookup(_ context.Context, req runtime.SessionLookupRequest) (runtime.ManagedSession, error) {
	f.lookupReq = &req
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

type fakeManagedSession struct {
	status runtime.SessionStatus
	closed bool
}

func (f *fakeManagedSession) ID() string { return f.status.SessionID }
func (f *fakeManagedSession) Status(context.Context) (runtime.SessionStatus, error) {
	return f.status, nil
}
func (f *fakeManagedSession) Send(context.Context, string) error { return nil }
func (f *fakeManagedSession) Close(context.Context) error {
	f.closed = true
	return nil
}

func setupTestRegistry(t *testing.T) {
	t.Helper()
	// Use a prefix that won't collide with real gastown sessions.
	// The "tr" prefix conflicts with actual rigs running on the host
	// (e.g., tr-refinery, tr-witness), causing tests that assert
	// "no session exists" to fail in gastown workspaces.
	reg := session.NewPrefixRegistry()
	reg.Register("xut", "testrig")
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })
}

func setupTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	setupTestRegistry(t)

	// Create temp directory structure
	tmpDir := t.TempDir()
	rigPath := filepath.Join(tmpDir, "testrig")
	if err := os.MkdirAll(filepath.Join(rigPath, ".runtime"), 0755); err != nil {
		t.Fatalf("mkdir .runtime: %v", err)
	}

	r := &rig.Rig{
		Name: "testrig",
		Path: rigPath,
	}

	return NewManager(r), rigPath
}

func TestManager_SessionName(t *testing.T) {
	mgr, _ := setupTestManager(t)

	want := "xut-refinery"
	got := mgr.SessionName()
	if got != want {
		t.Errorf("SessionName() = %s, want %s", got, want)
	}
}

func TestManager_IsRunning_NoSession(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Without a tmux session, IsRunning should return false
	// Note: this test doesn't create a tmux session, so it tests the "not running" case
	running, err := mgr.IsRunning()
	if err != nil {
		// If tmux server isn't running, HasSession returns an error
		// This is expected in test environments without tmux
		t.Logf("IsRunning returned error (expected without tmux): %v", err)
		return
	}

	if running {
		t.Error("IsRunning() = true, want false (no session created)")
	}
}

func TestManager_Status_NotRunning(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Without a tmux session, Status should return ErrNotRunning
	_, err := mgr.Status()
	if err == nil {
		t.Error("Status() expected error when not running")
	}
	// May return ErrNotRunning or a tmux server error
	t.Logf("Status returned error (expected): %v", err)
}

func TestManager_StartUsesAdapterForNonClaudeRoleConfig(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	refineryRigDir := filepath.Join(rigPath, "refinery", "rig")
	if err := os.MkdirAll(refineryRigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{
		"copilot-external": {
			Provider: "copilot",
			Command:  "copilot",
			CLIURL:   "http://127.0.0.1:4321",
		},
	}
	settings.RoleAgents = map[string]string{"refinery": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(rigPath, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}
	adapter := &fakeRuntimeStarter{}
	mgr.adapter = adapter

	if err := mgr.Start(false, ""); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("adapter requests = %#v, want 1", adapter.requests)
	}
	request := adapter.requests[0]
	if request.Role != "refinery" || request.RigName != "testrig" {
		t.Fatalf("request = %#v", request)
	}
	if request.IssueID != mgr.SessionName() {
		t.Fatalf("IssueID = %q, want %q", request.IssueID, mgr.SessionName())
	}
	if request.WorkDir != refineryRigDir {
		t.Fatalf("WorkDir = %q, want %q", request.WorkDir, refineryRigDir)
	}
	if request.Env["GT_REFINERY"] != "1" {
		t.Fatalf("Env = %#v, want GT_REFINERY=1", request.Env)
	}
}

func TestRefineryStartsExternalCopilotSession(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	refineryRigDir := filepath.Join(rigPath, "refinery", "rig")
	if err := os.MkdirAll(refineryRigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{
		"copilot-external": {
			Provider: "copilot",
			Command:  "copilot",
			CLIURL:   "http://127.0.0.1:4321",
		},
	}
	settings.RoleAgents = map[string]string{"refinery": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(rigPath, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}
	adapter := &fakeRuntimeStarter{}
	mgr.adapter = adapter

	if err := mgr.Start(false, ""); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("adapter requests = %#v, want 1", adapter.requests)
	}
	request := adapter.requests[0]
	if request.Role != "refinery" || request.RigName != "testrig" {
		t.Fatalf("request = %#v", request)
	}
	if request.SessionKind != config.ToolSessionKindPatrol {
		t.Fatalf("SessionKind = %q, want %q", request.SessionKind, config.ToolSessionKindPatrol)
	}
	if request.WorkDir != refineryRigDir {
		t.Fatalf("WorkDir = %q, want %q", request.WorkDir, refineryRigDir)
	}
	if request.Env["GT_REFINERY"] != "1" {
		t.Fatalf("Env = %#v, want GT_REFINERY=1", request.Env)
	}
	if request.ToolPolicy == nil || len(request.ToolPolicy.AvailableTools) == 0 {
		t.Fatalf("ToolPolicy = %#v, want resolved policy", request.ToolPolicy)
	}
	if !strings.Contains(request.Prompt, "Run `gt prime --hook` and begin patrol.") {
		t.Fatalf("Prompt = %q, want refinery patrol startup", request.Prompt)
	}
}

func TestRefineryResumesStoredExternalBinding(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
		Metadata:         map[string]string{"session_kind": "patrol"},
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}}
	mgr.adapter = adapter

	running, err := mgr.IsRunning()
	if err != nil {
		t.Fatalf("IsRunning() error = %v", err)
	}
	if !running {
		t.Fatal("IsRunning() = false, want true")
	}
	if adapter.lookupReq == nil {
		t.Fatal("lookupReq = nil, want lookup from stored binding")
	}
	if adapter.lookupReq.SessionID != "runtime-xyz" {
		t.Fatalf("SessionID = %q, want runtime-xyz", adapter.lookupReq.SessionID)
	}
	if adapter.lookupReq.Provider != "copilot-external" {
		t.Fatalf("Provider = %q, want copilot-external", adapter.lookupReq.Provider)
	}
	if adapter.lookupReq.Metadata["session_kind"] != "patrol" {
		t.Fatalf("Metadata = %#v, want session_kind=patrol", adapter.lookupReq.Metadata)
	}
}

func TestRefineryInvalidRuntimeSessionIDFailsCleanly(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-bad",
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
		Metadata:         map[string]string{"session_kind": "patrol"},
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	mgr.adapter = &fakeRuntimeStarter{err: fmt.Errorf("invalid runtime session id")}

	info, err := mgr.Status()
	if err == nil {
		t.Fatal("Status() error = nil, want ErrNotRunning")
	}
	if err != ErrNotRunning {
		t.Fatalf("Status() error = %v, want ErrNotRunning", err)
	}
	if info != nil {
		t.Fatalf("Status() = %#v, want nil", info)
	}
}

func TestManager_IsRunningUsesManagedRefineryBinding(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}}
	mgr.adapter = adapter

	running, err := mgr.IsRunning()
	if err != nil {
		t.Fatalf("IsRunning() error = %v", err)
	}
	if !running {
		t.Fatal("IsRunning() = false, want true")
	}
	if adapter.lookupReq == nil || adapter.lookupReq.SessionID != "runtime-xyz" {
		t.Fatalf("lookupReq = %#v", adapter.lookupReq)
	}
}

func TestManager_LifecycleStateReportsStoppingFromBinding(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStopping,
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}

	state, err := mgr.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleStopping {
		t.Fatalf("LifecycleState() = %q, want stopping", state)
	}
	running, err := mgr.IsRunning()
	if err != nil {
		t.Fatalf("IsRunning() error = %v", err)
	}
	if running {
		t.Fatal("IsRunning() = true, want false while stopping")
	}
}

func TestManager_LifecycleStateReportsStartingForFreshBindingWithoutLookup(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStarting,
		UpdatedAt:        time.Now().UTC(),
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	mgr.adapter = &fakeRuntimeStarter{err: fmt.Errorf("lookup unavailable")}

	state, err := mgr.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleStarting {
		t.Fatalf("LifecycleState() = %q, want starting", state)
	}
}

func TestManager_LifecycleStateReportsUnknownForStaleBindingWithoutLookup(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStarting,
		UpdatedAt:        time.Now().UTC().Add(-2 * runtime.SessionLifecycleStartingGrace),
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	mgr.adapter = &fakeRuntimeStarter{err: fmt.Errorf("lookup unavailable")}

	state, err := mgr.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleUnknown {
		t.Fatalf("LifecycleState() = %q, want unknown", state)
	}
}

func TestManager_LifecycleStateDoesNotReportRunningWhenManagedSessionNotReady(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleRunning,
		UpdatedAt:        time.Now().UTC(),
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	mgr.adapter = &fakeRuntimeStarter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: false}}}

	state, err := mgr.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleUnknown {
		t.Fatalf("LifecycleState() = %q, want unknown", state)
	}
}

func TestManager_StatusUsesManagedRefineryBinding(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}}
	mgr.adapter = adapter

	info, err := mgr.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if info == nil || info.Name != mgr.SessionName() {
		t.Fatalf("Status() = %#v, want session info for %s", info, mgr.SessionName())
	}
}

func TestManager_StopUsesManagedRefineryBinding(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	managed := &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: managed}
	mgr.adapter = adapter

	if err := mgr.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !managed.closed {
		t.Fatal("managed session was not closed")
	}
	stopping, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil {
		t.Fatalf("Load() during stop error = %v", err)
	}
	if stopping != nil {
		t.Fatalf("binding still exists after stop: %#v", stopping)
	}
}

func TestManager_IsHealthyUsesManagedRefineryBinding(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	binding := runtime.SessionBinding{
		IssueID:          mgr.SessionName(),
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      mgr.SessionName(),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "refinery", "rig"),
	}
	store := runtime.NewFileSessionBindingStore(filepath.Dir(rigPath))
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}}
	mgr.adapter = adapter

	if got := mgr.IsHealthy(time.Minute); got != tmux.SessionHealthy {
		t.Fatalf("IsHealthy() = %v, want %v", got, tmux.SessionHealthy)
	}
}

func TestManager_Queue_NoBeads(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Queue returns error when no beads database exists
	// This is expected - beads requires initialization
	_, err := mgr.Queue()
	if err == nil {
		// If beads is somehow available, queue should be empty
		t.Log("Queue() succeeded unexpectedly (beads may be available)")
		return
	}
	// Error is expected when beads isn't initialized
	t.Logf("Queue() returned error (expected without beads): %v", err)
}

func TestManager_Queue_FiltersClosedMergeRequests(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	testutil.RequireDoltContainer(t)
	port, _ := strconv.Atoi(testutil.DoltContainerPort())
	b := beads.NewIsolatedWithPort(rigPath, port)
	if err := b.Init("gt"); err != nil {
		t.Skipf("bd init unavailable in test environment: %v", err)
	}

	openIssue, err := b.Create(beads.CreateOptions{
		Title:  "Open MR",
		Labels: []string{"gt:merge-request"},
	})
	if err != nil {
		t.Fatalf("create open merge-request issue: %v", err)
	}
	closedIssue, err := b.Create(beads.CreateOptions{
		Title:  "Closed MR",
		Labels: []string{"gt:merge-request"},
	})
	if err != nil {
		t.Fatalf("create closed merge-request issue: %v", err)
	}
	closedStatus := "closed"
	if err := b.Update(closedIssue.ID, beads.UpdateOptions{Status: &closedStatus}); err != nil {
		t.Fatalf("close merge-request issue: %v", err)
	}

	queue, err := mgr.Queue()
	if err != nil {
		t.Fatalf("Queue() error: %v", err)
	}

	var sawOpen bool
	for _, item := range queue {
		if item.MR == nil {
			continue
		}
		if item.MR.ID == closedIssue.ID {
			t.Fatalf("queue contains closed merge-request %s", closedIssue.ID)
		}
		if item.MR.ID == openIssue.ID {
			sawOpen = true
		}
	}
	if !sawOpen {
		t.Fatalf("queue missing expected open merge-request %s", openIssue.ID)
	}
}

func TestManager_FindMR_NoBeads(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// FindMR returns error when no beads database exists
	_, err := mgr.FindMR("nonexistent-mr")
	if err == nil {
		t.Error("FindMR() expected error")
	}
	// Any error is acceptable when beads isn't initialized
	t.Logf("FindMR() returned error (expected): %v", err)
}

func TestManager_RegisterMR_Deprecated(t *testing.T) {
	mgr, _ := setupTestManager(t)

	mr := &MergeRequest{
		ID:     "gt-mr-test",
		Branch: "polecat/Test/gt-123",
		Worker: "Test",
		Status: MROpen,
	}

	// RegisterMR should return an error indicating deprecation
	err := mgr.RegisterMR(mr)
	if err == nil {
		t.Error("RegisterMR() expected error (deprecated)")
	}
}

func TestManager_Retry_Deprecated(t *testing.T) {
	mgr, _ := setupTestManager(t)

	// Retry is deprecated and should not error, just print a message
	err := mgr.Retry("any-id", false)
	if err != nil {
		t.Errorf("Retry() unexpected error: %v", err)
	}
}

func TestCompareScoredIssues_UsesDeterministicIDTieBreaker(t *testing.T) {
	t.Helper()

	first := scoredIssue{
		issue: &beads.Issue{
			ID:        "gt-1",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		score: 10,
	}
	second := scoredIssue{
		issue: &beads.Issue{
			ID:        "gt-2",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		score: 10,
	}

	if !compareScoredIssues(first, second) {
		t.Fatalf("expected gt-1 to sort before gt-2 for equal scores")
	}
	if compareScoredIssues(second, first) {
		t.Fatalf("expected gt-2 to sort after gt-1 for equal scores")
	}
}

func TestManager_PostMerge_ClosesMRAndSourceIssue(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	testutil.RequireDoltContainer(t)
	port, _ := strconv.Atoi(testutil.DoltContainerPort())
	b := beads.NewIsolatedWithPort(rigPath, port)
	if err := b.Init("gt"); err != nil {
		t.Skipf("bd init unavailable: %v", err)
	}

	// Create a source issue
	srcIssue, err := b.Create(beads.CreateOptions{
		Title:  "Implement feature X",
		Labels: []string{"gt:task"},
	})
	if err != nil {
		t.Fatalf("create source issue: %v", err)
	}

	// Create an MR bead with branch and source_issue fields
	mrDesc := "branch: polecat/test/gt-xyz\nsource_issue: " + srcIssue.ID + "\nworker: test\ntarget: main"
	mrIssue, err := b.Create(beads.CreateOptions{
		Title:       "MR for feature X",
		Labels:      []string{"gt:merge-request"},
		Description: mrDesc,
	})
	if err != nil {
		t.Fatalf("create MR issue: %v", err)
	}

	// Run PostMerge
	result, err := mgr.PostMerge(mrIssue.ID)
	if err != nil {
		t.Fatalf("PostMerge() error: %v", err)
	}

	// Verify result
	if !result.MRClosed {
		t.Error("PostMerge() MRClosed = false, want true")
	}
	if !result.SourceIssueClosed {
		t.Error("PostMerge() SourceIssueClosed = false, want true")
	}
	if result.SourceIssueID != srcIssue.ID {
		t.Errorf("PostMerge() SourceIssueID = %s, want %s", result.SourceIssueID, srcIssue.ID)
	}
	if result.MR.Branch != "polecat/test/gt-xyz" {
		t.Errorf("PostMerge() MR.Branch = %s, want polecat/test/gt-xyz", result.MR.Branch)
	}
}

func TestManager_PostMerge_AlreadyClosedMR(t *testing.T) {
	mgr, rigPath := setupTestManager(t)
	testutil.RequireDoltContainer(t)
	port, _ := strconv.Atoi(testutil.DoltContainerPort())
	b := beads.NewIsolatedWithPort(rigPath, port)
	if err := b.Init("gt"); err != nil {
		t.Skipf("bd init unavailable: %v", err)
	}

	// Create and close an MR bead
	mrIssue, err := b.Create(beads.CreateOptions{
		Title:       "Already merged MR",
		Labels:      []string{"gt:merge-request"},
		Description: "branch: polecat/old/gt-old\ntarget: main",
	})
	if err != nil {
		t.Fatalf("create MR issue: %v", err)
	}
	if err := b.Close(mrIssue.ID); err != nil {
		t.Fatalf("close MR issue: %v", err)
	}

	// PostMerge should fail since MR is already closed and won't be in queue
	_, err = mgr.PostMerge(mrIssue.ID)
	if err == nil {
		t.Error("PostMerge() expected error for already-closed MR")
	}
}

func TestManager_PostMerge_NotFound(t *testing.T) {
	mgr, _ := setupTestManager(t)

	_, err := mgr.PostMerge("nonexistent-mr-id")
	if err == nil {
		t.Error("PostMerge() expected error for nonexistent MR")
	}
}
