package cmd

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAskCommandRejectsUnsupportedTarget(t *testing.T) {
	err := runAsk(askCmd, []string{"alpha", "hello"})
	if err == nil {
		t.Fatal("expected error for unsupported target")
	}
	if got := err.Error(); got == "" || !containsAny(got, []string{"supports", "unsupported"}) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveAskSessionTownRoles(t *testing.T) {
	tests := map[string]string{
		"mayor":  "hq-mayor",
		"deacon": "hq-deacon",
	}
	for target, want := range tests {
		t.Run(target, func(t *testing.T) {
			got, err := resolveAskSession(target)
			if err != nil {
				t.Fatalf("resolveAskSession(%q) error = %v", target, err)
			}
			if got != want {
				t.Fatalf("resolveAskSession(%q) = %q, want %q", target, got, want)
			}
		})
	}
}

func TestResolveAskSessionRigRolesFromEnv(t *testing.T) {
	t.Setenv("GT_ROLE", "gastown/witness")
	t.Setenv("GT_RIG", "gastown")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PWD", wd)

	got, err := resolveAskSession("witness")
	if err != nil {
		t.Fatalf("resolveAskSession(witness) error = %v", err)
	}
	if got != "gt-witness" {
		t.Fatalf("resolveAskSession(witness) = %q, want gt-witness", got)
	}

	got, err = resolveAskSession("refinery")
	if err != nil {
		t.Fatalf("resolveAskSession(refinery) error = %v", err)
	}
	if got != "gt-refinery" {
		t.Fatalf("resolveAskSession(refinery) = %q, want gt-refinery", got)
	}
}

func TestAskCommandRequiresTwoArgs(t *testing.T) {
	if askCmd.Args == nil {
		t.Fatal("askCmd.Args should be configured")
	}
	if err := askCmd.Args(askCmd, []string{"mayor"}); err == nil {
		t.Fatal("expected args validation error")
	}
}

func TestAskTimeoutDefault(t *testing.T) {
	if askTimeout != 0 && askTimeout != 60*time.Second {
		t.Fatalf("askTimeout default = %v, want 0 or 60s before init/use", askTimeout)
	}
}

func TestShouldRecoverAsk(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "session error", err: errString("waiting for reply: session error: tool failed"), want: true},
		{name: "owner session not found", err: errString("waiting for owner-routed reply: owner ask failed: failed to send message: JSON-RPC Error -32603: Request session.send failed with message: Session not found: abc"), want: true},
		{name: "bad request", err: errString("CAPIError: 400 400 Bad Request"), want: true},
		{name: "type error", err: errString("TypeError: Cannot read properties of undefined"), want: true},
		{name: "ordinary timeout", err: errString("context deadline exceeded"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRecoverAsk(tt.err); got != tt.want {
				t.Fatalf("shouldRecoverAsk(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func containsAny(value string, needles []string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

type errString string

func (e errString) Error() string { return string(e) }
