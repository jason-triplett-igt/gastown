package runtime

import "time"

func DeriveLifecycleState(binding *SessionBinding, alive, ready bool, statusErr error) string {
	if binding != nil {
		switch binding.LifecycleState {
		case SessionLifecycleStopping:
			return SessionLifecycleStopping
		case SessionLifecycleStarting:
			if alive {
				if ready {
					return SessionLifecycleRunning
				}
				if recentlyUpdated(binding) {
					return SessionLifecycleStarting
				}
				return SessionLifecycleUnknown
			}
			if statusErr != nil {
				if recentlyUpdated(binding) {
					return SessionLifecycleStarting
				}
				return SessionLifecycleUnknown
			}
			if recentlyUpdated(binding) {
				return SessionLifecycleStarting
			}
			return SessionLifecycleStopped
		}
	}

	if statusErr != nil {
		if binding != nil {
			return SessionLifecycleUnknown
		}
		return SessionLifecycleStopped
	}
	if alive {
		if !ready {
			return SessionLifecycleUnknown
		}
		return SessionLifecycleRunning
	}
	return SessionLifecycleStopped
}

func recentlyUpdated(binding *SessionBinding) bool {
	if binding == nil || binding.UpdatedAt.IsZero() {
		return false
	}
	return time.Since(binding.UpdatedAt) <= SessionLifecycleStartingGrace
}
