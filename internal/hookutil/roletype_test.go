package hookutil

import "testing"

func TestIsAutonomousRole(t *testing.T) {
	autonomous := []string{"polecat", "crew", "witness", "refinery", "dog", "boot"}
	for _, role := range autonomous {
		if !IsAutonomousRole(role) {
			t.Errorf("IsAutonomousRole(%q) = false, want true", role)
		}
	}

	interactive := []string{"mayor", "deacon", "unknown", ""}
	for _, role := range interactive {
		if IsAutonomousRole(role) {
			t.Errorf("IsAutonomousRole(%q) = true, want false", role)
		}
	}
}

func TestHookPolicyForRoleDefaults(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{role: "polecat", want: "builder"},
		{role: "witness", want: "reviewer"},
		{role: "mayor", want: "orchestrator"},
	}

	for _, tt := range tests {
		if got := HookPolicyForRole("", "", tt.role); got != tt.want {
			t.Fatalf("HookPolicyForRole(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}
