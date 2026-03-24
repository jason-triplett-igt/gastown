package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

type fakeBindingStore struct {
	saved   []SessionBinding
	deleted []string
	err     error
}

type fakeLifecycleRecorder struct {
	events      []string
	lastPayload map[string]interface{}
}

func (f *fakeLifecycleRecorder) Record(_ context.Context, eventType, _ string, payload map[string]interface{}) error {
	f.events = append(f.events, eventType)
	f.lastPayload = payload
	return nil
}

func (f *fakeBindingStore) Save(_ context.Context, binding SessionBinding) error {
	if f.err != nil {
		return f.err
	}
	f.saved = append(f.saved, binding)
	return nil
}

func (f *fakeBindingStore) Load(context.Context, string, string, string, string) (*SessionBinding, error) {
	return nil, nil
}

func (f *fakeBindingStore) List(context.Context, string, string) ([]SessionBinding, error) {
	return append([]SessionBinding(nil), f.saved...), f.err
}

func (f *fakeBindingStore) Delete(_ context.Context, issueID, role, rigName, agentName string) error {
	f.deleted = append(f.deleted, bindingKey(issueID, role, rigName, agentName))
	return f.err
}

type fakeSessionController struct {
	newSessionName string
	newSessionDir  string
	newCommand     string
	created        bool
	sessions       map[string]bool
	alive          map[string]bool
	env            map[string]map[string]string
	nudges         []string
	waitSession    string
	waitConfig     *config.RuntimeConfig
	killed         []string
	setEnvErr      error
	hasSessionErr  error
	newSessionErr  error
	waitErr        error
	killErr        error
	nudgeErr       error
}

type fakeExternalConnector struct {
	startReq          *SessionLaunchRequest
	resumeReq         *SessionResumeRequest
	lookupReq         *SessionLookupRequest
	startSession      ManagedSession
	resumeSession     ManagedSession
	lookupSession     ManagedSession
	startErr          error
	resumeErr         error
	lookupErr         error
	startAgent        string
	resumeAgent       string
	lookupAgent       string
	startRuntimeConf  *config.RuntimeConfig
	resumeRuntimeConf *config.RuntimeConfig
	lookupRuntimeConf *config.RuntimeConfig
}

func (f *fakeExternalConnector) Start(_ context.Context, req SessionLaunchRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	f.startReq = &req
	f.startAgent = resolvedAgent
	f.startRuntimeConf = rc
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.startSession, nil
}

func (f *fakeExternalConnector) Resume(_ context.Context, req SessionResumeRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	f.resumeReq = &req
	f.resumeAgent = resolvedAgent
	f.resumeRuntimeConf = rc
	if f.resumeErr != nil {
		return nil, f.resumeErr
	}
	return f.resumeSession, nil
}

func (f *fakeExternalConnector) Lookup(_ context.Context, req SessionLookupRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	f.lookupReq = &req
	f.lookupAgent = resolvedAgent
	f.lookupRuntimeConf = rc
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.lookupSession, nil
}

type fakeManagedSession struct {
	id        string
	status    SessionStatus
	statusErr error
	sends     []string
	closeErr  error
	closed    bool
}

func (f *fakeManagedSession) ID() string { return f.id }

func (f *fakeManagedSession) Status(context.Context) (SessionStatus, error) {
	if f.statusErr != nil {
		return SessionStatus{}, f.statusErr
	}
	if f.status.SessionID == "" {
		f.status.SessionID = f.id
	}
	return f.status, nil
}

func (f *fakeManagedSession) Send(_ context.Context, message string) error {
	f.sends = append(f.sends, message)
	return nil
}

func (f *fakeManagedSession) Close(context.Context) error {
	f.closed = true
	return f.closeErr
}

func (f *fakeSessionController) NewSessionWithCommand(name, workDir, command string) error {
	if f.newSessionErr != nil {
		return f.newSessionErr
	}
	f.newSessionName = name
	f.newSessionDir = workDir
	f.newCommand = command
	f.created = true
	if f.sessions == nil {
		f.sessions = make(map[string]bool)
	}
	f.sessions[name] = true
	if f.alive == nil {
		f.alive = make(map[string]bool)
	}
	f.alive[name] = true
	return nil
}

func (f *fakeSessionController) HasSession(name string) (bool, error) {
	if f.hasSessionErr != nil {
		return false, f.hasSessionErr
	}
	return f.sessions[name], nil
}

func (f *fakeSessionController) KillSessionWithProcesses(name string) error {
	if f.killErr != nil {
		return f.killErr
	}
	f.killed = append(f.killed, name)
	delete(f.sessions, name)
	delete(f.alive, name)
	return nil
}

func (f *fakeSessionController) SetEnvironment(session, key, value string) error {
	if f.setEnvErr != nil {
		return f.setEnvErr
	}
	if f.env == nil {
		f.env = make(map[string]map[string]string)
	}
	if f.env[session] == nil {
		f.env[session] = make(map[string]string)
	}
	f.env[session][key] = value
	return nil
}

func (f *fakeSessionController) WaitForRuntimeReady(session string, rc *config.RuntimeConfig, _ time.Duration) error {
	f.waitSession = session
	f.waitConfig = rc
	return f.waitErr
}

func (f *fakeSessionController) NudgeSession(_ string, message string) error {
	if f.nudgeErr != nil {
		return f.nudgeErr
	}
	f.nudges = append(f.nudges, message)
	return nil
}

func (f *fakeSessionController) IsAgentAlive(session string) bool {
	return f.alive[session]
}

func TestTmuxSessionAdapterStartCreatesSession(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	store := &fakeBindingStore{}
	recorder := &fakeLifecycleRecorder{}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, store: store, recorder: recorder}
	workDir := t.TempDir()

	sess, err := adapter.Start(context.Background(), SessionLaunchRequest{
		Provider:    "copilot",
		IssueID:     "slotmachine-910.1.3",
		SessionName: "slotmachine-copilot",
		Role:        "crew",
		RigName:     "alpha",
		AgentName:   "dealer",
		WorkDir:     workDir,
		Prompt:      "Investigate issue",
		Env:         map[string]string{"EXTRA_FLAG": "on"},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if sess.ID() != "slotmachine-copilot" {
		t.Fatalf("session ID = %q, want slotmachine-copilot", sess.ID())
	}
	if !controller.created {
		t.Fatal("expected session creation")
	}
	if controller.newSessionName != "slotmachine-copilot" {
		t.Fatalf("new session name = %q", controller.newSessionName)
	}
	if !strings.Contains(controller.newCommand, "copilot --yolo") {
		t.Fatalf("command = %q, want copilot startup", controller.newCommand)
	}
	if got := controller.env["slotmachine-copilot"]["GT_AGENT"]; got != "copilot" {
		t.Fatalf("GT_AGENT = %q, want copilot", got)
	}
	if got := controller.env["slotmachine-copilot"]["GT_SESSION"]; got != "slotmachine-copilot" {
		t.Fatalf("GT_SESSION = %q, want slotmachine-copilot", got)
	}
	if got := controller.env["slotmachine-copilot"]["EXTRA_FLAG"]; got != "on" {
		t.Fatalf("EXTRA_FLAG = %q, want on", got)
	}
	if controller.waitSession != "slotmachine-copilot" {
		t.Fatalf("wait session = %q, want slotmachine-copilot", controller.waitSession)
	}
	if len(store.saved) != 1 || store.saved[0].IssueID != "slotmachine-910.1.3" {
		t.Fatalf("saved bindings = %#v", store.saved)
	}
	if got := store.saved[0].Metadata; got != nil {
		t.Fatalf("Metadata = %#v, want nil by default", got)
	}
	if len(recorder.events) == 0 || recorder.events[0] != TypeRuntimeSessionStart {
		t.Fatalf("lifecycle events = %#v", recorder.events)
	}
}

func TestTmuxSessionAdapterStartPersistsMetadata(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	store := &fakeBindingStore{}
	recorder := &fakeLifecycleRecorder{}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, store: store, recorder: recorder}
	workDir := t.TempDir()

	_, err := adapter.Start(context.Background(), SessionLaunchRequest{
		IssueID:     "slotmachine-910.4.1",
		SessionName: "gt-review-1",
		Role:        "witness",
		RigName:     "gastown",
		AgentName:   "review",
		WorkDir:     workDir,
		Metadata: map[string]string{
			"session_kind":  "review",
			"fresh_context": "true",
		},
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved bindings = %#v", store.saved)
	}
	if store.saved[0].Metadata["session_kind"] != "review" {
		t.Fatalf("Metadata = %#v", store.saved[0].Metadata)
	}
	if recorder.lastPayload["metadata_session_kind"] != "review" {
		t.Fatalf("lastPayload = %#v", recorder.lastPayload)
	}
}

func TestTmuxSessionAdapterStartPersistsBindingsWithoutIssueID(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	store := &fakeBindingStore{}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, store: store}
	workDir := t.TempDir()

	_, err := adapter.Start(context.Background(), SessionLaunchRequest{
		SessionName: "gt-witness",
		Role:        "witness",
		RigName:     "gastown",
		AgentName:   "witness",
		WorkDir:     workDir,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved bindings = %#v, want 1", store.saved)
	}
	if store.saved[0].IssueID != "" {
		t.Fatalf("IssueID = %q, want empty", store.saved[0].IssueID)
	}
	if store.saved[0].SessionName != "gt-witness" {
		t.Fatalf("SessionName = %q, want gt-witness", store.saved[0].SessionName)
	}
}

func TestTmuxSessionAdapterStartUsesExternalConnector(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	managed := &fakeManagedSession{id: "external-session-123"}
	external := &fakeExternalConnector{startSession: managed}
	store := &fakeBindingStore{}
	recorder := &fakeLifecycleRecorder{}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, external: external, store: store, recorder: recorder}
	workDir := t.TempDir()

	townRoot := t.TempDir()
	rigPath := t.TempDir()
	settingsDir := filepath.Join(rigPath, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{"copilot-external": {
		Provider: "copilot",
		Command:  "copilot",
		Args:     []string{"--yolo"},
		CLIURL:   "localhost:4321",
	}}
	settings.RoleAgents = map[string]string{"crew": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(settingsDir, "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}

	_, err := adapter.Start(context.Background(), SessionLaunchRequest{
		IssueID:     "slotmachine-910.5.7",
		SessionName: "slotmachine-copilot-external",
		Role:        "crew",
		WorkDir:     workDir,
		TownRoot:    townRoot,
		RigPath:     rigPath,
		Prompt:      "Investigate issue",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if controller.created {
		t.Fatalf("tmux should not create a session, command = %q", controller.newCommand)
	}
	if external.startReq == nil || external.startReq.SessionName != "slotmachine-copilot-external" {
		t.Fatalf("startReq = %#v", external.startReq)
	}
	if external.startRuntimeConf == nil || external.startRuntimeConf.CLIURL != "localhost:4321" {
		t.Fatalf("runtime config = %#v", external.startRuntimeConf)
	}
	if external.startAgent != "copilot-external" {
		t.Fatalf("resolved agent = %q, want copilot-external", external.startAgent)
	}
	if len(managed.sends) != 1 || managed.sends[0] != "Investigate issue" {
		t.Fatalf("startup sends = %#v", managed.sends)
	}
	if len(store.saved) != 1 || store.saved[0].RuntimeSessionID != "external-session-123" {
		t.Fatalf("saved bindings = %#v", store.saved)
	}
	if store.saved[0].Metadata["external_server"] != "true" || store.saved[0].Metadata["cli_url"] != "localhost:4321" {
		t.Fatalf("binding metadata = %#v", store.saved[0].Metadata)
	}
	if recorder.lastPayload["metadata_external_server"] != "true" {
		t.Fatalf("last payload = %#v", recorder.lastPayload)
	}
}

func TestTmuxSessionAdapterLookupUsesExternalConnector(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	lookupManaged := &fakeManagedSession{id: "runtime-session-lookup", status: SessionStatus{SessionID: "runtime-session-lookup", Alive: true, Ready: true}}
	external := &fakeExternalConnector{lookupSession: lookupManaged}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, external: external}

	townRoot := t.TempDir()
	rigPath := t.TempDir()
	settingsDir := filepath.Join(rigPath, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{"copilot-external": {
		Provider: "copilot",
		Command:  "copilot",
		CLIURL:   "localhost:4321",
	}}
	settings.RoleAgents = map[string]string{"witness": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(settingsDir, "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}

	sess, err := adapter.Lookup(context.Background(), SessionLookupRequest{
		SessionID:    "runtime-session-lookup",
		SessionName:  "gt-witness-review",
		Role:         "witness",
		TownRoot:     townRoot,
		RigPath:      rigPath,
		WorkDir:      t.TempDir(),
		ReadOnly:     true,
		AllowedTools: []string{"load_review"},
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if sess.ID() != "runtime-session-lookup" {
		t.Fatalf("session ID = %q, want runtime-session-lookup", sess.ID())
	}
	if external.lookupReq == nil || external.lookupReq.SessionID != "runtime-session-lookup" {
		t.Fatalf("lookupReq = %#v", external.lookupReq)
	}
	if external.lookupRuntimeConf == nil || external.lookupRuntimeConf.CLIURL != "localhost:4321" {
		t.Fatalf("lookup runtime config = %#v", external.lookupRuntimeConf)
	}
}

func TestTmuxSessionAdapterResumeUsesExternalConnector(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	store := &fakeBindingStore{}
	recorder := &fakeLifecycleRecorder{}
	external := &fakeExternalConnector{resumeSession: &fakeManagedSession{id: "runtime-session-77"}}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, store: store, recorder: recorder, external: external}
	workDir := t.TempDir()

	townRoot := t.TempDir()
	rigPath := t.TempDir()
	settingsDir := filepath.Join(rigPath, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{"copilot-external": {
		Provider: "copilot",
		Command:  "copilot",
		Args:     []string{"--yolo"},
		CLIURL:   "localhost:4321",
	}}
	settings.RoleAgents = map[string]string{"polecat": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(settingsDir, "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}

	sess, err := adapter.Resume(context.Background(), SessionResumeRequest{
		IssueID:     "slotmachine-910.5.7",
		SessionID:   "runtime-session-77",
		SessionName: "slotmachine-copilot-external-resume",
		Role:        "polecat",
		WorkDir:     workDir,
		TownRoot:    townRoot,
		RigPath:     rigPath,
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if sess.ID() != "runtime-session-77" {
		t.Fatalf("session ID = %q, want runtime-session-77", sess.ID())
	}
	if controller.created {
		t.Fatalf("tmux should not create a session, command = %q", controller.newCommand)
	}
	if external.resumeReq == nil || external.resumeReq.SessionID != "runtime-session-77" {
		t.Fatalf("resumeReq = %#v", external.resumeReq)
	}
	if external.resumeRuntimeConf == nil || external.resumeRuntimeConf.CLIURL != "localhost:4321" {
		t.Fatalf("runtime config = %#v", external.resumeRuntimeConf)
	}
	if len(store.saved) != 1 || store.saved[0].RuntimeSessionID != "runtime-session-77" {
		t.Fatalf("saved bindings = %#v", store.saved)
	}
	if recorder.lastPayload["metadata_cli_url"] != "localhost:4321" {
		t.Fatalf("last payload = %#v", recorder.lastPayload)
	}
}

func TestTmuxSessionAdapterExternalStartPropagatesConnectorError(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	external := &fakeExternalConnector{startErr: errors.New("boom")}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, external: external}

	townRoot := t.TempDir()
	rigPath := t.TempDir()
	settingsDir := filepath.Join(rigPath, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{"copilot-external": {
		Provider: "copilot",
		Command:  "copilot",
		CLIURL:   "localhost:4321",
	}}
	settings.RoleAgents = map[string]string{"crew": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(settingsDir, "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}

	_, err := adapter.Start(context.Background(), SessionLaunchRequest{SessionName: "s", Role: "crew", WorkDir: t.TempDir(), TownRoot: townRoot, RigPath: rigPath})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Start() error = %v, want propagated connector error", err)
	}
}

func TestAdapterBindingMetadataMarksExternalTrustBoundary(t *testing.T) {
	t.Parallel()
	metadata := adapterBindingMetadata(map[string]string{"session_kind": "builder"}, &config.RuntimeConfig{CLIURL: "localhost:4321"}, "patrol")
	if metadata["external_server"] != "true" {
		t.Fatalf("external_server = %q, want true", metadata["external_server"])
	}
	if metadata["cli_url"] != "localhost:4321" {
		t.Fatalf("cli_url = %q, want localhost:4321", metadata["cli_url"])
	}
	if metadata["session_kind"] != "builder" {
		t.Fatalf("session_kind = %q, want builder", metadata["session_kind"])
	}
}

func TestTmuxSessionAdapterResumeUsesResumeCommand(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: make(map[string]bool), alive: make(map[string]bool)}
	store := &fakeBindingStore{}
	recorder := &fakeLifecycleRecorder{}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second, store: store, recorder: recorder}
	workDir := t.TempDir()

	sess, err := adapter.Resume(context.Background(), SessionResumeRequest{
		Provider:    "claude",
		IssueID:     "slotmachine-910.1.3",
		SessionID:   "session-123",
		SessionName: "slotmachine-claude",
		Role:        "mayor",
		WorkDir:     workDir,
	})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if sess.ID() != "session-123" {
		t.Fatalf("session ID = %q, want session-123", sess.ID())
	}
	if !strings.Contains(controller.newCommand, "--resume session-123") {
		t.Fatalf("command = %q, want resume command", controller.newCommand)
	}
	if got := controller.env["slotmachine-claude"]["GT_SESSION_ID_ENV"]; got != "CLAUDE_SESSION_ID" {
		t.Fatalf("GT_SESSION_ID_ENV = %q, want CLAUDE_SESSION_ID", got)
	}
	if got := controller.env["slotmachine-claude"]["CLAUDE_SESSION_ID"]; got != "session-123" {
		t.Fatalf("CLAUDE_SESSION_ID = %q, want session-123", got)
	}
	if len(store.saved) != 1 || store.saved[0].RuntimeSessionID != "session-123" {
		t.Fatalf("saved bindings = %#v", store.saved)
	}
	if len(recorder.events) == 0 || recorder.events[0] != TypeRuntimeSessionResume {
		t.Fatalf("lifecycle events = %#v", recorder.events)
	}
}

func TestTmuxSessionAdapterLookupReturnsManagedSessionForTmux(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "rig")
	if err := os.MkdirAll(filepath.Join(rigPath, "settings"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{"copilot": {
		Provider: "copilot",
		Command:  "copilot",
		Args:     []string{"--yolo"},
	}}
	settings.RoleAgents = map[string]string{"crew": "copilot"}
	if err := config.SaveRigSettings(filepath.Join(rigPath, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}
	controller := &fakeSessionController{sessions: map[string]bool{"slotmachine-copilot": true}, alive: map[string]bool{"slotmachine-copilot": true}}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second}
	sess, err := adapter.Lookup(context.Background(), SessionLookupRequest{
		SessionID:   "runtime-123",
		Provider:    "copilot",
		SessionName: "slotmachine-copilot",
		Role:        "crew",
		TownRoot:    root,
		RigPath:     rigPath,
		WorkDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	status, err := sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Alive || status.SessionID != "runtime-123" {
		t.Fatalf("status = %#v", status)
	}
}

func TestTmuxSessionAdapterLookupReturnsExternalManagedSession(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rigPath := filepath.Join(root, "rig")
	if err := os.MkdirAll(filepath.Join(rigPath, "settings"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{"copilot-external": {
		Provider: "copilot",
		Command:  "copilot",
		CLIURL:   "http://127.0.0.1:4321",
	}}
	settings.RoleAgents = map[string]string{"witness": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(rigPath, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}
	lookupManaged := &fakeManagedSession{id: "runtime-xyz", status: SessionStatus{SessionID: "runtime-xyz", Alive: true, Ready: true}}
	adapter := &TmuxSessionAdapter{tmux: &fakeSessionController{}, startupTimeout: time.Second, external: &fakeExternalConnector{lookupSession: lookupManaged}}
	sess, err := adapter.Lookup(context.Background(), SessionLookupRequest{
		SessionID:   "runtime-xyz",
		Provider:    "copilot-external",
		SessionName: "slotmachine-review-1",
		Role:        "witness",
		TownRoot:    root,
		RigPath:     rigPath,
		WorkDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	status, err := sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.SessionID != "runtime-xyz" || !status.Alive {
		t.Fatalf("status = %#v", status)
	}
}

func TestTmuxManagedSessionStatusSendAndClose(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{
		sessions: map[string]bool{"slotmachine-run": true},
		alive:    map[string]bool{"slotmachine-run": true},
	}
	recorder := &fakeLifecycleRecorder{}
	sess := &tmuxManagedSession{
		sessionName: "slotmachine-run",
		runtimeID:   "runtime-777",
		provider:    "copilot",
		controller:  controller,
		recorder:    recorder,
		role:        "polecat",
		issueID:     "slotmachine-910",
	}

	status, err := sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Alive || !status.Ready {
		t.Fatalf("status = %#v, want alive and ready", status)
	}
	if len(recorder.events) == 0 || recorder.events[0] != TypeRuntimeSessionStatus {
		t.Fatalf("lifecycle events = %#v", recorder.events)
	}
	if err := sess.Send(context.Background(), "hello"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(controller.nudges) != 1 || controller.nudges[0] != "hello" {
		t.Fatalf("nudges = %#v", controller.nudges)
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(controller.killed) != 1 || controller.killed[0] != "slotmachine-run" {
		t.Fatalf("killed = %#v", controller.killed)
	}
	if recorder.events[len(recorder.events)-1] != TypeRuntimeSessionClose {
		t.Fatalf("lifecycle events = %#v", recorder.events)
	}
}

func TestTmuxSessionAdapterStartRejectsDuplicateSession(t *testing.T) {
	t.Parallel()
	controller := &fakeSessionController{sessions: map[string]bool{"dup-session": true}, alive: make(map[string]bool)}
	adapter := &TmuxSessionAdapter{tmux: controller, startupTimeout: time.Second}

	_, err := adapter.Start(context.Background(), SessionLaunchRequest{
		Provider:    "copilot",
		SessionName: "dup-session",
		Role:        "crew",
		WorkDir:     t.TempDir(),
	})
	if err == nil {
		t.Fatal("Start() error = nil, want duplicate session error")
	}
	if !strings.Contains(err.Error(), "session already exists") {
		t.Fatalf("Start() error = %v, want duplicate session message", err)
	}
}

func TestTmuxSessionAdapterWithBindingStoreReturnsAdapter(t *testing.T) {
	t.Parallel()
	adapter := &TmuxSessionAdapter{}
	store := &fakeBindingStore{}
	if got := adapter.WithBindingStore(store); got != adapter {
		t.Fatalf("WithBindingStore() = %#v, want receiver", got)
	}
	if adapter.BindingStore() != store {
		t.Fatalf("BindingStore() = %#v, want store", adapter.BindingStore())
	}
}
