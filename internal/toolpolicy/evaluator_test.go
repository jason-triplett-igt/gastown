package toolpolicy

import (
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/steveyegge/gastown/internal/config"
)

func TestPermissionHandler(t *testing.T) {
	policy := config.BuiltInToolPolicy(config.ToolProfileAskReadOnly)
	handler := PermissionHandler(policy)

	bash := "git status"
	res, err := handler(copilot.PermissionRequest{Kind: copilot.PermissionRequestKindShell, FullCommandText: &bash}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("shell handler error = %v", err)
	}
	if res.Kind != copilot.PermissionRequestResultKindApproved {
		t.Fatalf("shell result = %v, want approved", res.Kind)
	}

	url := "https://example.com"
	res, err = handler(copilot.PermissionRequest{Kind: copilot.PermissionRequestKindURL, URL: &url}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("url handler error = %v", err)
	}
	if res.Kind != copilot.PermissionRequestResultKindDeniedByRules {
		t.Fatalf("url result = %v, want denied", res.Kind)
	}
}

func TestPermissionHandlerLegacyPolicy(t *testing.T) {
	policy := config.LegacyToolPolicy("/tmp/gastown", []string{" load_review ", "load_review", "run_verification"}, true)
	handler := PermissionHandler(policy)

	readPath := "/tmp/gastown/review.md"
	res, err := handler(copilot.PermissionRequest{Kind: copilot.PermissionRequestKindRead, Path: &readPath}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("read handler error = %v", err)
	}
	if res.Kind != copilot.PermissionRequestResultKindApproved {
		t.Fatalf("read result = %v, want approved", res.Kind)
	}

	outsidePath := "/tmp/other/review.md"
	res, err = handler(copilot.PermissionRequest{Kind: copilot.PermissionRequestKindRead, Path: &outsidePath}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("outside read handler error = %v", err)
	}
	if res.Kind != copilot.PermissionRequestResultKindDeniedByRules {
		t.Fatalf("outside read result = %v, want denied", res.Kind)
	}

	toolName := "load_review"
	readOnly := true
	res, err = handler(copilot.PermissionRequest{Kind: copilot.PermissionRequestKindCustomTool, ToolName: &toolName, ReadOnly: &readOnly}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("custom tool handler error = %v", err)
	}
	if res.Kind != copilot.PermissionRequestResultKindApproved {
		t.Fatalf("custom tool result = %v, want approved", res.Kind)
	}

	readOnly = false
	res, err = handler(copilot.PermissionRequest{Kind: copilot.PermissionRequestKindCustomTool, ToolName: &toolName, ReadOnly: &readOnly}, copilot.PermissionInvocation{})
	if err != nil {
		t.Fatalf("writable custom tool handler error = %v", err)
	}
	if res.Kind != copilot.PermissionRequestResultKindDeniedByRules {
		t.Fatalf("writable custom tool result = %v, want denied", res.Kind)
	}
}
