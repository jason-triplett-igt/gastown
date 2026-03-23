package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

func TestExplainToolPolicyAsk(t *testing.T) {
	result, err := explainToolPolicy("witness", config.ToolSessionKindAsk, "", "")
	if err != nil {
		t.Fatalf("explainToolPolicy() error = %v", err)
	}
	if len(result.Policy.AvailableTools) != 3 {
		t.Fatalf("AvailableTools = %#v", result.Policy.AvailableTools)
	}
	if result.SessionKind != config.ToolSessionKindAsk {
		t.Fatalf("SessionKind = %q", result.SessionKind)
	}
}

func TestRunToolsExplainText(t *testing.T) {
	oldKind, oldJSON, oldRig, oldWorkDir := toolsExplainSessionKind, toolsExplainJSON, toolsExplainRig, toolsExplainWorkDir
	defer func() {
		toolsExplainSessionKind, toolsExplainJSON, toolsExplainRig, toolsExplainWorkDir = oldKind, oldJSON, oldRig, oldWorkDir
	}()
	toolsExplainSessionKind = config.ToolSessionKindPatrol
	toolsExplainJSON = false
	toolsExplainRig = ""
	toolsExplainWorkDir = "/tmp/mayor"

	var out bytes.Buffer
	toolsExplainCmd.SetOut(&out)
	toolsExplainCmd.SetErr(&out)
	if err := runToolsExplain(toolsExplainCmd, []string{"mayor"}); err != nil {
		t.Fatalf("runToolsExplain() error = %v", err)
	}
	printed := out.String()
	for _, want := range []string{"role=mayor", "session_kind=patrol", "available_tools=bd_show,bd_ready,bd_update,bd_close,bd_create,nudge_agent,send_mail", "rule action=approve kind=read tool= path_prefix=/tmp/mayor"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("output = %q, want %q", printed, want)
		}
	}
}

func TestRunToolsExplainJSON(t *testing.T) {
	oldKind, oldJSON, oldRig, oldWorkDir := toolsExplainSessionKind, toolsExplainJSON, toolsExplainRig, toolsExplainWorkDir
	defer func() {
		toolsExplainSessionKind, toolsExplainJSON, toolsExplainRig, toolsExplainWorkDir = oldKind, oldJSON, oldRig, oldWorkDir
	}()
	toolsExplainSessionKind = config.ToolSessionKindAsk
	toolsExplainJSON = true
	toolsExplainRig = ""
	toolsExplainWorkDir = ""

	var out bytes.Buffer
	toolsExplainCmd.SetOut(&out)
	toolsExplainCmd.SetErr(&out)
	if err := runToolsExplain(toolsExplainCmd, []string{"refinery"}); err != nil {
		t.Fatalf("runToolsExplain() error = %v", err)
	}
	var payload toolsExplainOutput
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v output=%q", err, out.String())
	}
	if payload.Role != "refinery" || payload.SessionKind != config.ToolSessionKindAsk {
		t.Fatalf("payload = %#v", payload)
	}
	if len(payload.Policy.AvailableTools) != 3 {
		t.Fatalf("payload.Policy.AvailableTools = %#v", payload.Policy.AvailableTools)
	}
}
