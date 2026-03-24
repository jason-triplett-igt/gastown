package cmd

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/session"
)

func TestSessionInfoJSONOutput(t *testing.T) {
	info := &polecat.SessionInfo{
		Polecat:   "alpha",
		SessionID: "gt-alpha",
		Running:   true,
		Ready:     true,
		Busy:      false,
		RigName:   "gastown",
		Attached:  false,
		Created:   time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC),
		Windows:   1,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed["polecat"] != "alpha" {
		t.Errorf("polecat = %v, want alpha", parsed["polecat"])
	}
	if parsed["session_id"] != "gt-alpha" {
		t.Errorf("session_id = %v, want gt-alpha", parsed["session_id"])
	}
	if parsed["running"] != true {
		t.Errorf("running = %v, want true", parsed["running"])
	}
	if parsed["ready"] != true {
		t.Errorf("ready = %v, want true", parsed["ready"])
	}
	if parsed["busy"] != false {
		t.Errorf("busy = %v, want false", parsed["busy"])
	}
	if parsed["rig_name"] != "gastown" {
		t.Errorf("rig_name = %v, want gastown", parsed["rig_name"])
	}
}

func TestSessionStatusCmdJSONFlagWiring(t *testing.T) {
	// Verify --json flag is registered on the session status command.
	// This catches regressions where flag binding is accidentally removed,
	// which would silently break formulas that depend on --json output.
	f := sessionStatusCmd.Flags().Lookup("json")
	if f == nil {
		t.Fatal("session status command missing --json flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--json default = %q, want \"false\"", f.DefValue)
	}
}

func TestSessionInfoJSONOutputNotRunning(t *testing.T) {
	info := &polecat.SessionInfo{
		Polecat:   "beta",
		SessionID: "gt-beta",
		Running:   false,
		Ready:     false,
		Busy:      false,
		RigName:   "testrig",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if parsed["running"] != false {
		t.Errorf("running = %v, want false", parsed["running"])
	}
	if parsed["ready"] != false {
		t.Errorf("ready = %v, want false", parsed["ready"])
	}
	if parsed["busy"] != false {
		t.Errorf("busy = %v, want false", parsed["busy"])
	}
}

func TestRunSessionStatusPrintsBindingObservability(t *testing.T) {
	townRoot := t.TempDir()
	rigName := "gastown"
	rigPath := filepath.Join(townRoot, rigName)
	if err := os.MkdirAll(filepath.Join(townRoot, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rigPath, "polecats", "toast"), 0o755); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(rigPath, "settings")
	if err := os.MkdirAll(settingsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{"copilot-external": {
		Provider: "copilot",
		Command:  "copilot",
		CLIURL:   "localhost:4321",
	}}
	settings.RoleAgents = map[string]string{"polecat": "copilot-external"}
	if err := config.SaveRigSettings(filepath.Join(settingsDir, "config.json"), settings); err != nil {
		t.Fatal(err)
	}
	rigsConfig := &config.RigsConfig{
		Version: config.CurrentRigsVersion,
		Rigs: map[string]config.RigEntry{
			rigName: {
				GitURL:    "file:///dev/null",
				LocalRepo: rigPath,
				AddedAt:   time.Now(),
				BeadsConfig: &config.BeadsConfig{
					Prefix: "gt",
				},
			},
		},
	}
	if err := config.SaveRigsConfig(filepath.Join(townRoot, "mayor", "rigs.json"), rigsConfig); err != nil {
		t.Fatal(err)
	}
	bindingDir := filepath.Join(townRoot, ".runtime", "session-bindings")
	if err := os.MkdirAll(bindingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binding := &polecat.SessionInfo{}
	_ = binding
	bindingData := `{
  "issue_id": "slotmachine-910.5.7",
  "role": "polecat",
  "rig_name": "gastown",
  "agent_name": "toast",
  "provider": "copilot-external",
  "session_name": "` + session.PolecatSessionName(session.PrefixFor(rigName), "toast") + `",
  "runtime_session_id": "runtime-xyz",
  "work_dir": "/tmp/worktree",
  "metadata": {
    "external_server": "true",
    "cli_url": "localhost:4321"
  }
}`
	if err := os.WriteFile(filepath.Join(bindingDir, "none--polecat--gastown--toast.json"), []byte(bindingData), 0o644); err != nil {
		t.Fatal(err)
	}
	oldWd, _ := os.Getwd()
	if err := os.Chdir(rigPath); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	if err := runSessionStatus(sessionStatusCmd, []string{"toast"}); err != nil {
		_ = w.Close()
		os.Stdout = oldStdout
		t.Fatalf("runSessionStatus() error = %v", err)
	}
	_ = w.Close()
	os.Stdout = oldStdout
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	printed := string(data)
	for _, want := range []string{"Runtime Session ID: runtime-xyz", "Provider: copilot-external", "Work Dir: /tmp/worktree", "cli_url=localhost:4321", "external_server=true"} {
		if !strings.Contains(printed, want) {
			t.Fatalf("output = %q, want %q", printed, want)
		}
	}
}
