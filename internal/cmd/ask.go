package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotbridge"
	"github.com/steveyegge/gastown/internal/copilotutil"
	"github.com/steveyegge/gastown/internal/deacon"
	"github.com/steveyegge/gastown/internal/mayor"
	"github.com/steveyegge/gastown/internal/refinery"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/toolcallbacks"
	"github.com/steveyegge/gastown/internal/toolpolicy"
	"github.com/steveyegge/gastown/internal/witness"
	"github.com/steveyegge/gastown/internal/workspace"
)

var askTimeout time.Duration
var askDebug bool

var askCmd = &cobra.Command{
	Use:         "ask <target> <message>",
	GroupID:     GroupComm,
	Annotations: map[string]string{AnnotationPolecatSafe: "true"},
	Short:       "Send a request and wait for a reply",
	Long: `Ask sends a direct request to a headless external-backed role and waits
for the assistant's reply.

This is the request/reply companion to gt nudge. It is intended for headless
roles like mayor or deacon when they are backed by copilot-external.

Examples:
  gt ask mayor "Are you there?"
  gt ask deacon "What patrol issue is stuck?"`,
	Args: cobra.ExactArgs(2),
	RunE: runAsk,
}

func init() {
	askCmd.Flags().DurationVar(&askTimeout, "timeout", 60*time.Second, "How long to wait for a reply")
	askCmd.Flags().BoolVar(&askDebug, "debug-events", false, "Print recent Copilot session events on timeout/error")
	rootCmd.AddCommand(askCmd)
}

func runAsk(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	target := strings.TrimSpace(args[0])
	message := strings.TrimSpace(args[1])
	if message == "" {
		return fmt.Errorf("message cannot be empty")
	}
	sessionName, err := resolveAskSession(target)
	if err != nil {
		return err
	}
	binding, err := runtime.NewFileSessionBindingStore(townRoot).Load(context.Background(), sessionName, "", "", "")
	if err != nil {
		return fmt.Errorf("loading session binding: %w", err)
	}
	if binding == nil || strings.TrimSpace(binding.RuntimeSessionID) == "" {
		return fmt.Errorf("no managed session binding found for %s", target)
	}
	cliURL := strings.TrimSpace(binding.Metadata["cli_url"])
	if cliURL == "" {
		return fmt.Errorf("managed session for %s does not expose cli_url metadata", target)
	}
	if askUsesFreshSession(target) {
		response, err := askFreshSession(binding, message, cliURL)
		if err != nil {
			if askDebug {
				printAskDiagnostics(binding, cliURL)
			}
			return err
		}
		if response == nil || response.Data.Content == nil {
			return fmt.Errorf("no assistant reply received")
		}
		fmt.Println(strings.TrimSpace(*response.Data.Content))
		return nil
	}
	response, err := askManagedSession(binding, message, cliURL)
	if err != nil {
		if askDebug {
			printAskDiagnostics(binding, cliURL)
		}
		if !shouldRecoverAsk(err) {
			return err
		}
		if deleteErr := deleteManagedSession(binding, cliURL); deleteErr != nil && askDebug {
			fmt.Printf("[ask debug] delete failed: %v\n", deleteErr)
		}
		if restartErr := restartAskTarget(target, townRoot); restartErr != nil {
			return fmt.Errorf("waiting for reply: %w (restart failed: %v)", err, restartErr)
		}
		binding, loadErr := runtime.NewFileSessionBindingStore(townRoot).Load(context.Background(), sessionName, "", "", "")
		if loadErr != nil {
			return fmt.Errorf("reloading session binding after restart: %w", loadErr)
		}
		if binding == nil || strings.TrimSpace(binding.RuntimeSessionID) == "" {
			return fmt.Errorf("restarted %s but no managed session binding was found", target)
		}
		cliURL = strings.TrimSpace(binding.Metadata["cli_url"])
		if cliURL == "" {
			return fmt.Errorf("restarted %s but binding has no cli_url metadata", target)
		}
		response, err = askManagedSession(binding, message, cliURL)
		if err != nil {
			if askDebug {
				printAskDiagnostics(binding, cliURL)
			}
			return err
		}
	}
	if response == nil || response.Data.Content == nil {
		return fmt.Errorf("no assistant reply received")
	}
	fmt.Println(strings.TrimSpace(*response.Data.Content))
	return nil
}

func askUsesFreshSession(target string) bool {
	switch strings.TrimSpace(target) {
	case "witness", "refinery":
		return true
	default:
		return false
	}
}

func askFreshSession(binding *runtime.SessionBinding, message, cliURL string) (*copilot.SessionEvent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: cliURL, Cwd: binding.WorkDir})
	defer func() { _ = copilotutil.StopClientQuietly(client) }()
	policy := config.ResolveToolPolicy(binding.Role, config.ToolSessionKindAsk)
	tools, availableTools, excludedTools, err := copilotbridge.Tools(ctx, copilotbridge.SessionContext{Binding: askBindingInfo(binding), TownRoot: bindingTownRoot(binding), WorkDir: binding.WorkDir, Policy: policy, Hooks: toolcallbacks.ForTown(bindingTownRoot(binding), binding.WorkDir)})
	if err != nil {
		return nil, fmt.Errorf("building ask tools: %w", err)
	}
	sess, err := client.CreateSession(ctx, &copilot.SessionConfig{
		OnPermissionRequest: toolpolicy.PermissionHandler(policy),
		Tools:               tools,
		WorkingDirectory:    binding.WorkDir,
		AvailableTools:      append([]string(nil), availableTools...),
		ExcludedTools:       append([]string(nil), excludedTools...),
		SystemMessage:       askSystemMessage(binding),
	})
	if err != nil {
		return nil, fmt.Errorf("creating fresh ask session: %w", err)
	}
	defer sess.Disconnect()
	response, err := sendAndWaitForReply(ctx, sess, message)
	if err != nil {
		return nil, fmt.Errorf("waiting for reply: %w", err)
	}
	return response, nil
}

func askSystemMessage(binding *runtime.SessionBinding) *copilot.SystemMessageConfig {
	if binding == nil {
		return nil
	}
	content := "You are answering a direct operator question in a short-lived diagnostic session. " +
		"Do not continue autonomous patrol behavior. Do not invent unavailable tools or helper commands. " +
		"If shell or project-specific tools are unavailable, answer from the current session context and say what is unknown. " +
		"Prefer a concise direct answer over planning or role bootstrapping."
	if binding.Role == "witness" {
		content += " You are not being asked to re-run witness startup or patrol hooks. Answer the operator's question directly."
	}
	if binding.Role == "refinery" {
		content += " You are not being asked to resume refinery patrol. Answer directly from current context instead of attempting merge-queue automation."
	}
	return &copilot.SystemMessageConfig{Mode: "append", Content: content}
}

func printAskDiagnostics(binding *runtime.SessionBinding, cliURL string) {
	if binding == nil || strings.TrimSpace(binding.RuntimeSessionID) == "" || strings.TrimSpace(cliURL) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: cliURL, Cwd: binding.WorkDir})
	defer func() { _ = copilotutil.StopClientQuietly(client) }()
	sess, err := client.ResumeSession(ctx, binding.RuntimeSessionID, &copilot.ResumeSessionConfig{
		OnPermissionRequest: func(req copilot.PermissionRequest, _ copilot.PermissionInvocation) (copilot.PermissionRequestResult, error) {
			return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindDeniedByRules}, nil
		},
		WorkingDirectory: binding.WorkDir,
		DisableResume:    true,
	})
	if err != nil {
		fmt.Printf("[ask debug] resume failed: %v\n", err)
		return
	}
	defer sess.Disconnect()
	messages, err := sess.GetMessages(ctx)
	if err != nil {
		fmt.Printf("[ask debug] get messages failed: %v\n", err)
		return
	}
	start := max(0, len(messages)-12)
	for _, event := range messages[start:] {
		summary := string(event.Type)
		if event.Data.Message != nil && strings.TrimSpace(*event.Data.Message) != "" {
			summary += ": " + strings.TrimSpace(*event.Data.Message)
		} else if event.Data.Content != nil && strings.TrimSpace(*event.Data.Content) != "" {
			content := strings.Join(strings.Fields(strings.TrimSpace(*event.Data.Content)), " ")
			if len(content) > 120 {
				content = content[:117] + "..."
			}
			summary += ": " + content
		}
		fmt.Printf("[ask debug] %s\n", summary)
		if event.Type == copilot.SessionEventTypeSessionIdle && event.Data.BackgroundTasks != nil {
			fmt.Printf("[ask debug] idle background tasks present\n")
		}
	}
}

func deleteManagedSession(binding *runtime.SessionBinding, cliURL string) error {
	if binding == nil || strings.TrimSpace(binding.RuntimeSessionID) == "" || strings.TrimSpace(cliURL) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: cliURL, Cwd: binding.WorkDir})
	defer func() { _ = copilotutil.StopClientQuietly(client) }()
	if err := client.DeleteSession(ctx, binding.RuntimeSessionID); err != nil {
		return err
	}
	return runtime.NewFileSessionBindingStore(bindingTownRoot(binding)).Delete(context.Background(), binding.IssueID, binding.Role, binding.RigName, binding.AgentName)
}

func bindingTownRoot(binding *runtime.SessionBinding) string {
	if binding == nil {
		return ""
	}
	if binding.RigName != "" && binding.WorkDir != "" {
		marker := "/" + binding.RigName + "/"
		if idx := strings.Index(binding.WorkDir, marker); idx >= 0 {
			return binding.WorkDir[:idx]
		}
	}
	if binding.WorkDir != "" {
		parts := strings.Split(strings.TrimSuffix(binding.WorkDir, "/"), "/")
		if len(parts) > 1 {
			if binding.Role == "mayor" || binding.Role == "deacon" {
				return strings.Join(parts[:len(parts)-1], "/")
			}
		}
	}
	return ""
}

func askBindingInfo(binding *runtime.SessionBinding) copilotbridge.BindingInfo {
	if binding == nil {
		return copilotbridge.BindingInfo{}
	}
	return copilotbridge.BindingInfo{
		IssueID:   binding.IssueID,
		Role:      binding.Role,
		RigName:   binding.RigName,
		AgentName: binding.AgentName,
		WorkDir:   binding.WorkDir,
		Metadata:  binding.Metadata,
	}
}

func askManagedSession(binding *runtime.SessionBinding, message, cliURL string) (*copilot.SessionEvent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), askTimeout)
	defer cancel()
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: cliURL, Cwd: binding.WorkDir})
	defer func() { _ = copilotutil.StopClientQuietly(client) }()
	sessionKind := strings.TrimSpace(binding.Metadata["session_kind"])
	if sessionKind == "" {
		sessionKind = config.ToolSessionKindPatrol
	}
	policy := config.ResolveToolPolicyForSession(bindingTownRoot(binding), "", binding.Role, sessionKind, binding.WorkDir)
	tools, availableTools, excludedTools, err := copilotbridge.Tools(ctx, copilotbridge.SessionContext{Binding: askBindingInfo(binding), TownRoot: bindingTownRoot(binding), WorkDir: binding.WorkDir, Policy: policy, Hooks: toolcallbacks.ForTown(bindingTownRoot(binding), binding.WorkDir)})
	if err != nil {
		return nil, fmt.Errorf("building managed ask tools: %w", err)
	}
	sess, err := client.ResumeSession(ctx, binding.RuntimeSessionID, &copilot.ResumeSessionConfig{
		OnPermissionRequest: toolpolicy.PermissionHandler(policy),
		Tools:               tools,
		AvailableTools:      append([]string(nil), availableTools...),
		ExcludedTools:       append([]string(nil), excludedTools...),
		WorkingDirectory:    binding.WorkDir,
		DisableResume:       true,
		SystemMessage:       askSystemMessage(binding),
	})
	if err != nil {
		return nil, fmt.Errorf("resuming managed session: %w", err)
	}
	defer sess.Disconnect()
	response, err := sendAndWaitForReply(ctx, sess, message)
	if err != nil {
		return nil, fmt.Errorf("waiting for reply: %w", err)
	}
	return response, nil
}

func sendAndWaitForReply(ctx context.Context, sess *copilot.Session, message string) (*copilot.SessionEvent, error) {
	baseCount := 0
	if existing, err := sess.GetMessages(ctx); err == nil {
		baseCount = len(existing)
	}
	if _, err := sess.Send(ctx, copilot.MessageOptions{Prompt: message}); err != nil {
		return nil, err
	}
	result := make(chan *copilot.SessionEvent, 1)
	errCh := make(chan error, 1)
	var lastAssistant *copilot.SessionEvent
	var toolErr error
	unsubscribe := sess.On(func(event copilot.SessionEvent) {
		switch event.Type {
		case copilot.SessionEventTypeAssistantMessage:
			eventCopy := event
			lastAssistant = &eventCopy
		case copilot.SessionEventTypeToolExecutionComplete, copilot.SessionEventTypeExternalToolCompleted:
			if event.Data.Success != nil && !*event.Data.Success {
				msg := "tool execution failed"
				if event.Data.Error != nil && event.Data.Error.ErrorClass != nil {
					msg = event.Data.Error.ErrorClass.Message
				}
				toolErr = fmt.Errorf("tool execution failed: %s", msg)
			}
		case copilot.SessionEventTypeAssistantTurnEnd, copilot.SessionEventTypeSessionIdle, copilot.SessionEventTypeSessionShutdown:
			if lastAssistant != nil {
				select {
				case result <- lastAssistant:
				default:
				}
			} else if toolErr != nil {
				select {
				case errCh <- toolErr:
				default:
				}
			}
		case copilot.SessionEventTypeSessionError:
			msg := "session error"
			if event.Data.Message != nil {
				msg = *event.Data.Message
			}
			select {
			case errCh <- fmt.Errorf("session error: %s", msg):
			default:
			}
		}
	})
	defer unsubscribe()
	if existing, err := existingReply(sess, ctx, baseCount); err == nil && existing != nil {
		return existing, nil
	} else if err != nil {
		return nil, err
	}
	select {
	case reply := <-result:
		return reply, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		if existing, err := existingReply(sess, context.Background(), baseCount); err == nil && existing != nil {
			return existing, nil
		} else if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("waiting for session completion: %w", ctx.Err())
	}
}

func existingReply(sess *copilot.Session, ctx context.Context, baseCount int) (*copilot.SessionEvent, error) {
	messages, err := sess.GetMessages(ctx)
	if err != nil {
		return nil, err
	}
	if baseCount < 0 {
		baseCount = 0
	}
	if baseCount > len(messages) {
		baseCount = len(messages)
	}
	var lastAssistant *copilot.SessionEvent
	for i := len(messages) - 1; i >= baseCount; i-- {
		event := messages[i]
		switch event.Type {
		case copilot.SessionEventTypeAssistantMessage:
			if lastAssistant == nil {
				eventCopy := event
				lastAssistant = &eventCopy
			}
		case copilot.SessionEventTypeToolExecutionComplete, copilot.SessionEventTypeExternalToolCompleted:
			if event.Data.Success != nil && !*event.Data.Success {
				if event.Data.Error != nil && event.Data.Error.ErrorClass != nil {
					return nil, fmt.Errorf("tool execution failed: %s", event.Data.Error.ErrorClass.Message)
				}
				return nil, fmt.Errorf("tool execution failed")
			}
			if lastAssistant != nil {
				return lastAssistant, nil
			}
		case copilot.SessionEventTypeAssistantTurnEnd, copilot.SessionEventTypeSessionIdle, copilot.SessionEventTypeSessionShutdown:
			if lastAssistant != nil {
				return lastAssistant, nil
			}
		case copilot.SessionEventTypeSessionError:
			if event.Data.Message != nil {
				return nil, fmt.Errorf("session error: %s", *event.Data.Message)
			}
			return nil, fmt.Errorf("session error")
		}
	}
	return nil, nil
}

func shouldRecoverAsk(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "session error") ||
		strings.Contains(text, "400 bad request") ||
		strings.Contains(text, "typeerror") ||
		strings.Contains(text, "runtime is broken")
}

func restartAskTarget(target, townRoot string) error {
	switch target {
	case "mayor":
		mgr := mayor.NewManager(townRoot)
		_ = mgr.Stop()
		return mgr.Start("")
	case "deacon":
		mgr := deacon.NewManager(townRoot)
		_ = mgr.Stop()
		return mgr.Start("")
	case "witness", "refinery":
		roleInfo, err := GetRoleWithContext(bindingWorkDirFallback(townRoot), townRoot)
		if err != nil || strings.TrimSpace(roleInfo.Rig) == "" {
			roleInfo, err = GetRole()
			if err != nil {
				return fmt.Errorf("determining rig for %s restart: %w", target, err)
			}
		}
		_, r, err := getRig(roleInfo.Rig)
		if err != nil {
			return err
		}
		if target == "witness" {
			mgr := witness.NewManager(r)
			_ = mgr.Stop()
			return mgr.Start(false, "", nil)
		}
		mgr := refinery.NewManager(r)
		_ = mgr.Stop()
		return mgr.Start(false, "")
	default:
		return fmt.Errorf("ask restart not supported for %s", target)
	}
}

func bindingWorkDirFallback(townRoot string) string {
	if strings.TrimSpace(townRoot) == "" {
		return "."
	}
	return townRoot
}

func resolveAskSession(target string) (string, error) {
	switch strings.TrimSpace(target) {
	case "mayor":
		return session.MayorSessionName(), nil
	case "deacon":
		return session.DeaconSessionName(), nil
	case "witness", "refinery":
		roleInfo, err := GetRole()
		if err != nil {
			return "", fmt.Errorf("cannot determine rig for %s: %w", target, err)
		}
		if strings.TrimSpace(roleInfo.Rig) == "" {
			return "", fmt.Errorf("cannot determine rig for %s (not in a rig context)", target)
		}
		rigPrefix := session.PrefixFor(roleInfo.Rig)
		if target == "witness" {
			return session.WitnessSessionName(rigPrefix), nil
		}
		return session.RefinerySessionName(rigPrefix), nil
	default:
		return "", fmt.Errorf("ask currently supports headless role shortcuts: mayor, deacon, witness, refinery")
	}
}
