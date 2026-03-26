//go:build integration

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/testutil"
)

const externalCopilotTestAgent = "copilot-external"

func TestExternalCopilotSessionSmoke(t *testing.T) {
	cliURL := requireExternalCopilotCLIURL(t)
	townRoot, rigPath, gtBinary, env := setupExternalCopilotIntegrationWorkspace(t, "smokeext")
	witnessDir := filepath.Join(rigPath, "witness")

	output := runGTCmdOutput(t, gtBinary, townRoot, env,
		"session", "smoke", "smokeext",
		"--role", "witness",
		"--workdir", witnessDir,
		"--cli-url", cliURL,
		"--prompt", "Reply with exactly READY.",
		"--resume-prompt", "Reply with exactly RESUMED.",
		"--timeout", "90s",
		"--json",
	)

	var result sessionSmokeResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("parse smoke output: %v\nraw: %s", err, output)
	}

	if result.Role != "witness" {
		t.Fatalf("Role = %q, want witness", result.Role)
	}
	if result.Provider != externalCopilotTestAgent {
		t.Fatalf("Provider = %q, want %q", result.Provider, externalCopilotTestAgent)
	}
	if result.CLIURL != cliURL {
		t.Fatalf("CLIURL = %q, want %q", result.CLIURL, cliURL)
	}
	if result.WorkDir != witnessDir {
		t.Fatalf("WorkDir = %q, want %q", result.WorkDir, witnessDir)
	}
	if result.RuntimeSessionID == "" {
		t.Fatal("RuntimeSessionID = empty, want populated session id")
	}
	if !result.CreateVerified || !result.ResumeVerified || !result.StopVerified {
		t.Fatalf("verification flags = %#v, want create/resume/stop all true", result)
	}
	if !strings.Contains(strings.ToUpper(result.FirstResponse), "READY") {
		t.Fatalf("FirstResponse = %q, want READY reply", result.FirstResponse)
	}
	if !strings.Contains(strings.ToUpper(result.SecondResponse), "RESUMED") {
		t.Fatalf("SecondResponse = %q, want RESUMED reply", result.SecondResponse)
	}
	if got := result.BindingMetadata["external_server"]; got != "true" {
		t.Fatalf("binding external_server = %q, want true", got)
	}
	if got := result.BindingMetadata["cli_url"]; got != cliURL {
		t.Fatalf("binding cli_url = %q, want %q", got, cliURL)
	}
	if got := result.BindingMetadata["owner_mode"]; got != "copilot-queue" {
		t.Fatalf("binding owner_mode = %q, want copilot-queue", got)
	}
}

func TestWitnessLifecycleWithExternalCopilot(t *testing.T) {
	cliURL := requireExternalCopilotCLIURL(t)
	townRoot, rigPath, gtBinary, env := setupExternalCopilotIntegrationWorkspace(t, "witnessext")
	writeExternalCopilotRigSettings(t, rigPath, cliURL)

	runGTCmdOutput(t, gtBinary, townRoot, env, "witness", "start", "witnessext")
	status := waitForWitnessRunningState(t, gtBinary, townRoot, env, "witnessext", true)
	if status.Session == "" {
		t.Fatal("witness status session = empty, want active session name")
	}

	runGTCmdOutput(t, gtBinary, townRoot, env, "witness", "stop", "witnessext")
	status = waitForWitnessRunningState(t, gtBinary, townRoot, env, "witnessext", false)
	if status.Running {
		t.Fatalf("witness status after stop = %#v, want running=false", status)
	}
	if status.State != "" && status.State != "stopped" {
		t.Fatalf("witness state after stop = %q, want stopped", status.State)
	}
}

func requireExternalCopilotCLIURL(t *testing.T) string {
	t.Helper()

	for _, key := range []string{"GT_TEST_COPILOT_CLI_URL", "GT_EXTERNAL_COPILOT_CLI_URL"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}

	t.Skip("set GT_TEST_COPILOT_CLI_URL to run external Copilot integration tests")
	return ""
}

func setupExternalCopilotIntegrationWorkspace(t *testing.T, rigName string) (string, string, string, []string) {
	t.Helper()

	homeDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(tempdir): %v", err)
	}
	configureTestGitIdentity(t, homeDir)

	townRoot := filepath.Join(homeDir, "town")
	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(mayorDir, 0o755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}
	if err := config.SaveTownConfig(filepath.Join(mayorDir, "town.json"), &config.TownConfig{
		Type:    "town",
		Name:    "external-copilot-test",
		Version: config.CurrentTownVersion,
	}); err != nil {
		t.Fatalf("save town.json: %v", err)
	}

	rigPath := filepath.Join(townRoot, rigName)
	createTestGitRepoAt(t, rigPath)
	for _, dir := range []string{"witness", "crew", "settings"} {
		if err := os.MkdirAll(filepath.Join(rigPath, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	rigs := &config.RigsConfig{
		Version: config.CurrentRigsVersion,
		Rigs: map[string]config.RigEntry{
			rigName: {
				GitURL: "https://example.com/external/copilot.git",
			},
		},
	}
	if err := config.SaveRigsConfig(filepath.Join(mayorDir, "rigs.json"), rigs); err != nil {
		t.Fatalf("save rigs.json: %v", err)
	}

	gtBinary := buildGT(t)
	env := testutil.CleanGTEnv("HOME=" + homeDir)
	return townRoot, rigPath, gtBinary, env
}

func writeExternalCopilotRigSettings(t *testing.T, rigPath, cliURL string) {
	t.Helper()

	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{
		externalCopilotTestAgent: {
			Provider: "copilot",
			Command:  "copilot",
			CLIURL:   cliURL,
		},
	}
	settings.RoleAgents = map[string]string{"witness": externalCopilotTestAgent}
	if err := config.SaveRigSettings(config.RigSettingsPath(rigPath), settings); err != nil {
		t.Fatalf("save rig settings: %v", err)
	}
}

func waitForWitnessRunningState(t *testing.T, gtBinary, townRoot string, env []string, rigName string, wantRunning bool) WitnessStatusOutput {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	for {
		status := readWitnessStatusJSON(t, gtBinary, townRoot, env, rigName)
		if status.Running == wantRunning {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("witness running=%t did not reach %t before timeout: %#v", status.Running, wantRunning, status)
		}
		time.Sleep(1 * time.Second)
	}
}

func readWitnessStatusJSON(t *testing.T, gtBinary, townRoot string, env []string, rigName string) WitnessStatusOutput {
	t.Helper()

	output := runGTCmdOutput(t, gtBinary, townRoot, env, "witness", "status", rigName, "--json")
	var status WitnessStatusOutput
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("parse witness status JSON: %v\nraw: %s", err, output)
	}
	return status
}
