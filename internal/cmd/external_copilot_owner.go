package cmd

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotbridge"
	"github.com/steveyegge/gastown/internal/copilotutil"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/toolapi"
	"github.com/steveyegge/gastown/internal/toolcallbacks"
	"github.com/steveyegge/gastown/internal/toolpolicy"
)

var (
	externalOwnerConfigPath string
)

var externalCopilotOwnerCmd = &cobra.Command{
	Use:    "external-copilot-owner",
	Short:  "Internal external Copilot owner loop",
	Hidden: true,
	RunE:   runExternalCopilotOwner,
}

func init() {
	externalCopilotOwnerCmd.Flags().StringVar(&externalOwnerConfigPath, "config", "", "Owner config path")
	_ = externalCopilotOwnerCmd.MarkFlagRequired("config")
	rootCmd.AddCommand(externalCopilotOwnerCmd)
}

func runExternalCopilotOwner(cmd *cobra.Command, args []string) error {
	_ = args
	cfg, err := runtime.ReadExternalOwnerConfig(strings.TrimSpace(externalOwnerConfigPath))
	if err != nil {
		return err
	}
	rc, _, err := config.ResolveAgentConfigWithOverride(cfg.TownRoot, cfg.RigPath, cfg.Provider)
	if err != nil {
		return fmt.Errorf("resolving owner runtime config: %w", err)
	}
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: strings.TrimSpace(rc.CLIURL), Cwd: cfg.WorkDir})
	defer copilotutil.ForceStopClientQuietly(client)
	policy := cfg.ToolPolicy
	var (
		ownerStateMu sync.Mutex
		activeState  *ownerRequestState
	)
	hookState := func() *ownerRequestState {
		ownerStateMu.Lock()
		defer ownerStateMu.Unlock()
		return activeState
	}
	tools, availableTools, excludedTools, err := copilotbridge.Tools(context.Background(), copilotbridge.SessionContext{
		Binding:  copilotbridge.BindingInfo{IssueID: cfg.IssueID, Role: cfg.Role, RigName: cfg.RigName, AgentName: cfg.AgentName, WorkDir: cfg.WorkDir, Metadata: cfg.Metadata},
		TownRoot: cfg.TownRoot,
		RigPath:  cfg.RigPath,
		WorkDir:  cfg.WorkDir,
		Policy:   policy,
		Hooks: mergeCallbacks(toolcallbacks.ForTown(cfg.TownRoot, cfg.WorkDir), func(message string) {
			if state := hookState(); state != nil {
				state.fail(fmt.Errorf("tool execution failed: %s", strings.TrimSpace(message)))
			}
		}),
	})
	if err != nil {
		return fmt.Errorf("building owner tools: %w", err)
	}
	var session *copilot.Session
	resumeID := strings.TrimSpace(cfg.ResumeSessionID)
	if resumeID != "" {
		session, err = client.ResumeSession(context.Background(), resumeID, &copilot.ResumeSessionConfig{
			Model:               strings.TrimSpace(cfg.RequestedModel),
			ReasoningEffort:     strings.TrimSpace(cfg.ReasoningEffort),
			OnPermissionRequest: toolpolicy.PermissionHandler(policy),
			Tools:               tools,
			AvailableTools:      append([]string(nil), availableTools...),
			ExcludedTools:       append([]string(nil), excludedTools...),
			WorkingDirectory:    cfg.WorkDir,
			DisableResume:       true,
			Hooks:               ownerSessionHooks(hookState),
		})
	} else {
		session, err = client.CreateSession(context.Background(), &copilot.SessionConfig{
			Model:               strings.TrimSpace(cfg.RequestedModel),
			ReasoningEffort:     strings.TrimSpace(cfg.ReasoningEffort),
			OnPermissionRequest: toolpolicy.PermissionHandler(policy),
			Tools:               tools,
			AvailableTools:      append([]string(nil), availableTools...),
			ExcludedTools:       append([]string(nil), excludedTools...),
			WorkingDirectory:    cfg.WorkDir,
			Hooks:               ownerSessionHooks(hookState),
		})
	}
	if err != nil {
		_ = runtime.WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, runtime.ExternalCopilotOwnerStatus{OwnerPID: 0, RuntimeSessionID: resumeID, Error: err.Error(), UpdatedAt: time.Now().UTC()})
		return fmt.Errorf("starting owner session: %w", err)
	}
	defer session.Disconnect()
	if strings.TrimSpace(cfg.StartupPrompt) != "" {
		if _, err := session.Send(context.Background(), copilot.MessageOptions{Prompt: cfg.StartupPrompt}); err != nil {
			_ = runtime.WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, runtime.ExternalCopilotOwnerStatus{OwnerPID: 0, RuntimeSessionID: session.SessionID, Error: err.Error(), UpdatedAt: time.Now().UTC()})
			return fmt.Errorf("sending owner startup prompt: %w", err)
		}
	}
	ownerPID := 0
	if pid := os.Getpid(); pid > 0 {
		ownerPID = pid
	}
	if err := runtime.WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, runtime.ExternalCopilotOwnerStatus{OwnerPID: ownerPID, RuntimeSessionID: session.SessionID, UpdatedAt: time.Now().UTC()}); err != nil {
		return err
	}
	for {
		paths, err := runtime.NextExternalOwnerRequests(cfg.TownRoot, cfg.SessionName)
		if err != nil {
			return err
		}
		for _, path := range paths {
			claimedPath, err := runtime.ClaimExternalOwnerRequest(path)
			if err != nil {
				continue
			}
			_ = runtime.WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, runtime.ExternalCopilotOwnerStatus{OwnerPID: ownerPID, RuntimeSessionID: session.SessionID, Busy: true, UpdatedAt: time.Now().UTC()})
			request, err := runtime.ReadExternalOwnerRequest(claimedPath)
			if err != nil {
				_ = runtime.RemoveExternalOwnerRequest(claimedPath)
				_ = runtime.WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, runtime.ExternalCopilotOwnerStatus{OwnerPID: ownerPID, RuntimeSessionID: session.SessionID, Busy: false, UpdatedAt: time.Now().UTC()})
				continue
			}
			response := runtime.ExternalCopilotOwnerResponse{ID: request.ID, CreatedAt: time.Now().UTC()}
			writeResponse := true
			switch request.Kind {
			case runtime.ExternalOwnerRequestKindSend:
				_, err = session.Send(context.Background(), copilot.MessageOptions{Prompt: request.Message})
			case runtime.ExternalOwnerRequestKindAsk:
				var event *copilot.SessionEvent
				requestCtx := context.Background()
				cancel := func() {}
				if request.TimeoutMS > 0 {
					requestCtx, cancel = context.WithTimeout(context.Background(), time.Duration(request.TimeoutMS)*time.Millisecond)
				}
				state := newOwnerRequestState(request.ID, request.Message)
				ownerStateMu.Lock()
				activeState = state
				ownerStateMu.Unlock()
				event, err = ownerSendAndWaitForReply(requestCtx, session, state)
				ownerStateMu.Lock()
				activeState = nil
				ownerStateMu.Unlock()
				cancel()
				if err == nil && event != nil && event.Data.Content != nil {
					response.Content = strings.TrimSpace(*event.Data.Content)
				}
				if err == nil && strings.TrimSpace(response.Content) == "" {
					if reply := state.reply(); reply != nil && reply.Data.Content != nil {
						response.Content = strings.TrimSpace(*reply.Data.Content)
					}
				}
				if err == nil && strings.TrimSpace(response.Content) == "" && state.hasToolActivity() {
					response.Content = "DONE"
				}
			default:
				err = fmt.Errorf("unsupported owner request kind: %s", request.Kind)
			}
			if err != nil {
				response.Error = err.Error()
			}
			if strings.TrimSpace(response.Content) == "" && strings.TrimSpace(response.Error) == "" {
				writeResponse = false
			}
			if copilotutil.DebugOwnerToolsEnabled() {
				fmt.Fprintf(os.Stderr, "[owner-loop] request=%s kind=%s write=%t error=%q content=%q\n", request.ID, request.Kind, writeResponse, response.Error, response.Content)
			}
			if writeResponse {
				_ = runtime.WriteExternalOwnerResponse(cfg.TownRoot, cfg.SessionName, response)
			}
			_ = runtime.RemoveExternalOwnerRequest(claimedPath)
			_ = runtime.WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, runtime.ExternalCopilotOwnerStatus{OwnerPID: ownerPID, RuntimeSessionID: session.SessionID, Busy: false, UpdatedAt: time.Now().UTC(), Error: response.Error})
		}
		_ = runtime.WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, runtime.ExternalCopilotOwnerStatus{OwnerPID: ownerPID, RuntimeSessionID: session.SessionID, Busy: false, UpdatedAt: time.Now().UTC()})
		time.Sleep(200 * time.Millisecond)
	}
}

func ownerSendAndWaitForReply(ctx context.Context, sess *copilot.Session, state *ownerRequestState) (*copilot.SessionEvent, error) {
	eventCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	message := state.message
	if _, err := sess.Send(ctx, copilot.MessageOptions{Prompt: message}); err != nil {
		return nil, err
	}
	result := make(chan *copilot.SessionEvent, 1)
	errCh := make(chan error, 1)
	var lastAssistant *copilot.SessionEvent
	var sawUserMessage bool
	messageSent := make(chan struct{}, 1)
	unsubscribe := sess.On(func(event copilot.SessionEvent) {
		switch event.Type {
		case copilot.SessionEventTypeUserMessage:
			if event.Data.Content != nil && strings.TrimSpace(*event.Data.Content) == strings.TrimSpace(message) {
				sawUserMessage = true
				select {
				case messageSent <- struct{}{}:
				default:
				}
			}
		case copilot.SessionEventTypeAssistantMessage:
			if !sawUserMessage {
				return
			}
			eventCopy := event
			lastAssistant = &eventCopy
		case copilot.SessionEventTypeToolExecutionComplete, copilot.SessionEventTypeExternalToolCompleted:
			if !sawUserMessage {
				return
			}
			if event.Data.Success != nil && !*event.Data.Success {
				msg := "tool execution failed"
				if event.Data.Error != nil && event.Data.Error.ErrorClass != nil {
					msg = event.Data.Error.ErrorClass.Message
				}
				state.fail(fmt.Errorf("tool execution failed: %s", msg))
				select {
				case errCh <- state.err():
				default:
				}
				cancel()
			}
		case copilot.SessionEventTypeAssistantTurnEnd, copilot.SessionEventTypeSessionIdle, copilot.SessionEventTypeSessionShutdown:
			if !sawUserMessage {
				return
			}
			if lastAssistant != nil {
				state.complete(lastAssistant)
				select {
				case result <- lastAssistant:
				default:
				}
			} else if state.err() != nil {
				select {
				case errCh <- state.err():
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
	select {
	case <-messageSent:
	case <-time.After(3 * time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case reply := <-result:
		return reply, nil
	case err := <-errCh:
		return nil, err
	case <-eventCtx.Done():
		if err := state.err(); err != nil {
			return nil, err
		}
		if reply := state.reply(); reply != nil {
			return reply, nil
		}
		return nil, eventCtx.Err()
	}
}

func mergeCallbacks(base toolapi.Callbacks, report func(message string)) toolapi.Callbacks {
	base.ReportToolError = report
	return base
}

type ownerRequestState struct {
	id      string
	message string
	mu      sync.Mutex
	replyEv *copilot.SessionEvent
	errVal  error
	tools   map[string]bool
}

func newOwnerRequestState(id, message string) *ownerRequestState {
	return &ownerRequestState{id: id, message: message, tools: map[string]bool{}}
}

func (s *ownerRequestState) fail(err error) {
	if s == nil || err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.errVal == nil {
		s.errVal = err
	}
}

func (s *ownerRequestState) err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errVal
}

func (s *ownerRequestState) complete(event *copilot.SessionEvent) {
	if s == nil || event == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.replyEv == nil {
		eventCopy := *event
		s.replyEv = &eventCopy
	}
}

func (s *ownerRequestState) reply() *copilot.SessionEvent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.replyEv
}

func (s *ownerRequestState) hasToolActivity() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ok := range s.tools {
		if ok {
			return true
		}
	}
	return false
}

func (s *ownerRequestState) recordTool(name string, result any) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tools == nil {
		s.tools = map[string]bool{}
	}
	ok := result != nil
	if result != nil {
		value := reflect.ValueOf(result)
		if value.IsValid() {
			switch value.Kind() {
			case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
				ok = !value.IsNil()
			default:
				ok = true
			}
		}
	}
	s.tools[strings.TrimSpace(name)] = ok
}

func ownerSessionHooks(getState func() *ownerRequestState) *copilot.SessionHooks {
	return &copilot.SessionHooks{
		OnUserPromptSubmitted: func(input copilot.UserPromptSubmittedHookInput, invocation copilot.HookInvocation) (*copilot.UserPromptSubmittedHookOutput, error) {
			_ = invocation
			if state := getState(); state != nil && strings.TrimSpace(input.Prompt) == strings.TrimSpace(state.message) {
				return nil, nil
			}
			return nil, nil
		},
		OnPostToolUse: func(input copilot.PostToolUseHookInput, invocation copilot.HookInvocation) (*copilot.PostToolUseHookOutput, error) {
			_ = invocation
			if state := getState(); state != nil {
				state.recordTool(input.ToolName, input.ToolResult)
			}
			return nil, nil
		},
		OnErrorOccurred: func(input copilot.ErrorOccurredHookInput, invocation copilot.HookInvocation) (*copilot.ErrorOccurredHookOutput, error) {
			_ = invocation
			if state := getState(); state != nil {
				state.fail(fmt.Errorf("%s", strings.TrimSpace(input.Error)))
			}
			return nil, nil
		},
	}
}
