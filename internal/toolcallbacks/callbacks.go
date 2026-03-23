package toolcallbacks

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/mail"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/toolapi"
)

func ForTown(townRoot, workDir string) toolapi.Callbacks {
	return toolapi.Callbacks{
		SendMail: func(from, to, subject, body, priority string) error {
			msg := mail.NewMessage(from, to, subject, body)
			if strings.EqualFold(strings.TrimSpace(priority), "urgent") {
				msg.Priority = mail.PriorityUrgent
			}
			return mail.NewRouterWithTownRoot(workDir, townRoot).Send(msg)
		},
		ResolveSessionName: ResolveSessionName,
		QueueNudge: func(sessionName, sender, message string) error {
			return nudge.Enqueue(townRoot, sessionName, nudge.QueuedNudge{Sender: sender, Message: message, Priority: nudge.PriorityNormal})
		},
	}
}

func ResolveSessionName(target string) (string, error) {
	target = strings.TrimSuffix(strings.TrimSpace(target), "/")
	switch target {
	case "mayor":
		return session.MayorSessionName(), nil
	case "deacon":
		return session.DeaconSessionName(), nil
	}
	parts := strings.Split(target, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("unsupported target %q", target)
	}
	prefix := session.PrefixFor(parts[0])
	switch parts[1] {
	case "witness":
		return session.WitnessSessionName(prefix), nil
	case "refinery":
		return session.RefinerySessionName(prefix), nil
	case "crew":
		if len(parts) == 3 {
			return session.CrewSessionName(prefix, parts[2]), nil
		}
	case "polecats":
		if len(parts) == 3 {
			return session.PolecatSessionName(prefix, parts[2]), nil
		}
	default:
		return session.PolecatSessionName(prefix, parts[1]), nil
	}
	return "", fmt.Errorf("unsupported target %q", target)
}
