package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/workspace"
)

func init() {
	rootCmd.AddCommand(workflowCmd)
	workflowCmd.AddCommand(workflowInspectCmd)
}

var workflowCmd = &cobra.Command{
	Use:     "workflow",
	GroupID: GroupDiag,
	Short:   "Inspect VSDD workflow state",
}

var workflowInspectCmd = &cobra.Command{
	Use:   "inspect <issue-id> [rig]",
	Short: "Explain current workflow and review gate state",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runWorkflowInspect,
}

func runWorkflowInspect(cmd *cobra.Command, args []string) error {
	issueID := args[0]
	rigName := ""
	if len(args) > 1 {
		rigName = args[1]
	}
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return err
	}
	var rigInfo *rig.Rig
	if rigName != "" {
		_, rigInfo, err = getRig(rigName)
		if err != nil {
			return err
		}
	} else {
		rigName, err = inferRigFromCwd(townRoot)
		if err != nil {
			return fmt.Errorf("rig required outside a rig directory: %w", err)
		}
		_, rigInfo, err = getRig(rigName)
		if err != nil {
			return err
		}
	}
	mgr := polecat.NewSessionManager(nil, rigInfo)
	explanation, err := mgr.ExplainWorkflowState(issueID, rigInfo.Path)
	if err != nil {
		return err
	}
	cmd.Println(explanation)
	return nil
}
