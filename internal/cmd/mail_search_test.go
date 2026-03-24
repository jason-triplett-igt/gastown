package cmd

import "testing"

func TestResolveMailSearchIdentityUsesExplicitFlag(t *testing.T) {
	mailSearchIdentity = "mayor/"
	defer func() { mailSearchIdentity = "" }()

	if got := resolveMailSearchIdentity(); got != "mayor/" {
		t.Fatalf("resolveMailSearchIdentity() = %q, want %q", got, "mayor/")
	}
}

func TestResolveMailSearchIdentityFallsBackToDetectedSender(t *testing.T) {
	t.Setenv("GT_ROLE", "mayor")
	t.Setenv("GT_RIG", "")
	t.Setenv("GT_POLECAT", "")
	t.Setenv("GT_CREW", "")
	mailSearchIdentity = ""

	if got := resolveMailSearchIdentity(); got != "mayor/" {
		t.Fatalf("resolveMailSearchIdentity() = %q, want %q", got, "mayor/")
	}
}
