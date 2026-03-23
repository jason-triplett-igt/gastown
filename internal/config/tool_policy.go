package config

import (
	"path/filepath"
	"strings"
)

const (
	ToolSessionKindAsk       = "ask"
	ToolSessionKindPatrol    = "patrol"
	ToolSessionKindReview    = "review"
	ToolSessionKindSmokeTest = "smoke_test"

	ToolProfileAskNoTools  = "ask-no-tools"
	ToolProfileAskReadOnly = "ask-readonly"
)

type ToolPolicy struct {
	AvailableTools []string       `json:"available_tools,omitempty"`
	ExcludedTools  []string       `json:"excluded_tools,omitempty"`
	ApprovalRules  []ApprovalRule `json:"approval_rules,omitempty"`
}

type ApprovalRule struct {
	Kind            string `json:"kind,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	CommandPrefix   string `json:"command_prefix,omitempty"`
	PathPrefix      string `json:"path_prefix,omitempty"`
	RequireReadOnly bool   `json:"require_read_only,omitempty"`
	Action          string `json:"action"`
}

func LegacyToolPolicy(workDir string, allowedTools []string, readOnly bool) ToolPolicy {
	tools := normalizeToolNames(allowedTools)
	rules := make([]ApprovalRule, 0, len(tools)+2)
	if strings.TrimSpace(workDir) != "" {
		rules = append(rules, ApprovalRule{
			Kind:       "read",
			PathPrefix: filepath.Clean(workDir),
			Action:     "approve",
		})
	}
	for _, tool := range tools {
		rule := ApprovalRule{
			Kind:     "custom-tool",
			ToolName: tool,
			Action:   "approve",
		}
		if readOnly {
			rule.RequireReadOnly = true
		}
		rules = append(rules, rule)
	}
	rules = append(rules, ApprovalRule{Action: "deny"})
	return ToolPolicy{
		AvailableTools: tools,
		ApprovalRules:  rules,
	}
}

func ResolveToolPolicyForSession(townRoot, rigPath, role, sessionKind, workDir string) ToolPolicy {
	switch strings.TrimSpace(sessionKind) {
	case ToolSessionKindAsk:
		return resolveAskToolPolicy(role)
	case ToolSessionKindReview:
		return LegacyToolPolicy(workDir, RoleAllowedTools(townRoot, rigPath, role), true)
	case ToolSessionKindSmokeTest:
		return LegacyToolPolicy(workDir, RoleAllowedTools(townRoot, rigPath, role), strings.TrimSpace(role) == "witness")
	case "", ToolSessionKindPatrol:
		fallthrough
	default:
		return LegacyToolPolicy(workDir, RoleAllowedTools(townRoot, rigPath, role), false)
	}
}

func BuiltInToolPolicy(profile string) ToolPolicy {
	switch strings.TrimSpace(profile) {
	case ToolProfileAskReadOnly:
		return ToolPolicy{
			AvailableTools: []string{"bash", "read", "grep"},
			ApprovalRules: []ApprovalRule{
				{Kind: "read", Action: "approve"},
				{Kind: "shell", Action: "approve"},
				{Kind: "custom-tool", ToolName: "grep", Action: "approve"},
				{Kind: "custom-tool", ToolName: "read", Action: "approve"},
				{Action: "deny"},
			},
		}
	case ToolProfileAskNoTools:
		fallthrough
	default:
		return ToolPolicy{
			AvailableTools: nil,
			ApprovalRules:  []ApprovalRule{{Action: "deny"}},
		}
	}
}

func ResolveToolPolicy(role, sessionKind string) ToolPolicy {
	if strings.TrimSpace(sessionKind) == ToolSessionKindAsk {
		return resolveAskToolPolicy(role)
	}
	return ResolveToolPolicyForSession("", "", role, sessionKind, "")
}

func resolveAskToolPolicy(role string) ToolPolicy {
	switch strings.TrimSpace(role) {
	case "witness", "refinery":
		return BuiltInToolPolicy(ToolProfileAskReadOnly)
	default:
		return BuiltInToolPolicy(ToolProfileAskNoTools)
	}
}

func normalizeToolNames(tools []string) []string {
	if len(tools) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tools))
	result := make([]string, 0, len(tools))
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := seen[tool]; ok {
			continue
		}
		seen[tool] = struct{}{}
		result = append(result, tool)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
