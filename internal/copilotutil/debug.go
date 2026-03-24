package copilotutil

import (
	"os"
	"strings"
)

func DebugEnabled(names ...string) bool {
	for _, name := range names {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return true
		}
	}
	return false
}

func DebugOwnerToolsEnabled() bool {
	return DebugEnabled("GT_DEBUG_OWNER_TOOLS", "GT_DEBUG_COPILOT_TOOLS", "GT_DEBUG_COPILOT")
}

func DebugCopilotToolsEnabled() bool {
	return DebugEnabled("GT_DEBUG_COPILOT_TOOLS", "GT_DEBUG_COPILOT")
}

func DebugCopilotModelEnabled() bool {
	return DebugEnabled("GT_DEBUG_COPILOT_MODEL", "GT_DEBUG_COPILOT")
}
