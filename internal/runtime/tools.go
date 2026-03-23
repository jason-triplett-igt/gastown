package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

const ToolBDShow = "bd_show"
const ToolBDReady = "bd_ready"
const ToolLoadReview = "load_review"
const ToolRunVerification = "run_verification"

type BeadsReader interface {
	Show(id string) (*BeadView, error)
}

type ReadyReader interface {
	Ready() ([]*BeadView, error)
}

type WorkflowInspector interface {
	InspectWorkflow(id string) (*WorkflowInspection, error)
}

type BeadView struct {
	ID          string
	Title       string
	Status      string
	IssueType   string
	Assignee    string
	Description string
}

type WorkflowInspection struct {
	IssueID            string
	Phase              string
	LastTransition     string
	LastRejection      string
	ReviewVerdict      string
	ReviewApproved     bool
	ReviewArtifactID   string
	ReviewSummary      string
	ReviewEvidence     []string
	ReviewFindings     []string
	ReviewContractOK   bool
	MissingArtifacts   []string
	RequiredArtifacts  []string
	SatisfiedArtifacts []string
	DispatchReady      bool
}

type ToolSupport struct {
	Issues   BeadsReader
	Ready    ReadyReader
	Workflow WorkflowInspector
}

type StaticToolExecutor struct {
	RoleTools map[string][]ToolDefinition
	Factories map[string]func(ctx context.Context, role string, call ToolCall) (ToolResult, error)
}

func (e StaticToolExecutor) Execute(ctx context.Context, role string, call ToolCall) (ToolResult, error) {
	if !toolAllowed(e.RoleTools, role, call.Name) {
		return ToolResult{}, fmt.Errorf("tool %s not allowed for role %s", call.Name, role)
	}
	if e.Factories == nil {
		return ToolResult{}, fmt.Errorf("no tool executor configured")
	}
	factory, ok := e.Factories[call.Name]
	if !ok {
		return ToolResult{}, fmt.Errorf("unknown tool: %s", call.Name)
	}
	return factory(ctx, role, call)
}

func toolAllowed(roleTools map[string][]ToolDefinition, role, toolName string) bool {
	if len(roleTools) == 0 {
		return true
	}
	tools, ok := roleTools[role]
	if !ok {
		return false
	}
	for _, tool := range tools {
		if tool.Name == toolName {
			return true
		}
	}
	return false
}

func DefaultToolCatalog() StaticToolCatalog {
	toolDefs := map[string]ToolDefinition{
		ToolBDShow: {
			Name:        ToolBDShow,
			Description: "Read one bead by id",
			ReadOnly:    true,
		},
		ToolBDReady: {
			Name:        ToolBDReady,
			Description: "List ready beads",
			ReadOnly:    true,
		},
		"run_single_test": {
			Name:        "run_single_test",
			Description: "Run one targeted test",
			ReadOnly:    false,
		},
		"run_unit_tests": {
			Name:        "run_unit_tests",
			Description: "Run project unit tests",
			ReadOnly:    false,
		},
		ToolLoadReview: {
			Name:        ToolLoadReview,
			Description: "Load review artifacts",
			ReadOnly:    true,
		},
		ToolRunVerification: {
			Name:        ToolRunVerification,
			Description: "Run verification checks",
			ReadOnly:    true,
		},
		"bd_update": {
			Name:        "bd_update",
			Description: "Update bead metadata",
			ReadOnly:    false,
		},
		"bd_close": {
			Name:        "bd_close",
			Description: "Close a bead",
			ReadOnly:    false,
		},
		"bd_create": {
			Name:        "bd_create",
			Description: "Create a bead",
			ReadOnly:    false,
		},
		"nudge_agent": {
			Name:        "nudge_agent",
			Description: "Nudge another agent",
			ReadOnly:    false,
		},
		"send_mail": {
			Name:        "send_mail",
			Description: "Send agent mail",
			ReadOnly:    false,
		},
	}

	roleTools := make(map[string][]ToolDefinition)
	for _, role := range config.AllRoles() {
		allowed := config.RoleAllowedTools("", "", role)
		tools := make([]ToolDefinition, 0, len(allowed))
		for _, name := range allowed {
			tool, ok := toolDefs[name]
			if !ok {
				tool = ToolDefinition{Name: name, Description: name}
			}
			tools = append(tools, tool)
		}
		roleTools[role] = tools
	}

	return StaticToolCatalog{
		RoleTools: roleTools,
	}
}

func NewToolExecutor(support ToolSupport) StaticToolExecutor {
	factories := map[string]func(context.Context, string, ToolCall) (ToolResult, error){}

	if support.Issues != nil {
		factories[ToolBDShow] = func(_ context.Context, _ string, call ToolCall) (ToolResult, error) {
			issueID := call.Arguments["id"]
			if issueID == "" {
				return ToolResult{}, fmt.Errorf("bd_show requires id")
			}
			issue, err := support.Issues.Show(issueID)
			if err != nil {
				return ToolResult{}, fmt.Errorf("bd_show %s: %w", issueID, err)
			}
			payload := map[string]string{
				"id":          issue.ID,
				"title":       issue.Title,
				"status":      issue.Status,
				"issue_type":  issue.IssueType,
				"assignee":    issue.Assignee,
				"description": issue.Description,
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return ToolResult{}, fmt.Errorf("encoding bd_show payload: %w", err)
			}
			return ToolResult{Text: string(encoded), Data: payload}, nil
		}
	}

	if support.Ready != nil {
		factories[ToolBDReady] = func(_ context.Context, _ string, _ ToolCall) (ToolResult, error) {
			issues, err := support.Ready.Ready()
			if err != nil {
				return ToolResult{}, fmt.Errorf("bd_ready: %w", err)
			}
			payload := make([]map[string]string, 0, len(issues))
			ids := make([]string, 0, len(issues))
			for _, issue := range issues {
				payload = append(payload, map[string]string{
					"id":         issue.ID,
					"title":      issue.Title,
					"status":     issue.Status,
					"issue_type": issue.IssueType,
				})
				ids = append(ids, issue.ID)
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				return ToolResult{}, fmt.Errorf("encoding bd_ready payload: %w", err)
			}
			return ToolResult{Text: string(encoded), Data: map[string]string{"count": fmt.Sprintf("%d", len(payload)), "ids": strings.Join(ids, ",")}}, nil
		}
	}

	if support.Workflow != nil {
		factories[ToolLoadReview] = func(_ context.Context, _ string, call ToolCall) (ToolResult, error) {
			issueID := call.Arguments["id"]
			if issueID == "" {
				return ToolResult{}, fmt.Errorf("load_review requires id")
			}
			inspection, err := support.Workflow.InspectWorkflow(issueID)
			if err != nil {
				return ToolResult{}, fmt.Errorf("load_review %s: %w", issueID, err)
			}
			payload := inspectionPayload(inspection)
			encoded, err := json.Marshal(payload)
			if err != nil {
				return ToolResult{}, fmt.Errorf("encoding load_review payload: %w", err)
			}
			return ToolResult{Text: string(encoded), Data: payload}, nil
		}

		factories[ToolRunVerification] = func(_ context.Context, _ string, call ToolCall) (ToolResult, error) {
			issueID := call.Arguments["id"]
			if issueID == "" {
				return ToolResult{}, fmt.Errorf("run_verification requires id")
			}
			inspection, err := support.Workflow.InspectWorkflow(issueID)
			if err != nil {
				return ToolResult{}, fmt.Errorf("run_verification %s: %w", issueID, err)
			}
			status := "ready"
			switch {
			case inspection.LastRejection != "":
				status = "blocked"
			case !inspection.DispatchReady:
				status = "missing_artifacts"
			case inspection.ReviewVerdict != "" && !inspection.ReviewApproved:
				status = "rejected"
			}
			payload := inspectionPayload(inspection)
			payload["verification_status"] = status
			encoded, err := json.Marshal(payload)
			if err != nil {
				return ToolResult{}, fmt.Errorf("encoding run_verification payload: %w", err)
			}
			return ToolResult{Text: string(encoded), Data: payload}, nil
		}
	}

	return StaticToolExecutor{
		RoleTools: DefaultToolCatalog().RoleTools,
		Factories: factories,
	}
}

func NewBeadsToolExecutor(reader BeadsReader) StaticToolExecutor {
	return NewToolExecutor(ToolSupport{Issues: reader})
}

func inspectionPayload(inspection *WorkflowInspection) map[string]string {
	return map[string]string{
		"id":                  inspection.IssueID,
		"phase":               inspection.Phase,
		"last_transition":     inspection.LastTransition,
		"last_rejection":      inspection.LastRejection,
		"review_verdict":      inspection.ReviewVerdict,
		"review_approved":     fmt.Sprintf("%t", inspection.ReviewApproved),
		"review_artifact":     inspection.ReviewArtifactID,
		"review_summary":      inspection.ReviewSummary,
		"review_evidence":     strings.Join(inspection.ReviewEvidence, ","),
		"review_findings":     strings.Join(inspection.ReviewFindings, ","),
		"review_contract_ok":  fmt.Sprintf("%t", inspection.ReviewContractOK),
		"dispatch_ready":      fmt.Sprintf("%t", inspection.DispatchReady),
		"required_artifacts":  strings.Join(inspection.RequiredArtifacts, ","),
		"satisfied_artifacts": strings.Join(inspection.SatisfiedArtifacts, ","),
		"missing_artifacts":   strings.Join(inspection.MissingArtifacts, ","),
	}
}
