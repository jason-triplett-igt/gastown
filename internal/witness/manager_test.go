package witness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
)

type fakeRuntimeStarter struct {
	requests  []runtime.SessionLaunchRequest
	req       runtime.SessionLaunchRequest
	lookupReq *runtime.SessionLookupRequest
	session   runtime.ManagedSession
	err       error
}

func (f *fakeRuntimeStarter) Start(_ context.Context, req runtime.SessionLaunchRequest) (runtime.ManagedSession, error) {
	f.req = req
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

func TestBuildWitnessStartCommand_UsesRoleConfig(t *testing.T) {
	t.Parallel()
	roleCfg := &beads.RoleConfig{
		StartCommand: "exec run --town {town} --rig {rig} --role {role}",
	}

	got, err := buildWitnessStartCommand("/town/rig", "gastown", "/town", "", "", roleCfg)
	if err != nil {
		t.Fatalf("buildWitnessStartCommand: %v", err)
	}

	want := "exec env -u CLAUDECODE NODE_OPTIONS='' run --town /town --rig gastown --role witness"
	if got != want {
		t.Errorf("buildWitnessStartCommand = %q, want %q", got, want)
	}
}

func TestBuildWitnessStartCommand_DefaultsToRuntime(t *testing.T) {
	t.Parallel()
	got, err := buildWitnessStartCommand("/town/rig", "gastown", "/town", "", "", nil)
	if err != nil {
		t.Fatalf("buildWitnessStartCommand: %v", err)
	}

	if !strings.Contains(got, "GT_ROLE=gastown/witness") {
		t.Errorf("expected GT_ROLE=gastown/witness in command, got %q", got)
	}
	if !strings.Contains(got, "BD_ACTOR=gastown/witness") {
		t.Errorf("expected BD_ACTOR=gastown/witness in command, got %q", got)
	}
}

// TestRoleConfigEnvVars_ExpandsQualifiedGTRole verifies that the TOML env vars
// expand GT_ROLE to a qualified value (e.g., "gastown/witness" not "witness").
func TestRoleConfigEnvVars_ExpandsQualifiedGTRole(t *testing.T) {
	t.Parallel()
	roleCfg := &beads.RoleConfig{
		EnvVars: map[string]string{
			"GT_ROLE":  "{rig}/witness",
			"GT_SCOPE": "rig",
		},
	}

	got := roleConfigEnvVars(roleCfg, "/town", "gastown")
	if got["GT_ROLE"] != "gastown/witness" {
		t.Errorf("GT_ROLE = %q, want %q", got["GT_ROLE"], "gastown/witness")
	}
	if got["GT_SCOPE"] != "rig" {
		t.Errorf("GT_SCOPE = %q, want %q", got["GT_SCOPE"], "rig")
	}
}

// TestRoleConfigEnvVars_NilConfig verifies nil roleConfig returns nil.
func TestRoleConfigEnvVars_NilConfig(t *testing.T) {
	t.Parallel()
	got := roleConfigEnvVars(nil, "/town", "gastown")
	if got != nil {
		t.Errorf("expected nil for nil roleConfig, got %v", got)
	}
}

func TestBuildWitnessStartCommand_AgentOverrideWins(t *testing.T) {
	t.Parallel()
	roleCfg := &beads.RoleConfig{
		StartCommand: "exec run --role {role}",
	}

	got, err := buildWitnessStartCommand("/town/rig", "gastown", "/town", "", "codex", roleCfg)
	if err != nil {
		t.Fatalf("buildWitnessStartCommand: %v", err)
	}
	if strings.Contains(got, "exec run") {
		t.Fatalf("expected agent override to bypass role start_command, got %q", got)
	}
	if !strings.Contains(got, "GT_ROLE=gastown/witness") {
		t.Errorf("expected GT_ROLE=gastown/witness in command, got %q", got)
	}
}

func TestSessionAdapterUsesInjectedAdapter(t *testing.T) {
	t.Parallel()
	adapter := &fakeRuntimeStarter{}
	m := &Manager{
		rig:     &rig.Rig{Name: "gastown", Path: "/tmp/gastown"},
		adapter: adapter,
	}
	if got := m.sessionAdapter(); got != adapter {
		t.Fatalf("sessionAdapter() = %#v, want injected adapter", got)
	}
}

func TestBuildReviewSessionScopeRequiresIssueID(t *testing.T) {
	t.Parallel()
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: t.TempDir()}}
	_, err := m.BuildReviewSessionScope("", "")
	if err == nil {
		t.Fatal("BuildReviewSessionScope() error = nil, want missing issue")
	}
}

func TestBuildReviewSessionScopeReturnsApprovedArtifactsAndReadOnlyTools(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(filepath.Join(rigPath, "witness"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := beads.NewIsolated(rigPath)
	if err := b.Init("review-scope"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	desc := beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{
		Phase:          beads.VSDDPhaseReview,
		SpecApproved:   true,
		TestsRed:       true,
		Implementation: true,
		ReviewVerdict:  "READY",
		ReviewApproved: true,
		LastTransition: "entered:review",
	}, &beads.VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "spec-review-1",
		TestPlanArtifactID:       "test-plan-1",
		RedTestEvidenceID:        "red-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-1",
		ReviewArtifactID:         "review-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Review Scope Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	townRoot := rigPath
	if err := os.MkdirAll(filepath.Join(townRoot, ".runtime", "session-bindings"), 0o755); err != nil {
		t.Fatal(err)
	}
	binding := runtime.SessionBinding{IssueID: created.ID, Role: "polecat", SessionName: "gt-builder-123"}
	data, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, ".runtime", "session-bindings", "binding.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	scope, err := m.BuildReviewSessionScope(created.ID, filepath.Join(rigPath, "witness"))
	if err != nil {
		t.Fatalf("BuildReviewSessionScope() error = %v", err)
	}
	if !scope.FreshContext || !scope.ReadOnly {
		t.Fatalf("scope = %#v", scope)
	}
	if scope.BuilderSession != "gt-builder-123" {
		t.Fatalf("BuilderSession = %q, want gt-builder-123", scope.BuilderSession)
	}
	if !contains(scope.AllowedTools, "load_review") || contains(scope.AllowedTools, "send_mail") {
		t.Fatalf("AllowedTools = %#v", scope.AllowedTools)
	}
	if !contains(scope.ApprovedArtifacts, "spec-1") || !contains(scope.ApprovedArtifacts, "builder-1") {
		t.Fatalf("ApprovedArtifacts = %#v", scope.ApprovedArtifacts)
	}
	if contains(scope.ApprovedArtifacts, "") {
		t.Fatalf("ApprovedArtifacts contains empty values: %#v", scope.ApprovedArtifacts)
	}
	if got := config.RoleAllowedTools(root, rigPath, "witness"); !contains(got, "run_verification") {
		t.Fatalf("RoleAllowedTools() = %#v", got)
	}
}

func TestLaunchReviewSessionStartsIndependentWitnessSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(filepath.Join(rigPath, "witness"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := beads.NewIsolated(rigPath)
	if err := b.Init("launch-review"); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	desc := beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{
		Phase:          beads.VSDDPhaseReview,
		SpecApproved:   true,
		TestsRed:       true,
		Implementation: true,
		LastTransition: "entered:review",
	}, &beads.VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "spec-review-1",
		TestPlanArtifactID:       "test-plan-1",
		RedTestEvidenceID:        "red-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Launch Review Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	adapter := &fakeRuntimeStarter{}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}, adapter: adapter}
	request, err := m.LaunchReviewSession(created.ID, filepath.Join(rigPath, "witness"), "")
	if err != nil {
		t.Fatalf("LaunchReviewSession() error = %v", err)
	}
	if request == nil || len(adapter.requests) != 1 {
		t.Fatalf("request = %#v requests = %#v", request, adapter.requests)
	}
	if request.Role != "witness" || request.AgentName != "review" {
		t.Fatalf("request = %#v", request)
	}
	if request.SessionName == "gt-witness" || !strings.Contains(request.SessionName, "review") {
		t.Fatalf("SessionName = %q, want independent review session", request.SessionName)
	}
	if request.Metadata["fresh_context"] != "true" || request.Metadata["read_only"] != "true" {
		t.Fatalf("Metadata = %#v", request.Metadata)
	}
	if request.SessionKind != config.ToolSessionKindReview || request.ToolPolicy == nil {
		t.Fatalf("request = %#v", request)
	}
	if !contains(request.ToolPolicy.AvailableTools, "load_review") || contains(request.ToolPolicy.AvailableTools, "send_mail") {
		t.Fatalf("ToolPolicy = %#v", request.ToolPolicy)
	}
	if request.Metadata["artifact_scope"] == "" || request.Metadata["allowed_tools"] == "" {
		t.Fatalf("Metadata = %#v", request.Metadata)
	}
	if scopeJSON := request.Env["GT_REVIEW_SCOPE"]; scopeJSON == "" {
		t.Fatal("GT_REVIEW_SCOPE not set")
	} else {
		var scope ReviewSessionScope
		if err := json.Unmarshal([]byte(scopeJSON), &scope); err != nil {
			t.Fatalf("unmarshal review scope: %v", err)
		}
		if !scope.FreshContext || !scope.ReadOnly || !contains(scope.AllowedTools, "load_review") {
			t.Fatalf("scope = %#v", scope)
		}
	}
	if !strings.Contains(request.Prompt, "fresh context") || !strings.Contains(request.Prompt, "approved artifacts") {
		t.Fatalf("Prompt = %q", request.Prompt)
	}
}

func TestIsRunningUsesManagedWitnessBinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(filepath.Join(root, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte(`{"type":"town","version":2,"name":"slotmachine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "copilot-external",
		SessionName:      sessionNameForTest("gastown"),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "witness"),
	}
	store := runtime.NewFileSessionBindingStore(root)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}, adapter: adapter}
	running, err := m.IsRunning()
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

func TestLifecycleStateReportsStoppingFromBinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(filepath.Join(root, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte(`{"type":"town","version":2,"name":"slotmachine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "copilot-external",
		SessionName:      sessionNameForTest("gastown"),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStopping,
		WorkDir:          filepath.Join(rigPath, "witness"),
	}
	store := runtime.NewFileSessionBindingStore(root)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}

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
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(filepath.Join(root, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte(`{"type":"town","version":2,"name":"slotmachine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "copilot-external",
		SessionName:      sessionNameForTest("gastown"),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStarting,
		UpdatedAt:        time.Now().UTC(),
		WorkDir:          filepath.Join(rigPath, "witness"),
	}
	store := runtime.NewFileSessionBindingStore(root)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}, adapter: &fakeRuntimeStarter{err: fmt.Errorf("lookup unavailable")}}

	state, err := m.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleStarting {
		t.Fatalf("LifecycleState() = %q, want starting", state)
	}
}

func TestLifecycleStateReportsUnknownForStaleBindingWithoutLookup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(filepath.Join(root, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mayor", "town.json"), []byte(`{"type":"town","version":2,"name":"slotmachine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "copilot-external",
		SessionName:      sessionNameForTest("gastown"),
		RuntimeSessionID: "runtime-xyz",
		LifecycleState:   runtime.SessionLifecycleStarting,
		UpdatedAt:        time.Now().UTC().Add(-2 * runtime.SessionLifecycleStartingGrace),
		WorkDir:          filepath.Join(rigPath, "witness"),
	}
	store := runtime.NewFileSessionBindingStore(root)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}, adapter: &fakeRuntimeStarter{err: fmt.Errorf("lookup unavailable")}}

	state, err := m.LifecycleState()
	if err != nil {
		t.Fatalf("LifecycleState() error = %v", err)
	}
	if state != runtime.SessionLifecycleUnknown {
		t.Fatalf("LifecycleState() = %q, want unknown", state)
	}
}

func TestStopUsesManagedWitnessBinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	townRoot := root
	if err := os.MkdirAll(filepath.Join(townRoot, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(townRoot, "mayor", "town.json"), []byte(`{"type":"town","version":2,"name":"slotmachine"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(townRoot, ".runtime", "session-bindings"), 0o755); err != nil {
		t.Fatal(err)
	}
	managed := &fakeManagedSession{status: runtime.SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "copilot-external",
		SessionName:      sessionNameForTest("gastown"),
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "witness"),
	}
	store := runtime.NewFileSessionBindingStore(townRoot)
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	adapter := &fakeRuntimeStarter{session: managed}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}, adapter: adapter}
	if err := m.Stop(); err != nil {
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

func TestStartUsesAdapterForNonClaudeRoleConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	witnessDir := filepath.Join(rigPath, "witness")
	if err := os.MkdirAll(witnessDir, 0o755); err != nil {
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
	settings.RoleAgents = map[string]string{"witness": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(rigPath, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}
	adapter := &fakeRuntimeStarter{}
	m := &Manager{rig: &rig.Rig{Name: "gastown", Path: rigPath}, adapter: adapter}

	if err := m.Start(false, "", nil); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(adapter.requests) != 1 {
		t.Fatalf("adapter requests = %#v, want 1", adapter.requests)
	}
	request := adapter.requests[0]
	if request.Role != "witness" || request.RigName != "gastown" {
		t.Fatalf("request = %#v", request)
	}
	if request.IssueID != sessionNameForTest("gastown") {
		t.Fatalf("IssueID = %q, want session name", request.IssueID)
	}
	if request.WorkDir != witnessDir {
		t.Fatalf("WorkDir = %q, want %q", request.WorkDir, witnessDir)
	}
	if request.Metadata != nil {
		t.Fatalf("Metadata = %#v, want nil", request.Metadata)
	}
	if request.SessionKind != config.ToolSessionKindPatrol || request.ToolPolicy == nil {
		t.Fatalf("request = %#v", request)
	}
	if !contains(request.ToolPolicy.AvailableTools, "run_verification") {
		t.Fatalf("ToolPolicy = %#v", request.ToolPolicy)
	}
}

func sessionNameForTest(rigName string) string {
	return session.WitnessSessionName(session.PrefixFor(rigName))
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
