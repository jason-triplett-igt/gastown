package runtime

import "testing"

func TestRuntimeSessionPayloadIncludesCommonFields(t *testing.T) {
	t.Parallel()
	payload := RuntimeSessionPayload("gt-toast", "runtime-123", "polecat", "slotmachine-910", "claude", map[string]interface{}{"alive": true})
	if payload["session"] != "gt-toast" {
		t.Fatalf("session = %v", payload["session"])
	}
	if payload["runtime_session_id"] != "runtime-123" {
		t.Fatalf("runtime_session_id = %v", payload["runtime_session_id"])
	}
	if payload["issue"] != "slotmachine-910" {
		t.Fatalf("issue = %v", payload["issue"])
	}
	if payload["provider"] != "claude" {
		t.Fatalf("provider = %v", payload["provider"])
	}
	if payload["alive"] != true {
		t.Fatalf("alive = %v", payload["alive"])
	}
}
