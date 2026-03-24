package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotutil"
	"github.com/steveyegge/gastown/internal/telemetry"
	"github.com/steveyegge/gastown/internal/toolapi"
)

type externalSessionConnector interface {
	Start(ctx context.Context, req SessionLaunchRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error)
	Resume(ctx context.Context, req SessionResumeRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error)
	Lookup(ctx context.Context, req SessionLookupRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error)
}

type copilotExternalSessionConnector struct{}

var launchExternalOwnerProcess = LaunchExternalOwnerProcess

func (copilotExternalSessionConnector) Start(ctx context.Context, req SessionLaunchRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	if strings.Contains(strings.TrimSpace(rc.CLIURL), "127.0.0.1") || strings.Contains(strings.TrimSpace(rc.CLIURL), "localhost") {
		if _, err := copilotutil.EnsureServer(ctx, req.TownRoot); err != nil {
			return nil, err
		}
	}
	policy := resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly)
	ownerStatus, err := launchExternalOwnerProcess(ctx, ExternalCopilotOwnerConfig{
		IssueID:          req.IssueID,
		Role:             req.Role,
		RigName:          req.RigName,
		RigPath:          req.RigPath,
		AgentName:        req.AgentName,
		Provider:         resolvedAgent,
		SessionName:      req.SessionName,
		SessionKind:      req.SessionKind,
		TownRoot:         req.TownRoot,
		WorkDir:          req.WorkDir,
		RuntimeConfigDir: req.RuntimeConfigDir,
		Metadata:         cloneStringMap(req.Metadata),
		ToolPolicy:       cloneToolPolicy(policy),
		StartupPrompt:    strings.TrimSpace(req.Prompt),
		RequestedModel:   strings.TrimSpace(rc.Model),
		ReasoningEffort:  strings.TrimSpace(rc.ReasoningEffort),
	})
	if err != nil {
		return nil, fmt.Errorf("starting external owner session: %w", err)
	}
	managed := &externalCopilotManagedSession{
		provider:    resolvedAgent,
		role:        req.Role,
		issueID:     req.IssueID,
		sessionName: req.SessionName,
		townRoot:    req.TownRoot,
		workDir:     req.WorkDir,
		rigPath:     req.RigPath,
		rigName:     req.RigName,
		agentName:   req.AgentName,
		sessionKind: req.SessionKind,
		metadata:    cloneStringMap(req.Metadata),
		toolPolicy:  policy,
		toolHooks:   req.ToolCallbacks,
	}
	managed.metadata = cloneStringMap(req.Metadata)
	if managed.metadata == nil {
		managed.metadata = make(map[string]string)
	}
	for key, value := range OwnerBindingMetadata(ExternalOwnerDir(req.TownRoot, req.SessionName), ownerStatus.OwnerPID) {
		managed.metadata[key] = value
	}
	managed.runtimeID = ownerStatus.RuntimeSessionID
	return managed, nil
}

func (copilotExternalSessionConnector) Resume(ctx context.Context, req SessionResumeRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	if strings.Contains(strings.TrimSpace(rc.CLIURL), "127.0.0.1") || strings.Contains(strings.TrimSpace(rc.CLIURL), "localhost") {
		if _, err := copilotutil.EnsureServer(ctx, req.TownRoot); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("runtime session id is required for external resume")
	}
	status, err := launchExternalOwnerProcess(ctx, ExternalCopilotOwnerConfig{
		IssueID:          req.IssueID,
		Role:             req.Role,
		RigName:          req.RigName,
		RigPath:          req.RigPath,
		AgentName:        req.AgentName,
		Provider:         resolvedAgent,
		SessionName:      req.SessionName,
		SessionKind:      req.SessionKind,
		TownRoot:         req.TownRoot,
		WorkDir:          req.WorkDir,
		RuntimeConfigDir: req.RuntimeConfigDir,
		Metadata:         cloneStringMap(req.Metadata),
		ToolPolicy:       cloneToolPolicy(resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly)),
		RequestedModel:   strings.TrimSpace(rc.Model),
		ReasoningEffort:  strings.TrimSpace(rc.ReasoningEffort),
		ResumeSessionID:  req.SessionID,
	})
	if err != nil {
		return nil, fmt.Errorf("resuming external owner session: %w", err)
	}
	metadata := cloneStringMap(req.Metadata)
	if metadata == nil {
		metadata = make(map[string]string)
	}
	for key, value := range OwnerBindingMetadata(ExternalOwnerDir(req.TownRoot, req.SessionName), status.OwnerPID) {
		metadata[key] = value
	}
	return &externalCopilotManagedSession{
		provider:    resolvedAgent,
		role:        req.Role,
		issueID:     req.IssueID,
		sessionName: req.SessionName,
		runtimeID:   status.RuntimeSessionID,
		townRoot:    req.TownRoot,
		workDir:     req.WorkDir,
		rigPath:     req.RigPath,
		rigName:     req.RigName,
		agentName:   req.AgentName,
		sessionKind: req.SessionKind,
		metadata:    metadata,
		toolPolicy:  resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly),
		toolHooks:   req.ToolCallbacks,
	}, nil
}

func (copilotExternalSessionConnector) Lookup(ctx context.Context, req SessionLookupRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	if IsExternalOwnerBinding(&SessionBinding{SessionName: req.SessionName, Metadata: req.Metadata}) {
		return &externalCopilotManagedSession{
			provider:    resolvedAgent,
			role:        req.Role,
			issueID:     req.IssueID,
			sessionName: req.SessionName,
			runtimeID:   req.SessionID,
			townRoot:    req.TownRoot,
			workDir:     req.WorkDir,
			rigPath:     req.RigPath,
			rigName:     req.RigName,
			agentName:   req.AgentName,
			sessionKind: req.SessionKind,
			metadata:    cloneStringMap(req.Metadata),
			toolPolicy:  resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly),
			toolHooks:   req.ToolCallbacks,
		}, nil
	}
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("runtime session id is required for external lookup")
	}
	return &externalCopilotManagedSession{
		provider:    resolvedAgent,
		role:        req.Role,
		issueID:     req.IssueID,
		sessionName: req.SessionName,
		runtimeID:   req.SessionID,
		townRoot:    req.TownRoot,
		workDir:     req.WorkDir,
		rigPath:     req.RigPath,
		rigName:     req.RigName,
		agentName:   req.AgentName,
		sessionKind: req.SessionKind,
		metadata:    cloneStringMap(req.Metadata),
		toolPolicy:  resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly),
		toolHooks:   req.ToolCallbacks,
	}, nil
}

func resolveExternalToolPolicy(role, townRoot, rigPath, workDir, sessionKind string, metadata map[string]string, explicit *config.ToolPolicy, allowedTools []string, readOnly bool) config.ToolPolicy {
	if explicit != nil {
		return cloneToolPolicy(*explicit)
	}
	resolvedKind := strings.TrimSpace(sessionKind)
	if resolvedKind == "" && metadata != nil {
		resolvedKind = strings.TrimSpace(metadata["session_kind"])
	}
	if resolvedKind != "" {
		return config.ResolveToolPolicyForSession(townRoot, rigPath, role, resolvedKind, workDir)
	}
	return config.LegacyToolPolicy(workDir, allowedTools, readOnly)
}

func cloneToolPolicy(policy config.ToolPolicy) config.ToolPolicy {
	cloned := config.ToolPolicy{
		AvailableTools: append([]string(nil), policy.AvailableTools...),
		ExcludedTools:  append([]string(nil), policy.ExcludedTools...),
	}
	if len(policy.ApprovalRules) > 0 {
		cloned.ApprovalRules = append([]config.ApprovalRule(nil), policy.ApprovalRules...)
	}
	return cloned
}

func debugExternalTools(phase, role, sessionName string, availableTools, excludedTools []string) {
	if !copilotutil.DebugCopilotToolsEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "[copilot-tools] phase=%s role=%s session=%s available=%s excluded=%s\n",
		phase,
		role,
		sessionName,
		strings.Join(availableTools, ","),
		strings.Join(excludedTools, ","),
	)
}

func debugExternalModel(ctx context.Context, session *copilot.Session, phase, role, sessionName, requestedModel, reasoningEffort string) {
	if !copilotutil.DebugCopilotModelEnabled() || session == nil || session.RPC == nil || session.RPC.Model == nil {
		return
	}
	info, err := session.RPC.Model.GetCurrent(ctx)
	resolved := ""
	if err == nil && info != nil && info.ModelID != nil {
		resolved = strings.TrimSpace(*info.ModelID)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "[copilot-model] phase=%s role=%s session=%s requested=%s effort=%s error=%v\n", phase, role, sessionName, requestedModel, reasoningEffort, err)
		return
	}
	fmt.Fprintf(os.Stderr, "[copilot-model] phase=%s role=%s session=%s requested=%s effort=%s resolved=%s\n", phase, role, sessionName, requestedModel, reasoningEffort, resolved)
}

func newExternalCopilotClient(rc *config.RuntimeConfig, workDir string) (*copilot.Client, error) {
	if !usesExternalServer(rc) {
		return nil, fmt.Errorf("external copilot client requires cli_url")
	}
	if strings.TrimSpace(rc.CLIURL) == "" {
		return nil, fmt.Errorf("external copilot client requires explicit cli_url trust boundary")
	}
	options := &copilot.ClientOptions{
		CLIUrl: strings.TrimSpace(rc.CLIURL),
	}
	if workDir != "" {
		options.Cwd = workDir
	}
	if otelConfig := copilotTelemetryConfig(); otelConfig != nil {
		options.Telemetry = otelConfig
	}
	client := copilot.NewClient(options)
	return client, nil
}

func copilotTelemetryConfig() *copilot.TelemetryConfig {
	metricsURL := strings.TrimSpace(os.Getenv(telemetry.EnvMetricsURL))
	logsURL := strings.TrimSpace(os.Getenv(telemetry.EnvLogsURL))
	if metricsURL == "" && logsURL == "" {
		return nil
	}
	endpoint := metricsURL
	if endpoint == "" {
		endpoint = logsURL
	}
	if endpoint == "" {
		return nil
	}
	captureContent := false
	return &copilot.TelemetryConfig{
		OTLPEndpoint:   endpoint,
		ExporterType:   "otlp-http",
		SourceName:     "gastown-copilot-external",
		CaptureContent: &captureContent,
	}
}

type externalCopilotManagedSession struct {
	provider    string
	role        string
	issueID     string
	sessionName string
	runtimeID   string
	townRoot    string
	workDir     string
	rigPath     string
	rigName     string
	agentName   string
	sessionKind string
	metadata    map[string]string
	toolPolicy  config.ToolPolicy
	toolHooks   toolapi.Callbacks
	closed      bool
}

func (s *externalCopilotManagedSession) ID() string {
	if s.runtimeID != "" {
		return s.runtimeID
	}
	return ""
}

func (s *externalCopilotManagedSession) Status(_ context.Context) (SessionStatus, error) {
	if IsExternalOwnerBinding(&SessionBinding{SessionName: s.sessionName, Metadata: s.metadata}) {
		discovery, err := DiscoverExternalOwner(s.townRoot, &SessionBinding{SessionName: s.sessionName, RuntimeSessionID: s.runtimeID, Metadata: s.metadata, WorkDir: s.workDir, RigName: s.rigName})
		if err != nil {
			return SessionStatus{Provider: s.provider, SessionID: s.ID(), Ready: false, Alive: false, Busy: false}, err
		}
		alive := discovery.OwnerAlive && strings.TrimSpace(s.runtimeID) != ""
		ready := alive && discovery.HeartbeatLive
		if discovery.Status != nil && discovery.Status.Ready != nil {
			ready = ready && *discovery.Status.Ready
		}
		busy := false
		if ready && discovery.Status != nil {
			busy = discovery.Status.Busy
		}
		status := SessionStatus{Provider: s.provider, SessionID: s.ID(), Ready: ready, Alive: alive, Busy: busy}
		if discovery.Status != nil && strings.TrimSpace(discovery.Status.Error) != "" {
			status.Ready = false
			status.Busy = false
			return status, fmt.Errorf("external owner reported error: %s", strings.TrimSpace(discovery.Status.Error))
		}
		return status, nil
	}
	return SessionStatus{
		Provider:  s.provider,
		SessionID: s.ID(),
		Ready:     !s.closed,
		Alive:     !s.closed,
		Busy:      false,
	}, nil
}

func (s *externalCopilotManagedSession) Send(ctx context.Context, message string) error {
	if strings.TrimSpace(message) == "" {
		return nil
	}
	if IsExternalOwnerBinding(&SessionBinding{SessionName: s.sessionName, Metadata: s.metadata}) {
		if err := SendExternalOwner(s.townRoot, &SessionBinding{SessionName: s.sessionName, Metadata: s.metadata}, message); err != nil {
			return fmt.Errorf("queueing external owner send: %w", err)
		}
		return nil
	}
	return fmt.Errorf("external copilot session is not owner-managed")
}

func (s *externalCopilotManagedSession) Close(_ context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	if IsExternalOwnerBinding(&SessionBinding{SessionName: s.sessionName, Metadata: s.metadata}) {
		if status, err := ReadExternalOwnerStatus(s.townRoot, s.sessionName); err == nil {
			if status.OwnerPID > 0 {
				if proc, findErr := os.FindProcess(status.OwnerPID); findErr == nil {
					_ = proc.Kill()
				}
			}
		}
		_ = ResetExternalOwnerState(s.townRoot, s.sessionName)
		return nil
	}
	return nil
}
