package copilotbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/toolapi"
)

type BindingInfo struct {
	IssueID   string
	Role      string
	RigName   string
	AgentName string
	WorkDir   string
	Metadata  map[string]string
}

type SessionContext struct {
	Binding  BindingInfo
	TownRoot string
	RigPath  string
	WorkDir  string
	Policy   config.ToolPolicy
	Hooks    toolapi.Callbacks
}

type beadIDParams struct {
	ID string `json:"id"`
}

type beadCreateParams struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Parent      string `json:"parent"`
	Priority    int    `json:"priority"`
}

type beadUpdateParams struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Assignee    string `json:"assignee"`
	Priority    int    `json:"priority"`
}

type sendMailParams struct {
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Priority string `json:"priority"`
}

type nudgeAgentParams struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

func Tools(ctx context.Context, sessionCtx SessionContext) ([]copilot.Tool, []string, []string, error) {
	allowed := make(map[string]struct{}, len(sessionCtx.Policy.AvailableTools))
	for _, name := range sessionCtx.Policy.AvailableTools {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = struct{}{}
		}
	}
	roleTools := config.RoleAllowedTools(sessionCtx.TownRoot, sessionCtx.RigPath, sessionCtx.Binding.Role)
	tools := make([]copilot.Tool, 0, len(roleTools))
	available := make([]string, 0, len(roleTools))
	for _, name := range roleTools {
		if len(allowed) > 0 {
			if _, ok := allowed[name]; !ok {
				continue
			}
		}
		handler := bridgeHandler(sessionCtx, name)
		if handler == nil {
			continue
		}
		tool := copilot.Tool{
			Name:        name,
			Description: toolDescription(name),
			Parameters:  toolParameters(name),
			Handler:     handler,
		}
		if typed := typedTool(sessionCtx, name); typed.Name != "" {
			tool = typed
		}
		tools = append(tools, tool)
		available = append(available, name)
	}
	return tools, available, nil, nil
}

func typedTool(sessionCtx SessionContext, name string) copilot.Tool {
	switch name {
	case "bd_show":
		return copilot.DefineTool("bd_show", toolDescription(name), func(params beadIDParams, inv copilot.ToolInvocation) (map[string]string, error) {
			issue, err := beads.New(resolveWorkDir(sessionCtx)).Show(strings.TrimSpace(params.ID))
			if err != nil {
				return nil, err
			}
			return map[string]string{"id": issue.ID, "title": issue.Title, "status": issue.Status, "issue_type": issue.Type, "assignee": issue.Assignee, "description": issue.Description}, nil
		})
	case "bd_create":
		return copilot.DefineTool("bd_create", toolDescription(name), func(params beadCreateParams, inv copilot.ToolInvocation) (map[string]string, error) {
			issue, err := beads.New(resolveWorkDir(sessionCtx)).Create(beads.CreateOptions{Title: params.Title, Description: params.Description, Type: params.Type, Parent: params.Parent, Priority: params.Priority, Actor: actorIdentity(sessionCtx.Binding)})
			if err != nil {
				return nil, err
			}
			return map[string]string{"id": issue.ID, "title": issue.Title, "status": issue.Status}, nil
		})
	case "bd_update":
		return copilot.DefineTool("bd_update", toolDescription(name), func(params beadUpdateParams, inv copilot.ToolInvocation) (string, error) {
			id := strings.TrimSpace(params.ID)
			if id == "" {
				return "", fmt.Errorf("bd_update requires id")
			}
			opts := beads.UpdateOptions{}
			if v := strings.TrimSpace(params.Title); v != "" {
				opts.Title = &v
			}
			if v := strings.TrimSpace(params.Status); v != "" {
				opts.Status = &v
			}
			if v := strings.TrimSpace(params.Description); v != "" {
				opts.Description = &v
			}
			if v := strings.TrimSpace(params.Assignee); v != "" {
				opts.Assignee = &v
			}
			if params.Priority != 0 {
				p := params.Priority
				opts.Priority = &p
			}
			if err := beads.New(resolveWorkDir(sessionCtx)).Update(id, opts); err != nil {
				return "", err
			}
			return "updated", nil
		})
	case "bd_close":
		return copilot.DefineTool("bd_close", toolDescription(name), func(params beadIDParams, inv copilot.ToolInvocation) (string, error) {
			id := strings.TrimSpace(params.ID)
			if id == "" {
				return "", fmt.Errorf("bd_close requires id")
			}
			if err := beads.New(resolveWorkDir(sessionCtx)).Close(id); err != nil {
				return "", err
			}
			return "closed", nil
		})
	case "send_mail":
		return copilot.DefineTool("send_mail", toolDescription(name), func(params sendMailParams, inv copilot.ToolInvocation) (string, error) {
			if sessionCtx.Hooks.SendMail == nil {
				if sessionCtx.Hooks.ReportToolError != nil {
					sessionCtx.Hooks.ReportToolError("send_mail is unavailable")
				}
				return "", fmt.Errorf("send_mail is unavailable")
			}
			if err := sessionCtx.Hooks.SendMail(actorIdentity(sessionCtx.Binding), strings.TrimSpace(params.To), strings.TrimSpace(params.Subject), strings.TrimSpace(params.Body), strings.TrimSpace(params.Priority)); err != nil {
				if sessionCtx.Hooks.ReportToolError != nil {
					sessionCtx.Hooks.ReportToolError(err.Error())
				}
				return "", err
			}
			return "sent", nil
		})
	case "nudge_agent":
		return copilot.DefineTool("nudge_agent", toolDescription(name), func(params nudgeAgentParams, inv copilot.ToolInvocation) (string, error) {
			if sessionCtx.Hooks.ResolveSessionName == nil || sessionCtx.Hooks.QueueNudge == nil {
				if sessionCtx.Hooks.ReportToolError != nil {
					sessionCtx.Hooks.ReportToolError("nudge_agent is unavailable")
				}
				return "", fmt.Errorf("nudge_agent is unavailable")
			}
			sessionName, err := sessionCtx.Hooks.ResolveSessionName(strings.TrimSpace(params.Target))
			if err != nil {
				if sessionCtx.Hooks.ReportToolError != nil {
					sessionCtx.Hooks.ReportToolError(err.Error())
				}
				return "", err
			}
			if err := sessionCtx.Hooks.QueueNudge(sessionName, actorIdentity(sessionCtx.Binding), strings.TrimSpace(params.Message)); err != nil {
				if sessionCtx.Hooks.ReportToolError != nil {
					sessionCtx.Hooks.ReportToolError(err.Error())
				}
				return "", err
			}
			return "queued", nil
		})
	default:
		return copilot.Tool{}
	}
}

func bridgeHandler(sessionCtx SessionContext, name string) copilot.ToolHandler {
	switch name {
	case "bd_show":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			args, err := toolArguments(inv)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			id := strings.TrimSpace(args["id"])
			if id == "" {
				return copilot.ToolResult{}, fmt.Errorf("bd_show requires id")
			}
			issue, err := beads.New(resolveWorkDir(sessionCtx)).Show(id)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			return jsonResult(map[string]string{"id": issue.ID, "title": issue.Title, "status": issue.Status, "issue_type": issue.Type, "assignee": issue.Assignee, "description": issue.Description})
		}
	case "bd_ready":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			issues, err := beads.New(resolveWorkDir(sessionCtx)).Ready()
			if err != nil {
				return copilot.ToolResult{}, err
			}
			payload := make([]map[string]string, 0, len(issues))
			for _, issue := range issues {
				payload = append(payload, map[string]string{"id": issue.ID, "title": issue.Title, "status": issue.Status, "issue_type": issue.Type})
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			return copilot.ToolResult{TextResultForLLM: string(encoded), ResultType: "success"}, nil
		}
	case "load_review":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			return workflowToolResult(resolveWorkDir(sessionCtx), inv, false)
		}
	case "run_verification":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			return workflowToolResult(resolveWorkDir(sessionCtx), inv, true)
		}
	case "bd_create":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			args, err := toolArguments(inv)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			issue, err := beads.New(resolveWorkDir(sessionCtx)).Create(beads.CreateOptions{
				Title:       args["title"],
				Description: args["description"],
				Type:        args["type"],
				Parent:      args["parent"],
				Priority:    parseInt(args["priority"], 2),
				Actor:       actorIdentity(sessionCtx.Binding),
			})
			if err != nil {
				return copilot.ToolResult{}, err
			}
			return jsonResult(map[string]string{"id": issue.ID, "title": issue.Title, "status": issue.Status})
		}
	case "bd_update":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			args, err := toolArguments(inv)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			id := strings.TrimSpace(args["id"])
			if id == "" {
				return copilot.ToolResult{}, fmt.Errorf("bd_update requires id")
			}
			opts := beads.UpdateOptions{}
			if v := strings.TrimSpace(args["title"]); v != "" {
				opts.Title = &v
			}
			if v := strings.TrimSpace(args["status"]); v != "" {
				opts.Status = &v
			}
			if v := strings.TrimSpace(args["description"]); v != "" {
				opts.Description = &v
			}
			if v := strings.TrimSpace(args["assignee"]); v != "" {
				opts.Assignee = &v
			}
			if p, ok := maybeParseInt(args["priority"]); ok {
				opts.Priority = &p
			}
			if err := beads.New(resolveWorkDir(sessionCtx)).Update(id, opts); err != nil {
				return copilot.ToolResult{}, err
			}
			return textResult("updated"), nil
		}
	case "bd_close":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			args, err := toolArguments(inv)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			id := strings.TrimSpace(args["id"])
			if id == "" {
				return copilot.ToolResult{}, fmt.Errorf("bd_close requires id")
			}
			bd := beads.New(resolveWorkDir(sessionCtx))
			if reason := strings.TrimSpace(args["reason"]); reason != "" {
				if err := bd.CloseWithReason(reason, id); err != nil {
					return copilot.ToolResult{}, err
				}
			} else {
				if err := bd.Close(id); err != nil {
					return copilot.ToolResult{}, err
				}
			}
			return textResult("closed"), nil
		}
	case "send_mail":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			args, err := toolArguments(inv)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			to := strings.TrimSpace(args["to"])
			subject := strings.TrimSpace(args["subject"])
			body := strings.TrimSpace(args["body"])
			if to == "" || subject == "" || body == "" {
				return copilot.ToolResult{}, fmt.Errorf("send_mail requires to, subject, and body")
			}
			if sessionCtx.Hooks.SendMail == nil {
				return copilot.ToolResult{}, fmt.Errorf("send_mail is unavailable")
			}
			if err := sessionCtx.Hooks.SendMail(actorIdentity(sessionCtx.Binding), to, subject, body, args["priority"]); err != nil {
				return copilot.ToolResult{}, err
			}
			return textResult("sent"), nil
		}
	case "nudge_agent":
		return func(inv copilot.ToolInvocation) (copilot.ToolResult, error) {
			args, err := toolArguments(inv)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			target := strings.TrimSpace(args["target"])
			message := strings.TrimSpace(args["message"])
			if target == "" || message == "" {
				return copilot.ToolResult{}, fmt.Errorf("nudge_agent requires target and message")
			}
			if sessionCtx.Hooks.ResolveSessionName == nil || sessionCtx.Hooks.QueueNudge == nil {
				return copilot.ToolResult{}, fmt.Errorf("nudge_agent is unavailable")
			}
			sessionName, err := sessionCtx.Hooks.ResolveSessionName(target)
			if err != nil {
				return copilot.ToolResult{}, err
			}
			if err := sessionCtx.Hooks.QueueNudge(sessionName, actorIdentity(sessionCtx.Binding), message); err != nil {
				return copilot.ToolResult{}, err
			}
			return textResult("queued"), nil
		}
	default:
		return nil
	}
}

func resolveWorkDir(sessionCtx SessionContext) string {
	if strings.TrimSpace(sessionCtx.WorkDir) != "" {
		return sessionCtx.WorkDir
	}
	if strings.TrimSpace(sessionCtx.RigPath) != "" {
		return sessionCtx.RigPath
	}
	return sessionCtx.TownRoot
}

func actorIdentity(binding BindingInfo) string {
	if strings.TrimSpace(binding.RigName) != "" {
		return fmt.Sprintf("%s/%s", binding.RigName, binding.Role)
	}
	return binding.Role + "/"
}

func toolArguments(inv copilot.ToolInvocation) (map[string]string, error) {
	if inv.Arguments == nil {
		return map[string]string{}, nil
	}
	switch typed := inv.Arguments.(type) {
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
		encoded, err := json.Marshal(inv.Arguments)
		if err != nil {
			return nil, err
		}
		var result map[string]any
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, err
		}
		return toolArguments(copilot.ToolInvocation{Arguments: result})
	}
}

func toolParameters(name string) map[string]any {
	params := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	properties := params["properties"].(map[string]any)
	switch name {
	case "bd_show", "load_review", "run_verification", "bd_close":
		properties["id"] = map[string]any{"type": "string"}
	case "bd_update":
		properties["id"] = map[string]any{"type": "string"}
		properties["title"] = map[string]any{"type": "string"}
		properties["status"] = map[string]any{"type": "string"}
		properties["description"] = map[string]any{"type": "string"}
		properties["assignee"] = map[string]any{"type": "string"}
		properties["priority"] = map[string]any{"type": "integer"}
	case "bd_create":
		properties["title"] = map[string]any{"type": "string"}
		properties["description"] = map[string]any{"type": "string"}
		properties["type"] = map[string]any{"type": "string"}
		properties["parent"] = map[string]any{"type": "string"}
		properties["priority"] = map[string]any{"type": "integer"}
	case "send_mail":
		properties["to"] = map[string]any{"type": "string"}
		properties["subject"] = map[string]any{"type": "string"}
		properties["body"] = map[string]any{"type": "string"}
		properties["priority"] = map[string]any{"type": "string"}
	case "nudge_agent":
		properties["target"] = map[string]any{"type": "string"}
		properties["message"] = map[string]any{"type": "string"}
	}
	return params
}

func toolDescription(name string) string {
	switch name {
	case "bd_show":
		return "Read one bead by id"
	case "bd_ready":
		return "List ready beads"
	case "load_review":
		return "Load review artifacts"
	case "run_verification":
		return "Run verification checks"
	case "bd_update":
		return "Update bead metadata"
	case "bd_close":
		return "Close a bead"
	case "bd_create":
		return "Create a bead"
	case "nudge_agent":
		return "Nudge another agent"
	case "send_mail":
		return "Send agent mail"
	default:
		return name
	}
}

func textResult(text string) copilot.ToolResult {
	return copilot.ToolResult{TextResultForLLM: text, ResultType: "success"}
}

func jsonResult(payload map[string]string) (copilot.ToolResult, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return copilot.ToolResult{}, err
	}
	return copilot.ToolResult{TextResultForLLM: string(encoded), ResultType: "success"}, nil
}

func maybeParseInt(raw string) (int, bool) {
	var value int
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &value); err != nil {
		return 0, false
	}
	return value, true
}

func parseInt(raw string, fallback int) int {
	if value, ok := maybeParseInt(raw); ok {
		return value
	}
	return fallback
}

func workflowToolResult(workDir string, inv copilot.ToolInvocation, verification bool) (copilot.ToolResult, error) {
	args, err := toolArguments(inv)
	if err != nil {
		return copilot.ToolResult{}, err
	}
	id := strings.TrimSpace(args["id"])
	if id == "" {
		if verification {
			return copilot.ToolResult{}, fmt.Errorf("run_verification requires id")
		}
		return copilot.ToolResult{}, fmt.Errorf("load_review requires id")
	}
	issue, err := beads.New(workDir).Show(id)
	if err != nil {
		return copilot.ToolResult{}, err
	}
	inspection := beads.InspectVSDDWorkflow(issue)
	if inspection == nil {
		return copilot.ToolResult{}, fmt.Errorf("issue %s has no vsdd workflow state", id)
	}
	artifacts := beads.ParseVSDDArtifactFields(issue)
	reviewArtifactID := ""
	if artifacts != nil {
		reviewArtifactID = artifacts.ReviewArtifactID
	}
	payload := map[string]string{
		"id":                  id,
		"phase":               string(inspection.Phase),
		"last_transition":     inspection.LastTransition,
		"last_rejection":      inspection.LastRejection,
		"review_verdict":      inspection.ReviewVerdict,
		"review_approved":     fmt.Sprintf("%t", inspection.ReviewApproved),
		"review_artifact":     reviewArtifactID,
		"review_summary":      inspection.ReviewSummary,
		"review_evidence":     strings.Join(inspection.ReviewEvidence, ","),
		"review_findings":     strings.Join(inspection.ReviewFindings, ","),
		"review_contract_ok":  fmt.Sprintf("%t", inspection.ReviewContractOK),
		"dispatch_ready":      fmt.Sprintf("%t", inspection.DispatchReady),
		"required_artifacts":  strings.Join(inspection.RequiredArtifacts, ","),
		"satisfied_artifacts": strings.Join(inspection.SatisfiedArtifacts, ","),
		"missing_artifacts":   strings.Join(inspection.MissingArtifacts, ","),
	}
	if verification {
		status := "ready"
		switch {
		case inspection.LastRejection != "":
			status = "blocked"
		case !inspection.DispatchReady:
			status = "missing_artifacts"
		case inspection.ReviewVerdict != "" && !inspection.ReviewApproved:
			status = "rejected"
		}
		payload["verification_status"] = status
	}
	return jsonResult(payload)
}
