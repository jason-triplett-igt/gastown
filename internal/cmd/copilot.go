package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/copilotutil"
)

var copilotCmd = &cobra.Command{
	Use:     "copilot",
	GroupID: GroupDiag,
	Short:   "Manage Copilot runtime infrastructure",
	RunE:    requireSubcommand,
}

var copilotServerCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage the local Copilot headless server",
	RunE:  requireSubcommand,
}

var copilotServerStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Copilot headless server status",
	RunE:  runCopilotServerStatus,
}

var copilotServerStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the local Copilot headless server",
	RunE:  runCopilotServerStart,
}

var copilotServerStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the local Copilot headless server",
	RunE:  runCopilotServerStop,
}

var copilotServerRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the local Copilot headless server",
	RunE:  runCopilotServerRestart,
}

func init() {
	copilotServerCmd.AddCommand(copilotServerStatusCmd)
	copilotServerCmd.AddCommand(copilotServerStartCmd)
	copilotServerCmd.AddCommand(copilotServerStopCmd)
	copilotServerCmd.AddCommand(copilotServerRestartCmd)
	copilotCmd.AddCommand(copilotServerCmd)
	rootCmd.AddCommand(copilotCmd)
}

func runCopilotServerStatus(cmd *cobra.Command, args []string) error {
	_ = args
	townRoot, err := copilotutil.TownRootOrError()
	if err != nil {
		return err
	}
	status, err := copilotutil.Status(townRoot)
	if err != nil {
		return err
	}
	return printCopilotServerStatus(cmd, status)
}

func runCopilotServerStart(cmd *cobra.Command, args []string) error {
	_ = args
	townRoot, err := copilotutil.TownRootOrError()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status, err := copilotutil.Start(ctx, townRoot)
	if err != nil {
		return err
	}
	return printCopilotServerStatus(cmd, status)
}

func runCopilotServerStop(cmd *cobra.Command, args []string) error {
	_ = args
	townRoot, err := copilotutil.TownRootOrError()
	if err != nil {
		return err
	}
	status, err := copilotutil.Stop(townRoot)
	if err != nil {
		return err
	}
	return printCopilotServerStatus(cmd, status)
}

func runCopilotServerRestart(cmd *cobra.Command, args []string) error {
	_ = args
	townRoot, err := copilotutil.TownRootOrError()
	if err != nil {
		return err
	}
	_, _ = copilotutil.Stop(townRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	status, err := copilotutil.Start(ctx, townRoot)
	if err != nil {
		return err
	}
	return printCopilotServerStatus(cmd, status)
}

func printCopilotServerStatus(cmd *cobra.Command, status *copilotutil.ServerStatus) error {
	if status == nil {
		return fmt.Errorf("copilot server status unavailable")
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(status)
}
