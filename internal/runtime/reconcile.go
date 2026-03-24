package runtime

import (
	"context"
	"strings"
)

type ReconcileResult struct {
	Scanned       int `json:"scanned"`
	StaleCleared  int `json:"stale_cleared"`
	OwnerAlive    int `json:"owner_alive"`
	OwnerMissing  int `json:"owner_missing"`
	AutoRecovered int `json:"auto_recovered"`
}

type AutoRecoverAction func(ctx context.Context, binding SessionBinding) error

func ReconcileExternalOwners(ctx context.Context, townRoot string) (*ReconcileResult, error) {
	return ReconcileExternalOwnersWithRecover(ctx, townRoot, nil)
}

func ReconcileExternalOwnersWithRecover(ctx context.Context, townRoot string, recoverFn AutoRecoverAction) (*ReconcileResult, error) {
	store := NewFileSessionBindingStore(townRoot)
	bindings, err := store.List(ctx, "", "")
	if err != nil {
		return nil, err
	}
	result := &ReconcileResult{}
	for i := range bindings {
		binding := bindings[i]
		if !IsExternalOwnerBinding(&binding) {
			continue
		}
		result.Scanned++
		discovery, err := DiscoverExternalOwner(townRoot, &binding)
		if err != nil {
			continue
		}
		if discovery.OwnerAlive {
			result.OwnerAlive++
			continue
		}
		result.OwnerMissing++
		original := binding
		if discovery.NeedsRecovery || strings.TrimSpace(binding.Metadata[ExternalOwnerPIDMetadataKey]) != "" {
			if err := MarkExternalOwnerRecovered(ctx, store, &binding); err == nil {
				result.StaleCleared++
				if recoverFn != nil && autoRecoverEligible(original) {
					if err := recoverFn(ctx, original); err == nil {
						result.AutoRecovered++
					}
				}
			}
		}
	}
	return result, nil
}

func autoRecoverEligible(binding SessionBinding) bool {
	if strings.TrimSpace(binding.RuntimeSessionID) == "" {
		return false
	}
	switch strings.TrimSpace(binding.Role) {
	case "mayor", "deacon", "witness", "refinery":
		return true
	default:
		return false
	}
}
