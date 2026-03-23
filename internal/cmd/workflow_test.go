package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

func TestRunWorkflowInspectPrintsReviewIntegrityState(t *testing.T) {
	townRoot := t.TempDir()
	rigName := "gastown"
	rigPath := filepath.Join(townRoot, rigName)
	if err := os.MkdirAll(filepath.Join(townRoot, "mayor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rigPath, 0o755); err != nil {
		t.Fatal(err)
	}
	rigsConfig := &config.RigsConfig{
		Version: config.CurrentRigsVersion,
		Rigs: map[string]config.RigEntry{
			rigName: {
				GitURL:    "file:///dev/null",
				LocalRepo: rigPath,
				AddedAt:   time.Now(),
				BeadsConfig: &config.BeadsConfig{
					Prefix: "gt",
				},
			},
		},
	}
	if err := config.SaveRigsConfig(filepath.Join(townRoot, "mayor", "rigs.json"), rigsConfig); err != nil {
		t.Fatal(err)
	}
	b := beads.NewIsolated(rigPath)
	if err := b.Init("workflow-cmd"); err != nil {
		t.Fatal(err)
	}
	desc, err := beads.PersistReviewVerdict(&beads.Issue{Description: beads.PersistVSDDState(&beads.Issue{}, &beads.VSDDPhaseFields{Phase: beads.VSDDPhaseReview, SpecApproved: true, TestsRed: true, Implementation: true}, &beads.VSDDArtifactFields{SpecArtifactID: "spec-1", SpecReviewArtifactID: "spec-review-1", TestPlanArtifactID: "tests-1", RedTestEvidenceID: "red-1", ImplementationArtifactID: "impl-1", BuilderEvidenceID: "builder-1"})}, beads.VSDDReviewVerdictInput{Verdict: "READY", Summary: "Review passed.", EvidenceRefs: []string{"spec-1", "impl-1"}, FreshContext: true, ReviewArtifactID: "review-1"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := b.Create(beads.CreateOptions{Title: "Workflow Inspect Task", Description: desc, Labels: []string{"gt:task"}})
	if err != nil {
		t.Fatal(err)
	}
	oldWd, _ := os.Getwd()
	if err := os.Chdir(rigPath); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()

	var out bytes.Buffer
	workflowInspectCmd.SetOut(&out)
	workflowInspectCmd.SetErr(&out)
	if err := runWorkflowInspect(workflowInspectCmd, []string{created.ID}); err != nil {
		t.Fatalf("runWorkflowInspect() error = %v", err)
	}
	printed := out.String()
	for _, want := range []string{"phase=review", "dispatch_ready=true", "review_contract_ok=true", "review_verdict=READY", "review_summary=Review passed."} {
		if !strings.Contains(printed, want) {
			t.Fatalf("output = %q, want %q", printed, want)
		}
	}
}
