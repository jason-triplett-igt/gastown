package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/copilotutil"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	modelJSON        bool
	modelRoleRig     string
	modelSessionKind string
)

type modelInfoOutput struct {
	ID                        string   `json:"id"`
	Name                      string   `json:"name"`
	DefaultReasoningEffort    string   `json:"default_reasoning_effort,omitempty"`
	SupportedReasoningEfforts []string `json:"supported_reasoning_efforts,omitempty"`
	SupportsReasoningEffort   bool     `json:"supports_reasoning_effort,omitempty"`
	SupportsVision            bool     `json:"supports_vision,omitempty"`
	PolicyState               string   `json:"policy_state,omitempty"`
	PolicyTerms               string   `json:"policy_terms,omitempty"`
}

type effectiveModelOutput struct {
	Role             string `json:"role"`
	SessionKind      string `json:"session_kind"`
	TownRoot         string `json:"town_root,omitempty"`
	RigPath          string `json:"rig_path,omitempty"`
	ResolvedAgent    string `json:"resolved_agent,omitempty"`
	Provider         string `json:"provider,omitempty"`
	CLIURL           string `json:"cli_url,omitempty"`
	RequestedModel   string `json:"requested_model,omitempty"`
	ReasoningEffort  string `json:"reasoning_effort,omitempty"`
	ResolvedModel    string `json:"resolved_model,omitempty"`
	ManagedSessionID string `json:"managed_session_id,omitempty"`
}

var modelCmd = &cobra.Command{
	Use:     "model",
	GroupID: GroupDiag,
	Short:   "Inspect Copilot model state",
	RunE:    requireSubcommand,
}

var modelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available Copilot models",
	RunE:  runModelList,
}

var modelEffectiveCmd = &cobra.Command{
	Use:   "effective <role>",
	Short: "Show effective model for a role/session",
	Args:  cobra.ExactArgs(1),
	RunE:  runModelEffective,
}

func init() {
	modelListCmd.Flags().BoolVar(&modelJSON, "json", false, "Output as JSON")
	modelEffectiveCmd.Flags().BoolVar(&modelJSON, "json", false, "Output as JSON")
	modelEffectiveCmd.Flags().StringVar(&modelRoleRig, "rig", "", "Rig name for rig-scoped role resolution")
	modelEffectiveCmd.Flags().StringVar(&modelSessionKind, "session-kind", config.ToolSessionKindPatrol, "Session kind for role resolution context")
	modelCmd.AddCommand(modelListCmd)
	modelCmd.AddCommand(modelEffectiveCmd)
	rootCmd.AddCommand(modelCmd)
}

func runModelList(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return err
	}
	rc := config.ResolveRoleAgentConfig("mayor", townRoot, "")
	if rc == nil || !usesCopilotExternalForModel(rc) {
		return fmt.Errorf("current town is not configured to use copilot external for mayor")
	}
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: strings.TrimSpace(rc.CLIURL)})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.Start(ctx); err != nil {
		return err
	}
	defer func() { _ = copilotutil.StopClientQuietly(client) }()
	models, err := client.ListModels(ctx)
	if err != nil {
		return err
	}
	out := make([]modelInfoOutput, 0, len(models))
	for _, model := range models {
		item := modelInfoOutput{ID: model.ID, Name: model.Name, SupportedReasoningEfforts: append([]string(nil), model.SupportedReasoningEfforts...)}
		item.DefaultReasoningEffort = model.DefaultReasoningEffort
		item.SupportsReasoningEffort = model.Capabilities.Supports.ReasoningEffort
		item.SupportsVision = model.Capabilities.Supports.Vision
		if model.Policy != nil {
			item.PolicyState = model.Policy.State
			item.PolicyTerms = model.Policy.Terms
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if modelJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, item := range out {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\tdefault_effort=%s\n", item.ID, item.Name, item.DefaultReasoningEffort)
	}
	return nil
}

func runModelEffective(cmd *cobra.Command, args []string) error {
	result, err := effectiveModel(args[0], modelRoleRig, modelSessionKind)
	if err != nil {
		return err
	}
	if modelJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "role=%s\n", result.Role)
	fmt.Fprintf(cmd.OutOrStdout(), "session_kind=%s\n", result.SessionKind)
	fmt.Fprintf(cmd.OutOrStdout(), "resolved_agent=%s\n", result.ResolvedAgent)
	fmt.Fprintf(cmd.OutOrStdout(), "provider=%s\n", result.Provider)
	fmt.Fprintf(cmd.OutOrStdout(), "cli_url=%s\n", result.CLIURL)
	fmt.Fprintf(cmd.OutOrStdout(), "requested_model=%s\n", result.RequestedModel)
	fmt.Fprintf(cmd.OutOrStdout(), "reasoning_effort=%s\n", result.ReasoningEffort)
	fmt.Fprintf(cmd.OutOrStdout(), "resolved_model=%s\n", result.ResolvedModel)
	if result.ManagedSessionID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "managed_session_id=%s\n", result.ManagedSessionID)
	}
	return nil
}

func effectiveModel(role, rigName, sessionKind string) (*effectiveModelOutput, error) {
	townRoot, rigPath, err := resolveToolExplainScope(role, rigName)
	if err != nil {
		return nil, err
	}
	role = strings.TrimSpace(role)
	if role == "" {
		return nil, fmt.Errorf("role is required")
	}
	rc := config.ResolveRoleAgentConfig(role, townRoot, rigPath)
	if rc == nil {
		return nil, fmt.Errorf("no runtime config resolved for role %s", role)
	}
	out := &effectiveModelOutput{
		Role:            role,
		SessionKind:     strings.TrimSpace(sessionKind),
		TownRoot:        townRoot,
		RigPath:         rigPath,
		ResolvedAgent:   rc.ResolvedAgent,
		Provider:        rc.Provider,
		CLIURL:          rc.CLIURL,
		RequestedModel:  rc.Model,
		ReasoningEffort: rc.ReasoningEffort,
	}
	if !usesCopilotExternalForModel(rc) {
		return out, nil
	}
	client := copilot.NewClient(&copilot.ClientOptions{CLIUrl: strings.TrimSpace(rc.CLIURL)})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if townRoot != "" {
		binding, err := runtime.NewFileSessionBindingStore(townRoot).Load(ctx, sessionNameForRole(role, rigPath), "", "", "")
		if err == nil && binding != nil && strings.TrimSpace(binding.RuntimeSessionID) != "" {
			out.ManagedSessionID = binding.RuntimeSessionID
			sess, resumeErr := client.ResumeSession(ctx, binding.RuntimeSessionID, &copilot.ResumeSessionConfig{WorkingDirectory: binding.WorkDir, DisableResume: true})
			if resumeErr == nil {
				defer sess.Disconnect()
				if current, currentErr := sess.RPC.Model.GetCurrent(ctx); currentErr == nil && current != nil && current.ModelID != nil {
					out.ResolvedModel = *current.ModelID
				}
			}
		}
	}
	return out, nil
}

func usesCopilotExternalForModel(rc *config.RuntimeConfig) bool {
	if rc == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(rc.Provider), "copilot") && strings.TrimSpace(rc.CLIURL) != ""
}

func sessionNameForRole(role, rigPath string) string {
	switch strings.TrimSpace(role) {
	case "mayor":
		return session.MayorSessionName()
	case "deacon":
		return session.DeaconSessionName()
	case "witness":
		return session.WitnessSessionName(session.PrefixFor(filepathBaseOrRig(rigPath)))
	case "refinery":
		return session.RefinerySessionName(session.PrefixFor(filepathBaseOrRig(rigPath)))
	default:
		return strings.TrimSpace(role)
	}
}

func filepathBaseOrRig(rigPath string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(rigPath), "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
