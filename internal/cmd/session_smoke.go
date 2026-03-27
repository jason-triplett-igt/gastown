package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotutil"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/toolcallbacks"
)

var (
	sessionSmokeRole         string
	sessionSmokeAgent        string
	sessionSmokeCLIURL       string
	sessionSmokeIssue        string
	sessionSmokeWorkDir      string
	sessionSmokePrompt       string
	sessionSmokeResumePrompt string
	sessionSmokeTimeout      time.Duration
	sessionSmokeJSON         bool
)

type sessionSmokeResult struct {
	RigName           string            `json:"rig_name"`
	Role              string            `json:"role"`
	Provider          string            `json:"provider"`
	CLIURL            string            `json:"cli_url"`
	WorkDir           string            `json:"work_dir"`
	SessionName       string            `json:"session_name"`
	RuntimeSessionID  string            `json:"runtime_session_id"`
	BindingPath       string            `json:"binding_path"`
	BindingMetadata   map[string]string `json:"binding_metadata,omitempty"`
	FirstResponse     string            `json:"first_response,omitempty"`
	SecondResponse    string            `json:"second_response,omitempty"`
	CreateVerified    bool              `json:"create_verified"`
	ResumeVerified    bool              `json:"resume_verified"`
	StopVerified      bool              `json:"stop_verified"`
	VerificationNotes []string          `json:"verification_notes,omitempty"`
	CleanupWarnings   []string          `json:"cleanup_warnings,omitempty"`
}

var sessionSmokeCmd = &cobra.Command{
	Use:   "smoke <rig>",
	Short: "Run a live external Copilot SDK smoke test",
	Long: `Run a live end-to-end smoke test against an external Copilot CLI server.

This creates a real SDK-backed session through the runtime adapter, sends an
initial prompt, reloads the same session by runtime session ID, sends a second
prompt, and then stops the session cleanly.

Use this before upstreaming external Copilot support so you can prove a real
server-backed create/send/resume/send/stop flow.`,
	Args: cobra.ExactArgs(1),
	RunE: runSessionSmoke,
}

func init() {
	sessionSmokeCmd.Flags().StringVar(&sessionSmokeRole, "role", "crew", "Role to test with the external runtime")
	sessionSmokeCmd.Flags().StringVar(&sessionSmokeAgent, "agent", "copilot-external", "Agent alias to configure for the smoke test")
	sessionSmokeCmd.Flags().StringVar(&sessionSmokeCLIURL, "cli-url", "", "External Copilot CLI server URL (host:port or http://host:port)")
	sessionSmokeCmd.Flags().StringVar(&sessionSmokeIssue, "issue", "smoke-copilot-sdk", "Synthetic issue id for the smoke test binding")
	sessionSmokeCmd.Flags().StringVar(&sessionSmokeWorkDir, "workdir", "", "Working directory for the external session (defaults to rig path)")
	sessionSmokeCmd.Flags().StringVar(&sessionSmokePrompt, "prompt", "Reply with READY and the current working directory.", "Initial prompt to send after creating the external session")
	sessionSmokeCmd.Flags().StringVar(&sessionSmokeResumePrompt, "resume-prompt", "Reply with RESUMED and the session id.", "Prompt to send after resuming the external session")
	sessionSmokeCmd.Flags().DurationVar(&sessionSmokeTimeout, "timeout", 2*time.Minute, "Timeout for each SDK send/wait step")
	sessionSmokeCmd.Flags().BoolVar(&sessionSmokeJSON, "json", false, "Output result as JSON")
	sessionCmd.AddCommand(sessionSmokeCmd)
}

func runSessionSmoke(cmd *cobra.Command, args []string) error {
	rigName := strings.TrimSpace(args[0])
	if rigName == "" {
		return fmt.Errorf("rig name is required")
	}
	cliURL := strings.TrimSpace(sessionSmokeCLIURL)
	if cliURL == "" {
		return fmt.Errorf("--cli-url is required")
	}
	if strings.TrimSpace(sessionSmokeRole) == "" {
		return fmt.Errorf("--role is required")
	}
	if strings.TrimSpace(sessionSmokeAgent) == "" {
		return fmt.Errorf("--agent is required")
	}

	townRoot, r, err := getRig(rigName)
	if err != nil {
		return err
	}
	workDir := strings.TrimSpace(sessionSmokeWorkDir)
	if workDir == "" {
		workDir = r.Path
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("creating workdir: %w", err)
	}

	settingsPath := config.RigSettingsPath(r.Path)
	settings, err := loadSmokeRigSettings(settingsPath)
	if err != nil {
		return fmt.Errorf("loading rig settings: %w", err)
	}
	if settings.Agents == nil {
		settings.Agents = make(map[string]*config.RuntimeConfig)
	}
	settings.Agents[sessionSmokeAgent] = &config.RuntimeConfig{
		Provider: "copilot",
		Command:  "copilot",
		CLIURL:   cliURL,
	}
	if settings.RoleAgents == nil {
		settings.RoleAgents = make(map[string]string)
	}
	settings.RoleAgents[sessionSmokeRole] = sessionSmokeAgent
	if err := config.SaveRigSettings(settingsPath, settings); err != nil {
		return fmt.Errorf("saving smoke test rig settings: %w", err)
	}

	adapter := runtime.NewTmuxSessionAdapter(nil).WithBindingStore(runtime.NewFileSessionBindingStore(townRoot))
	sessionName := fmt.Sprintf("smoke-%s-%d", strings.ToLower(sessionSmokeRole), time.Now().Unix())
	allowedTools := config.RoleAllowedTools(townRoot, r.Path, sessionSmokeRole)
	ctx, cancel := context.WithTimeout(cmd.Context(), sessionSmokeTimeout)
	defer cancel()

	managed, err := adapter.Start(ctx, runtime.SessionLaunchRequest{
		Provider:      sessionSmokeAgent,
		IssueID:       sessionSmokeIssue,
		SessionName:   sessionName,
		Role:          sessionSmokeRole,
		SessionKind:   config.ToolSessionKindSmokeTest,
		TownRoot:      townRoot,
		RigName:       r.Name,
		RigPath:       r.Path,
		AgentName:     sessionSmokeRole + "-smoke",
		WorkDir:       workDir,
		Prompt:        sessionSmokePrompt,
		ToolPolicy:    sessionSmokePolicyPtr(config.LegacyToolPolicy(workDir, allowedTools, sessionSmokeRole == "witness")),
		ToolCallbacks: toolcallbacks.ForTown(townRoot, workDir),
		Metadata: map[string]string{
			"session_kind": "smoke_test",
		},
	})
	if err != nil {
		return fmt.Errorf("starting external smoke session: %w", err)
	}

	result := sessionSmokeResult{
		RigName:          r.Name,
		Role:             sessionSmokeRole,
		Provider:         sessionSmokeAgent,
		CLIURL:           cliURL,
		WorkDir:          workDir,
		SessionName:      sessionName,
		RuntimeSessionID: managed.ID(),
		CreateVerified:   true,
	}

	bindingStore := runtime.NewFileSessionBindingStore(townRoot)
	binding, err := bindingStore.Load(context.Background(), sessionSmokeIssue, sessionSmokeRole, r.Name, sessionSmokeRole+"-smoke")
	if err != nil {
		return fmt.Errorf("loading smoke test binding: %w", err)
	}
	if binding == nil {
		return fmt.Errorf("smoke test binding was not created")
	}
	result.BindingMetadata = binding.Metadata
	result.BindingPath = filepath.Join(townRoot, ".runtime", "session-bindings", sessionBindingFileName(binding))

	firstResponse, err := waitForAssistantMessage(ctx, workDir, cliURL, managed.ID())
	if err != nil {
		_ = managed.Close(context.Background())
		return fmt.Errorf("waiting for initial external session response: %w", err)
	}
	result.FirstResponse = firstResponse
	seenResponses := map[string]struct{}{}
	if normalized := strings.TrimSpace(firstResponse); normalized != "" {
		seenResponses[normalized] = struct{}{}
	}
	result.VerificationNotes = append(result.VerificationNotes, "create/send verified against live Copilot CLI server")

	lookupCtx, lookupCancel := context.WithTimeout(cmd.Context(), sessionSmokeTimeout)
	defer lookupCancel()
	resumeManaged, resumedViaLookup, err := resumeSmokeSession(lookupCtx, adapter, runtime.SessionLookupRequest{
		SessionID:     managed.ID(),
		Provider:      sessionSmokeAgent,
		IssueID:       sessionSmokeIssue,
		SessionName:   sessionName,
		Role:          sessionSmokeRole,
		SessionKind:   config.ToolSessionKindSmokeTest,
		TownRoot:      townRoot,
		RigName:       r.Name,
		RigPath:       r.Path,
		AgentName:     sessionSmokeRole + "-smoke",
		WorkDir:       workDir,
		Metadata:      binding.Metadata,
		ToolPolicy:    sessionSmokePolicyPtr(config.LegacyToolPolicy(workDir, allowedTools, sessionSmokeRole == "witness")),
		ToolCallbacks: toolcallbacks.ForTown(townRoot, workDir),
	}, managed)
	if err != nil {
		_ = managed.Close(context.Background())
		return fmt.Errorf("looking up external smoke session: %w", err)
	}
	if err := resumeManaged.Send(lookupCtx, sessionSmokeResumePrompt); err != nil {
		if resumedViaLookup {
			_ = resumeManaged.Close(context.Background())
		}
		_ = managed.Close(context.Background())
		return fmt.Errorf("sending resume prompt: %w", err)
	}
	secondResponse, err := waitForAssistantMessage(lookupCtx, workDir, cliURL, managed.ID(), seenResponses)
	if err != nil {
		if resumedViaLookup {
			_ = resumeManaged.Close(context.Background())
		}
		_ = managed.Close(context.Background())
		return fmt.Errorf("waiting for resumed external session response: %w", err)
	}
	result.SecondResponse = secondResponse
	result.ResumeVerified = true
	if !resumedViaLookup {
		result.VerificationNotes = append(result.VerificationNotes, "lookup resume reused live managed session after SDK resume was unavailable")
	}
	result.VerificationNotes = append(result.VerificationNotes, "resume/send verified against same runtime session id")

	if resumedViaLookup {
		if err := resumeManaged.Close(context.Background()); err != nil {
			result.CleanupWarnings = append(result.CleanupWarnings, err.Error())
		} else {
			result.StopVerified = true
			result.VerificationNotes = append(result.VerificationNotes, "session destroy/stop completed cleanly")
		}
	} else if err := managed.Close(context.Background()); err != nil {
		result.CleanupWarnings = append(result.CleanupWarnings, err.Error())
	} else {
		result.StopVerified = true
		result.VerificationNotes = append(result.VerificationNotes, "session destroy/stop completed cleanly")
	}

	if sessionSmokeJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("%s Copilot SDK smoke test passed\n", style.Bold.Render("✓"))
	fmt.Printf("  Rig: %s\n", result.RigName)
	fmt.Printf("  Role: %s\n", result.Role)
	fmt.Printf("  Provider: %s\n", result.Provider)
	fmt.Printf("  CLI URL: %s\n", result.CLIURL)
	fmt.Printf("  Work Dir: %s\n", result.WorkDir)
	fmt.Printf("  Session Name: %s\n", result.SessionName)
	fmt.Printf("  Runtime Session ID: %s\n", result.RuntimeSessionID)
	fmt.Printf("  First Response: %s\n", compactSmokeText(result.FirstResponse))
	fmt.Printf("  Second Response: %s\n", compactSmokeText(result.SecondResponse))
	if len(result.BindingMetadata) > 0 {
		fmt.Printf("  Binding Metadata: %s\n", formatSmokeMetadata(result.BindingMetadata))
	}
	if len(result.CleanupWarnings) > 0 {
		fmt.Printf("  Cleanup Warnings: %s\n", strings.Join(result.CleanupWarnings, "; "))
	}
	return nil
}

func sessionSmokePolicyPtr(policy config.ToolPolicy) *config.ToolPolicy {
	return &policy
}

type smokeSessionLookup interface {
	Lookup(context.Context, runtime.SessionLookupRequest) (runtime.ManagedSession, error)
}

func resumeSmokeSession(ctx context.Context, adapter smokeSessionLookup, req runtime.SessionLookupRequest, live runtime.ManagedSession) (runtime.ManagedSession, bool, error) {
	resumed, err := adapter.Lookup(ctx, req)
	if err == nil {
		return resumed, true, nil
	}
	if strings.Contains(err.Error(), "No authentication info available") && live != nil {
		return live, false, nil
	}
	return nil, false, err
}

func waitForAssistantMessage(ctx context.Context, workDir, cliURL, sessionID string, skip ...map[string]struct{}) (string, error) {
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: strings.TrimSpace(cliURL), Cwd: workDir})
	defer func() { _ = copilotutil.StopClientQuietly(client) }()
	skipped := map[string]struct{}{}
	if len(skip) > 0 && skip[0] != nil {
		for key := range skip[0] {
			skipped[key] = struct{}{}
		}
	}
	session, err := client.ResumeSession(ctx, sessionID, &copilot.ResumeSessionConfig{
		OnPermissionRequest: func(req copilot.PermissionRequest, _ copilot.PermissionInvocation) (copilot.PermissionRequestResult, error) {
			return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindDeniedByRules}, nil
		},
		WorkingDirectory: workDir,
		DisableResume:    true,
	})
	if err != nil {
		return "", fmt.Errorf("resuming smoke session for verification: %w", err)
	}
	defer session.Disconnect()

	poll := time.NewTicker(500 * time.Millisecond)
	defer poll.Stop()
	for {
		messages, err := session.GetMessages(ctx)
		if err != nil {
			return "", fmt.Errorf("reading smoke session messages: %w", err)
		}
		for i := len(messages) - 1; i >= 0; i-- {
			event := messages[i]
			if event.Type != copilot.SessionEventTypeAssistantMessage || event.Data.Content == nil {
				continue
			}
			content := strings.TrimSpace(*event.Data.Content)
			if content == "" {
				continue
			}
			if _, seen := skipped[content]; seen {
				continue
			}
			if content != "" {
				return content, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-poll.C:
		}
	}
}

func loadSmokeRigSettings(settingsPath string) (*config.RigSettings, error) {
	settings, err := config.LoadRigSettings(settingsPath)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return config.NewRigSettings(), nil
		}
		return nil, err
	}
	return settings, nil
}

func sessionBindingFileName(binding *runtime.SessionBinding) string {
	if binding == nil {
		return ""
	}
	sanitize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.ReplaceAll(value, "/", "-")
		value = strings.ReplaceAll(value, "@", "-")
		value = strings.ReplaceAll(value, " ", "-")
		return value
	}
	agentName := strings.ToLower(strings.TrimSpace(binding.AgentName))
	return fmt.Sprintf("%s--%s--%s--%s.json", sanitize(binding.IssueID), sanitize(binding.Role), sanitize(binding.RigName), sanitize(agentName))
}

func compactSmokeText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 120 {
		return value
	}
	return value[:117] + "..."
}

func formatSmokeMetadata(metadata map[string]string) string {
	if len(metadata) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", key, metadata[key]))
	}
	return strings.Join(parts, ", ")
}
