package sessionenv

import "testing"

func TestSessionIDFromEnv(t *testing.T) {
	t.Setenv("GT_SESSION_ID_ENV", "")
	t.Setenv("GT_AGENT", "")
	t.Setenv("CLAUDE_SESSION_ID", "")
	if got := SessionIDFromEnv(); got != "" {
		t.Fatalf("SessionIDFromEnv() = %q, want empty", got)
	}

	t.Setenv("GT_SESSION_ID_ENV", "MY_SESSION_ENV")
	t.Setenv("MY_SESSION_ENV", "abc123")
	if got := SessionIDFromEnv(); got != "abc123" {
		t.Fatalf("SessionIDFromEnv() = %q, want abc123", got)
	}
}
