package runtime

import (
	"context"
	"fmt"
	"os"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotbridge"
	"github.com/steveyegge/gastown/internal/copilotutil"
	"github.com/steveyegge/gastown/internal/telemetry"
	"github.com/steveyegge/gastown/internal/toolpolicy"
)

type externalSessionConnector interface {
	Start(ctx context.Context, req SessionLaunchRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error)
	Resume(ctx context.Context, req SessionResumeRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error)
	Lookup(ctx context.Context, req SessionLookupRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error)
}

type copilotExternalSessionConnector struct{}

func (copilotExternalSessionConnector) Start(ctx context.Context, req SessionLaunchRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	client, err := newExternalCopilotClient(rc, req.WorkDir)
	if err != nil {
		return nil, err
	}
	policy := resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly)
	tools, availableTools, excludedTools, err := copilotbridge.Tools(ctx, copilotbridge.SessionContext{Binding: copilotbridge.BindingInfo{IssueID: req.IssueID, Role: req.Role, RigName: req.RigName, AgentName: req.AgentName, WorkDir: req.WorkDir, Metadata: cloneStringMap(req.Metadata)}, TownRoot: req.TownRoot, RigPath: req.RigPath, WorkDir: req.WorkDir, Policy: policy, Hooks: req.ToolCallbacks})
	if err != nil {
		copilotutil.ForceStopClientQuietly(client)
		return nil, fmt.Errorf("building external copilot tools: %w", err)
	}
	session, err := client.CreateSession(ctx, &copilot.SessionConfig{
		OnPermissionRequest: toolpolicy.PermissionHandler(policy),
		Tools:               tools,
		AvailableTools:      append([]string(nil), availableTools...),
		ExcludedTools:       append([]string(nil), excludedTools...),
		WorkingDirectory:    req.WorkDir,
	})
	if err != nil {
		copilotutil.ForceStopClientQuietly(client)
		return nil, fmt.Errorf("creating external copilot session: %w", err)
	}
	return &externalCopilotManagedSession{
		client:      client,
		session:     session,
		provider:    resolvedAgent,
		role:        req.Role,
		issueID:     req.IssueID,
		sessionName: req.SessionName,
	}, nil
}

func (copilotExternalSessionConnector) Resume(ctx context.Context, req SessionResumeRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	client, err := newExternalCopilotClient(rc, req.WorkDir)
	if err != nil {
		return nil, err
	}
	policy := resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly)
	tools, availableTools, excludedTools, err := copilotbridge.Tools(ctx, copilotbridge.SessionContext{Binding: copilotbridge.BindingInfo{IssueID: req.IssueID, Role: req.Role, RigName: req.RigName, AgentName: req.AgentName, WorkDir: req.WorkDir, Metadata: cloneStringMap(req.Metadata)}, TownRoot: req.TownRoot, RigPath: req.RigPath, WorkDir: req.WorkDir, Policy: policy, Hooks: req.ToolCallbacks})
	if err != nil {
		copilotutil.ForceStopClientQuietly(client)
		return nil, fmt.Errorf("building external copilot tools: %w", err)
	}
	session, err := client.ResumeSession(ctx, req.SessionID, &copilot.ResumeSessionConfig{
		OnPermissionRequest: toolpolicy.PermissionHandler(policy),
		Tools:               tools,
		AvailableTools:      append([]string(nil), availableTools...),
		ExcludedTools:       append([]string(nil), excludedTools...),
		WorkingDirectory:    req.WorkDir,
	})
	if err != nil {
		copilotutil.ForceStopClientQuietly(client)
		return nil, fmt.Errorf("resuming external copilot session: %w", err)
	}
	return &externalCopilotManagedSession{
		client:      client,
		session:     session,
		provider:    resolvedAgent,
		role:        req.Role,
		issueID:     req.IssueID,
		sessionName: req.SessionName,
		runtimeID:   req.SessionID,
	}, nil
}

func (copilotExternalSessionConnector) Lookup(ctx context.Context, req SessionLookupRequest, rc *config.RuntimeConfig, resolvedAgent string) (ManagedSession, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return nil, fmt.Errorf("runtime session id is required for external lookup")
	}
	client, err := newExternalCopilotClient(rc, req.WorkDir)
	if err != nil {
		return nil, err
	}
	policy := resolveExternalToolPolicy(req.Role, req.TownRoot, req.RigPath, req.WorkDir, req.SessionKind, req.Metadata, req.ToolPolicy, req.AllowedTools, req.ReadOnly)
	tools, availableTools, excludedTools, err := copilotbridge.Tools(ctx, copilotbridge.SessionContext{Binding: copilotbridge.BindingInfo{IssueID: req.IssueID, Role: req.Role, RigName: req.RigName, AgentName: req.AgentName, WorkDir: req.WorkDir, Metadata: cloneStringMap(req.Metadata)}, TownRoot: req.TownRoot, RigPath: req.RigPath, WorkDir: req.WorkDir, Policy: policy, Hooks: req.ToolCallbacks})
	if err != nil {
		copilotutil.ForceStopClientQuietly(client)
		return nil, fmt.Errorf("building external copilot tools: %w", err)
	}
	session, err := client.ResumeSession(ctx, req.SessionID, &copilot.ResumeSessionConfig{
		OnPermissionRequest: toolpolicy.PermissionHandler(policy),
		Tools:               tools,
		AvailableTools:      append([]string(nil), availableTools...),
		ExcludedTools:       append([]string(nil), excludedTools...),
		WorkingDirectory:    req.WorkDir,
		DisableResume:       true,
	})
	if err != nil {
		copilotutil.ForceStopClientQuietly(client)
		return nil, fmt.Errorf("looking up external copilot session: %w", err)
	}
	return &externalCopilotManagedSession{
		client:      client,
		session:     session,
		provider:    resolvedAgent,
		role:        req.Role,
		issueID:     req.IssueID,
		sessionName: req.SessionName,
		runtimeID:   req.SessionID,
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
	client      *copilot.Client
	session     *copilot.Session
	provider    string
	role        string
	issueID     string
	sessionName string
	runtimeID   string
	closed      bool
}

func (s *externalCopilotManagedSession) ID() string {
	if s.runtimeID != "" {
		return s.runtimeID
	}
	if s.session == nil {
		return ""
	}
	return s.session.SessionID
}

func (s *externalCopilotManagedSession) Status(_ context.Context) (SessionStatus, error) {
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
	if s.session == nil {
		return fmt.Errorf("external copilot session is not connected")
	}
	_, err := s.session.Send(ctx, copilot.MessageOptions{Prompt: message})
	if err != nil {
		return fmt.Errorf("sending to external copilot session: %w", err)
	}
	return nil
}

func (s *externalCopilotManagedSession) Close(_ context.Context) error {
	if s.closed {
		return nil
	}
	s.closed = true
	var result error
	if s.session != nil {
		if err := s.session.Disconnect(); err != nil {
			result = fmt.Errorf("disconnecting external copilot session: %w", err)
		}
	}
	if s.client != nil {
		if err := copilotutil.StopClientQuietly(s.client); err != nil && result == nil {
			result = fmt.Errorf("stopping external copilot client: %w", err)
		}
	}
	return result
}
