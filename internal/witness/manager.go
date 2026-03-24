package witness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/toolcallbacks"
	"github.com/steveyegge/gastown/internal/workspace"
)

type runtimeSessionStarter interface {
	Start(context.Context, runtime.SessionLaunchRequest) (runtime.ManagedSession, error)
	Lookup(context.Context, runtime.SessionLookupRequest) (runtime.ManagedSession, error)
}

// Common errors
var (
	ErrNotRunning     = errors.New("witness not running")
	ErrAlreadyRunning = errors.New("witness already running")
)

// Manager handles witness lifecycle and monitoring operations.
// ZFC-compliant: tmux session is the source of truth for running state.
type Manager struct {
	rig     *rig.Rig
	adapter runtimeSessionStarter
}

type ReviewSessionScope struct {
	IssueID           string   `json:"issue_id"`
	IssueTitle        string   `json:"issue_title,omitempty"`
	Phase             string   `json:"phase,omitempty"`
	AllowedTools      []string `json:"allowed_tools,omitempty"`
	ApprovedArtifacts []string `json:"approved_artifacts,omitempty"`
	ReviewArtifactID  string   `json:"review_artifact_id,omitempty"`
	BuilderSession    string   `json:"builder_session,omitempty"`
	FreshContext      bool     `json:"fresh_context"`
	ReadOnly          bool     `json:"read_only"`
}

// NewManager creates a new witness manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig: r,
	}
}

func (m *Manager) sessionAdapter() runtimeSessionStarter {
	if m.adapter != nil {
		return m.adapter
	}
	adapter := runtime.NewTmuxSessionAdapter(tmux.NewTmux())
	return adapter.WithBindingStore(runtime.NewFileSessionBindingStore(m.townRoot()))
}

func (m *Manager) loadWitnessBinding() (*runtime.SessionBinding, error) {
	if m == nil || m.rig == nil {
		return nil, fmt.Errorf("rig is required")
	}
	store := runtime.NewFileSessionBindingStore(m.townRoot())
	binding, err := store.Load(context.Background(), "", "witness", m.rig.Name, "witness")
	if err != nil {
		return nil, fmt.Errorf("loading witness binding: %w", err)
	}
	return binding, nil
}

func (m *Manager) lookupManagedSession(sessionName string) (runtime.ManagedSession, error) {
	binding, err := m.loadWitnessBinding()
	if err != nil {
		return nil, err
	}
	if binding == nil || binding.SessionName != sessionName || binding.RuntimeSessionID == "" {
		return nil, nil
	}
	return m.sessionAdapter().Lookup(context.Background(), runtime.SessionLookupRequest{
		SessionID:   binding.RuntimeSessionID,
		Provider:    binding.Provider,
		IssueID:     binding.IssueID,
		SessionName: binding.SessionName,
		Role:        binding.Role,
		TownRoot:    m.townRoot(),
		RigName:     m.rig.Name,
		RigPath:     m.rig.Path,
		AgentName:   binding.AgentName,
		WorkDir:     binding.WorkDir,
		Metadata:    binding.Metadata,
	})
}

func (m *Manager) deleteWitnessBinding() error {
	binding, err := m.loadWitnessBinding()
	if err != nil {
		return err
	}
	if binding == nil {
		return nil
	}
	store := runtime.NewFileSessionBindingStore(m.townRoot())
	if err := store.Delete(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName); err != nil {
		return fmt.Errorf("deleting witness binding: %w", err)
	}
	return nil
}

func (m *Manager) saveWitnessBinding(binding runtime.SessionBinding) error {
	store := runtime.NewFileSessionBindingStore(m.townRoot())
	if err := store.Save(context.Background(), binding); err != nil {
		return fmt.Errorf("saving witness binding: %w", err)
	}
	return nil
}

func (m *Manager) markWitnessStopping() error {
	binding, err := m.loadWitnessBinding()
	if err != nil {
		return err
	}
	if binding == nil {
		return nil
	}
	binding.LifecycleState = runtime.SessionLifecycleStopping
	binding.UpdatedAt = time.Now().UTC()
	return m.saveWitnessBinding(*binding)
}

func (m *Manager) lifecycleState() (string, error) {
	binding, err := m.loadWitnessBinding()
	if err != nil || binding == nil {
		return "", err
	}
	return binding.LifecycleState, nil
}

// LifecycleState returns the current witness lifecycle state.
func (m *Manager) LifecycleState() (string, error) {
	binding, bindErr := m.loadWitnessBinding()
	if bindErr == nil && binding != nil {
		if managed, err := m.lookupManagedSession(m.SessionName()); err == nil && managed != nil {
			status, statusErr := managed.Status(context.Background())
			return runtime.DeriveLifecycleState(binding, status.Alive, status.Ready, statusErr), nil
		}
		return runtime.DeriveLifecycleState(binding, false, false, fmt.Errorf("runtime status unavailable")), nil
	}
	if tmux.NewTmux().CheckSessionHealth(m.SessionName(), 0) == tmux.SessionHealthy {
		return runtime.SessionLifecycleRunning, nil
	}
	return runtime.SessionLifecycleStopped, nil
}

func (m *Manager) LaunchReviewSession(issueID string, workDir string, agentOverride string) (*runtime.SessionLaunchRequest, error) {
	if issueID == "" {
		return nil, fmt.Errorf("issue id is required")
	}
	townRoot := m.townRoot()
	if workDir == "" {
		workDir = m.witnessDir()
	}
	scope, err := m.BuildReviewSessionScope(issueID, workDir)
	if err != nil {
		return nil, err
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return nil, fmt.Errorf("encoding review scope: %w", err)
	}
	sessionID := reviewSessionName(session.PrefixFor(m.rig.Name), issueID)
	startupPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: session.BeaconRecipient("witness", "review", m.rig.Name),
		Sender:    "mayor",
		Topic:     "review",
		MolID:     issueID,
	}, buildReviewInstructions(scope))
	request := &runtime.SessionLaunchRequest{
		Provider:    agentOverride,
		IssueID:     issueID,
		SessionName: sessionID,
		Role:        "witness",
		SessionKind: config.ToolSessionKindReview,
		TownRoot:    townRoot,
		RigName:     m.rig.Name,
		RigPath:     m.rig.Path,
		AgentName:   "review",
		WorkDir:     workDir,
		Prompt:      startupPrompt,
		Env: map[string]string{
			"GT_REVIEW_SCOPE": string(scopeJSON),
		},
		Metadata: map[string]string{
			"session_kind":    "review",
			"fresh_context":   "true",
			"read_only":       "true",
			"artifact_scope":  strings.Join(scope.ApprovedArtifacts, ","),
			"allowed_tools":   strings.Join(scope.AllowedTools, ","),
			"builder_session": scope.BuilderSession,
		},
		AcceptStartupDialogs: true,
		ToolPolicy:           toolPolicyPtr(config.LegacyToolPolicy(workDir, scope.AllowedTools, true)),
		ToolCallbacks:        toolcallbacks.ForTown(townRoot, workDir),
	}
	if _, err := m.sessionAdapter().Start(context.Background(), *request); err != nil {
		return nil, fmt.Errorf("starting review runtime session: %w", err)
	}
	return request, nil
}

func (m *Manager) BuildReviewSessionScope(issueID string, workDir string) (*ReviewSessionScope, error) {
	if issueID == "" {
		return nil, fmt.Errorf("issue id is required")
	}
	if workDir == "" {
		workDir = m.witnessDir()
	}
	b := beads.New(workDir)
	issue, err := b.Show(issueID)
	if err != nil {
		return nil, fmt.Errorf("loading review issue: %w", err)
	}
	inspection := beads.InspectVSDDWorkflow(issue)
	if inspection == nil {
		return nil, fmt.Errorf("issue %s has no vsdd workflow state", issueID)
	}
	artifacts := beads.ParseVSDDArtifactFields(issue)
	if artifacts == nil {
		artifacts = &beads.VSDDArtifactFields{}
	}
	scope := &ReviewSessionScope{
		IssueID:           issueID,
		IssueTitle:        issue.Title,
		Phase:             string(inspection.Phase),
		AllowedTools:      config.RoleAllowedTools(m.townRoot(), m.rig.Path, "witness"),
		ApprovedArtifacts: approvedArtifactIDs(*artifacts),
		ReviewArtifactID:  artifacts.ReviewArtifactID,
		FreshContext:      true,
		ReadOnly:          true,
	}
	scope.BuilderSession = findBoundSessionName(m.townRoot(), issueID, "polecat")
	return scope, nil
}

func findBoundSessionName(townRoot, issueID, role string) string {
	entries, err := os.ReadDir(filepath.Join(townRoot, ".runtime", "session-bindings"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(townRoot, ".runtime", "session-bindings", entry.Name()))
		if readErr != nil {
			continue
		}
		var binding runtime.SessionBinding
		if json.Unmarshal(data, &binding) == nil && binding.IssueID == issueID && binding.Role == role {
			return binding.SessionName
		}
	}
	return ""
}

func approvedArtifactIDs(fields beads.VSDDArtifactFields) []string {
	artifacts := make([]string, 0, 7)
	appendIf := func(value string) {
		if value != "" {
			artifacts = append(artifacts, value)
		}
	}
	appendIf(fields.SpecArtifactID)
	appendIf(fields.SpecReviewArtifactID)
	appendIf(fields.TestPlanArtifactID)
	appendIf(fields.RedTestEvidenceID)
	appendIf(fields.ImplementationArtifactID)
	appendIf(fields.BuilderEvidenceID)
	appendIf(fields.ReviewArtifactID)
	return artifacts
}

func reviewSessionName(rigPrefix, issueID string) string {
	return fmt.Sprintf("%s-review-%s", rigPrefix, sanitizeReviewSessionPart(issueID))
}

func sanitizeReviewSessionPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "@", "-", ".", "-")
	value = replacer.Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	value = strings.Trim(value, "-")
	if value == "" {
		return "review"
	}
	return value
}

func buildReviewInstructions(scope *ReviewSessionScope) string {
	artifacts := "none"
	if len(scope.ApprovedArtifacts) > 0 {
		artifacts = strings.Join(scope.ApprovedArtifacts, ", ")
	}
	tools := strings.Join(scope.AllowedTools, ", ")
	return fmt.Sprintf("Review issue %s in fresh context only. Treat any prior builder conversation as non-evidence. Inspect only approved artifacts [%s]. Operate read-only and use only allowed reviewer tools [%s]. Record findings against the supplied evidence set.", scope.IssueID, artifacts, tools)
}

// IsRunning checks if the witness session is active and healthy.
// Checks both tmux session existence AND agent process liveness to avoid
// reporting zombie sessions (tmux alive but Claude dead) as "running".
// ZFC: tmux session existence is the source of truth for session state,
// but agent liveness determines if the session is actually functional.
func (m *Manager) IsRunning() (bool, error) {
	if state, err := m.lifecycleState(); err == nil && state == runtime.SessionLifecycleStopping {
		return false, nil
	}
	if managed, err := m.lookupManagedSession(m.SessionName()); err == nil && managed != nil {
		status, statusErr := managed.Status(context.Background())
		if statusErr == nil {
			return status.Alive, nil
		}
	}
	t := tmux.NewTmux()
	status := t.CheckSessionHealth(m.SessionName(), 0)
	return status == tmux.SessionHealthy, nil
}

// IsHealthy checks if the witness is running and has been active recently.
// Unlike IsRunning which only checks process liveness, this also detects hung
// sessions where Claude is alive but hasn't produced output in maxInactivity.
// Returns the detailed ZombieStatus for callers that need to distinguish
// between different failure modes.
func (m *Manager) IsHealthy(maxInactivity time.Duration) tmux.ZombieStatus {
	if state, err := m.lifecycleState(); err == nil && state == runtime.SessionLifecycleStopping {
		return tmux.SessionDead
	}
	t := tmux.NewTmux()
	return t.CheckSessionHealth(m.SessionName(), maxInactivity)
}

// SessionName returns the tmux session name for this witness.
func (m *Manager) SessionName() string {
	return session.WitnessSessionName(session.PrefixFor(m.rig.Name))
}

// Status returns information about the witness session.
// ZFC-compliant: tmux session is the source of truth.
func (m *Manager) Status() (*tmux.SessionInfo, error) {
	if state, err := m.lifecycleState(); err == nil && state == runtime.SessionLifecycleStopping {
		return &tmux.SessionInfo{Name: m.SessionName()}, nil
	}
	if managed, err := m.lookupManagedSession(m.SessionName()); err == nil && managed != nil {
		status, statusErr := managed.Status(context.Background())
		if statusErr == nil {
			if !status.Alive {
				return nil, ErrNotRunning
			}
			return &tmux.SessionInfo{Name: m.SessionName()}, nil
		}
	}
	t := tmux.NewTmux()
	sessionID := m.SessionName()

	running, err := t.HasSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	return t.GetSessionInfo(sessionID)
}

// witnessDir returns the working directory for the witness.
// Prefers witness/rig/, falls back to witness/, then rig root.
func (m *Manager) witnessDir() string {
	witnessRigDir := filepath.Join(m.rig.Path, "witness", "rig")
	if _, err := os.Stat(witnessRigDir); err == nil {
		return witnessRigDir
	}

	witnessDir := filepath.Join(m.rig.Path, "witness")
	if _, err := os.Stat(witnessDir); err == nil {
		return witnessDir
	}

	return m.rig.Path
}

// Start starts the witness.
// If foreground is true, returns an error (foreground mode deprecated).
// Otherwise, spawns a Claude agent in a tmux session.
// agentOverride optionally specifies a different agent alias to use.
// envOverrides are KEY=VALUE pairs that override all other env var sources.
// ZFC-compliant: no state file, tmux session is source of truth.
func (m *Manager) Start(foreground bool, agentOverride string, envOverrides []string) error {
	t := tmux.NewTmux()
	sessionID := m.SessionName()

	if foreground {
		// Foreground mode is deprecated - patrol logic moved to mol-witness-patrol
		return fmt.Errorf("foreground mode is deprecated; use background mode (remove --foreground flag)")
	}

	// Check if session already exists
	running, _ := t.HasSession(sessionID)
	if running {
		// Session exists - check if Claude is actually running (healthy vs zombie)
		if t.IsAgentAlive(sessionID) {
			// Healthy - Claude is running
			return ErrAlreadyRunning
		}
		// Zombie detected — tmux alive but agent dead.
		// Mitigate TOCTOU gap: the agent may be slow to start, appearing
		// dead during initialization. Record session creation time, wait
		// briefly, then re-verify before killing to avoid destroying a
		// session that just became healthy.
		createdAt, _ := t.GetSessionCreatedUnix(sessionID)
		time.Sleep(constants.ZombieKillGracePeriod)

		// Re-check: abort kill if agent started or session was replaced
		if t.IsAgentAlive(sessionID) {
			return ErrAlreadyRunning
		}
		if createdNow, _ := t.GetSessionCreatedUnix(sessionID); createdAt > 0 && createdNow != createdAt {
			// Session was replaced between checks — another process already
			// handled the zombie. Treat as already running; caller can retry.
			return ErrAlreadyRunning
		}

		if err := t.KillSession(sessionID); err != nil {
			return fmt.Errorf("killing zombie session: %w", err)
		}
	}

	// Note: No PID check per ZFC - tmux session is the source of truth

	// Working directory
	witnessDir := m.witnessDir()

	// Ensure runtime settings exist in the shared witness parent directory.
	// Settings are passed to Claude Code via --settings flag.
	// ResolveRoleAgentConfig is internally serialized (resolveConfigMu in
	// package config) to prevent concurrent rig starts from corrupting the
	// global agent registry.
	townRoot := m.townRoot()
	runtimeConfig := config.ResolveRoleAgentConfig("witness", townRoot, m.rig.Path)
	witnessSettingsDir := config.RoleSettingsDir("witness", m.rig.Path)
	if err := runtime.EnsureSettingsForRole(witnessSettingsDir, witnessDir, "witness", runtimeConfig); err != nil {
		return fmt.Errorf("ensuring runtime settings: %w", err)
	}

	// Ensure .gitignore has required Gas Town patterns
	if err := rig.EnsureGitignorePatterns(witnessDir); err != nil {
		style.PrintWarning("could not update witness .gitignore: %v", err)
	}

	roleConfig, err := m.roleConfig()
	if err != nil {
		// Non-fatal: role config is optional. Log and continue with defaults.
		log.Printf("warning: could not load witness role config for %s: %v", m.rig.Name, err)
		roleConfig = nil
	}

	// Build startup command first
	// NOTE: No gt prime injection needed - SessionStart hook handles it automatically
	// Export GT_ROLE and BD_ACTOR in the command since tmux SetEnvironment only affects new panes
	// Pass m.rig.Path so rig agent settings are honored (not town-level defaults)
	command, err := buildWitnessStartCommand(m.rig.Path, m.rig.Name, townRoot, sessionID, agentOverride, roleConfig)
	if err != nil {
		return err
	}
	initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: session.BeaconRecipient("witness", "", m.rig.Name),
		Sender:    "deacon",
		Topic:     "patrol",
	}, "Run `gt prime --hook` and begin patrol.")

	// Generate the GASTA run ID for this witness session.
	runID := uuid.New().String()

	resolvedAgentUsesAdapter := agentOverride != "" || !config.IsResolvedAgentClaude(runtimeConfig)

	// Use the runtime adapter for explicit agent overrides and non-Claude runtimes,
	// including Copilot external. Keep the direct tmux path only for Claude-backed
	// custom role launchers that rely on the historical start_command behavior.
	if roleConfig != nil && roleConfig.StartCommand != "" && agentOverride == "" && !resolvedAgentUsesAdapter {
		if err := t.NewSessionWithCommand(sessionID, witnessDir, command); err != nil {
			return fmt.Errorf("creating tmux session: %w", err)
		}
	} else {
		if _, err := m.sessionAdapter().Start(context.Background(), runtime.SessionLaunchRequest{
			Provider:             agentOverride,
			IssueID:              sessionID,
			SessionName:          sessionID,
			Role:                 "witness",
			SessionKind:          config.ToolSessionKindPatrol,
			TownRoot:             townRoot,
			RigName:              m.rig.Name,
			RigPath:              m.rig.Path,
			AgentName:            "witness",
			WorkDir:              witnessDir,
			Prompt:               initialPrompt,
			AcceptStartupDialogs: true,
			ToolPolicy:           toolPolicyPtr(config.ResolveToolPolicyForSession(townRoot, m.rig.Path, "witness", config.ToolSessionKindPatrol, witnessDir)),
			ToolCallbacks:        toolcallbacks.ForTown(townRoot, witnessDir),
		}); err != nil {
			return fmt.Errorf("starting witness runtime session: %w", err)
		}
	}

	// Set environment variables (non-fatal: session works without these)
	// Use centralized AgentEnv for consistency across all role startup paths
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:        "witness",
		Rig:         m.rig.Name,
		TownRoot:    townRoot,
		Agent:       agentOverride,
		SessionName: sessionID,
	})
	envVars = session.MergeRuntimeLivenessEnv(envVars, runtimeConfig)
	if !resolvedAgentUsesAdapter {
		for k, v := range envVars {
			_ = t.SetEnvironment(sessionID, k, v)
		}
		_ = t.SetEnvironment(sessionID, "GT_RUN", runID)
	}
	// Apply role config env vars if present (non-fatal).
	// Skip keys already set by AgentEnv to prevent TOML env overriding
	// the canonical qualified GT_ROLE (e.g., "gastown/witness" not "witness").
	// See: https://github.com/steveyegge/gastown/issues/2492
	if !resolvedAgentUsesAdapter {
		for key, value := range roleConfigEnvVars(roleConfig, townRoot, m.rig.Name) {
			if existing, alreadySet := envVars[key]; alreadySet {
				log.Printf("witness env: skipping TOML %s=%q (AgentEnv already set %q)", key, value, existing)
				continue
			}
			_ = t.SetEnvironment(sessionID, key, value)
		}
	}
	// Apply CLI env overrides (highest priority, non-fatal).
	if !resolvedAgentUsesAdapter {
		for _, override := range envOverrides {
			if key, value, ok := strings.Cut(override, "="); ok {
				_ = t.SetEnvironment(sessionID, key, value)
			}
		}
	}

	// Apply Gas Town theming (non-fatal: theming failure doesn't affect operation)
	if !resolvedAgentUsesAdapter {
		theme := tmux.ResolveSessionTheme(townRoot, m.rig.Name, "witness")
		_ = t.ConfigureGasTownSession(sessionID, theme, m.rig.Name, "witness", "witness")
	}

	if roleConfig != nil && roleConfig.StartCommand != "" && agentOverride == "" && !resolvedAgentUsesAdapter {
		// Wait for Claude to start - fatal if Claude fails to launch
		if err := t.WaitForCommand(sessionID, constants.SupportedShells, constants.ClaudeStartTimeout); err != nil {
			// Kill the zombie session before returning error
			_ = t.KillSessionWithProcesses(sessionID)
			return fmt.Errorf("waiting for witness to start: %w", err)
		}

		// Accept startup dialogs (workspace trust + bypass permissions) if they appear.
		if err := t.AcceptStartupDialogs(sessionID); err != nil {
			log.Printf("warning: accepting startup dialogs for %s: %v", sessionID, err)
		}
	}

	// Track PID for defense-in-depth orphan cleanup (non-fatal)
	if !resolvedAgentUsesAdapter {
		if err := session.TrackSessionPID(townRoot, sessionID, t); err != nil {
			log.Printf("warning: tracking session PID for %s: %v", sessionID, err)
		}
	}

	if !resolvedAgentUsesAdapter {
		_ = runtime.RunStartupFallback(t, sessionID, "witness", runtimeConfig)
		_ = runtime.DeliverStartupPromptFallback(t, sessionID, initialPrompt, runtimeConfig, constants.ClaudeStartTimeout)
	}

	// Stream witness's Claude Code JSONL conversation log to VictoriaLogs (opt-in).
	if !resolvedAgentUsesAdapter && os.Getenv("GT_LOG_AGENT_OUTPUT") == "true" && os.Getenv("GT_OTEL_LOGS_URL") != "" {
		if err := session.ActivateAgentLogging(sessionID, witnessDir, runID); err != nil {
			log.Printf("warning: agent log watcher setup failed for %s: %v", sessionID, err)
		}
	}

	// Record the agent instantiation event (GASTA root span).
	session.RecordAgentInstantiateFromDir(context.Background(), runID, runtimeConfig.ResolvedAgent,
		"witness", "witness", sessionID, m.rig.Name, townRoot, "", witnessDir)

	time.Sleep(constants.ShutdownNotifyDelay)

	return nil
}

func toolPolicyPtr(policy config.ToolPolicy) *config.ToolPolicy {
	return &policy
}

func (m *Manager) roleConfig() (*beads.RoleConfig, error) {
	townRoot := m.townRoot()
	roleDef, err := config.LoadRoleDefinition(townRoot, m.rig.Path, "witness")
	if err != nil {
		return nil, fmt.Errorf("loading witness role config: %w", err)
	}
	return &beads.RoleConfig{
		SessionPattern: roleDef.Session.Pattern,
		WorkDirPattern: roleDef.Session.WorkDir,
		NeedsPreSync:   roleDef.Session.NeedsPreSync,
		StartCommand:   roleDef.Session.StartCommand,
		EnvVars:        roleDef.Env,
	}, nil
}

func (m *Manager) townRoot() string {
	townRoot, err := workspace.Find(m.rig.Path)
	if err != nil || townRoot == "" {
		return m.rig.Path
	}
	return townRoot
}

func roleConfigEnvVars(roleConfig *beads.RoleConfig, townRoot, rigName string) map[string]string {
	if roleConfig == nil || len(roleConfig.EnvVars) == 0 {
		return nil
	}
	expanded := make(map[string]string, len(roleConfig.EnvVars))
	for key, value := range roleConfig.EnvVars {
		expanded[key] = beads.ExpandRolePattern(value, townRoot, rigName, "", "witness", session.PrefixFor(rigName))
	}
	return expanded
}

func buildWitnessStartCommand(rigPath, rigName, townRoot, sessionName, agentOverride string, roleConfig *beads.RoleConfig) (string, error) {
	if agentOverride != "" {
		roleConfig = nil
	}
	if roleConfig != nil && roleConfig.StartCommand != "" {
		rc := config.ResolveRoleAgentConfig("witness", townRoot, rigPath)
		if !config.IsResolvedAgentClaude(rc) {
			// Non-Claude agent: skip TOML start_command entirely.
			// Built-in role TOMLs hardcode "exec claude ..." which is wrong
			// for non-Claude agents. Fall through to BuildStartupCommandFromConfig
			// which uses the resolved agent's command and args.
		} else if !isBuiltinClaudeStartCommand(roleConfig.StartCommand) {
			// Custom (non-builtin) start_command with Claude agent: use TOML
			// pattern with template expansion.
			cmd := beads.ExpandRolePattern(roleConfig.StartCommand, townRoot, rigName, "", "witness", session.PrefixFor(rigName))
			if strings.HasPrefix(cmd, "exec ") {
				cmd = "exec env -u CLAUDECODE NODE_OPTIONS='' " + strings.TrimPrefix(cmd, "exec ")
			} else {
				cmd = "env -u CLAUDECODE NODE_OPTIONS='' " + cmd
			}
			return cmd, nil
		}
		// Non-Claude agent OR Claude with built-in start_command: fall
		// through to BuildStartupCommandFromConfig for proper agent and
		// model flag resolution.
	}
	initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: session.BeaconRecipient("witness", "", rigName),
		Sender:    "deacon",
		Topic:     "patrol",
	}, "Run `gt prime --hook` and begin patrol.")
	command, err := config.BuildStartupCommandFromConfig(config.AgentEnvConfig{
		Role:        "witness",
		Rig:         rigName,
		TownRoot:    townRoot,
		Prompt:      initialPrompt,
		Topic:       "patrol",
		SessionName: sessionName,
	}, rigPath, initialPrompt, agentOverride)
	if err != nil {
		return "", fmt.Errorf("building startup command: %w", err)
	}
	return command, nil
}

// isBuiltinClaudeStartCommand returns true if the start_command is the
// built-in default from role TOMLs ("exec claude --dangerously-skip-permissions").
// Custom start_commands (e.g., "exec run --town {town}") return false.
func isBuiltinClaudeStartCommand(cmd string) bool {
	trimmed := strings.TrimPrefix(cmd, "exec ")
	return trimmed == "claude --dangerously-skip-permissions"
}

// Stop stops the witness.
// ZFC-compliant: tmux session is the source of truth.
func (m *Manager) Stop() error {
	if managed, err := m.lookupManagedSession(m.SessionName()); err == nil && managed != nil {
		status, statusErr := managed.Status(context.Background())
		if statusErr == nil && status.Alive {
			_ = m.markWitnessStopping()
			if err := managed.Close(context.Background()); err != nil {
				return err
			}
			return m.deleteWitnessBinding()
		}
	}
	t := tmux.NewTmux()
	sessionID := m.SessionName()

	// Check if tmux session exists
	running, _ := t.HasSession(sessionID)
	if !running {
		return ErrNotRunning
	}

	// Kill the tmux session
	if err := t.KillSession(sessionID); err != nil {
		return err
	}
	return m.deleteWitnessBinding()
}
