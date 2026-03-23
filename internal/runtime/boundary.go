package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/toolapi"
)

type SessionLaunchRequest struct {
	Provider             string
	IssueID              string
	SessionName          string
	Role                 string
	SessionKind          string
	TownRoot             string
	RigName              string
	RigPath              string
	AgentName            string
	WorkDir              string
	Prompt               string
	RuntimeConfigDir     string
	AcceptStartupDialogs bool
	Env                  map[string]string
	Metadata             map[string]string
	ToolPolicy           *config.ToolPolicy
	ToolCallbacks        toolapi.Callbacks
	ReadOnly             bool
	AllowedTools         []string
}

type SessionResumeRequest struct {
	SessionID            string
	Provider             string
	IssueID              string
	SessionName          string
	Role                 string
	SessionKind          string
	TownRoot             string
	RigName              string
	RigPath              string
	AgentName            string
	WorkDir              string
	RuntimeConfigDir     string
	AcceptStartupDialogs bool
	Env                  map[string]string
	Metadata             map[string]string
	ToolPolicy           *config.ToolPolicy
	ToolCallbacks        toolapi.Callbacks
	ReadOnly             bool
	AllowedTools         []string
}

type SessionStatus struct {
	Provider  string
	SessionID string
	Ready     bool
	Alive     bool
	Busy      bool
}

type ManagedSession interface {
	ID() string
	Status(ctx context.Context) (SessionStatus, error)
	Send(ctx context.Context, message string) error
	Close(ctx context.Context) error
}

type SessionAdapter interface {
	Start(ctx context.Context, req SessionLaunchRequest) (ManagedSession, error)
	Resume(ctx context.Context, req SessionResumeRequest) (ManagedSession, error)
	Lookup(ctx context.Context, req SessionLookupRequest) (ManagedSession, error)
}

type SessionLookupRequest struct {
	SessionID     string
	Provider      string
	IssueID       string
	SessionName   string
	Role          string
	SessionKind   string
	TownRoot      string
	RigName       string
	RigPath       string
	AgentName     string
	WorkDir       string
	Metadata      map[string]string
	Env           map[string]string
	ToolPolicy    *config.ToolPolicy
	ToolCallbacks toolapi.Callbacks
	ReadOnly      bool
	AllowedTools  []string
}

type AuthRequest struct {
	Provider string
	Role     string
	RigPath  string
	WorkDir  string
	Env      map[string]string
}

type AuthResult struct {
	Env map[string]string
}

type AuthProvider interface {
	Prepare(ctx context.Context, req AuthRequest) (AuthResult, error)
}

type HookRequest struct {
	SettingsDir   string
	WorkDir       string
	Role          string
	RuntimeConfig *config.RuntimeConfig
}

type HookProvisioner interface {
	Ensure(ctx context.Context, req HookRequest) error
}

type ToolDefinition struct {
	Name        string
	Description string
	ReadOnly    bool
}

type ToolCall struct {
	Name      string
	Arguments map[string]string
}

type ToolResult struct {
	Text string
	Data map[string]string
}

type ToolCatalog interface {
	ToolsForRole(ctx context.Context, role string) ([]ToolDefinition, error)
}

type ToolExecutor interface {
	Execute(ctx context.Context, role string, call ToolCall) (ToolResult, error)
}

type Boundary struct {
	Sessions SessionAdapter
	Auth     AuthProvider
	Hooks    HookProvisioner
	Tools    ToolCatalog
	ToolExec ToolExecutor
}

func (b Boundary) Validate() error {
	missing := make([]string, 0, 5)
	if b.Sessions == nil {
		missing = append(missing, "sessions")
	}
	if b.Auth == nil {
		missing = append(missing, "auth")
	}
	if b.Hooks == nil {
		missing = append(missing, "hooks")
	}
	if b.Tools == nil {
		missing = append(missing, "tools")
	}
	if b.ToolExec == nil {
		missing = append(missing, "tool_exec")
	}
	if len(missing) == 0 {
		return nil
	}

	sort.Strings(missing)
	return fmt.Errorf("runtime boundary missing components: %s", strings.Join(missing, ", "))
}

type ConfigHookProvisioner struct{}

func (ConfigHookProvisioner) Ensure(_ context.Context, req HookRequest) error {
	return EnsureSettingsForRole(req.SettingsDir, req.WorkDir, req.Role, req.RuntimeConfig)
}

type StaticAuthProvider struct{}

func (StaticAuthProvider) Prepare(_ context.Context, req AuthRequest) (AuthResult, error) {
	return AuthResult{Env: cloneStringMap(req.Env)}, nil
}

type StaticToolCatalog struct {
	Default   []ToolDefinition
	RoleTools map[string][]ToolDefinition
}

func (c StaticToolCatalog) ToolsForRole(_ context.Context, role string) ([]ToolDefinition, error) {
	tools := c.Default
	if c.RoleTools != nil {
		if roleTools, ok := c.RoleTools[role]; ok {
			tools = roleTools
		}
	}
	return cloneTools(tools), nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneTools(values []ToolDefinition) []ToolDefinition {
	if values == nil {
		return nil
	}

	cloned := make([]ToolDefinition, len(values))
	copy(cloned, values)
	return cloned
}
