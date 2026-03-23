package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestResolveToolPolicy(t *testing.T) {
	tests := []struct {
		role      string
		session   string
		wantTools int
	}{
		{role: "witness", session: ToolSessionKindAsk, wantTools: 3},
		{role: "refinery", session: ToolSessionKindAsk, wantTools: 3},
		{role: "mayor", session: ToolSessionKindAsk, wantTools: 0},
	}
	for _, tt := range tests {
		t.Run(tt.role+":"+tt.session, func(t *testing.T) {
			policy := ResolveToolPolicy(tt.role, tt.session)
			if len(policy.AvailableTools) != tt.wantTools {
				t.Fatalf("ResolveToolPolicy(%q,%q) tools = %#v", tt.role, tt.session, policy.AvailableTools)
			}
			if len(policy.ApprovalRules) == 0 {
				t.Fatalf("ResolveToolPolicy(%q,%q) has no approval rules", tt.role, tt.session)
			}
		})
	}
}

func TestResolveToolPolicyForSession(t *testing.T) {
	patrol := ResolveToolPolicyForSession("", "", "mayor", ToolSessionKindPatrol, "/tmp/mayor")
	if !reflect.DeepEqual(patrol.AvailableTools, []string{"bd_show", "bd_ready", "bd_update", "bd_close", "bd_create", "nudge_agent", "send_mail"}) {
		t.Fatalf("patrol.AvailableTools = %#v", patrol.AvailableTools)
	}
	if patrol.ApprovalRules[0].PathPrefix != filepath.Clean("/tmp/mayor") {
		t.Fatalf("patrol.ApprovalRules[0] = %#v", patrol.ApprovalRules[0])
	}

	review := ResolveToolPolicyForSession("", "", "witness", ToolSessionKindReview, "/tmp/witness")
	if !review.ApprovalRules[1].RequireReadOnly {
		t.Fatalf("review.ApprovalRules[1] = %#v", review.ApprovalRules[1])
	}
}

func TestLegacyToolPolicy(t *testing.T) {
	policy := LegacyToolPolicy("/tmp/work", []string{" load_review ", "", "load_review", "run_verification"}, true)
	if !reflect.DeepEqual(policy.AvailableTools, []string{"load_review", "run_verification"}) {
		t.Fatalf("AvailableTools = %#v", policy.AvailableTools)
	}
	if len(policy.ApprovalRules) != 4 {
		t.Fatalf("ApprovalRules = %#v", policy.ApprovalRules)
	}
	if policy.ApprovalRules[0].Kind != "read" || policy.ApprovalRules[0].PathPrefix != filepath.Clean("/tmp/work") {
		t.Fatalf("read rule = %#v", policy.ApprovalRules[0])
	}
	if !policy.ApprovalRules[1].RequireReadOnly || policy.ApprovalRules[1].ToolName != "load_review" {
		t.Fatalf("first tool rule = %#v", policy.ApprovalRules[1])
	}
	if policy.ApprovalRules[len(policy.ApprovalRules)-1].Action != "deny" {
		t.Fatalf("final rule = %#v", policy.ApprovalRules[len(policy.ApprovalRules)-1])
	}
}
