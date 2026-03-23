package mayor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/workspace"
)

type fakeRuntimeAdapter struct {
	lookupReq *runtime.SessionLookupRequest
	session   runtime.ManagedSession
	err       error
}

func (f *fakeRuntimeAdapter) Start(context.Context, runtime.SessionLaunchRequest) (runtime.ManagedSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

func (f *fakeRuntimeAdapter) Resume(context.Context, runtime.SessionResumeRequest) (runtime.ManagedSession, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

func (f *fakeRuntimeAdapter) Lookup(_ context.Context, req runtime.SessionLookupRequest) (runtime.ManagedSession, error) {
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

func TestNewManager(t *testing.T) {
	m := NewManager("/tmp/test-town")
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.townRoot != "/tmp/test-town" {
		t.Errorf("townRoot = %q, want %q", m.townRoot, "/tmp/test-town")
	}
}

func TestManager_mayorDir(t *testing.T) {
	m := NewManager("/tmp/test-town")
	got := m.mayorDir()
	want := filepath.Join("/tmp/test-town", "mayor")
	if got != want {
		t.Errorf("mayorDir() = %q, want %q", got, want)
	}
}

func TestSessionName_ReturnsConsistentValue(t *testing.T) {
	name := SessionName()
	if name == "" {
		t.Error("SessionName() returned empty string")
	}
	// Verify idempotent
	if SessionName() != name {
		t.Error("SessionName() returned different values on subsequent calls")
	}
}

func TestManager_SessionName_MatchesPackageFunc(t *testing.T) {
	m := NewManager("/tmp/test-town")
	if m.SessionName() != SessionName() {
		t.Errorf("Manager.SessionName() = %q, SessionName() = %q — should match",
			m.SessionName(), SessionName())
	}
}

func TestManager_Errors(t *testing.T) {
	if ErrNotRunning.Error() != "mayor not running" {
		t.Errorf("ErrNotRunning = %q", ErrNotRunning)
	}
	if ErrAlreadyRunning.Error() != "mayor already running" {
		t.Errorf("ErrAlreadyRunning = %q", ErrAlreadyRunning)
	}
}

func TestGetMayorPrime(t *testing.T) {
	// Create a temporary directory with town.json
	tmpDir, err := os.MkdirTemp("", "mayor-prime-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create mayor directory
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatalf("failed to create mayor dir: %v", err)
	}

	// Create a minimal town.json
	townConfig := &config.TownConfig{
		Name: "test-town",
	}
	townConfigPath := filepath.Join(tmpDir, workspace.PrimaryMarker)
	if err := config.SaveTownConfig(townConfigPath, townConfig); err != nil {
		t.Fatalf("failed to save town config: %v", err)
	}

	// Test GetMayorPrime
	content, err := GetMayorPrime(tmpDir)
	if err != nil {
		t.Fatalf("GetMayorPrime failed: %v", err)
	}

	// Verify content has expected elements
	if !strings.Contains(content, "[prime-rendered-at:") {
		t.Error("GetMayorPrime should contain timestamp marker")
	}
	if !strings.Contains(content, "# Mayor Context") {
		t.Error("GetMayorPrime should render mayor template")
	}
	if !strings.Contains(content, tmpDir) {
		t.Error("GetMayorPrime should contain town root path")
	}
}

func TestGetMayorPrime_InvalidTownRoot(t *testing.T) {
	// Test with non-existent directory - should still return content
	// (town name defaults to "unknown" on error)
	content, err := GetMayorPrime("/nonexistent/path")
	if err != nil {
		t.Fatalf("GetMayorPrime should not fail with invalid town root: %v", err)
	}

	// Should still have the template content
	if !strings.Contains(content, "# Mayor Context") {
		t.Error("GetMayorPrime should render mayor template even with invalid town root")
	}
}

func TestMayorLifecycleStateReportsStartingFromFreshBinding(t *testing.T) {
	townRoot := t.TempDir()
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "mayor",
		AgentName:        "mayor",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStarting,
		UpdatedAt:        time.Now().UTC(),
		WorkDir:          filepath.Join(townRoot, "mayor"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, adapter: &fakeRuntimeAdapter{err: context.DeadlineExceeded}}

	state, err := m.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleStarting {
		t.Fatalf("LifecycleState() = %q, want starting", state)
	}
}

func TestMayorCombinedStatusUsesManagedBindingAsTmuxMode(t *testing.T) {
	townRoot := t.TempDir()
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "mayor",
		AgentName:        "mayor",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleRunning,
		UpdatedAt:        time.Now().UTC(),
		WorkDir:          filepath.Join(townRoot, "mayor"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, adapter: &fakeRuntimeAdapter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}}}

	status, err := m.CombinedStatus()
	if err != nil {
		t.Fatalf("CombinedStatus() error = %v", err)
	}
	if !status.Active || status.Mode != ModeTMUX {
		t.Fatalf("status = %#v", status)
	}
	if status.State != runtime.SessionLifecycleRunning {
		t.Fatalf("status.State = %q, want running", status.State)
	}
	if status.Tmux == nil || status.Tmux.Name != SessionName() {
		t.Fatalf("status.Tmux = %#v", status.Tmux)
	}
}

func TestMayorStopUsesManagedBinding(t *testing.T) {
	townRoot := t.TempDir()
	managed := &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "mayor",
		AgentName:        "mayor",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleRunning,
		UpdatedAt:        time.Now().UTC(),
		WorkDir:          filepath.Join(townRoot, "mayor"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, adapter: &fakeRuntimeAdapter{session: managed}}

	if err := m.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !managed.closed {
		t.Fatal("managed session was not closed")
	}
	got, err := store.Load(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
	if err != nil {
		t.Fatalf("Load() after stop error = %v", err)
	}
	if got != nil {
		t.Fatalf("binding still exists after stop: %#v", got)
	}
}
