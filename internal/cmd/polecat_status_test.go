package cmd

import (
	"encoding/json"
	"testing"

	"github.com/steveyegge/gastown/internal/polecat"
)

func TestPolecatStatusJSONIncludesReadyAndBusyFields(t *testing.T) {
	status := PolecatStatus{
		Rig:            "gastown",
		Name:           "toast",
		SessionRunning: true,
		SessionReady:   false,
		SessionBusy:    true,
		StatusError:    "owner degraded",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if parsed["session_running"] != true {
		t.Fatalf("session_running = %v, want true", parsed["session_running"])
	}
	if parsed["session_ready"] != false {
		t.Fatalf("session_ready = %v, want false", parsed["session_ready"])
	}
	if parsed["session_busy"] != true {
		t.Fatalf("session_busy = %v, want true", parsed["session_busy"])
	}
	if parsed["status_error"] != "owner degraded" {
		t.Fatalf("status_error = %v, want owner degraded", parsed["status_error"])
	}
}

func TestSessionInfoJSONSupportsStatusError(t *testing.T) {
	info := polecat.SessionInfo{
		Polecat:     "toast",
		SessionID:   "runtime-123",
		Running:     true,
		Ready:       false,
		Busy:        true,
		RigName:     "gastown",
		StatusError: "owner degraded",
	}

	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if parsed["status_error"] != "owner degraded" {
		t.Fatalf("status_error = %v, want owner degraded", parsed["status_error"])
	}
}
