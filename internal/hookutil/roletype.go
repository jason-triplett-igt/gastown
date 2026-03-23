// Package hookutil provides shared utilities for agent hook installers.
package hookutil

import (
	"github.com/steveyegge/gastown/internal/config"
)

// IsAutonomousRole reports whether a role's hook policy maps to autonomous
// startup behavior with the current compatibility templates.
func IsAutonomousRole(role string) bool {
	switch HookPolicyForRole("", "", role) {
	case "builder", "reviewer":
		return true
	default:
		return false
	}
}

func HookPolicyForRole(townRoot, rigPath, role string) string {
	if role == "boot" {
		return "builder"
	}
	policy := config.RoleHookPolicy(townRoot, rigPath, role)
	if policy != "" {
		return policy
	}
	return "orchestrator"
}

func HookPolicyFromWorkDir(_ string, role string) string {
	return HookPolicyForRole("", "", role)
}
