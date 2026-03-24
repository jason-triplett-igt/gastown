package runtime

import (
	"context"

	"github.com/steveyegge/gastown/internal/events"
)

const (
	TypeRuntimeSessionStart  = "runtime_session_start"
	TypeRuntimeSessionResume = "runtime_session_resume"
	TypeRuntimeSessionClose  = "runtime_session_close"
	TypeRuntimeSessionStatus = "runtime_session_status"
	TypeRuntimeSessionBusy   = "runtime_session_busy"
	TypeRuntimeSessionIdle   = "runtime_session_idle"
)

type LifecycleRecorder interface {
	Record(ctx context.Context, eventType, actor string, payload map[string]interface{}) error
}

type FeedLifecycleRecorder struct{}

func (FeedLifecycleRecorder) Record(_ context.Context, eventType, actor string, payload map[string]interface{}) error {
	return events.LogFeed(eventType, actor, payload)
}

func RuntimeSessionPayload(sessionName, runtimeSessionID, role, issueID, provider string, extra map[string]interface{}) map[string]interface{} {
	payload := map[string]interface{}{
		"session": sessionName,
		"role":    role,
	}
	if runtimeSessionID != "" {
		payload["runtime_session_id"] = runtimeSessionID
	}
	if issueID != "" {
		payload["issue"] = issueID
	}
	if provider != "" {
		payload["provider"] = provider
	}
	for key, value := range extra {
		payload[key] = value
	}
	return payload
}
