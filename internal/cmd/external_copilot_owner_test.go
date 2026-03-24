package cmd

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/steveyegge/gastown/internal/runtime"
)

func TestRecordOwnerBusyTransitionEmitsBusyAndIdleEvents(t *testing.T) {
	cfg := &runtime.ExternalCopilotOwnerConfig{
		IssueID:     "slotmachine-910.9.9",
		Role:        "mayor",
		Provider:    "copilot-external",
		SessionName: "hq-mayor",
		WorkDir:     filepath.Join(t.TempDir(), "mayor"),
	}
	busy := false
	type recorded struct {
		eventType string
		actor     string
		payload   map[string]interface{}
	}
	var got []recorded
	oldRecorder := recordOwnerLifecycleEvent
	t.Cleanup(func() { recordOwnerLifecycleEvent = oldRecorder })
	recordOwnerLifecycleEvent = func(eventType, actor string, payload map[string]interface{}) error {
		got = append(got, recorded{eventType: eventType, actor: actor, payload: payload})
		return nil
	}

	recordOwnerBusyTransition(cfg, "runtime-1", true, &busy)
	recordOwnerBusyTransition(cfg, "runtime-1", true, &busy)
	recordOwnerBusyTransition(cfg, "runtime-1", false, &busy)

	if len(got) != 2 {
		t.Fatalf("recorded events = %d, want 2", len(got))
	}
	if got[0].eventType != runtime.TypeRuntimeSessionBusy || got[1].eventType != runtime.TypeRuntimeSessionIdle {
		t.Fatalf("events = %#v", got)
	}
	if got[0].actor != "mayor" || got[1].actor != "mayor" {
		t.Fatalf("actors = %#v", got)
	}
	if got[0].payload["busy"] != true || got[1].payload["busy"] != false {
		t.Fatalf("payloads = %#v", got)
	}
	if got[0].payload["session"] != "hq-mayor" || got[1].payload["runtime_session_id"] != "runtime-1" {
		t.Fatalf("payloads = %#v", got)
	}
}

func TestRecordOwnerBusyTransitionNoopsOnRepeatedState(t *testing.T) {
	cfg := &runtime.ExternalCopilotOwnerConfig{Role: "mayor", SessionName: "hq-mayor"}
	busy := false
	var got []string
	oldRecorder := recordOwnerLifecycleEvent
	t.Cleanup(func() { recordOwnerLifecycleEvent = oldRecorder })
	recordOwnerLifecycleEvent = func(eventType, actor string, payload map[string]interface{}) error {
		got = append(got, eventType)
		return nil
	}

	recordOwnerBusyTransition(cfg, "runtime-1", false, &busy)
	recordOwnerBusyTransition(cfg, "runtime-1", false, &busy)

	if !reflect.DeepEqual(got, []string(nil)) {
		t.Fatalf("events = %#v, want none", got)
	}
}
