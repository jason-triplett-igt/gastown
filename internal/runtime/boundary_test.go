package runtime

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

type stubSessionAdapter struct{}

func (stubSessionAdapter) Start(context.Context, SessionLaunchRequest) (ManagedSession, error) {
	return nil, nil
}

func (stubSessionAdapter) Resume(context.Context, SessionResumeRequest) (ManagedSession, error) {
	return nil, nil
}

func (stubSessionAdapter) Lookup(context.Context, SessionLookupRequest) (ManagedSession, error) {
	return nil, nil
}

type stubBindingStore struct{}

func (stubBindingStore) Save(context.Context, SessionBinding) error { return nil }
func (stubBindingStore) Load(context.Context, string, string, string, string) (*SessionBinding, error) {
	return nil, nil
}
func (stubBindingStore) List(context.Context, string, string) ([]SessionBinding, error) {
	return nil, nil
}
func (stubBindingStore) Delete(context.Context, string, string, string, string) error { return nil }

func TestBoundaryValidateReportsMissingComponents(t *testing.T) {
	err := (Boundary{}).Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want missing components")
	}

	for _, want := range []string{"auth", "hooks", "sessions", "tools"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate() error = %q, want component %q", err, want)
		}
	}
}

func TestBoundaryValidateAcceptsCompleteBoundary(t *testing.T) {
	b := Boundary{
		Sessions: stubSessionAdapter{},
		Auth:     StaticAuthProvider{},
		Hooks:    ConfigHookProvisioner{},
		Tools:    StaticToolCatalog{},
		ToolExec: StaticToolExecutor{},
	}

	if err := b.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want nil", err)
	}
}

func TestConfigHookProvisionerDelegatesToRuntimeSettings(t *testing.T) {
	settingsDir := t.TempDir()
	workDir := t.TempDir()

	err := (ConfigHookProvisioner{}).Ensure(context.Background(), HookRequest{
		SettingsDir: settingsDir,
		WorkDir:     workDir,
		Role:        "crew",
		RuntimeConfig: &config.RuntimeConfig{
			Hooks: &config.RuntimeHooksConfig{
				Provider:     "copilot",
				Dir:          ".copilot",
				SettingsFile: "copilot-instructions.md",
			},
		},
	})
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}

	if _, err := os.Stat(workDir + "/.copilot/copilot-instructions.md"); err != nil {
		t.Fatalf("expected Copilot instructions in workDir: %v", err)
	}
	if _, err := os.Stat(settingsDir + "/.copilot/copilot-instructions.md"); err == nil {
		t.Fatal("expected settingsDir to remain unused for Copilot")
	}
}

func TestStaticAuthProviderClonesEnv(t *testing.T) {
	provider := StaticAuthProvider{}
	inputEnv := map[string]string{"TOKEN": "abc"}
	result, err := provider.Prepare(context.Background(), AuthRequest{
		Env: inputEnv,
	})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	result.Env["TOKEN"] = "mutated"
	if got := inputEnv["TOKEN"]; got != "abc" {
		t.Fatalf("input env mutated = %q, want abc", got)
	}
}

func TestStaticToolCatalogReturnsRoleSpecificCopies(t *testing.T) {
	catalog := StaticToolCatalog{
		Default: []ToolDefinition{{Name: "default-tool"}},
		RoleTools: map[string][]ToolDefinition{
			"reviewer": {{Name: "read-only", ReadOnly: true}},
		},
	}

	tools, err := catalog.ToolsForRole(context.Background(), "reviewer")
	if err != nil {
		t.Fatalf("ToolsForRole() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "read-only" || !tools[0].ReadOnly {
		t.Fatalf("ToolsForRole(reviewer) = %#v", tools)
	}

	tools[0].Name = "mutated"
	toolsAgain, err := catalog.ToolsForRole(context.Background(), "reviewer")
	if err != nil {
		t.Fatalf("ToolsForRole() second call error = %v", err)
	}
	if toolsAgain[0].Name != "read-only" {
		t.Fatalf("ToolsForRole() returned shared slice, got %#v", toolsAgain)
	}
}

func TestDefaultToolCatalogEnforcesDistinctRoleBoundaries(t *testing.T) {
	catalog := DefaultToolCatalog()

	builderTools, err := catalog.ToolsForRole(context.Background(), "crew")
	if err != nil {
		t.Fatalf("ToolsForRole(crew) error = %v", err)
	}
	reviewerTools, err := catalog.ToolsForRole(context.Background(), "witness")
	if err != nil {
		t.Fatalf("ToolsForRole(witness) error = %v", err)
	}
	orchestratorTools, err := catalog.ToolsForRole(context.Background(), "mayor")
	if err != nil {
		t.Fatalf("ToolsForRole(mayor) error = %v", err)
	}

	assertToolPresence(t, builderTools, "run_single_test", true)
	assertToolPresence(t, builderTools, ToolLoadReview, false)
	assertToolPresence(t, reviewerTools, ToolLoadReview, true)
	assertToolPresence(t, reviewerTools, "send_mail", false)
	assertToolPresence(t, orchestratorTools, ToolBDReady, true)
	assertToolPresence(t, orchestratorTools, "run_unit_tests", false)
}

func assertToolPresence(t *testing.T, tools []ToolDefinition, name string, want bool) {
	t.Helper()
	found := false
	for _, tool := range tools {
		if tool.Name == name {
			found = true
			break
		}
	}
	if found != want {
		t.Fatalf("tools = %#v, found %q=%v want %v", tools, name, found, want)
	}
}
