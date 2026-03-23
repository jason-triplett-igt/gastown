package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	toolsExplainSessionKind string
	toolsExplainJSON        bool
	toolsExplainRig         string
	toolsExplainWorkDir     string
)

type toolsExplainOutput struct {
	Role        string            `json:"role"`
	SessionKind string            `json:"session_kind"`
	TownRoot    string            `json:"town_root,omitempty"`
	RigPath     string            `json:"rig_path,omitempty"`
	WorkDir     string            `json:"work_dir,omitempty"`
	Policy      config.ToolPolicy `json:"policy"`
}

var toolsCmd = &cobra.Command{
	Use:     "tools",
	GroupID: GroupDiag,
	Short:   "Inspect tool policy state",
	RunE:    requireSubcommand,
}

var toolsExplainCmd = &cobra.Command{
	Use:   "explain <role>",
	Short: "Explain the effective tool policy for a role",
	Args:  cobra.ExactArgs(1),
	RunE:  runToolsExplain,
}

func init() {
	toolsExplainCmd.Flags().StringVar(&toolsExplainSessionKind, "session-kind", config.ToolSessionKindPatrol, "Session kind to explain")
	toolsExplainCmd.Flags().BoolVar(&toolsExplainJSON, "json", false, "Output policy as JSON")
	toolsExplainCmd.Flags().StringVar(&toolsExplainRig, "rig", "", "Rig name for rig-scoped role overrides")
	toolsExplainCmd.Flags().StringVar(&toolsExplainWorkDir, "workdir", "", "Working directory to use for path-scoped read rules")
	toolsCmd.AddCommand(toolsExplainCmd)
	rootCmd.AddCommand(toolsCmd)
}

func runToolsExplain(cmd *cobra.Command, args []string) error {
	result, err := explainToolPolicy(args[0], toolsExplainSessionKind, toolsExplainRig, toolsExplainWorkDir)
	if err != nil {
		return err
	}
	if toolsExplainJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "role=%s\n", result.Role)
	fmt.Fprintf(cmd.OutOrStdout(), "session_kind=%s\n", result.SessionKind)
	if result.TownRoot != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "town_root=%s\n", result.TownRoot)
	}
	if result.RigPath != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "rig_path=%s\n", result.RigPath)
	}
	if result.WorkDir != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "work_dir=%s\n", result.WorkDir)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "available_tools=%s\n", strings.Join(result.Policy.AvailableTools, ","))
	if len(result.Policy.ExcludedTools) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "excluded_tools=%s\n", strings.Join(result.Policy.ExcludedTools, ","))
	}
	for _, rule := range result.Policy.ApprovalRules {
		fmt.Fprintf(cmd.OutOrStdout(), "rule action=%s kind=%s tool=%s path_prefix=%s command_prefix=%s require_read_only=%t\n",
			rule.Action,
			rule.Kind,
			rule.ToolName,
			rule.PathPrefix,
			rule.CommandPrefix,
			rule.RequireReadOnly,
		)
	}
	return nil
}

func explainToolPolicy(role, sessionKind, rigName, workDir string) (*toolsExplainOutput, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return nil, fmt.Errorf("role is required")
	}
	resolvedKind := strings.TrimSpace(sessionKind)
	if resolvedKind == "" {
		resolvedKind = config.ToolSessionKindPatrol
	}
	townRoot, rigPath, err := resolveToolExplainScope(role, rigName)
	if err != nil {
		return nil, err
	}
	resolvedWorkDir := strings.TrimSpace(workDir)
	if resolvedWorkDir == "" {
		resolvedWorkDir = defaultToolExplainWorkDir(townRoot, rigPath, role)
	}
	return &toolsExplainOutput{
		Role:        role,
		SessionKind: resolvedKind,
		TownRoot:    townRoot,
		RigPath:     rigPath,
		WorkDir:     resolvedWorkDir,
		Policy:      config.ResolveToolPolicyForSession(townRoot, rigPath, role, resolvedKind, resolvedWorkDir),
	}, nil
}

func resolveToolExplainScope(role, rigName string) (string, string, error) {
	rigName = strings.TrimSpace(rigName)
	if rigName != "" {
		townRoot, r, err := getRig(rigName)
		if err != nil {
			return "", "", err
		}
		return townRoot, r.Path, nil
	}
	townRoot, err := workspace.FindFromCwd()
	if err != nil || strings.TrimSpace(townRoot) == "" {
		return "", "", nil
	}
	if role == "mayor" || role == "deacon" || role == "boot" {
		return townRoot, "", nil
	}
	roleInfo, err := GetRoleWithContext(townRoot, townRoot)
	if err == nil && strings.TrimSpace(roleInfo.Rig) != "" {
		r, rigErr := os.Stat(filepath.Join(townRoot, roleInfo.Rig))
		if rigErr == nil && r.IsDir() {
			return townRoot, filepath.Join(townRoot, roleInfo.Rig), nil
		}
	}
	return townRoot, "", nil
}

func defaultToolExplainWorkDir(townRoot, rigPath, role string) string {
	switch strings.TrimSpace(role) {
	case "mayor":
		if townRoot != "" {
			return filepath.Join(townRoot, "mayor")
		}
	case "deacon":
		if townRoot != "" {
			return filepath.Join(townRoot, "deacon")
		}
	case "witness":
		if rigPath == "" {
			return ""
		}
		if candidate := filepath.Join(rigPath, "witness", "rig"); dirExists(candidate) {
			return candidate
		}
		if candidate := filepath.Join(rigPath, "witness"); dirExists(candidate) {
			return candidate
		}
		return rigPath
	case "refinery":
		if rigPath == "" {
			return ""
		}
		if candidate := filepath.Join(rigPath, "refinery", "rig"); dirExists(candidate) {
			return candidate
		}
		return rigPath
	default:
		if rigPath != "" {
			return rigPath
		}
		return townRoot
	}
	return ""
}

func dirExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
