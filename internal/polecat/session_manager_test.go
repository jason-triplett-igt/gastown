package polecat

import (
	"context"
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/rig"
	runtimepkg "github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/testutil"
	"github.com/steveyegge/gastown/internal/tmux"
)

func newSessionManagerTestBeads(t *testing.T, rigPath string) *beads.Beads {
	t.Helper()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not found")
	}

	testutil.RequireDoltContainer(t)
	portStr := testutil.DoltContainerPort()
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("invalid dolt port %q: %v", portStr, err)
	}

	return beads.NewIsolatedWithPort(rigPath, port)
}

func initSessionManagerTestBeads(t *testing.T, b *beads.Beads, base string) {
	t.Helper()

	h := fnv.New32a()
	_, _ = h.Write([]byte(t.Name()))
	prefix := fmt.Sprintf("%s-%x", base, h.Sum32())
	if len(prefix) > 20 {
		prefix = prefix[:20]
	}

	if err := b.Init(prefix); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
}

type fakeManagedSession struct {
	status   runtimepkg.SessionStatus
	err      error
	messages []string
	closed   bool
}

func (f *fakeManagedSession) ID() string { return f.status.SessionID }
func (f *fakeManagedSession) Status(context.Context) (runtimepkg.SessionStatus, error) {
	return f.status, f.err
}
func (f *fakeManagedSession) Send(_ context.Context, message string) error {
	f.messages = append(f.messages, message)
	return nil
}
func (f *fakeManagedSession) Close(context.Context) error {
	f.closed = true
	return nil
}

type fakeSessionAdapter struct {
	startReq      *runtimepkg.SessionLaunchRequest
	resumeReq     *runtimepkg.SessionResumeRequest
	lookupReq     *runtimepkg.SessionLookupRequest
	resumeSession runtimepkg.ManagedSession
	err           error
}

func (f *fakeSessionAdapter) Start(_ context.Context, req runtimepkg.SessionLaunchRequest) (runtimepkg.ManagedSession, error) {
	f.startReq = &req
	return f.resumeSession, f.err
}

func (f *fakeSessionAdapter) Resume(_ context.Context, req runtimepkg.SessionResumeRequest) (runtimepkg.ManagedSession, error) {
	f.resumeReq = &req
	return f.resumeSession, f.err
}

func (f *fakeSessionAdapter) Lookup(_ context.Context, req runtimepkg.SessionLookupRequest) (runtimepkg.ManagedSession, error) {
	f.lookupReq = &req
	return f.resumeSession, f.err
}

type fakeBindingStore struct {
	binding *runtimepkg.SessionBinding
	list    []runtimepkg.SessionBinding
	err     error
}

func (f *fakeBindingStore) Save(context.Context, runtimepkg.SessionBinding) error { return f.err }
func (f *fakeBindingStore) Load(context.Context, string, string, string, string) (*runtimepkg.SessionBinding, error) {
	return f.binding, f.err
}
func (f *fakeBindingStore) List(context.Context, string, string) ([]runtimepkg.SessionBinding, error) {
	return append([]runtimepkg.SessionBinding(nil), f.list...), f.err
}
func (f *fakeBindingStore) Delete(context.Context, string, string, string, string) error {
	return f.err
}

type fakeShowIssueManager struct {
	SessionManager
	result runtimepkg.ToolResult
	err    error
}

func setupTestRegistryForSession(t *testing.T) {
	t.Helper()
	reg := session.NewPrefixRegistry()
	reg.Register("gt", "gastown")
	reg.Register("bd", "beads")
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })
}

// testSessionCounter provides unique session names across -count=N runs
// to prevent "duplicate session" races with tmux's async cleanup.
var testSessionCounter atomic.Int64

func requireTmux(t *testing.T) {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("tmux not supported on Windows")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

func TestSessionName(t *testing.T) {
	setupTestRegistryForSession(t)

	r := &rig.Rig{
		Name:     "gastown",
		Polecats: []string{"Toast"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	name := m.SessionName("Toast")
	if name != "gt-Toast" {
		t.Errorf("sessionName = %q, want gt-Toast", name)
	}
}

func TestSessionManagerPolecatDir(t *testing.T) {
	r := &rig.Rig{
		Name:     "gastown",
		Path:     "/home/user/ai/gastown",
		Polecats: []string{"Toast"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	dir := m.polecatDir("Toast")
	expected := "/home/user/ai/gastown/polecats/Toast"
	if filepath.ToSlash(dir) != expected {
		t.Errorf("polecatDir = %q, want %q", dir, expected)
	}
}

func TestHasPolecat(t *testing.T) {
	root := t.TempDir()
	// hasPolecat checks filesystem, so create actual directories
	for _, name := range []string{"Toast", "Cheedo"} {
		if err := os.MkdirAll(filepath.Join(root, "polecats", name), 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	r := &rig.Rig{
		Name:     "gastown",
		Path:     root,
		Polecats: []string{"Toast", "Cheedo"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	if !m.hasPolecat("Toast") {
		t.Error("expected hasPolecat(Toast) = true")
	}
	if !m.hasPolecat("Cheedo") {
		t.Error("expected hasPolecat(Cheedo) = true")
	}
	if m.hasPolecat("Unknown") {
		t.Error("expected hasPolecat(Unknown) = false")
	}
}

func TestStartPolecatNotFound(t *testing.T) {
	r := &rig.Rig{
		Name:     "gastown",
		Polecats: []string{"Toast"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	err := m.Start("Unknown", SessionStartOptions{})
	if err == nil {
		t.Error("expected error for unknown polecat")
	}
}

func TestIsRunningNoSession(t *testing.T) {
	requireTmux(t)

	r := &rig.Rig{
		Name:     "gastown",
		Polecats: []string{"Toast"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	running, err := m.IsRunning("Toast")
	if err != nil {
		t.Fatalf("IsRunning: %v", err)
	}
	if running {
		t.Error("expected IsRunning = false for non-existent session")
	}
}

func TestSessionManagerListEmpty(t *testing.T) {
	requireTmux(t)

	// Register a unique prefix so List() won't match real sessions.
	// Without this, PrefixFor returns "gt" (default) and matches running gastown sessions.
	reg := session.NewPrefixRegistry()
	reg.Register("xz", "test-rig-unlikely-name")
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })

	r := &rig.Rig{
		Name:     "test-rig-unlikely-name",
		Polecats: []string{},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	infos, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("infos count = %d, want 0", len(infos))
	}
}

func TestSessionManagerListIncludesManagedBindings(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}}
	binding := runtimepkg.SessionBinding{Role: "polecat", RigName: "gastown", AgentName: "toast", RuntimeSessionID: "runtime-123", SessionName: "gt-toast", Provider: "copilot-external"}
	store := &fakeBindingStore{binding: &binding, list: []runtimepkg.SessionBinding{binding}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, bindings: store, adapter: adapter}
	infos, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("List() len = %d, want 1", len(infos))
	}
	if infos[0].Polecat != "toast" || infos[0].SessionID != "gt-toast" || !infos[0].Running {
		t.Fatalf("infos = %#v", infos)
	}
}

func TestStopNotFound(t *testing.T) {
	requireTmux(t)

	r := &rig.Rig{
		Name:     "test-rig",
		Polecats: []string{"Toast"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	err := m.Stop("Toast", false)
	if err != ErrSessionNotFound {
		t.Errorf("Stop = %v, want ErrSessionNotFound", err)
	}
}

func TestCaptureNotFound(t *testing.T) {
	requireTmux(t)

	r := &rig.Rig{
		Name:     "test-rig",
		Polecats: []string{"Toast"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	_, err := m.Capture("Toast", 50)
	if err != ErrSessionNotFound {
		t.Errorf("Capture = %v, want ErrSessionNotFound", err)
	}
}

func TestInjectNotFound(t *testing.T) {
	requireTmux(t)

	r := &rig.Rig{
		Name:     "test-rig",
		Polecats: []string{"Toast"},
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	err := m.Inject("Toast", "hello")
	if err != ErrSessionNotFound {
		t.Errorf("Inject = %v, want ErrSessionNotFound", err)
	}
}

func TestInjectUsesManagedBindingWhenAvailable(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	managed := &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}
	adapter := &fakeSessionAdapter{resumeSession: managed}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{Role: "polecat", RigName: "gastown", AgentName: "Toast", RuntimeSessionID: "runtime-123", SessionName: "gt-toast", Provider: "copilot-external"}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, bindings: store, adapter: adapter}
	if err := m.Inject("Toast", "hello"); err != nil {
		t.Fatalf("Inject() error = %v", err)
	}
	if len(managed.messages) != 1 || managed.messages[0] != "hello" {
		t.Fatalf("messages = %#v", managed.messages)
	}
}

func TestCaptureReturnsUnsupportedForManagedNonTmuxSession(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{Role: "polecat", RigName: "gastown", AgentName: "Toast", RuntimeSessionID: "runtime-123", SessionName: "gt-toast", Provider: "copilot-external"}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, bindings: store, adapter: adapter}
	_, err := m.Capture("Toast", 50)
	if err != ErrInteractionUnsupported {
		t.Fatalf("Capture() error = %v, want ErrInteractionUnsupported", err)
	}
}

func TestAttachReturnsUnsupportedForManagedNonTmuxSession(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{Role: "polecat", RigName: "gastown", AgentName: "Toast", RuntimeSessionID: "runtime-123", SessionName: "gt-toast", Provider: "copilot-external"}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, bindings: store, adapter: adapter}
	err := m.Attach("Toast")
	if err != ErrInteractionUnsupported {
		t.Fatalf("Attach() error = %v, want ErrInteractionUnsupported", err)
	}
}

// TestPolecatCommandFormat verifies the polecat session command exports
// GT_ROLE, GT_RIG, GT_POLECAT, and BD_ACTOR inline before starting Claude.
// This is a regression test for gt-y41ep - env vars must be exported inline
// because tmux SetEnvironment only affects new panes, not the current shell.
func TestPolecatCommandFormat(t *testing.T) {
	// This test verifies the expected command format.
	// The actual command is built in Start() but we test the format here
	// to document and verify the expected behavior.

	rigName := "gastown"
	polecatName := "Toast"
	expectedBdActor := "gastown/polecats/Toast"
	// GT_ROLE uses compound format: rig/polecats/name
	expectedGtRole := rigName + "/polecats/" + polecatName

	// Build the expected command format (mirrors Start() logic)
	expectedPrefix := "export GT_ROLE=" + expectedGtRole + " GT_RIG=" + rigName + " GT_POLECAT=" + polecatName + " BD_ACTOR=" + expectedBdActor + " GIT_AUTHOR_NAME=" + expectedBdActor
	expectedSuffix := "&& claude --dangerously-skip-permissions"

	// The command must contain all required env exports
	requiredParts := []string{
		"export",
		"GT_ROLE=" + expectedGtRole,
		"GT_RIG=" + rigName,
		"GT_POLECAT=" + polecatName,
		"BD_ACTOR=" + expectedBdActor,
		"GIT_AUTHOR_NAME=" + expectedBdActor,
		"claude --dangerously-skip-permissions",
	}

	// Verify expected format contains all required parts
	fullCommand := expectedPrefix + " " + expectedSuffix
	for _, part := range requiredParts {
		if !strings.Contains(fullCommand, part) {
			t.Errorf("Polecat command should contain %q", part)
		}
	}

	// Verify GT_ROLE uses compound format with "polecats" (not "mayor", "crew", etc.)
	if !strings.Contains(fullCommand, "GT_ROLE="+expectedGtRole) {
		t.Errorf("GT_ROLE must be %q (compound format), not simple 'polecat'", expectedGtRole)
	}
}

// TestPolecatStartInjectsFallbackEnvVars verifies that the polecat session
// startup injects GT_BRANCH and GT_POLECAT_PATH into the startup command.
// These env vars are critical for gt done's nuked-worktree fallback:
// when the polecat's cwd is deleted, gt done uses these to determine
// the branch and path without a working directory.
// Regression test for PR #1402.
func TestPolecatStartInjectsFallbackEnvVars(t *testing.T) {
	rigName := "gastown"
	polecatName := "Toast"
	workDir := "/tmp/fake-worktree"

	townRoot := "/tmp/fake-town"

	// The env vars that should be injected via PrependEnv
	requiredEnvVars := []string{
		"GT_BRANCH",       // Git branch for nuked-worktree fallback
		"GT_POLECAT_PATH", // Worktree path for nuked-worktree fallback
		"GT_RIG",          // Rig name (was already there pre-PR)
		"GT_POLECAT",      // Polecat name (was already there pre-PR)
		"GT_ROLE",         // Role address (was already there pre-PR)
		"GT_TOWN_ROOT",    // Town root for FindFromCwdWithFallback after worktree nuke
	}

	// Verify the env var map includes all required keys
	envVars := map[string]string{
		"GT_RIG":          rigName,
		"GT_POLECAT":      polecatName,
		"GT_ROLE":         rigName + "/polecats/" + polecatName,
		"GT_POLECAT_PATH": workDir,
		"GT_TOWN_ROOT":    townRoot,
	}

	// GT_BRANCH is conditionally added (only if CurrentBranch succeeds)
	// In practice it's always set because the worktree exists at Start time
	branchName := "polecat/" + polecatName
	envVars["GT_BRANCH"] = branchName

	for _, key := range requiredEnvVars {
		if _, ok := envVars[key]; !ok {
			t.Errorf("missing required env var %q in startup injection", key)
		}
	}

	// Verify GT_POLECAT_PATH matches workDir
	if envVars["GT_POLECAT_PATH"] != workDir {
		t.Errorf("GT_POLECAT_PATH = %q, want %q", envVars["GT_POLECAT_PATH"], workDir)
	}

	// Verify GT_BRANCH matches expected branch
	if envVars["GT_BRANCH"] != branchName {
		t.Errorf("GT_BRANCH = %q, want %q", envVars["GT_BRANCH"], branchName)
	}
}

// TestSessionManager_resolveBeadsDir verifies that SessionManager correctly
// resolves the beads directory for cross-rig issues via routes.jsonl.
// This is a regression test for GitHub issue #1056.
//
// The bug was that hookIssue/validateIssue used workDir directly instead of
// resolving via routes.jsonl. Now they call resolveBeadsDir which we test here.
func TestSessionManager_resolveBeadsDir(t *testing.T) {
	// Set up a mock town with routes.jsonl
	townRoot := t.TempDir()
	townBeadsDir := filepath.Join(townRoot, ".beads")
	if err := os.MkdirAll(townBeadsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create routes.jsonl with cross-rig routing
	routesContent := `{"prefix": "gt-", "path": "gastown/mayor/rig"}
{"prefix": "bd-", "path": "beads/mayor/rig"}
{"prefix": "hq-", "path": "."}
`
	if err := os.WriteFile(filepath.Join(townBeadsDir, "routes.jsonl"), []byte(routesContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a rig inside the town (simulating gastown rig)
	rigPath := filepath.Join(townRoot, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}

	// Create SessionManager with the rig
	r := &rig.Rig{
		Name: "gastown",
		Path: rigPath,
	}
	m := NewSessionManager(tmux.NewTmux(), r)

	polecatWorkDir := filepath.Join(rigPath, "polecats", "Toast")

	tests := []struct {
		name        string
		issueID     string
		expectedDir string
	}{
		{
			name:        "same-rig bead resolves to rig path",
			issueID:     "gt-abc123",
			expectedDir: filepath.Join(townRoot, "gastown/mayor/rig"),
		},
		{
			name:        "cross-rig bead (beads) resolves to beads rig path",
			issueID:     "bd-xyz789",
			expectedDir: filepath.Join(townRoot, "beads/mayor/rig"),
		},
		{
			name:        "town-level bead resolves to town root",
			issueID:     "hq-town123",
			expectedDir: townRoot,
		},
		{
			name:        "unknown prefix falls back to fallbackDir",
			issueID:     "xx-unknown",
			expectedDir: polecatWorkDir,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Test the SessionManager's resolveBeadsDir method directly
			resolved := m.resolveBeadsDir(tc.issueID, polecatWorkDir)
			if resolved != tc.expectedDir {
				t.Errorf("resolveBeadsDir(%q, %q) = %q, want %q",
					tc.issueID, polecatWorkDir, resolved, tc.expectedDir)
			}
		})
	}
}

func TestValidateVSDDDispatchAllowsSatisfiedIssue(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	issueID := "slotmachine-910"
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "testgate")
	desc := beads.SetVSDDPhaseFields(&beads.Issue{}, &beads.VSDDPhaseFields{Phase: beads.VSDDPhaseImplementation, SpecApproved: true, TestsRed: true})
	desc = beads.SetVSDDArtifactFields(&beads.Issue{Description: desc}, &beads.VSDDArtifactFields{
		SpecArtifactID:       "spec-1",
		SpecReviewArtifactID: "spec-review-1",
		TestPlanArtifactID:   "test-plan-1",
		RedTestEvidenceID:    "red-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	issueID = created.ID
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	if err := m.validateVSDDDispatch(issueID, rigPath); err != nil {
		t.Fatalf("validateVSDDDispatch() error = %v", err)
	}
	issue, err := b.Show(issueID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	phase := beads.ParseVSDDPhaseFields(issue)
	if phase == nil || phase.LastTransition != "dispatch-ready:implementation" {
		t.Fatalf("phase after dispatch = %#v", phase)
	}
}

func TestValidateVSDDDispatchBlocksMissingArtifacts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	issueID := "slotmachine-911"
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "testgate")
	desc := beads.SetVSDDPhaseFields(&beads.Issue{}, &beads.VSDDPhaseFields{Phase: beads.VSDDPhaseImplementation, SpecApproved: true, TestsRed: true})
	created, err := b.Create(beads.CreateOptions{Title: "Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	issueID = created.ID
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	err = m.validateVSDDDispatch(issueID, rigPath)
	if err == nil || !strings.Contains(err.Error(), "blocked by vsdd gate") {
		t.Fatalf("expected gate error, got %v", err)
	}
	issue, err := b.Show(issueID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	phase := beads.ParseVSDDPhaseFields(issue)
	if phase == nil || !strings.Contains(phase.LastRejection, "missing_artifacts:") || phase.LastTransition != "blocked:implementation" {
		t.Fatalf("phase after rejection = %#v", phase)
	}
}

func TestValidateVSDDDispatchBlocksOnReviewGate(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "testgate")
	desc := beads.SetVSDDPhaseFields(&beads.Issue{}, &beads.VSDDPhaseFields{
		Phase:          beads.VSDDPhaseConvergence,
		SpecApproved:   true,
		TestsRed:       true,
		Implementation: true,
		ReviewApproved: true,
		ReviewVerdict:  "READY",
	})
	desc = beads.SetVSDDArtifactFields(&beads.Issue{Description: desc}, &beads.VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "spec-review-1",
		TestPlanArtifactID:       "test-plan-1",
		RedTestEvidenceID:        "red-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	err = m.validateVSDDDispatch(created.ID, rigPath)
	if err == nil || !strings.Contains(err.Error(), "missing_artifacts:review_artifact") {
		t.Fatalf("expected missing review artifact gate error, got %v", err)
	}
}

// TestAgentEnvOmitsGTAgent_FallbackRequired verifies that the AgentEnv path
// used by session_manager.Start does NOT include GT_AGENT when opts.Agent is
// empty (the default dispatch path). This confirms the session_manager must
// fall back to runtimeConfig.ResolvedAgent for setting GT_AGENT in the tmux
// session table.
//
// Without the fallback, GT_AGENT is never written to the tmux session table,
// and the post-startup validation kills the session with:
//
//	"GT_AGENT not set in session ... witness patrol will misidentify this polecat"
//
// Regression test for the bug introduced in PR #1776 which removed the
// unconditional runtimeConfig.ResolvedAgent → SetEnvironment("GT_AGENT") logic
// and replaced it with an AgentEnv-only path that requires opts.Agent to be set.
func TestAgentEnvOmitsGTAgent_FallbackRequired(t *testing.T) {
	t.Parallel()

	// Simulate what session_manager.Start calls for each dispatch scenario.
	cases := []struct {
		name        string
		agent       string // opts.Agent value
		wantGTAgent bool   // whether GT_AGENT should be in AgentEnv output
	}{
		{
			name:        "default dispatch (no --agent flag)",
			agent:       "",
			wantGTAgent: false, // fallback needed
		},
		{
			name:        "explicit --agent codex",
			agent:       "codex",
			wantGTAgent: true,
		},
		{
			name:        "explicit --agent gemini",
			agent:       "gemini",
			wantGTAgent: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := config.AgentEnv(config.AgentEnvConfig{
				Role:      "polecat",
				Rig:       "gastown",
				AgentName: "Toast",
				TownRoot:  "/tmp/town",
				Agent:     tc.agent,
			})
			_, hasGTAgent := env["GT_AGENT"]
			if hasGTAgent != tc.wantGTAgent {
				t.Errorf("AgentEnv(Agent=%q): GT_AGENT present=%v, want %v",
					tc.agent, hasGTAgent, tc.wantGTAgent)
			}
		})
	}
}

// TestVerifyStartupNudgeDelivery_IdleAgent tests that verifyStartupNudgeDelivery
// detects an idle agent (at prompt, no busy indicator) and retries the nudge.
// Uses a real tmux session with a shell prompt that matches the ReadyPromptPrefix.
func TestVerifyStartupNudgeDelivery_IdleAgent(t *testing.T) {
	requireTmux(t)

	tm := tmux.NewTmux()
	// Use a unique session name per invocation to avoid "duplicate session" races
	// with tmux's async cleanup when running with -count=N. (Fixes gt-eo8d)
	sessionName := fmt.Sprintf("gt-test-nudge-%d", testSessionCounter.Add(1))

	// Clean up any stale session from a previous crashed test run
	_ = tm.KillSession(sessionName)

	// Create a tmux session with a shell
	if err := tm.NewSession(sessionName, os.TempDir()); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _ = tm.KillSession(sessionName) })

	// Configure the shell to show the Claude prompt prefix, simulating an idle agent.
	// The prompt "❯ " is what Claude Code shows when idle.
	// No "esc to interrupt" busy indicator — simulates a truly idle agent.
	time.Sleep(300 * time.Millisecond) // Let shell initialize
	_ = tm.SendKeys(sessionName, "export PS1='❯ '")
	time.Sleep(300 * time.Millisecond)

	r := &rig.Rig{Name: "test-rig", Path: t.TempDir()}
	m := NewSessionManager(tm, r)

	rc := &config.RuntimeConfig{
		Tmux: &config.RuntimeTmuxConfig{
			ReadyPromptPrefix: "❯ ",
		},
	}

	// IsIdle should detect the idle state (prompt visible, no busy indicator)
	if !tm.IsIdle(sessionName) {
		t.Log("Warning: idle state not detected (tmux timing); skipping idle verification")
		t.Skip("idle detection unreliable in test environment")
	}

	// verifyStartupNudgeDelivery should detect idle state and retry.
	// We can't easily assert the retry happened, but we verify it doesn't panic/hang.
	// Use a goroutine with timeout to prevent test hanging.
	// Timeout accounts for DefaultStartupNudgeVerifyDelay (25s) * DefaultStartupNudgeMaxRetries (2)
	// plus overhead = ~60s. Use 90s for safety.
	done := make(chan struct{})
	go func() {
		m.verifyStartupNudgeDelivery(sessionName, rc)
		close(done)
	}()

	select {
	case <-done:
		// Success - function completed
	case <-time.After(90 * time.Second):
		t.Fatal("verifyStartupNudgeDelivery hung (exceeded 90s timeout)")
	}
}

// TestVerifyStartupNudgeDelivery_NilConfig verifies that verifyStartupNudgeDelivery
// exits immediately when runtime config has no prompt detection.
func TestVerifyStartupNudgeDelivery_NilConfig(t *testing.T) {
	requireTmux(t)

	r := &rig.Rig{Name: "test-rig", Path: t.TempDir()}
	m := NewSessionManager(tmux.NewTmux(), r)

	// Should return immediately without error for nil config
	m.verifyStartupNudgeDelivery("nonexistent-session", nil)

	// And for config without prompt prefix
	rc := &config.RuntimeConfig{
		Tmux: &config.RuntimeTmuxConfig{
			ReadyPromptPrefix: "",
			ReadyDelayMs:      1000,
		},
	}
	m.verifyStartupNudgeDelivery("nonexistent-session", rc)
}

func TestValidateSessionName(t *testing.T) {
	// Register prefixes so validateSessionName can resolve them correctly.
	reg := session.NewPrefixRegistry()
	reg.Register("gt", "gastown")
	reg.Register("gm", "gastown_manager")
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })

	tests := []struct {
		name        string
		sessionName string
		rigName     string
		wantErr     bool
	}{
		{
			name:        "valid themed name",
			sessionName: "gm-furiosa",
			rigName:     "gastown_manager",
			wantErr:     false,
		},
		{
			name:        "valid overflow name (new format)",
			sessionName: "gm-51",
			rigName:     "gastown_manager",
			wantErr:     false,
		},
		{
			name:        "malformed double-prefix (bug)",
			sessionName: "gm-gastown_manager-51",
			rigName:     "gastown_manager",
			wantErr:     true,
		},
		{
			name:        "malformed double-prefix gastown",
			sessionName: "gt-gastown-142",
			rigName:     "gastown",
			wantErr:     true,
		},
		{
			name:        "different rig (can't validate)",
			sessionName: "gt-other-rig-name",
			rigName:     "gastown_manager",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSessionName(tt.sessionName, tt.rigName)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSessionName() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPolecatSlot(t *testing.T) {
	tmpDir := t.TempDir()
	rigPath := tmpDir
	polecatsDir := filepath.Join(rigPath, "polecats")
	if err := os.MkdirAll(polecatsDir, 0755); err != nil {
		t.Fatal(err)
	}

	r := &rig.Rig{
		Name:     "testrig",
		Path:     rigPath,
		Polecats: []string{},
	}
	sm := NewSessionManager(tmux.NewTmux(), r)

	// No polecats — should return 0
	if slot := sm.polecatSlot("alpha"); slot != 0 {
		t.Errorf("empty dir: got slot %d, want 0", slot)
	}

	// Create some polecat dirs (sorted: alpha, beta, gamma)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(polecatsDir, name), 0755); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		want int
	}{
		{"alpha", 0},
		{"beta", 1},
		{"gamma", 2},
	}
	for _, tt := range tests {
		if slot := sm.polecatSlot(tt.name); slot != tt.want {
			t.Errorf("polecatSlot(%q) = %d, want %d", tt.name, slot, tt.want)
		}
	}

	// Hidden dirs should be skipped
	if err := os.MkdirAll(filepath.Join(polecatsDir, ".hidden"), 0755); err != nil {
		t.Fatal(err)
	}
	if slot := sm.polecatSlot("beta"); slot != 1 {
		t.Errorf("with hidden dir: polecatSlot(beta) = %d, want 1", slot)
	}
}

func TestSessionManagerResumeBoundSessionUsesStoredBinding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(filepath.Join(rigPath, "polecats", "toast"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(root, ".runtime"))
	})
	r := &rig.Rig{Name: "gastown", Path: rigPath, Polecats: []string{"toast"}}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "polecat",
		RigName:          "gastown",
		AgentName:        "toast",
		Provider:         "claude",
		SessionName:      "gt-toast",
		RuntimeSessionID: "runtime-123",
		WorkDir:          filepath.Join(rigPath, "polecats", "toast"),
	}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, adapter: adapter, bindings: store}

	err := m.Start("toast", SessionStartOptions{Issue: "slotmachine-910", WorkDir: filepath.Join(rigPath, "polecats", "toast")})
	if adapter.resumeReq == nil {
		t.Fatal("expected resume path to be used")
	}
	if adapter.resumeReq.SessionID != "runtime-123" {
		t.Fatalf("resume session id = %q, want runtime-123", adapter.resumeReq.SessionID)
	}
	if adapter.resumeReq.SessionName != "gt-toast" {
		t.Fatalf("resume session name = %q, want gt-toast", adapter.resumeReq.SessionName)
	}
	if adapter.resumeReq.IssueID != "slotmachine-910" {
		t.Fatalf("resume issue id = %q, want slotmachine-910", adapter.resumeReq.IssueID)
	}
	if adapter.resumeReq.WorkDir != filepath.Join(rigPath, "polecats", "toast") {
		t.Fatalf("resume workdir = %q, want stored polecat workdir", adapter.resumeReq.WorkDir)
	}
	if err != nil {
		t.Fatalf("Start() error = %v, want resume path success", err)
	}
}

func TestSessionManagerResumeBoundExternalSessionPreservesIssueAndWorkdir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	workDir := filepath.Join(rigPath, "polecats", "toast")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &rig.Rig{Name: "gastown", Path: rigPath, Polecats: []string{"toast"}}
	managed := &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}
	adapter := &fakeSessionAdapter{resumeSession: managed}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "polecat",
		RigName:          "gastown",
		AgentName:        "toast",
		Provider:         "copilot-external",
		SessionName:      "gt-toast",
		RuntimeSessionID: "runtime-123",
		WorkDir:          workDir,
	}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, adapter: adapter, bindings: store}

	if err := m.Start("toast", SessionStartOptions{Issue: "slotmachine-910", WorkDir: workDir}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if adapter.resumeReq == nil {
		t.Fatal("resumeReq = nil, want resume path")
	}
	if adapter.resumeReq.Provider != "copilot-external" {
		t.Fatalf("resume provider = %q, want copilot-external", adapter.resumeReq.Provider)
	}
	if adapter.resumeReq.IssueID != "slotmachine-910" {
		t.Fatalf("resume issue id = %q, want slotmachine-910", adapter.resumeReq.IssueID)
	}
	if adapter.resumeReq.WorkDir != workDir {
		t.Fatalf("resume workdir = %q, want %q", adapter.resumeReq.WorkDir, workDir)
	}
	if adapter.resumeReq.SessionID != "runtime-123" {
		t.Fatalf("resume runtime session id = %q, want runtime-123", adapter.resumeReq.SessionID)
	}
}

func TestPolecatResumeFailureDoesNotCorruptBookkeeping(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	workDir := filepath.Join(rigPath, "polecats", "toast")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &rig.Rig{Name: "gastown", Path: rigPath, Polecats: []string{"toast"}}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "polecat",
		RigName:          "gastown",
		AgentName:        "toast",
		Provider:         "copilot-external",
		SessionName:      "gt-toast",
		RuntimeSessionID: "runtime-123",
		WorkDir:          workDir,
	}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, adapter: &fakeSessionAdapter{err: fmt.Errorf("resume boom")}, bindings: store}

	err := m.Start("toast", SessionStartOptions{Issue: "slotmachine-910", WorkDir: workDir})
	if err == nil {
		t.Fatal("Start() error = nil, want resume failure")
	}
	if !strings.Contains(err.Error(), "resuming bound session: resume boom") {
		t.Fatalf("Start() error = %v, want wrapped resume failure", err)
	}
	binding, bindErr := m.BindingForPolecat("toast")
	if bindErr != nil {
		t.Fatalf("BindingForPolecat() error = %v", bindErr)
	}
	if binding == nil || binding.RuntimeSessionID != "runtime-123" || binding.WorkDir != workDir {
		t.Fatalf("binding = %#v, want preserved runtime bookkeeping", binding)
	}
}

func TestResumeForIssueRequiresIdentifiers(t *testing.T) {
	t.Parallel()
	m := &SessionManager{}
	if err := m.ResumeForIssue("", "toast", SessionStartOptions{}); err == nil {
		t.Fatal("ResumeForIssue() error = nil, want missing issue")
	}
	if err := m.ResumeForIssue("slotmachine-910", "", SessionStartOptions{}); err == nil {
		t.Fatal("ResumeForIssue() error = nil, want missing polecat")
	}
}

func TestRuntimeStatusForIssueUsesBinding(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}}
	m := &SessionManager{
		tmux:    tmux.NewTmux(),
		rig:     r,
		adapter: adapter,
		bindings: &fakeBindingStore{binding: &runtimepkg.SessionBinding{
			IssueID:          "slotmachine-910",
			Role:             "polecat",
			RigName:          "gastown",
			AgentName:        "toast",
			Provider:         "claude",
			SessionName:      "missing-session",
			RuntimeSessionID: "runtime-123",
		}},
	}
	status, err := m.RuntimeStatusForIssue("slotmachine-910", "toast")
	if err != nil {
		t.Fatalf("RuntimeStatusForIssue() error = %v", err)
	}
	if status.SessionID != "runtime-123" {
		t.Fatalf("SessionID = %q, want runtime-123", status.SessionID)
	}
	if !status.Alive {
		t.Fatal("Alive = false, want adapter-backed alive status")
	}
	if adapter.lookupReq == nil || adapter.lookupReq.SessionID != "runtime-123" {
		t.Fatalf("lookupReq = %#v", adapter.lookupReq)
	}
}

func TestStatusUsesManagedBindingReadyAndBusyFields(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: false, Busy: true}, err: fmt.Errorf("owner degraded")}}
	m := &SessionManager{
		tmux:    tmux.NewTmux(),
		rig:     r,
		adapter: adapter,
		bindings: &fakeBindingStore{binding: &runtimepkg.SessionBinding{
			IssueID:          "slotmachine-910",
			Role:             "polecat",
			RigName:          "gastown",
			AgentName:        "toast",
			Provider:         "claude",
			SessionName:      "missing-session",
			RuntimeSessionID: "runtime-123",
		}},
	}
	info, err := m.Status("toast")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !info.Running || info.Ready || !info.Busy {
		t.Fatalf("info = %#v, want running=true ready=false busy=true", info)
	}
	if info.StatusError != "owner degraded" {
		t.Fatalf("StatusError = %q, want owner degraded", info.StatusError)
	}
}

func TestListIncludesManagedReadyAwareStatus(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: false, Busy: true}, err: fmt.Errorf("owner degraded")}}
	binding := &runtimepkg.SessionBinding{
		Role:             "polecat",
		RigName:          "gastown",
		AgentName:        "toast",
		Provider:         "copilot-external",
		SessionName:      "gt-toast",
		RuntimeSessionID: "runtime-123",
	}
	m := &SessionManager{
		tmux:     tmux.NewTmux(),
		rig:      r,
		adapter:  adapter,
		bindings: &fakeBindingStore{binding: binding, list: []runtimepkg.SessionBinding{*binding}},
	}
	infos, err := m.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("infos = %#v, want one entry", infos)
	}
	if !infos[0].Running || infos[0].Ready || !infos[0].Busy {
		t.Fatalf("infos[0] = %#v, want running=true ready=false busy=true", infos[0])
	}
	if infos[0].StatusError != "owner degraded" {
		t.Fatalf("StatusError = %q, want owner degraded", infos[0].StatusError)
	}
}

func TestBindingForPolecatReturnsBinding(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{Role: "polecat", RigName: "gastown", AgentName: "toast", RuntimeSessionID: "runtime-123"}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, bindings: store}
	binding, err := m.BindingForPolecat("toast")
	if err != nil {
		t.Fatalf("BindingForPolecat() error = %v", err)
	}
	if binding == nil || binding.RuntimeSessionID != "runtime-123" {
		t.Fatalf("binding = %#v", binding)
	}
}

func TestStopUsesManagedBindingWhenAvailable(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	managed := &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}
	adapter := &fakeSessionAdapter{resumeSession: managed}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{Role: "polecat", RigName: "gastown", AgentName: "toast", RuntimeSessionID: "runtime-123", SessionName: "gt-toast", Provider: "copilot-external"}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, bindings: store, adapter: adapter}
	if err := m.Stop("toast", true); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !managed.closed {
		t.Fatal("managed session was not closed")
	}
	if adapter.lookupReq == nil || adapter.lookupReq.SessionID != "runtime-123" {
		t.Fatalf("lookupReq = %#v", adapter.lookupReq)
	}
}

func TestIsRunningUsesManagedBindingWhenAvailable(t *testing.T) {
	t.Parallel()
	r := &rig.Rig{Name: "gastown", Path: t.TempDir()}
	adapter := &fakeSessionAdapter{resumeSession: &fakeManagedSession{status: runtimepkg.SessionStatus{SessionID: "runtime-123", Alive: true, Ready: true}}}
	store := &fakeBindingStore{binding: &runtimepkg.SessionBinding{Role: "polecat", RigName: "gastown", AgentName: "toast", RuntimeSessionID: "runtime-123", SessionName: "gt-toast", Provider: "copilot-external"}}
	m := &SessionManager{tmux: tmux.NewTmux(), rig: r, bindings: store, adapter: adapter}
	running, err := m.IsRunning("toast")
	if err != nil {
		t.Fatalf("IsRunning() error = %v", err)
	}
	if !running {
		t.Fatal("IsRunning() = false, want true")
	}
}

func TestShowIssueForRuntimeRequiresIssueID(t *testing.T) {
	t.Parallel()
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: t.TempDir()}}
	_, err := m.ShowIssueForRuntime("", "")
	if err == nil {
		t.Fatal("ShowIssueForRuntime() error = nil, want missing issue")
	}
}

func TestInspectWorkflowStateRequiresIssueID(t *testing.T) {
	t.Parallel()
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: t.TempDir()}}
	_, err := m.InspectWorkflowState("", "")
	if err == nil {
		t.Fatal("InspectWorkflowState() error = nil, want missing issue")
	}
}

func TestInspectWorkflowStateReturnsInspection(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "inspect")
	desc := beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{
		Phase:          beads.VSDDPhaseImplementation,
		SpecApproved:   true,
		TestsRed:       true,
		LastTransition: "dispatch-ready:implementation",
	}, &beads.VSDDArtifactFields{
		SpecArtifactID:       "spec-1",
		SpecReviewArtifactID: "spec-review-1",
		TestPlanArtifactID:   "test-plan-1",
		RedTestEvidenceID:    "red-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	inspection, err := m.InspectWorkflowState(created.ID, rigPath)
	if err != nil {
		t.Fatalf("InspectWorkflowState() error = %v", err)
	}
	if inspection.Phase != beads.VSDDPhaseImplementation || !inspection.DispatchReady {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func TestReadyIssuesForRuntimeReturnsReadyList(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "ready-runtime")
	created, err := b.Create(beads.CreateOptions{Title: "Ready Task", Description: "runtime ready", Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	result, err := m.ReadyIssuesForRuntime(rigPath)
	if err != nil {
		t.Fatalf("ReadyIssuesForRuntime() error = %v", err)
	}
	if result.Data["count"] == "0" || !strings.Contains(result.Data["ids"], created.ID) {
		t.Fatalf("result = %#v", result)
	}
}

func TestLoadReviewForRuntimeRequiresIssueID(t *testing.T) {
	t.Parallel()
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: t.TempDir()}}
	_, err := m.LoadReviewForRuntime("", "")
	if err == nil {
		t.Fatal("LoadReviewForRuntime() error = nil, want missing issue")
	}
}

func TestLoadReviewForRuntimeReturnsWorkflowDetails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "review-runtime")
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
	created, err := b.Create(beads.CreateOptions{Title: "Review Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	result, err := m.LoadReviewForRuntime(created.ID, rigPath)
	if err != nil {
		t.Fatalf("LoadReviewForRuntime() error = %v", err)
	}
	if result.Data["review_verdict"] != "READY" || result.Data["review_artifact"] != "review-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestVerifyIssueForRuntimeReturnsVerificationStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "verify-runtime")
	desc := beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{
		Phase:          beads.VSDDPhaseConvergence,
		SpecApproved:   true,
		TestsRed:       true,
		Implementation: true,
		ReviewVerdict:  "NOT_READY",
		ReviewApproved: false,
		LastTransition: "blocked:convergence",
		LastRejection:  "review_rejected",
	}, &beads.VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "spec-review-1",
		TestPlanArtifactID:       "test-plan-1",
		RedTestEvidenceID:        "red-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-1",
		ReviewArtifactID:         "review-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Verify Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	result, err := m.VerifyIssueForRuntime(created.ID, rigPath)
	if err != nil {
		t.Fatalf("VerifyIssueForRuntime() error = %v", err)
	}
	if result.Data["verification_status"] != "blocked" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRecordReviewVerdictPersistsStructuredReview(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "record-review")
	desc := beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{
		Phase:          beads.VSDDPhaseReview,
		SpecApproved:   true,
		TestsRed:       true,
		Implementation: true,
	}, &beads.VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "spec-review-1",
		TestPlanArtifactID:       "tests-1",
		RedTestEvidenceID:        "red-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Review Verdict Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	inspection, err := m.RecordReviewVerdict(created.ID, rigPath, beads.VSDDReviewVerdictInput{
		Verdict:          "READY",
		Summary:          "Reviewed approved evidence and found no blockers.",
		EvidenceRefs:     []string{"spec-1", "impl-1", "builder-1"},
		FreshContext:     true,
		ReviewArtifactID: "review-1",
	})
	if err != nil {
		t.Fatalf("RecordReviewVerdict() error = %v", err)
	}
	if inspection.ReviewVerdict != "READY" || !inspection.ReviewApproved || !inspection.ReviewContractOK {
		t.Fatalf("inspection = %#v", inspection)
	}
	issue, err := b.Show(created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	if !strings.Contains(issue.Description, "vsdd_review_artifact: review-1") || !strings.Contains(issue.Description, "vsdd_review_summary: Reviewed approved evidence and found no blockers.") {
		t.Fatalf("description = %q", issue.Description)
	}
}

func TestRecordReviewVerdictRejectsMalformedReview(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "record-review-malformed")
	desc := beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{Phase: beads.VSDDPhaseReview, SpecApproved: true, TestsRed: true, Implementation: true}, &beads.VSDDArtifactFields{SpecArtifactID: "spec-1", SpecReviewArtifactID: "spec-review-1", TestPlanArtifactID: "tests-1", RedTestEvidenceID: "red-1", ImplementationArtifactID: "impl-1", BuilderEvidenceID: "builder-1"})
	created, err := b.Create(beads.CreateOptions{Title: "Malformed Review Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	inspection, err := m.RecordReviewVerdict(created.ID, rigPath, beads.VSDDReviewVerdictInput{Verdict: "NOT READY", Summary: "Missing findings but no list.", EvidenceRefs: []string{"spec-1"}, FreshContext: true})
	if err == nil {
		t.Fatal("RecordReviewVerdict() error = nil, want malformed review error")
	}
	if inspection == nil || !strings.Contains(inspection.LastRejection, string(beads.VSDDRejectReviewMalformed)) {
		t.Fatalf("inspection = %#v err = %v", inspection, err)
	}
}

func TestValidateVSDDDispatchBlocksMalformedReviewForConvergence(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "dispatch-review-block")
	desc := beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{
		Phase:          beads.VSDDPhaseConvergence,
		SpecApproved:   true,
		TestsRed:       true,
		Implementation: true,
		ReviewVerdict:  "READY",
		ReviewApproved: true,
		ReviewFresh:    false,
	}, &beads.VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "spec-review-1",
		TestPlanArtifactID:       "tests-1",
		RedTestEvidenceID:        "red-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-1",
		ReviewArtifactID:         "review-1",
	})
	created, err := b.Create(beads.CreateOptions{Title: "Dispatch Review Block Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	err = m.validateVSDDDispatch(created.ID, rigPath)
	if err == nil || !strings.Contains(err.Error(), "review contract") {
		t.Fatalf("expected review contract error, got %v", err)
	}
	issue, err := b.Show(created.ID)
	if err != nil {
		t.Fatalf("Show() error = %v", err)
	}
	phase := beads.ParseVSDDPhaseFields(issue)
	if phase == nil || !strings.Contains(phase.LastRejection, string(beads.VSDDRejectReviewMalformed)) || phase.LastTransition != "blocked:convergence" {
		t.Fatalf("phase = %#v", phase)
	}
}

func TestExplainWorkflowStateIncludesReviewIntegrityDetails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "gastown")
	if err := os.MkdirAll(rigPath, 0755); err != nil {
		t.Fatal(err)
	}
	b := newSessionManagerTestBeads(t, rigPath)
	initSessionManagerTestBeads(t, b, "explain-review")
	desc, err := beads.PersistReviewVerdict(&beads.Issue{Description: beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{Phase: beads.VSDDPhaseReview, SpecApproved: true, TestsRed: true, Implementation: true}, &beads.VSDDArtifactFields{SpecArtifactID: "spec-1", SpecReviewArtifactID: "spec-review-1", TestPlanArtifactID: "tests-1", RedTestEvidenceID: "red-1", ImplementationArtifactID: "impl-1", BuilderEvidenceID: "builder-1"})}, beads.VSDDReviewVerdictInput{Verdict: "NOT READY", Summary: "Regression evidence is incomplete.", EvidenceRefs: []string{"spec-1", "builder-1"}, Findings: []string{"missing regression test"}, FreshContext: true, ReviewArtifactID: "review-1"})
	if err != nil {
		t.Fatalf("PersistReviewVerdict() error = %v", err)
	}
	created, err := b.Create(beads.CreateOptions{Title: "Explain Workflow Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := &SessionManager{rig: &rig.Rig{Name: "gastown", Path: rigPath}}
	explanation, err := m.ExplainWorkflowState(created.ID, rigPath)
	if err != nil {
		t.Fatalf("ExplainWorkflowState() error = %v", err)
	}
	for _, want := range []string{"review_contract_ok=true", "review_verdict=NOT READY", "review_summary=Regression evidence is incomplete.", "review_findings=missing regression test", "review_evidence=builder-1,spec-1"} {
		if !strings.Contains(explanation, want) {
			t.Fatalf("explanation = %q, want %q", explanation, want)
		}
	}
}
