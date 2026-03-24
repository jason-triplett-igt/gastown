package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/tmux"
)

const DefaultAdapterStartupTimeout = 30 * time.Second

type tmuxSessionController interface {
	NewSessionWithCommand(name, workDir, command string) error
	HasSession(name string) (bool, error)
	KillSessionWithProcesses(name string) error
	SetEnvironment(session, key, value string) error
	WaitForRuntimeReady(session string, rc *config.RuntimeConfig, timeout time.Duration) error
	NudgeSession(session, message string) error
	IsAgentAlive(session string) bool
}

type TmuxSessionAdapter struct {
	tmux           tmuxSessionController
	startupTimeout time.Duration
	store          SessionBindingStore
	recorder       LifecycleRecorder
	external       externalSessionConnector
}

func NewTmuxSessionAdapter(controller *tmux.Tmux) *TmuxSessionAdapter {
	if controller == nil {
		controller = tmux.NewTmux()
	}
	return &TmuxSessionAdapter{
		tmux:           controller,
		startupTimeout: DefaultAdapterStartupTimeout,
		recorder:       FeedLifecycleRecorder{},
		external:       copilotExternalSessionConnector{},
	}
}

func (a *TmuxSessionAdapter) WithBindingStore(store SessionBindingStore) *TmuxSessionAdapter {
	a.store = store
	return a
}

func (a *TmuxSessionAdapter) BindingStore() SessionBindingStore {
	return a.store
}

func (a *TmuxSessionAdapter) WithLifecycleRecorder(recorder LifecycleRecorder) *TmuxSessionAdapter {
	a.recorder = recorder
	return a
}

func (a *TmuxSessionAdapter) Start(ctx context.Context, req SessionLaunchRequest) (ManagedSession, error) {
	_ = ctx
	if err := validateLaunchRequest(req); err != nil {
		return nil, err
	}

	rc, resolvedAgent, err := resolveAdapterRuntime(req.Provider, req.Role, req.TownRoot, req.RigPath)
	if err != nil {
		return nil, err
	}
	if err := ensureAdapterSettings(ctx, req.WorkDir, req.Role, req.RigPath, rc); err != nil {
		return nil, err
	}
	if usesExternalServer(rc) {
		managedSession, err := a.externalConnector().Start(ctx, req, rc, resolvedAgent)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.Prompt) != "" {
			if err := managedSession.Send(ctx, req.Prompt); err != nil {
				_ = managedSession.Close(context.Background())
				return nil, fmt.Errorf("sending startup prompt to external runtime: %w", err)
			}
		}
		metadata := adapterBindingMetadata(req.Metadata, rc, req.SessionKind)
		if externalManaged, ok := managedSession.(*externalCopilotManagedSession); ok {
			for key, value := range externalManaged.metadata {
				metadata[key] = value
			}
		}
		if err := a.saveBinding(ctx, SessionBinding{
			IssueID:          req.IssueID,
			Role:             req.Role,
			RigName:          req.RigName,
			AgentName:        req.AgentName,
			Provider:         resolvedAgent,
			SessionName:      req.SessionName,
			RuntimeSessionID: managedSession.ID(),
			WorkDir:          req.WorkDir,
			LifecycleState:   SessionLifecycleStarting,
			Metadata:         metadata,
		}); err != nil {
			return nil, err
		}
		extra := map[string]interface{}{"work_dir": req.WorkDir}
		for key, value := range metadata {
			extra["metadata_"+key] = value
		}
		a.recordLifecycle(ctx, TypeRuntimeSessionStart, req.Role, req.SessionName, managedSession.ID(), req.IssueID, resolvedAgent, extra)
		return managedSession, nil
	}

	command, err := adapterLaunchCommand(req, rc)
	if err != nil {
		return nil, err
	}
	if err := a.startSession(req.SessionName, req.WorkDir, command, rc); err != nil {
		return nil, err
	}
	if err := a.setSessionEnvironment(req.SessionName, adapterSessionEnv(req, rc, resolvedAgent, "")); err != nil {
		return nil, err
	}
	if err := a.saveBinding(ctx, SessionBinding{
		IssueID:          req.IssueID,
		Role:             req.Role,
		RigName:          req.RigName,
		AgentName:        req.AgentName,
		Provider:         resolvedAgent,
		SessionName:      req.SessionName,
		RuntimeSessionID: req.SessionName,
		WorkDir:          req.WorkDir,
		LifecycleState:   SessionLifecycleRunning,
		Metadata:         adapterBindingMetadata(req.Metadata, rc, req.SessionKind),
	}); err != nil {
		return nil, err
	}
	extra := map[string]interface{}{"work_dir": req.WorkDir}
	for key, value := range adapterBindingMetadata(req.Metadata, rc, req.SessionKind) {
		extra["metadata_"+key] = value
	}
	a.recordLifecycle(ctx, TypeRuntimeSessionStart, req.Role, req.SessionName, req.SessionName, req.IssueID, resolvedAgent, extra)

	return &tmuxManagedSession{
		sessionName: req.SessionName,
		runtimeID:   req.SessionName,
		provider:    resolvedAgent,
		controller:  a.tmux,
		recorder:    a.recorder,
		role:        req.Role,
		issueID:     req.IssueID,
	}, nil
}

func (a *TmuxSessionAdapter) Resume(ctx context.Context, req SessionResumeRequest) (ManagedSession, error) {
	_ = ctx
	if err := validateResumeRequest(req); err != nil {
		return nil, err
	}

	rc, resolvedAgent, err := resolveAdapterRuntime(req.Provider, req.Role, req.TownRoot, req.RigPath)
	if err != nil {
		return nil, err
	}
	if err := ensureAdapterSettings(ctx, req.WorkDir, req.Role, req.RigPath, rc); err != nil {
		return nil, err
	}
	if usesExternalServer(rc) {
		managedSession, err := a.externalConnector().Resume(ctx, req, rc, resolvedAgent)
		if err != nil {
			return nil, err
		}
		metadata := adapterBindingMetadata(req.Metadata, rc, req.SessionKind)
		if externalManaged, ok := managedSession.(*externalCopilotManagedSession); ok {
			for key, value := range externalManaged.metadata {
				metadata[key] = value
			}
		}
		if err := a.saveBinding(ctx, SessionBinding{
			IssueID:          req.IssueID,
			Role:             req.Role,
			RigName:          req.RigName,
			AgentName:        req.AgentName,
			Provider:         resolvedAgent,
			SessionName:      req.SessionName,
			RuntimeSessionID: managedSession.ID(),
			WorkDir:          req.WorkDir,
			LifecycleState:   SessionLifecycleRunning,
			Metadata:         metadata,
		}); err != nil {
			return nil, err
		}
		extra := map[string]interface{}{"work_dir": req.WorkDir}
		for key, value := range metadata {
			extra["metadata_"+key] = value
		}
		a.recordLifecycle(ctx, TypeRuntimeSessionResume, req.Role, req.SessionName, managedSession.ID(), req.IssueID, resolvedAgent, extra)
		return managedSession, nil
	}

	command, err := adapterResumeCommand(req, rc, resolvedAgent)
	if err != nil {
		return nil, err
	}
	if err := a.startSession(req.SessionName, req.WorkDir, command, rc); err != nil {
		return nil, err
	}
	if err := a.setSessionEnvironment(req.SessionName, adapterSessionEnvFromResume(req, rc, resolvedAgent)); err != nil {
		return nil, err
	}
	if err := a.saveBinding(ctx, SessionBinding{
		IssueID:          req.IssueID,
		Role:             req.Role,
		RigName:          req.RigName,
		AgentName:        req.AgentName,
		Provider:         resolvedAgent,
		SessionName:      req.SessionName,
		RuntimeSessionID: req.SessionID,
		WorkDir:          req.WorkDir,
		LifecycleState:   SessionLifecycleRunning,
		Metadata:         adapterBindingMetadata(req.Metadata, rc, req.SessionKind),
	}); err != nil {
		return nil, err
	}
	extra := map[string]interface{}{"work_dir": req.WorkDir}
	for key, value := range adapterBindingMetadata(req.Metadata, rc, req.SessionKind) {
		extra["metadata_"+key] = value
	}
	a.recordLifecycle(ctx, TypeRuntimeSessionResume, req.Role, req.SessionName, req.SessionID, req.IssueID, resolvedAgent, extra)

	return &tmuxManagedSession{
		sessionName: req.SessionName,
		runtimeID:   req.SessionID,
		provider:    resolvedAgent,
		controller:  a.tmux,
		recorder:    a.recorder,
		role:        req.Role,
		issueID:     req.IssueID,
	}, nil
}

func (a *TmuxSessionAdapter) Lookup(ctx context.Context, req SessionLookupRequest) (ManagedSession, error) {
	_ = ctx
	if req.SessionName == "" {
		return nil, fmt.Errorf("session name is required")
	}
	rc, resolvedAgent, err := resolveAdapterRuntime(req.Provider, req.Role, req.TownRoot, req.RigPath)
	if err != nil {
		return nil, err
	}
	runtimeID := req.SessionID
	if runtimeID == "" {
		runtimeID = req.SessionName
	}
	if usesExternalServer(rc) {
		if runtimeID == "" {
			return nil, fmt.Errorf("runtime session id is required for external sessions")
		}
		return a.externalConnector().Lookup(ctx, req, rc, resolvedAgent)
	}
	return &tmuxManagedSession{
		sessionName: req.SessionName,
		runtimeID:   runtimeID,
		provider:    resolvedAgent,
		controller:  a.tmux,
		recorder:    a.recorder,
		role:        req.Role,
		issueID:     req.IssueID,
	}, nil
}

func (a *TmuxSessionAdapter) externalConnector() externalSessionConnector {
	if a.external != nil {
		return a.external
	}
	return copilotExternalSessionConnector{}
}

type tmuxManagedSession struct {
	sessionName string
	runtimeID   string
	provider    string
	controller  tmuxSessionController
	recorder    LifecycleRecorder
	role        string
	issueID     string
}

func (s *tmuxManagedSession) ID() string {
	if s.runtimeID != "" {
		return s.runtimeID
	}
	return s.sessionName
}

func (s *tmuxManagedSession) Status(_ context.Context) (SessionStatus, error) {
	running, err := s.controller.HasSession(s.sessionName)
	if err != nil {
		return SessionStatus{}, fmt.Errorf("checking session: %w", err)
	}
	alive := false
	if running {
		alive = s.controller.IsAgentAlive(s.sessionName)
	}
	status := SessionStatus{
		Provider:  s.provider,
		SessionID: s.ID(),
		Ready:     alive,
		Alive:     alive,
		Busy:      false,
	}
	if s.recorder != nil {
		_ = s.recorder.Record(context.Background(), TypeRuntimeSessionStatus, s.role, RuntimeSessionPayload(s.sessionName, s.ID(), s.role, s.issueID, s.provider, map[string]interface{}{"alive": status.Alive, "ready": status.Ready, "busy": status.Busy}))
	}
	return status, nil
}

func (s *tmuxManagedSession) Send(_ context.Context, message string) error {
	if message == "" {
		return nil
	}
	if err := s.controller.NudgeSession(s.sessionName, message); err != nil {
		return fmt.Errorf("nudging session: %w", err)
	}
	return nil
}

func (s *tmuxManagedSession) Close(_ context.Context) error {
	if s.recorder != nil {
		_ = s.recorder.Record(context.Background(), TypeRuntimeSessionClose, s.role, RuntimeSessionPayload(s.sessionName, s.ID(), s.role, s.issueID, s.provider, nil))
	}
	if err := s.controller.KillSessionWithProcesses(s.sessionName); err != nil {
		return fmt.Errorf("killing session: %w", err)
	}
	return nil
}

func (a *TmuxSessionAdapter) recordLifecycle(ctx context.Context, eventType, role, sessionName, runtimeSessionID, issueID, provider string, extra map[string]interface{}) {
	if a.recorder == nil {
		return
	}
	_ = a.recorder.Record(ctx, eventType, role, RuntimeSessionPayload(sessionName, runtimeSessionID, role, issueID, provider, extra))
}

func (a *TmuxSessionAdapter) saveBinding(ctx context.Context, binding SessionBinding) error {
	if a.store == nil {
		return nil
	}
	if err := a.store.Save(ctx, binding); err != nil {
		return fmt.Errorf("saving session binding: %w", err)
	}
	return nil
}

func validateLaunchRequest(req SessionLaunchRequest) error {
	if req.SessionName == "" {
		return fmt.Errorf("session name is required")
	}
	if req.Role == "" {
		return fmt.Errorf("role is required")
	}
	if req.WorkDir == "" {
		return fmt.Errorf("work directory is required")
	}
	return nil
}

func validateResumeRequest(req SessionResumeRequest) error {
	if req.SessionID == "" {
		return fmt.Errorf("runtime session id is required")
	}
	if req.SessionName == "" {
		return fmt.Errorf("session name is required")
	}
	if req.Role == "" {
		return fmt.Errorf("role is required")
	}
	if req.WorkDir == "" {
		return fmt.Errorf("work directory is required")
	}
	return nil
}

func resolveAdapterRuntime(provider, role, townRoot, rigPath string) (*config.RuntimeConfig, string, error) {
	if provider != "" {
		rc, agentName, err := config.ResolveAgentConfigWithOverride(townRoot, rigPath, provider)
		if err != nil {
			return nil, "", fmt.Errorf("resolving agent config: %w", err)
		}
		return rc, agentName, nil
	}

	rc := config.ResolveRoleAgentConfig(role, townRoot, rigPath)
	if rc == nil {
		return nil, "", fmt.Errorf("resolving agent config: no runtime config for role %s", role)
	}
	resolved := rc.ResolvedAgent
	if resolved == "" {
		resolved = rc.Provider
	}
	return rc, resolved, nil
}

func ensureAdapterSettings(ctx context.Context, workDir, role, rigPath string, rc *config.RuntimeConfig) error {
	settingsDir := config.RoleSettingsDir(role, rigPath)
	if settingsDir == "" {
		settingsDir = workDir
	}
	if err := (ConfigHookProvisioner{}).Ensure(ctx, HookRequest{
		SettingsDir:   settingsDir,
		WorkDir:       workDir,
		Role:          role,
		RuntimeConfig: rc,
	}); err != nil {
		return fmt.Errorf("ensuring runtime settings: %w", err)
	}
	return nil
}

func adapterLaunchCommand(req SessionLaunchRequest, rc *config.RuntimeConfig) (string, error) {
	if usesExternalServer(rc) {
		return adapterExternalServerCommand(rc), nil
	}
	command, err := config.BuildStartupCommandFromConfig(config.AgentEnvConfig{
		Role:             req.Role,
		Rig:              req.RigName,
		AgentName:        req.AgentName,
		TownRoot:         req.TownRoot,
		RuntimeConfigDir: req.RuntimeConfigDir,
		Agent:            req.Provider,
		Prompt:           req.Prompt,
		SessionName:      req.SessionName,
	}, req.RigPath, req.Prompt, req.Provider)
	if err != nil {
		return "", fmt.Errorf("building startup command: %w", err)
	}
	return prependAdapterEnv(command, rc, req.RuntimeConfigDir, req.Env), nil
}

func adapterResumeCommand(req SessionResumeRequest, rc *config.RuntimeConfig, resolvedAgent string) (string, error) {
	if usesExternalServer(rc) {
		return adapterExternalServerCommand(rc), nil
	}
	resumeCommand := config.BuildResumeCommand(resolvedAgent, req.SessionID)
	if resumeCommand == "" {
		return "", fmt.Errorf("agent %q does not support session resume", resolvedAgent)
	}
	return prependAdapterEnv(resumeCommand, rc, req.RuntimeConfigDir, req.Env), nil
}

func adapterExternalServerCommand(rc *config.RuntimeConfig) string {
	url := strings.TrimSpace(rc.CLIURL)
	if url == "" {
		return ""
	}
	return config.PrependEnv("sleep infinity", map[string]string{"COPILOT_CLI_URL": url})
}

func usesExternalServer(rc *config.RuntimeConfig) bool {
	return rc != nil && strings.TrimSpace(rc.CLIURL) != ""
}

func adapterBindingMetadata(metadata map[string]string, rc *config.RuntimeConfig, sessionKind string) map[string]string {
	result := cloneStringMap(metadata)
	if strings.TrimSpace(sessionKind) != "" {
		if result == nil {
			result = make(map[string]string, 1)
		}
		if strings.TrimSpace(result["session_kind"]) == "" {
			result["session_kind"] = strings.TrimSpace(sessionKind)
		}
	}
	if usesExternalServer(rc) {
		if result == nil {
			result = make(map[string]string, 2)
		}
		result["external_server"] = "true"
		result["cli_url"] = strings.TrimSpace(rc.CLIURL)
	}
	return result
}

func prependAdapterEnv(command string, rc *config.RuntimeConfig, runtimeConfigDir string, extraEnv map[string]string) string {
	envVars := cloneStringMap(extraEnv)
	if envVars == nil {
		envVars = make(map[string]string)
	}
	if rc != nil {
		for key, value := range rc.Env {
			envVars[key] = value
		}
		if rc.CLIURL != "" {
			envVars["COPILOT_CLI_URL"] = rc.CLIURL
		}
		if rc.Session != nil && rc.Session.ConfigDirEnv != "" && runtimeConfigDir != "" {
			envVars[rc.Session.ConfigDirEnv] = runtimeConfigDir
		}
	}
	return config.PrependEnv(command, envVars)
}

func adapterSessionEnv(req SessionLaunchRequest, rc *config.RuntimeConfig, resolvedAgent, runtimeSessionID string) map[string]string {
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:             req.Role,
		Rig:              req.RigName,
		AgentName:        req.AgentName,
		TownRoot:         req.TownRoot,
		RuntimeConfigDir: req.RuntimeConfigDir,
		Agent:            req.Provider,
		SessionName:      req.SessionName,
	})
	return mergeAdapterSessionEnv(envVars, rc, resolvedAgent, runtimeSessionID, req.Env)
}

func adapterSessionEnvFromResume(req SessionResumeRequest, rc *config.RuntimeConfig, resolvedAgent string) map[string]string {
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:             req.Role,
		Rig:              req.RigName,
		AgentName:        req.AgentName,
		TownRoot:         req.TownRoot,
		RuntimeConfigDir: req.RuntimeConfigDir,
		Agent:            req.Provider,
		SessionName:      req.SessionName,
	})
	return mergeAdapterSessionEnv(envVars, rc, resolvedAgent, req.SessionID, req.Env)
}

func mergeAdapterSessionEnv(base map[string]string, rc *config.RuntimeConfig, resolvedAgent, runtimeSessionID string, extra map[string]string) map[string]string {
	envVars := cloneStringMap(base)
	if envVars == nil {
		envVars = make(map[string]string)
	}
	if resolvedAgent != "" {
		envVars["GT_AGENT"] = resolvedAgent
	}
	command := ""
	if rc != nil {
		command = rc.Command
		if rc.CLIURL != "" {
			envVars["COPILOT_CLI_URL"] = rc.CLIURL
		}
		if rc.Session != nil && rc.Session.SessionIDEnv != "" {
			envVars["GT_SESSION_ID_ENV"] = rc.Session.SessionIDEnv
			if runtimeSessionID != "" {
				envVars[rc.Session.SessionIDEnv] = runtimeSessionID
			}
		}
	}
	processNames := config.ResolveProcessNames(envVars["GT_AGENT"], command)
	if len(processNames) > 0 {
		envVars["GT_PROCESS_NAMES"] = joinCSV(processNames)
	}
	for key, value := range extra {
		envVars[key] = value
	}
	return envVars
}

func joinCSV(values []string) string {
	if len(values) == 0 {
		return ""
	}
	copyValues := append([]string(nil), values...)
	sort.Strings(copyValues)
	return copyValues[0] + func() string {
		if len(copyValues) == 1 {
			return ""
		}
		result := ""
		for _, value := range copyValues[1:] {
			result += "," + value
		}
		return result
	}()
}

func (a *TmuxSessionAdapter) startSession(sessionName, workDir, command string, rc *config.RuntimeConfig) error {
	exists, err := a.tmux.HasSession(sessionName)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if exists {
		return fmt.Errorf("session already exists: %s", sessionName)
	}
	if err := a.tmux.NewSessionWithCommand(sessionName, workDir, command); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}
	if err := a.tmux.WaitForRuntimeReady(sessionName, rc, a.startupTimeout); err != nil {
		return fmt.Errorf("waiting for runtime ready: %w", err)
	}
	return nil
}

func (a *TmuxSessionAdapter) setSessionEnvironment(sessionName string, envVars map[string]string) error {
	for _, key := range sortedKeys(envVars) {
		if err := a.tmux.SetEnvironment(sessionName, key, envVars[key]); err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
	}
	return nil
}

func sortedKeys(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
