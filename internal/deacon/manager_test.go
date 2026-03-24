package deacon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/runtime"
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

// mockTmux implements tmuxOps for testing.
type mockTmux struct {
	hasSessionResult bool
	hasSessionErr    error
	agentAlive       bool
	killErr          error
	newSessionErr    error
	waitErr          error
	sessionInfo      *tmux.SessionInfo
	sessionInfoErr   error
	sendKeysErr      error

	// Call tracking
	killCalls       []string
	newSessionCalls int
}

func (m *mockTmux) HasSession(name string) (bool, error) {
	return m.hasSessionResult, m.hasSessionErr
}

func (m *mockTmux) IsAgentAlive(_ string) bool {
	return m.agentAlive
}

func (m *mockTmux) KillSessionWithProcesses(name string) error {
	m.killCalls = append(m.killCalls, name)
	return m.killErr
}

func (m *mockTmux) NewSessionWithCommand(_, _, _ string) error {
	m.newSessionCalls++
	return m.newSessionErr
}

func (m *mockTmux) SetRemainOnExit(_ string, _ bool) error { return nil }
func (m *mockTmux) SetEnvironment(_, _, _ string) error    { return nil }
func (m *mockTmux) GetPaneID(_ string) (string, error)     { return "%0", nil }
func (m *mockTmux) ConfigureGasTownSession(_ string, _ *tmux.Theme, _, _, _ string) error {
	return nil
}

func (m *mockTmux) WaitForCommand(_ string, _ []string, _ time.Duration) error {
	return m.waitErr
}

func (m *mockTmux) SetAutoRespawnHook(_ string) error             { return nil }
func (m *mockTmux) AcceptStartupDialogs(_ string) error           { return nil }
func (m *mockTmux) AcceptWorkspaceTrustDialog(_ string) error     { return nil }
func (m *mockTmux) AcceptBypassPermissionsWarning(_ string) error { return nil }
func (m *mockTmux) SendKeysRaw(_, _ string) error                 { return m.sendKeysErr }
func (m *mockTmux) GetSessionInfo(_ string) (*tmux.SessionInfo, error) {
	return m.sessionInfo, m.sessionInfoErr
}

func newTestManager(townRoot string, mock *mockTmux) *Manager {
	return &Manager{
		townRoot: townRoot,
		tmux:     mock,
	}
}

func writeTownMarker(t *testing.T, townRoot string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(townRoot, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"type":"town","version":2,"name":"slotmachine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewManager(t *testing.T) {
	m := NewManager("/tmp/test-town")
	if m.townRoot != "/tmp/test-town" {
		t.Errorf("townRoot = %q, want %q", m.townRoot, "/tmp/test-town")
	}
	if m.tmux == nil {
		t.Error("tmux should not be nil")
	}
}

func TestManager_SessionName(t *testing.T) {
	m := NewManager("/tmp/test-town")
	name := m.SessionName()
	if name == "" {
		t.Error("SessionName() should not be empty")
	}
	// Should match package-level SessionName
	if name != SessionName() {
		t.Errorf("method SessionName() = %q, package SessionName() = %q", name, SessionName())
	}
}

func TestManager_deaconDir(t *testing.T) {
	m := NewManager("/tmp/test-town")
	expected := filepath.Join("/tmp/test-town", "deacon")
	if m.deaconDir() != expected {
		t.Errorf("deaconDir() = %q, want %q", m.deaconDir(), expected)
	}
}

func TestStart_AlreadyRunning(t *testing.T) {
	mock := &mockTmux{
		hasSessionResult: true,
		agentAlive:       true,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Start("")
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("Start() error = %v, want ErrAlreadyRunning", err)
	}
}

func TestStart_ZombieDetected_KillFails(t *testing.T) {
	killErr := errors.New("kill failed: session locked")
	mock := &mockTmux{
		hasSessionResult: true,
		agentAlive:       false, // zombie: tmux alive, agent dead
		killErr:          killErr,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Start("")
	if err == nil {
		t.Fatal("Start() should return error when zombie kill fails")
	}
	if !errors.Is(err, killErr) {
		t.Errorf("Start() error = %v, should wrap %v", err, killErr)
	}
	if len(mock.killCalls) != 1 {
		t.Errorf("expected 1 kill call, got %d", len(mock.killCalls))
	}
	if len(mock.killCalls) > 0 && mock.killCalls[0] != m.SessionName() {
		t.Errorf("killed session %q, want %q", mock.killCalls[0], m.SessionName())
	}
}

func TestStart_ZombieDetected_KillSucceeds(t *testing.T) {
	// Zombie kill succeeds, Start continues into config/runtime.
	// We verify the zombie was detected and killed.
	mock := &mockTmux{
		hasSessionResult: true,
		agentAlive:       false, // zombie
		killErr:          nil,   // kill succeeds
	}
	m := newTestManager(t.TempDir(), mock)

	// Start will proceed past zombie kill into config resolution.
	// It may fail on config.BuildAgentStartupCommandWithAgentOverride
	// in the test environment - that's fine, we're verifying zombie handling.
	_ = m.Start("")

	if len(mock.killCalls) != 1 {
		t.Errorf("expected 1 zombie kill call, got %d", len(mock.killCalls))
	}
}

func TestStart_NoExistingSession(t *testing.T) {
	// No existing session - Start proceeds to create one.
	// Will hit config/runtime calls which may error in test env.
	mock := &mockTmux{
		hasSessionResult: false,
	}
	m := newTestManager(t.TempDir(), mock)

	_ = m.Start("")

	// Should NOT have tried to kill anything
	if len(mock.killCalls) != 0 {
		t.Errorf("expected 0 kill calls, got %d", len(mock.killCalls))
	}
}

func TestStart_HasSessionError(t *testing.T) {
	// HasSession error is ignored (line 59: running, _ := ...).
	// When HasSession errors, running=false, so Start proceeds normally.
	mock := &mockTmux{
		hasSessionResult: false,
		hasSessionErr:    errors.New("tmux not available"),
	}
	m := newTestManager(t.TempDir(), mock)

	_ = m.Start("")

	// Should NOT have tried to kill anything
	if len(mock.killCalls) != 0 {
		t.Errorf("expected 0 kill calls when HasSession errors, got %d", len(mock.killCalls))
	}
}

func TestStart_SessionCreateFails(t *testing.T) {
	// Test that NewSessionWithCommand failure is propagated.
	mock := &mockTmux{
		hasSessionResult: false,
		newSessionErr:    errors.New("tmux server not running"),
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Start("claude")
	if err == nil {
		// If we got past config without error, session creation should have failed.
		// But config may have failed first - check if NewSessionWithCommand was called.
		if mock.newSessionCalls > 0 {
			t.Fatal("Start() should return error when session creation fails")
		}
		// Config failed before reaching session creation - acceptable in test env.
		return
	}

	// If NewSessionWithCommand was called and failed, error should wrap it.
	if mock.newSessionCalls > 0 {
		if got := err.Error(); got == "" {
			t.Error("error should have content")
		}
	}
}

func TestStart_WaitForCommandFails(t *testing.T) {
	// WaitForCommand failure should kill the session and return error.
	waitErr := errors.New("timeout waiting for agent")
	mock := &mockTmux{
		hasSessionResult: false,
		newSessionErr:    nil,
		waitErr:          waitErr,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Start("claude")

	// If we got past config to WaitForCommand, verify cleanup behavior.
	if mock.newSessionCalls > 0 {
		// Session was created, WaitForCommand was called.
		if err == nil {
			t.Fatal("Start() should return error when WaitForCommand fails")
		}
		if !errors.Is(err, waitErr) {
			t.Errorf("Start() error = %v, should wrap %v", err, waitErr)
		}
		// Should have killed the zombie session as cleanup (line 122).
		if len(mock.killCalls) == 0 {
			t.Error("expected cleanup kill call after WaitForCommand failure")
		}
	}
	// If config failed before reaching NewSessionWithCommand, that's
	// acceptable - the WaitForCommand path isn't reachable in test env.
}

func TestStop_NotRunning(t *testing.T) {
	mock := &mockTmux{
		hasSessionResult: false,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("Stop() error = %v, want ErrNotRunning", err)
	}
}

func TestStop_HasSessionError(t *testing.T) {
	sessionErr := errors.New("tmux server crashed")
	mock := &mockTmux{
		hasSessionErr: sessionErr,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if err == nil {
		t.Fatal("Stop() should return error when HasSession fails")
	}
	if !errors.Is(err, sessionErr) {
		t.Errorf("Stop() error = %v, should wrap %v", err, sessionErr)
	}
}

func TestStop_Success(t *testing.T) {
	mock := &mockTmux{
		hasSessionResult: true,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if err != nil {
		t.Errorf("Stop() error = %v, want nil", err)
	}
	if len(mock.killCalls) != 1 {
		t.Errorf("expected 1 kill call, got %d", len(mock.killCalls))
	}
}

func TestStart_UsesAdapterForNonClaudeRoleConfig(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	settings := config.NewTownSettings()
	settings.Agents = map[string]*config.RuntimeConfig{
		"copilot-external": {
			Provider: "copilot",
			Command:  "copilot",
			CLIURL:   "http://127.0.0.1:4321",
		},
	}
	settings.RoleAgents = map[string]string{"deacon": "copilot-external"}
	if err := config.SaveTownSettings(filepath.Join(townRoot, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveTownSettings() error = %v", err)
	}
	mock := &mockTmux{}
	adapter := &fakeRuntimeStarter{}
	m := &Manager{townRoot: townRoot, tmux: mock, adapter: adapter}

	if err := m.Start(""); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("adapter requests = %#v, want 1", adapter.requests)
	}
	if mock.newSessionCalls != 0 {
		t.Fatalf("tmux new session calls = %d, want 0", mock.newSessionCalls)
	}
	request := adapter.requests[0]
	if request.Role != "deacon" || request.SessionName != SessionName() {
		t.Fatalf("request = %#v", request)
	}
	if request.IssueID != SessionName() {
		t.Fatalf("IssueID = %q, want %q", request.IssueID, SessionName())
	}
	if request.WorkDir != filepath.Join(townRoot, "deacon") {
		t.Fatalf("WorkDir = %q", request.WorkDir)
	}
	if !strings.Contains(request.Prompt, "I am Deacon") {
		t.Fatalf("Prompt = %q", request.Prompt)
	}
}

func TestDeaconStartsExternalCopilotSession(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	settings := config.NewTownSettings()
	settings.Agents = map[string]*config.RuntimeConfig{
		"copilot-external": {
			Provider: "copilot",
			Command:  "copilot",
			CLIURL:   "http://127.0.0.1:4321",
		},
	}
	settings.RoleAgents = map[string]string{"deacon": "copilot-external"}
	if err := config.SaveTownSettings(filepath.Join(townRoot, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveTownSettings() error = %v", err)
	}
	adapter := &fakeRuntimeStarter{}
	m := &Manager{townRoot: townRoot, tmux: &mockTmux{}, adapter: adapter}

	if err := m.Start(""); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("adapter requests = %#v, want 1", adapter.requests)
	}
	request := adapter.requests[0]
	if request.Provider != "" {
		t.Fatalf("Provider override = %q, want empty role-config resolution", request.Provider)
	}
	if request.Role != "deacon" || request.SessionName != SessionName() {
		t.Fatalf("request = %#v", request)
	}
	if request.SessionKind != config.ToolSessionKindPatrol {
		t.Fatalf("SessionKind = %q, want %q", request.SessionKind, config.ToolSessionKindPatrol)
	}
	if request.ToolPolicy == nil || len(request.ToolPolicy.AvailableTools) == 0 {
		t.Fatalf("ToolPolicy = %#v, want resolved policy", request.ToolPolicy)
	}
	if request.WorkDir != filepath.Join(townRoot, "deacon") {
		t.Fatalf("WorkDir = %q, want %q", request.WorkDir, filepath.Join(townRoot, "deacon"))
	}
	if request.AgentName != "deacon" || request.IssueID != SessionName() {
		t.Fatalf("request = %#v", request)
	}
	if !strings.Contains(request.Prompt, "gt deacon heartbeat") {
		t.Fatalf("Prompt = %q, want startup patrol instructions", request.Prompt)
	}
}

func TestDeaconResumesStoredExternalBinding(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "deacon",
		AgentName:        "deacon",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(townRoot, "deacon"),
		Metadata:         map[string]string{"session_kind": "patrol"},
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}}
	m := &Manager{townRoot: townRoot, tmux: &mockTmux{}, adapter: adapter}

	running, err := m.IsRunning()
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

func TestDeaconInvalidRuntimeSessionIDFailsCleanly(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "deacon",
		AgentName:        "deacon",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-bad",
		WorkDir:          filepath.Join(townRoot, "deacon"),
		Metadata:         map[string]string{"session_kind": "patrol"},
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, tmux: &mockTmux{}, adapter: &fakeRuntimeStarter{err: fmt.Errorf("invalid runtime session id")}}

	info, err := m.Status()
	if err == nil {
		t.Fatal("Status() error = nil, want ErrNotRunning")
	}
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("Status() error = %v, want ErrNotRunning", err)
	}
	if info != nil {
		t.Fatalf("Status() = %#v, want nil", info)
	}
}

func TestLifecycleStateReportsStoppingFromBinding(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "deacon",
		AgentName:        "deacon",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStopping,
		WorkDir:          filepath.Join(townRoot, "deacon"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, tmux: &mockTmux{}}

	state, err := m.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleStopping {
		t.Fatalf("LifecycleState() = %q, want stopping", state)
	}
	running, err := m.IsRunning()
	if err != nil {
		t.Fatalf("IsRunning() error = %v", err)
	}
	if running {
		t.Fatal("IsRunning() = true, want false while stopping")
	}
}

func TestLifecycleStateReportsStartingForFreshBindingWithoutLookup(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "deacon",
		AgentName:        "deacon",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStarting,
		UpdatedAt:        time.Now().UTC(),
		WorkDir:          filepath.Join(townRoot, "deacon"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, tmux: &mockTmux{}, adapter: &fakeRuntimeStarter{err: fmt.Errorf("lookup unavailable")}}

	state, err := m.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleStarting {
		t.Fatalf("LifecycleState() = %q, want starting", state)
	}
}

func TestLifecycleStateReportsUnknownForStaleBindingWithoutLookup(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "deacon",
		AgentName:        "deacon",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStarting,
		UpdatedAt:        time.Now().UTC().Add(-2 * runtime.SessionLifecycleStartingGrace),
		WorkDir:          filepath.Join(townRoot, "deacon"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, tmux: &mockTmux{}, adapter: &fakeRuntimeStarter{err: fmt.Errorf("lookup unavailable")}}

	state, err := m.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleUnknown {
		t.Fatalf("LifecycleState() = %q, want unknown", state)
	}
}

func TestStop_UsesManagedDeaconBinding(t *testing.T) {
	townRoot := t.TempDir()
	writeTownMarker(t, townRoot)
	managed := &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}
	binding := runtime.SessionBinding{
		IssueID:          SessionName(),
		Role:             "deacon",
		AgentName:        "deacon",
		Provider:         "copilot-external",
		SessionName:      SessionName(),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(townRoot, "deacon"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{townRoot: townRoot, tmux: &mockTmux{}, adapter: &fakeRuntimeStarter{session: managed}}

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
		data, _ := json.Marshal(got)
		t.Fatalf("binding still exists after stop: %s", data)
	}
}

func TestStop_KillFails(t *testing.T) {
	killErr := errors.New("permission denied")
	mock := &mockTmux{
		hasSessionResult: true,
		killErr:          killErr,
	}
	m := newTestManager(t.TempDir(), mock)

	err := m.Stop()
	if err == nil {
		t.Fatal("Stop() should return error when kill fails")
	}
	if !errors.Is(err, killErr) {
		t.Errorf("Stop() error = %v, should wrap %v", err, killErr)
	}
}

func TestIsRunning(t *testing.T) {
	tests := []struct {
		name    string
		running bool
		err     error
		wantRun bool
		wantErr bool
	}{
		{
			name:    "running",
			running: true,
			wantRun: true,
		},
		{
			name:    "not running",
			running: false,
			wantRun: false,
		},
		{
			name:    "error",
			err:     errors.New("tmux error"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockTmux{
				hasSessionResult: tc.running,
				hasSessionErr:    tc.err,
			}
			m := newTestManager(t.TempDir(), mock)

			running, err := m.IsRunning()
			if (err != nil) != tc.wantErr {
				t.Errorf("IsRunning() error = %v, wantErr = %v", err, tc.wantErr)
			}
			if running != tc.wantRun {
				t.Errorf("IsRunning() = %v, want %v", running, tc.wantRun)
			}
		})
	}
}

func TestStatus_NotRunning(t *testing.T) {
	mock := &mockTmux{
		hasSessionResult: false,
	}
	m := newTestManager(t.TempDir(), mock)

	info, err := m.Status()
	if !errors.Is(err, ErrNotRunning) {
		t.Errorf("Status() error = %v, want ErrNotRunning", err)
	}
	if info != nil {
		t.Error("Status() should return nil info when not running")
	}
}

func TestStatus_HasSessionError(t *testing.T) {
	sessionErr := errors.New("tmux gone")
	mock := &mockTmux{
		hasSessionErr: sessionErr,
	}
	m := newTestManager(t.TempDir(), mock)

	info, err := m.Status()
	if err == nil {
		t.Fatal("Status() should return error when HasSession fails")
	}
	if !errors.Is(err, sessionErr) {
		t.Errorf("Status() error = %v, should wrap %v", err, sessionErr)
	}
	if info != nil {
		t.Error("Status() should return nil info on error")
	}
}

func TestStatus_Running(t *testing.T) {
	expected := &tmux.SessionInfo{
		Name:    "hq-deacon",
		Windows: 1,
	}
	mock := &mockTmux{
		hasSessionResult: true,
		sessionInfo:      expected,
	}
	m := newTestManager(t.TempDir(), mock)

	info, err := m.Status()
	if err != nil {
		t.Errorf("Status() error = %v", err)
	}
	if info != expected {
		t.Errorf("Status() = %v, want %v", info, expected)
	}
}

func TestStatus_GetSessionInfoError(t *testing.T) {
	infoErr := errors.New("session info unavailable")
	mock := &mockTmux{
		hasSessionResult: true,
		sessionInfoErr:   infoErr,
	}
	m := newTestManager(t.TempDir(), mock)

	info, err := m.Status()
	if err == nil {
		t.Fatal("Status() should return error when GetSessionInfo fails")
	}
	if !errors.Is(err, infoErr) {
		t.Errorf("Status() error = %v, should wrap %v", err, infoErr)
	}
	if info != nil {
		t.Error("Status() should return nil info on error")
	}
}
