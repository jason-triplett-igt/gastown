package copilotbridge_test

import (
	"context"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotbridge"
)

func TestToolsIncludesMayorPatrolTools(t *testing.T) {
	tools, available, _, err := copilotbridge.Tools(context.Background(), copilotbridge.SessionContext{
		Binding:  copilotbridge.BindingInfo{Role: "mayor"},
		TownRoot: "/tmp/town",
		WorkDir:  "/tmp/town/mayor",
		Policy:   config.ToolPolicy{AvailableTools: []string{"bd_show", "bd_ready", "bd_update", "bd_close", "bd_create", "nudge_agent", "send_mail"}},
	})
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 7 || len(available) != 7 {
		t.Fatalf("tools=%d available=%d", len(tools), len(available))
	}
}
