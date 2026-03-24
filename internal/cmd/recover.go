package cmd

import (
	"context"
	"fmt"

	"github.com/steveyegge/gastown/internal/deacon"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/workspace"
)

func autoRecoverBinding(ctx context.Context, binding runtime.SessionBinding) error {
	_ = ctx
	switch binding.Role {
	case "mayor":
		mgr, err := getMayorManager()
		if err != nil {
			return err
		}
		return mgr.Start("")
	case "deacon":
		townRoot, err := workspace.FindFromCwdOrError()
		if err != nil {
			return err
		}
		mgr := deacon.NewManager(townRoot)
		return mgr.Start("")
	case "witness":
		mgr, err := getWitnessManager(binding.RigName)
		if err != nil {
			return err
		}
		return mgr.Start(false, "", nil)
	case "refinery":
		mgr, _, _, err := getRefineryManager(binding.RigName)
		if err != nil {
			return err
		}
		return mgr.Start(false, "")
	default:
		return fmt.Errorf("auto-recovery unsupported for role %s", binding.Role)
	}
}
