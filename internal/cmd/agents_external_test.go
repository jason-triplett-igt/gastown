package cmd

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
)

func TestMergeManagedAgentSessionsAddsExternalBindingWithoutTmuxSession(t *testing.T) {
	setupNudgeTestRegistry(t)
	townRoot := t.TempDir()
	store := runtime.NewFileSessionBindingStore(townRoot)
	binding := runtime.SessionBinding{
		IssueID:          "xut-refinery",
		Role:             "refinery",
		RigName:          "testrig",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      session.RefinerySessionName(session.PrefixFor("testrig")),
		RuntimeSessionID: "runtime-123",
		WorkDir:          filepath.Join(townRoot, "testrig", "refinery", "rig"),
		Metadata:         map[string]string{"external_server": "true", "owner_mode": "copilot-queue"},
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	agents := mergeManagedAgentSessions(townRoot, nil, false)
	if len(agents) != 1 {
		t.Fatalf("len(agents) = %d, want 1", len(agents))
	}
	if agents[0].Name != binding.SessionName {
		t.Fatalf("agent name = %q, want %q", agents[0].Name, binding.SessionName)
	}
	if agents[0].Type != AgentRefinery {
		t.Fatalf("agent type = %v, want AgentRefinery", agents[0].Type)
	}
}
