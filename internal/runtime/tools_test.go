package runtime

import (
	"context"
	"errors"
	"testing"
)

type fakeBeadsReader struct {
	issue *BeadView
	err   error
}

type fakeReadyReader struct {
	issues []*BeadView
	err    error
}

type fakeWorkflowInspector struct {
	inspection *WorkflowInspection
	err        error
}

func (f fakeBeadsReader) Show(id string) (*BeadView, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.issue != nil {
		return f.issue, nil
	}
	return &BeadView{ID: id, Title: "Untitled", Status: "open", IssueType: "task"}, nil
}

func (f fakeReadyReader) Ready() ([]*BeadView, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.issues, nil
}

func (f fakeWorkflowInspector) InspectWorkflow(id string) (*WorkflowInspection, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.inspection != nil {
		return f.inspection, nil
	}
	return &WorkflowInspection{IssueID: id, Phase: "review", DispatchReady: true}, nil
}

func TestDefaultToolCatalogIncludesBDShow(t *testing.T) {
	t.Parallel()
	tools, err := DefaultToolCatalog().ToolsForRole(context.Background(), "witness")
	if err != nil {
		t.Fatalf("ToolsForRole() error = %v", err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name == ToolBDShow && tool.ReadOnly {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ToolsForRole() = %#v", tools)
	}
}

func TestDefaultToolCatalogVariesByRole(t *testing.T) {
	t.Parallel()
	catalog := DefaultToolCatalog()

	builderTools, err := catalog.ToolsForRole(context.Background(), "crew")
	if err != nil {
		t.Fatalf("ToolsForRole(crew) error = %v", err)
	}
	reviewerTools, err := catalog.ToolsForRole(context.Background(), "witness")
	if err != nil {
		t.Fatalf("ToolsForRole(witness) error = %v", err)
	}
	orchestratorTools, err := catalog.ToolsForRole(context.Background(), "mayor")
	if err != nil {
		t.Fatalf("ToolsForRole(mayor) error = %v", err)
	}

	assertHasTool(t, builderTools, "run_single_test")
	assertLacksTool(t, builderTools, "send_mail")
	assertLacksTool(t, builderTools, ToolBDReady)
	assertHasTool(t, reviewerTools, "load_review")
	assertLacksTool(t, reviewerTools, "run_single_test")
	assertLacksTool(t, reviewerTools, "send_mail")
	assertHasTool(t, orchestratorTools, "send_mail")
	assertHasTool(t, orchestratorTools, ToolBDReady)
	assertLacksTool(t, orchestratorTools, "run_single_test")
}

func TestBeadsToolExecutorExecutesBDShow(t *testing.T) {
	t.Parallel()
	exec := NewBeadsToolExecutor(fakeBeadsReader{issue: &BeadView{ID: "slotmachine-910", Title: "Program", Status: "open", IssueType: "epic"}})
	result, err := exec.Execute(context.Background(), "witness", ToolCall{Name: ToolBDShow, Arguments: map[string]string{"id": "slotmachine-910"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Data["id"] != "slotmachine-910" {
		t.Fatalf("result id = %q", result.Data["id"])
	}
	if result.Data["title"] != "Program" {
		t.Fatalf("result title = %q", result.Data["title"])
	}
}

func TestBeadsToolExecutorRequiresID(t *testing.T) {
	t.Parallel()
	exec := NewBeadsToolExecutor(fakeBeadsReader{})
	_, err := exec.Execute(context.Background(), "witness", ToolCall{Name: ToolBDShow, Arguments: map[string]string{}})
	if err == nil {
		t.Fatal("Execute() error = nil, want missing id")
	}
}

func TestBeadsToolExecutorSurfacesReaderErrors(t *testing.T) {
	t.Parallel()
	exec := NewBeadsToolExecutor(fakeBeadsReader{err: errors.New("boom")})
	_, err := exec.Execute(context.Background(), "witness", ToolCall{Name: ToolBDShow, Arguments: map[string]string{"id": "slotmachine-910"}})
	if err == nil {
		t.Fatal("Execute() error = nil, want reader error")
	}
}

func TestBeadsToolExecutorRejectsDisallowedTool(t *testing.T) {
	t.Parallel()
	exec := NewBeadsToolExecutor(fakeBeadsReader{})
	_, err := exec.Execute(context.Background(), "witness", ToolCall{Name: "run_single_test", Arguments: map[string]string{"id": "slotmachine-910"}})
	if err == nil {
		t.Fatal("Execute() error = nil, want disallowed tool error")
	}
}

func TestToolExecutorExecutesBDReady(t *testing.T) {
	t.Parallel()
	exec := NewToolExecutor(ToolSupport{Ready: fakeReadyReader{issues: []*BeadView{{ID: "slotmachine-910", Title: "Task", Status: "open", IssueType: "task"}}}})
	result, err := exec.Execute(context.Background(), "mayor", ToolCall{Name: ToolBDReady})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Data["count"] != "1" || result.Data["ids"] != "slotmachine-910" {
		t.Fatalf("result = %#v", result)
	}
}

func TestToolExecutorExecutesLoadReview(t *testing.T) {
	t.Parallel()
	exec := NewToolExecutor(ToolSupport{Workflow: fakeWorkflowInspector{inspection: &WorkflowInspection{IssueID: "slotmachine-910", Phase: "review", ReviewVerdict: "READY", ReviewArtifactID: "review-1", ReviewSummary: "looks good", ReviewEvidence: []string{"spec-1", "impl-1"}, ReviewFindings: []string{"none"}, ReviewContractOK: true, DispatchReady: true}}})
	result, err := exec.Execute(context.Background(), "witness", ToolCall{Name: ToolLoadReview, Arguments: map[string]string{"id": "slotmachine-910"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Data["review_verdict"] != "READY" || result.Data["review_artifact"] != "review-1" {
		t.Fatalf("result = %#v", result)
	}
	if result.Data["review_summary"] != "looks good" || result.Data["review_contract_ok"] != "true" {
		t.Fatalf("result = %#v", result)
	}
}

func TestToolExecutorExecutesRunVerification(t *testing.T) {
	t.Parallel()
	exec := NewToolExecutor(ToolSupport{Workflow: fakeWorkflowInspector{inspection: &WorkflowInspection{IssueID: "slotmachine-910", Phase: "convergence", LastRejection: "review_rejected", DispatchReady: false}}})
	result, err := exec.Execute(context.Background(), "witness", ToolCall{Name: ToolRunVerification, Arguments: map[string]string{"id": "slotmachine-910"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Data["verification_status"] != "blocked" {
		t.Fatalf("result = %#v", result)
	}
}

func TestToolExecutorRejectsLoadReviewForBuilder(t *testing.T) {
	t.Parallel()
	exec := NewToolExecutor(ToolSupport{Workflow: fakeWorkflowInspector{inspection: &WorkflowInspection{IssueID: "slotmachine-910"}}})
	_, err := exec.Execute(context.Background(), "crew", ToolCall{Name: ToolLoadReview, Arguments: map[string]string{"id": "slotmachine-910"}})
	if err == nil {
		t.Fatal("Execute() error = nil, want disallowed tool error")
	}
}

func TestToolExecutorRequiresWorkflowID(t *testing.T) {
	t.Parallel()
	exec := NewToolExecutor(ToolSupport{Workflow: fakeWorkflowInspector{}})
	_, err := exec.Execute(context.Background(), "witness", ToolCall{Name: ToolLoadReview, Arguments: map[string]string{}})
	if err == nil {
		t.Fatal("Execute() error = nil, want missing id")
	}
}

func TestDefaultToolCatalogReviewerToolsAreReadOnly(t *testing.T) {
	t.Parallel()
	tools, err := DefaultToolCatalog().ToolsForRole(context.Background(), "witness")
	if err != nil {
		t.Fatalf("ToolsForRole() error = %v", err)
	}
	for _, tool := range tools {
		if !tool.ReadOnly {
			t.Fatalf("tool = %#v, want reviewer tools to be read-only", tool)
		}
	}
}

func assertHasTool(t *testing.T, tools []ToolDefinition, name string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return
		}
	}
	t.Fatalf("tools = %#v, want %q", tools, name)
}

func assertLacksTool(t *testing.T, tools []ToolDefinition, name string) {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			t.Fatalf("tools = %#v, did not want %q", tools, name)
		}
	}
}
