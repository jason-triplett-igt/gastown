package toolpolicy

import (
	"path/filepath"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/steveyegge/gastown/internal/config"
)

func PermissionHandler(policy config.ToolPolicy) copilot.PermissionHandlerFunc {
	return func(request copilot.PermissionRequest, _ copilot.PermissionInvocation) (copilot.PermissionRequestResult, error) {
		if allows(policy, request) {
			return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindApproved}, nil
		}
		return copilot.PermissionRequestResult{Kind: copilot.PermissionRequestResultKindDeniedByRules}, nil
	}
}

func allows(policy config.ToolPolicy, request copilot.PermissionRequest) bool {
	kind := permissionKind(request)
	toolName := strings.TrimSpace(valueOrEmpty(request.ToolName))
	path := strings.TrimSpace(valueOrEmpty(request.Path))
	cmd := strings.TrimSpace(valueOrEmpty(request.FullCommandText))
	for _, rule := range policy.ApprovalRules {
		if rule.Kind != "" && rule.Kind != kind {
			continue
		}
		if rule.ToolName != "" && rule.ToolName != toolName {
			continue
		}
		if rule.PathPrefix != "" && !strings.HasPrefix(path, rule.PathPrefix) {
			continue
		}
		if rule.CommandPrefix != "" && !strings.HasPrefix(cmd, rule.CommandPrefix) {
			continue
		}
		if rule.RequireReadOnly && request.ReadOnly != nil && !*request.ReadOnly {
			continue
		}
		return strings.EqualFold(rule.Action, "approve")
	}
	return false
}

func pathWithinBase(baseDir, candidate string) bool {
	if strings.TrimSpace(baseDir) == "" {
		return false
	}
	cleanBase := filepath.Clean(baseDir)
	cleanCandidate := filepath.Clean(candidate)
	if cleanBase == cleanCandidate {
		return true
	}
	rel, err := filepath.Rel(cleanBase, cleanCandidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func permissionKind(request copilot.PermissionRequest) string {
	return strings.TrimSpace(string(request.Kind))
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
