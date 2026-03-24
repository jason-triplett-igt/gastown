package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotbridge"
	"github.com/steveyegge/gastown/internal/copilotutil"
	"github.com/steveyegge/gastown/internal/toolapi"
	"github.com/steveyegge/gastown/internal/toolcallbacks"
)

type eventRecord struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	Content string `json:"content,omitempty"`
	Model   string `json:"model,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Error   string `json:"error,omitempty"`
	Success *bool  `json:"success,omitempty"`
}

type toolCallRecord struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type toolResultContentRecord struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

type toolExecutionRecord struct {
	ToolCallID      string                    `json:"tool_call_id,omitempty"`
	ToolName        string                    `json:"tool_name,omitempty"`
	Success         *bool                     `json:"success,omitempty"`
	Error           string                    `json:"error,omitempty"`
	ResultContent   string                    `json:"result_content,omitempty"`
	DetailedContent string                    `json:"detailed_content,omitempty"`
	ResultKind      string                    `json:"result_kind,omitempty"`
	Contents        []toolResultContentRecord `json:"contents,omitempty"`
}

type mailRecord struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Priority string `json:"priority,omitempty"`
	Path     string `json:"path,omitempty"`
}

type nudgeRecord struct {
	SessionName string `json:"session_name"`
	Sender      string `json:"sender"`
	Message     string `json:"message"`
	Path        string `json:"path,omitempty"`
}

type runOutput struct {
	WorkDir          string                `json:"workdir"`
	Prompt           string                `json:"prompt"`
	RequestedModel   string                `json:"requested_model,omitempty"`
	ReasoningEffort  string                `json:"reasoning_effort,omitempty"`
	ResolvedModel    string                `json:"resolved_model,omitempty"`
	SessionID        string                `json:"session_id,omitempty"`
	FinalContent     string                `json:"final_content,omitempty"`
	EventTypes       []string              `json:"event_types,omitempty"`
	Events           []eventRecord         `json:"events,omitempty"`
	ToolCalls        []toolCallRecord      `json:"tool_calls,omitempty"`
	ToolExecutions   []toolExecutionRecord `json:"tool_executions,omitempty"`
	SimpleToolHits   int                   `json:"simple_tool_hits"`
	SimpleToolArgs   []string              `json:"simple_tool_args,omitempty"`
	ToolUseObserved  bool                  `json:"tool_use_observed"`
	ModelQueryError  string                `json:"model_query_error,omitempty"`
	SessionRunError  string                `json:"session_run_error,omitempty"`
	CreateError      string                `json:"create_error,omitempty"`
	ListModelsSample []string              `json:"list_models_sample,omitempty"`
	ArtifactDir      string                `json:"artifact_dir,omitempty"`
	MailCalls        []mailRecord          `json:"mail_calls,omitempty"`
	NudgeCalls       []nudgeRecord         `json:"nudge_calls,omitempty"`
}

func main() {
	var (
		cliURL              = flag.String("cli-url", "http://127.0.0.1:4321", "Copilot CLI server URL")
		workDir             = flag.String("workdir", "", "Working directory for the session")
		prompt              = flag.String("prompt", "", "Prompt to send")
		model               = flag.String("model", "", "Requested model id")
		reasoning           = flag.String("reasoning-effort", "", "Requested reasoning effort")
		resumeSessionID     = flag.String("resume-session-id", "", "Resume an existing session id instead of creating a new session")
		disableResume       = flag.Bool("disable-resume", false, "Use DisableResume when resuming a session")
		systemMessage       = flag.String("system-message", "", "Optional system message content to append to the session")
		timeout             = flag.Duration("timeout", 45*time.Second, "Overall timeout")
		streaming           = flag.Bool("streaming", true, "Enable streaming session events")
		approveAllPerms     = flag.Bool("approve-all-permissions", false, "Approve all permission requests for built-in tools")
		withSimpleTool      = flag.Bool("with-simple-tool", false, "Register a minimal local tool named simple_echo")
		withDelegationTools = flag.Bool("with-delegation-tools", false, "Register Gastown send_mail and nudge_agent tools with local artifact callbacks")
		listModels          = flag.Bool("list-models", false, "List models before creating the session")
		artifactDir         = flag.String("artifact-dir", "", "Directory where delegation tool side effects should be recorded")
		role                = flag.String("role", "mayor", "Role identity to use for Gastown tool bindings")
		rigName             = flag.String("rig-name", "gastown", "Rig name to use for Gastown tool bindings")
	)
	flag.Parse()

	if strings.TrimSpace(*workDir) == "" || strings.TrimSpace(*prompt) == "" {
		fmt.Fprintln(os.Stderr, "usage: go run tmp_copilot_repro.go --workdir <dir> --prompt <text> [--model <id>] [--reasoning-effort <effort>] [--with-simple-tool] [--list-models]")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: strings.TrimSpace(*cliURL), Cwd: *workDir})
	defer copilotutil.ForceStopClientQuietly(client)

	out := runOutput{
		WorkDir:         *workDir,
		Prompt:          *prompt,
		RequestedModel:  strings.TrimSpace(*model),
		ReasoningEffort: strings.TrimSpace(*reasoning),
	}

	if *listModels {
		models, err := client.ListModels(ctx)
		if err == nil {
			sample := make([]string, 0, len(models))
			for _, item := range models {
				sample = append(sample, item.ID)
			}
			sort.Strings(sample)
			if len(sample) > 12 {
				sample = sample[:12]
			}
			out.ListModelsSample = sample
		}
	}

	var (
		eventsMu       sync.Mutex
		events         []eventRecord
		eventTypesSeen = map[string]struct{}{}
		toolCalls      []toolCallRecord
		toolExecutions []toolExecutionRecord
		mailCalls      []mailRecord
		nudgeCalls     []nudgeRecord
		simpleToolHits int
		simpleToolArgs []string
	)

	tools := []copilot.Tool{}
	availableTools := []string{}
	if *withSimpleTool {
		tools = append(tools, copilot.Tool{
			Name:        "simple_echo",
			Description: "Echo a single value for validation",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"value": map[string]any{"type": "string"},
				},
				"required": []string{"value"},
			},
			Handler: func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
				args, err := toolArguments(inv.Arguments)
				if err != nil {
					return copilot.ToolResult{}, err
				}
				value := strings.TrimSpace(args["value"])
				eventsMu.Lock()
				defer eventsMu.Unlock()
				simpleToolHits++
				simpleToolArgs = append(simpleToolArgs, value)
				toolCalls = append(toolCalls, toolCallRecord{Name: "simple_echo", Arguments: map[string]any{"value": value}})
				payload, err := json.Marshal(map[string]string{"echo": value})
				if err != nil {
					return copilot.ToolResult{}, err
				}
				return copilot.ToolResult{TextResultForLLM: string(payload), ResultType: "success"}, nil
			},
		})
		availableTools = append(availableTools, "simple_echo")
	}
	if *withDelegationTools {
		artifacts := strings.TrimSpace(*artifactDir)
		if artifacts == "" {
			tmpDir, err := os.MkdirTemp("", "gastown-copilot-repro-*")
			if err != nil {
				out.CreateError = fmt.Sprintf("create artifact dir: %v", err)
				emit(out)
				os.Exit(1)
			}
			artifacts = tmpDir
		}
		if err := os.MkdirAll(artifacts, 0o755); err != nil {
			out.CreateError = fmt.Sprintf("create artifact dir: %v", err)
			emit(out)
			os.Exit(1)
		}
		out.ArtifactDir = artifacts
		gastownTools, gastownAvailable, _, err := copilotbridge.Tools(ctx, copilotbridge.SessionContext{
			Binding: copilotbridge.BindingInfo{
				Role:    strings.TrimSpace(*role),
				RigName: strings.TrimSpace(*rigName),
				WorkDir: *workDir,
			},
			TownRoot: *workDir,
			WorkDir:  *workDir,
			Policy:   config.ToolPolicy{AvailableTools: []string{"send_mail", "nudge_agent"}},
			Hooks:    delegationCallbacks(artifacts, &eventsMu, &mailCalls, &nudgeCalls),
		})
		if err != nil {
			out.CreateError = fmt.Sprintf("build delegation tools: %v", err)
			emit(out)
			os.Exit(1)
		}
		tools = append(tools, gastownTools...)
		availableTools = append(availableTools, gastownAvailable...)
	}

	var systemMessageConfig *copilot.SystemMessageConfig
	if strings.TrimSpace(*systemMessage) != "" {
		systemMessageConfig = &copilot.SystemMessageConfig{Mode: "append", Content: strings.TrimSpace(*systemMessage)}
	}
	var sess *copilot.Session
	var err error
	resumeID := strings.TrimSpace(*resumeSessionID)
	if resumeID != "" {
		sess, err = client.ResumeSession(ctx, resumeID, &copilot.ResumeSessionConfig{
			Model:               strings.TrimSpace(*model),
			ReasoningEffort:     strings.TrimSpace(*reasoning),
			WorkingDirectory:    *workDir,
			Tools:               tools,
			AvailableTools:      append([]string(nil), availableTools...),
			OnPermissionRequest: permissionHandler(*approveAllPerms),
			DisableResume:       *disableResume,
			SystemMessage:       systemMessageConfig,
			Streaming:           *streaming,
		})
	} else {
		sess, err = client.CreateSession(ctx, &copilot.SessionConfig{
			Model:               strings.TrimSpace(*model),
			ReasoningEffort:     strings.TrimSpace(*reasoning),
			WorkingDirectory:    *workDir,
			Tools:               tools,
			AvailableTools:      append([]string(nil), availableTools...),
			OnPermissionRequest: permissionHandler(*approveAllPerms),
			SystemMessage:       systemMessageConfig,
			Streaming:           *streaming,
		})
	}
	if err != nil {
		out.CreateError = err.Error()
		emit(out)
		os.Exit(1)
	}
	defer sess.Disconnect()
	out.SessionID = sess.SessionID

	if sess.RPC != nil && sess.RPC.Model != nil {
		if current, currentErr := sess.RPC.Model.GetCurrent(ctx); currentErr != nil {
			out.ModelQueryError = currentErr.Error()
		} else if current != nil && current.ModelID != nil {
			out.ResolvedModel = strings.TrimSpace(*current.ModelID)
		}
	}

	unsubscribe := sess.On(func(event copilot.SessionEvent) {
		rec := eventRecord{Type: string(event.Type)}
		if event.Data.Message != nil {
			rec.Message = strings.TrimSpace(*event.Data.Message)
		}
		if event.Data.Content != nil {
			rec.Content = strings.TrimSpace(*event.Data.Content)
		}
		if event.Data.Model != nil {
			rec.Model = strings.TrimSpace(*event.Data.Model)
		}
		if event.Data.ToolName != nil {
			rec.Tool = strings.TrimSpace(*event.Data.ToolName)
		}
		if event.Data.Error != nil && event.Data.Error.ErrorClass != nil {
			rec.Error = strings.TrimSpace(event.Data.Error.ErrorClass.Message)
		}
		if event.Data.Success != nil {
			success := *event.Data.Success
			rec.Success = &success
		}
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, rec)
		eventTypesSeen[rec.Type] = struct{}{}
		if rec.Tool != "" {
			toolCalls = append(toolCalls, toolCallRecord{Name: rec.Tool})
		}
		if event.Type == copilot.SessionEventTypeToolExecutionComplete {
			execRec := toolExecutionRecord{}
			if event.Data.ToolCallID != nil {
				execRec.ToolCallID = strings.TrimSpace(*event.Data.ToolCallID)
			}
			if event.Data.ToolName != nil {
				execRec.ToolName = strings.TrimSpace(*event.Data.ToolName)
			}
			if event.Data.Success != nil {
				success := *event.Data.Success
				execRec.Success = &success
			}
			if event.Data.Error != nil && event.Data.Error.ErrorClass != nil {
				execRec.Error = strings.TrimSpace(event.Data.Error.ErrorClass.Message)
			}
			if event.Data.Result != nil {
				if event.Data.Result.Content != nil {
					execRec.ResultContent = strings.TrimSpace(*event.Data.Result.Content)
				}
				if event.Data.Result.DetailedContent != nil {
					execRec.DetailedContent = strings.TrimSpace(*event.Data.Result.DetailedContent)
				}
				if event.Data.Result.Kind != nil {
					execRec.ResultKind = string(*event.Data.Result.Kind)
				}
				for _, content := range event.Data.Result.Contents {
					contentRec := toolResultContentRecord{Type: string(content.Type)}
					if content.Text != nil {
						contentRec.Text = strings.TrimSpace(*content.Text)
					}
					if content.Cwd != nil {
						contentRec.Cwd = strings.TrimSpace(*content.Cwd)
					}
					if content.ExitCode != nil {
						exitCode := int(*content.ExitCode)
						contentRec.ExitCode = &exitCode
					}
					execRec.Contents = append(execRec.Contents, contentRec)
				}
			}
			toolExecutions = append(toolExecutions, execRec)
		}
	})
	defer unsubscribe()

	resp, err := sess.SendAndWait(ctx, copilot.MessageOptions{Prompt: *prompt})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		out.SessionRunError = err.Error()
	} else if err != nil {
		out.SessionRunError = err.Error()
	}
	if resp != nil && resp.Data.Content != nil {
		out.FinalContent = strings.TrimSpace(*resp.Data.Content)
	}

	eventsMu.Lock()
	out.Events = append([]eventRecord(nil), events...)
	out.ToolCalls = append([]toolCallRecord(nil), toolCalls...)
	out.ToolExecutions = append([]toolExecutionRecord(nil), toolExecutions...)
	out.MailCalls = append([]mailRecord(nil), mailCalls...)
	out.NudgeCalls = append([]nudgeRecord(nil), nudgeCalls...)
	out.SimpleToolHits = simpleToolHits
	out.SimpleToolArgs = append([]string(nil), simpleToolArgs...)
	eventTypes := make([]string, 0, len(eventTypesSeen))
	for eventType := range eventTypesSeen {
		eventTypes = append(eventTypes, eventType)
	}
	eventsMu.Unlock()
	sort.Strings(eventTypes)
	out.EventTypes = eventTypes
	out.ToolUseObserved = out.SimpleToolHits > 0 || len(out.ToolCalls) > 0

	emit(out)
	if out.CreateError != "" || out.SessionRunError != "" {
		os.Exit(1)
	}
}

func denyAllPermissions(req copilot.PermissionRequest, inv copilot.PermissionInvocation) (copilot.PermissionRequestResult, error) {
	_ = inv
	if req.Kind == copilot.PermissionRequestKindCustomTool {
		return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindApproved}, nil
	}
	return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindDeniedByRules}, nil
}

func approveAllPermissions(req copilot.PermissionRequest, inv copilot.PermissionInvocation) (copilot.PermissionRequestResult, error) {
	_ = req
	_ = inv
	return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindApproved}, nil
}

func permissionHandler(approveAll bool) copilot.PermissionHandlerFunc {
	if approveAll {
		return approveAllPermissions
	}
	return denyAllPermissions
}

func emit(out runOutput) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "encode error: %v\n", err)
		os.Exit(1)
	}
}

func delegationCallbacks(artifactDir string, mu *sync.Mutex, mailCalls *[]mailRecord, nudgeCalls *[]nudgeRecord) toolapi.Callbacks {
	return toolapi.Callbacks{
		SendMail: func(from, to, subject, body, priority string) error {
			rec := mailRecord{
				From:     strings.TrimSpace(from),
				To:       strings.TrimSpace(to),
				Subject:  strings.TrimSpace(subject),
				Body:     strings.TrimSpace(body),
				Priority: strings.TrimSpace(priority),
			}
			path, err := writeArtifact(artifactDir, "mail", rec)
			if err != nil {
				return err
			}
			rec.Path = path
			mu.Lock()
			defer mu.Unlock()
			*mailCalls = append(*mailCalls, rec)
			return nil
		},
		ResolveSessionName: toolcallbacks.ResolveSessionName,
		QueueNudge: func(sessionName, sender, message string) error {
			rec := nudgeRecord{
				SessionName: strings.TrimSpace(sessionName),
				Sender:      strings.TrimSpace(sender),
				Message:     strings.TrimSpace(message),
			}
			path, err := writeArtifact(artifactDir, "nudge", rec)
			if err != nil {
				return err
			}
			rec.Path = path
			mu.Lock()
			defer mu.Unlock()
			*nudgeCalls = append(*nudgeCalls, rec)
			return nil
		},
	}
}

func writeArtifact(dir, prefix string, payload any) (string, error) {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%d.json", prefix, time.Now().UnixNano())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func toolArguments(raw any) (map[string]string, error) {
	if raw == nil {
		return map[string]string{}, nil
	}
	switch typed := raw.(type) {
	case map[string]string:
		return typed, nil
	case map[string]any:
		result := make(map[string]string, len(typed))
		for key, value := range typed {
			switch v := value.(type) {
			case string:
				result[key] = v
			case nil:
				result[key] = ""
			default:
				encoded, err := json.Marshal(v)
				if err != nil {
					return nil, err
				}
				result[key] = strings.Trim(string(encoded), `"`)
			}
		}
		return result, nil
	default:
		encoded, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, err
		}
		return toolArguments(result)
	}
}
