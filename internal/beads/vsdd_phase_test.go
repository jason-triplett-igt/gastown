package beads

import (
	"strings"
	"testing"
)

func TestParseAndSetVSDDPhaseFields(t *testing.T) {
	t.Parallel()
	issue := &Issue{Description: "vsdd_phase: tests\nvsdd_spec_approved: true\n\nBody line"}
	fields := ParseVSDDPhaseFields(issue)
	if fields == nil || fields.Phase != VSDDPhaseTests || !fields.SpecApproved {
		t.Fatalf("ParseVSDDPhaseFields() = %#v", fields)
	}
	updated := SetVSDDPhaseFields(issue, &VSDDPhaseFields{Phase: VSDDPhaseReview, SpecApproved: true, TestsRed: true, Implementation: true})
	if updated == "" || !containsAll(updated, []string{"vsdd_phase: review", "vsdd_implementation_done: true", "Body line"}) {
		t.Fatalf("SetVSDDPhaseFields() = %q", updated)
	}
}

func TestValidateReviewVerdictContract(t *testing.T) {
	t.Parallel()
	valid := &VSDDPhaseFields{
		Phase:          VSDDPhaseReview,
		Implementation: true,
		ReviewVerdict:  "READY",
		ReviewSummary:  "Reviewed approved artifacts and found no blocking issues.",
		ReviewEvidence: []string{"spec-1", "impl-1", "review-1"},
		ReviewFresh:    true,
	}
	if err := ValidateReviewVerdictContract(valid); err != nil {
		t.Fatalf("ValidateReviewVerdictContract() error = %v", err)
	}
	invalid := &VSDDPhaseFields{ReviewVerdict: "MAYBE", ReviewFresh: false}
	if err := ValidateReviewVerdictContract(invalid); err == nil || !strings.Contains(err.Error(), string(VSDDRejectReviewMalformed)) {
		t.Fatalf("expected malformed review error, got %v", err)
	}
	notReady := &VSDDPhaseFields{
		ReviewVerdict:  "NOT READY",
		ReviewSummary:  "Builder evidence misses regression coverage.",
		ReviewEvidence: []string{"builder-1"},
		ReviewFresh:    true,
	}
	if err := ValidateReviewVerdictContract(notReady); err == nil || !strings.Contains(err.Error(), "findings") {
		t.Fatalf("expected findings requirement error, got %v", err)
	}
}

func TestValidateVSDDTransition(t *testing.T) {
	t.Parallel()
	if err := ValidateVSDDTransition(&VSDDPhaseFields{Phase: VSDDPhaseSpec, SpecApproved: false}, VSDDPhaseTests); err == nil || err.Error() != string(VSDDRejectSpecRequired) {
		t.Fatalf("expected spec_required, got %v", err)
	}
	if err := ValidateVSDDTransition(&VSDDPhaseFields{Phase: VSDDPhaseTests, SpecApproved: true, TestsRed: true}, VSDDPhaseImplementation); err != nil {
		t.Fatalf("expected implementation transition to pass, got %v", err)
	}
	if err := ValidateVSDDTransition(&VSDDPhaseFields{Phase: VSDDPhaseReview, ReviewVerdict: "NOT READY", ReviewApproved: false}, VSDDPhaseConvergence); err == nil || err.Error() != string(VSDDRejectReviewFailed) {
		t.Fatalf("expected review_failed, got %v", err)
	}
}

func TestValidateVSDDTransitionMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		current VSDDPhaseFields
		target  VSDDPhase
		wantErr string
	}{
		{name: "spec to tests blocked without approved spec", current: VSDDPhaseFields{Phase: VSDDPhaseSpec}, target: VSDDPhaseTests, wantErr: string(VSDDRejectSpecRequired)},
		{name: "tests to implementation blocked without red tests", current: VSDDPhaseFields{Phase: VSDDPhaseTests, SpecApproved: true}, target: VSDDPhaseImplementation, wantErr: string(VSDDRejectTestsRequired)},
		{name: "implementation to review blocked without implementation", current: VSDDPhaseFields{Phase: VSDDPhaseImplementation, SpecApproved: true, TestsRed: true}, target: VSDDPhaseReview, wantErr: string(VSDDRejectImplementationReq)},
		{name: "review to convergence blocked without review verdict", current: VSDDPhaseFields{Phase: VSDDPhaseReview, Implementation: true}, target: VSDDPhaseConvergence, wantErr: string(VSDDRejectReviewRequired)},
		{name: "review to convergence allowed with ready verdict", current: VSDDPhaseFields{Phase: VSDDPhaseReview, Implementation: true, ReviewVerdict: "READY", ReviewApproved: true}, target: VSDDPhaseConvergence},
		{name: "convergence to done allowed", current: VSDDPhaseFields{Phase: VSDDPhaseConvergence, ReviewVerdict: "READY", ReviewApproved: true}, target: VSDDPhaseDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVSDDTransition(&tt.current, tt.target)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateVSDDTransition() error = %v", err)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("ValidateVSDDTransition() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestNextVSDDStateNormalizesFlags(t *testing.T) {
	t.Parallel()
	next, err := NextVSDDState(&VSDDPhaseFields{Phase: VSDDPhaseTests, SpecApproved: true, TestsRed: true}, VSDDPhaseImplementation)
	if err != nil {
		t.Fatalf("NextVSDDState() error = %v", err)
	}
	if next.Phase != VSDDPhaseImplementation {
		t.Fatalf("Phase = %q", next.Phase)
	}
	if !next.SpecApproved || !next.TestsRed {
		t.Fatalf("normalized flags = %#v", next)
	}

	rejected, err := NextVSDDState(&VSDDPhaseFields{Phase: VSDDPhaseSpec}, VSDDPhaseImplementation)
	if err == nil {
		t.Fatal("expected transition rejection")
	}
	if rejected.LastRejection == "" {
		t.Fatal("expected rejection reason to be recorded")
	}
	if rejected.LastTransition == "" {
		t.Fatal("expected rejection transition to be recorded")
	}
}

func TestRequiredArtifactsForPhase(t *testing.T) {
	t.Parallel()
	req := RequiredArtifactsForPhase(VSDDPhaseConvergence)
	if !req.SpecArtifact || !req.ReviewArtifact {
		t.Fatalf("requirements = %#v", req)
	}
	if req.ConvergenceEvidence {
		t.Fatalf("convergence should not require convergence evidence on entry: %#v", req)
	}
}

func TestVSDDArtifactFieldsSatisfiesRequirements(t *testing.T) {
	t.Parallel()
	artifacts := VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "spec-review-1",
		TestPlanArtifactID:       "test-plan-1",
		RedTestEvidenceID:        "red-tests-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-evidence-1",
		ReviewArtifactID:         "review-1",
		ConvergenceEvidenceID:    "conv-1",
	}
	ok, missing := artifacts.Satisfies(RequiredArtifactsForPhase(VSDDPhaseDone))
	if !ok || len(missing) != 0 {
		t.Fatalf("Satisfies() = %v, %v", ok, missing)
	}

	artifacts.ReviewArtifactID = ""
	ok, missing = artifacts.Satisfies(RequiredArtifactsForPhase(VSDDPhaseConvergence))
	if ok || len(missing) != 1 || missing[0] != "review_artifact" {
		t.Fatalf("expected missing review_artifact, got ok=%v missing=%v", ok, missing)
	}
}

func TestParseAndSetVSDDArtifactFields(t *testing.T) {
	t.Parallel()
	issue := &Issue{Description: "vsdd_spec_artifact: spec-1\nvsdd_test_plan_artifact: tests-1\n\nBody"}
	fields := ParseVSDDArtifactFields(issue)
	if fields == nil || fields.SpecArtifactID != "spec-1" || fields.TestPlanArtifactID != "tests-1" {
		t.Fatalf("ParseVSDDArtifactFields() = %#v", fields)
	}
	updated := SetVSDDArtifactFields(issue, &VSDDArtifactFields{SpecArtifactID: "spec-2", ReviewArtifactID: "review-9"})
	if !containsAll(updated, []string{"vsdd_spec_artifact: spec-2", "vsdd_review_artifact: review-9", "Body"}) {
		t.Fatalf("SetVSDDArtifactFields() = %q", updated)
	}
}

func TestPersistVSDDState(t *testing.T) {
	t.Parallel()
	issue := &Issue{Description: "Body"}
	phase := &VSDDPhaseFields{Phase: VSDDPhaseReview, SpecApproved: true, TestsRed: true, Implementation: true, LastTransition: "entered:review"}
	artifacts := &VSDDArtifactFields{ReviewArtifactID: "review-1"}
	updated := PersistVSDDState(issue, phase, artifacts)
	if !containsAll(updated, []string{"vsdd_phase: review", "vsdd_last_transition: entered:review", "vsdd_review_artifact: review-1", "Body"}) {
		t.Fatalf("PersistVSDDState() = %q", updated)
	}
}

func TestPersistReviewVerdict(t *testing.T) {
	t.Parallel()
	issue := &Issue{Description: PersistVSDDState(&Issue{Description: "Body"}, &VSDDPhaseFields{Phase: VSDDPhaseReview, SpecApproved: true, TestsRed: true, Implementation: true}, &VSDDArtifactFields{SpecArtifactID: "spec-1", SpecReviewArtifactID: "spec-review-1", TestPlanArtifactID: "tests-1", RedTestEvidenceID: "red-1", ImplementationArtifactID: "impl-1", BuilderEvidenceID: "builder-1"})}
	updated, err := PersistReviewVerdict(issue, VSDDReviewVerdictInput{
		Verdict:          "READY",
		Summary:          "Reviewed approved evidence and found no blockers.",
		EvidenceRefs:     []string{"spec-1", "impl-1", "builder-1"},
		Findings:         nil,
		FreshContext:     true,
		ReviewArtifactID: "review-1",
	})
	if err != nil {
		t.Fatalf("PersistReviewVerdict() error = %v", err)
	}
	if !containsAll(updated, []string{"vsdd_review_verdict: READY", "vsdd_review_summary: Reviewed approved evidence and found no blockers.", "vsdd_review_artifact: review-1", "vsdd_review_fresh_context: true"}) {
		t.Fatalf("PersistReviewVerdict() = %q", updated)
	}
	blocked, err := PersistReviewVerdict(issue, VSDDReviewVerdictInput{Verdict: "NOT READY", Summary: "Missing findings", EvidenceRefs: []string{"spec-1"}, FreshContext: true})
	if err == nil || !strings.Contains(err.Error(), string(VSDDRejectReviewMalformed)) {
		t.Fatalf("expected malformed review error, got %v", err)
	}
	if !strings.Contains(blocked, "vsdd_last_rejection: review_malformed:findings") {
		t.Fatalf("blocked review state = %q", blocked)
	}
}

func TestValidateDispatchGate(t *testing.T) {
	t.Parallel()
	phase := &VSDDPhaseFields{Phase: VSDDPhaseTests, SpecApproved: true, TestsRed: true}
	artifacts := &VSDDArtifactFields{
		SpecArtifactID:       "spec-1",
		SpecReviewArtifactID: "spec-review-1",
		TestPlanArtifactID:   "test-plan-1",
		RedTestEvidenceID:    "red-1",
	}
	if err := ValidateDispatchGate(phase, artifacts, VSDDPhaseImplementation); err != nil {
		t.Fatalf("ValidateDispatchGate() error = %v", err)
	}

	err := ValidateDispatchGate(phase, &VSDDArtifactFields{}, VSDDPhaseImplementation)
	if err == nil || !strings.Contains(err.Error(), "missing_artifacts:") {
		t.Fatalf("expected missing artifact error, got %v", err)
	}
}

func TestValidateDispatchGateDiagnostics(t *testing.T) {
	t.Parallel()
	phase := &VSDDPhaseFields{Phase: VSDDPhaseConvergence, SpecApproved: true, TestsRed: true, Implementation: true}
	artifacts := &VSDDArtifactFields{
		SpecArtifactID:           "spec-1",
		SpecReviewArtifactID:     "review-spec-1",
		TestPlanArtifactID:       "tests-1",
		RedTestEvidenceID:        "red-1",
		ImplementationArtifactID: "impl-1",
		BuilderEvidenceID:        "builder-1",
		ReviewArtifactID:         "review-artifact-1",
	}
	err := ValidateDispatchGate(phase, artifacts, VSDDPhaseConvergence)
	if err != nil {
		t.Fatalf("expected convergence gate to pass with required artifacts, got %v", err)
	}

	artifacts.ReviewArtifactID = ""
	err = ValidateDispatchGate(phase, artifacts, VSDDPhaseConvergence)
	if err == nil || !strings.Contains(err.Error(), "missing_artifacts:review_artifact") {
		t.Fatalf("expected missing review artifact, got %v", err)
	}
}

func TestInspectVSDDWorkflow(t *testing.T) {
	t.Parallel()
	issue := &Issue{Description: PersistVSDDState(
		&Issue{Description: "Body"},
		&VSDDPhaseFields{
			Phase:          VSDDPhaseImplementation,
			SpecApproved:   true,
			TestsRed:       true,
			LastTransition: "dispatch-ready:implementation",
		},
		&VSDDArtifactFields{
			SpecArtifactID:       "spec-1",
			SpecReviewArtifactID: "spec-review-1",
			TestPlanArtifactID:   "test-plan-1",
			RedTestEvidenceID:    "red-1",
		},
	)}
	inspection := InspectVSDDWorkflow(issue)
	if inspection == nil {
		t.Fatal("InspectVSDDWorkflow() = nil")
	}
	if inspection.Phase != VSDDPhaseImplementation || !inspection.DispatchReady {
		t.Fatalf("inspection = %#v", inspection)
	}
	if len(inspection.MissingArtifacts) != 0 {
		t.Fatalf("missing artifacts = %#v", inspection.MissingArtifacts)
	}

	blocked := &Issue{Description: PersistVSDDState(
		&Issue{},
		&VSDDPhaseFields{
			Phase:          VSDDPhaseImplementation,
			SpecApproved:   true,
			TestsRed:       true,
			LastTransition: "blocked:implementation",
			LastRejection:  "missing_artifacts:review_artifact",
		},
		&VSDDArtifactFields{SpecArtifactID: "spec-1"},
	)}
	inspection = InspectVSDDWorkflow(blocked)
	if inspection.DispatchReady {
		t.Fatalf("expected blocked inspection, got %#v", inspection)
	}
	if len(inspection.MissingArtifacts) == 0 {
		t.Fatalf("expected missing artifacts, got %#v", inspection)
	}

	reviewed := &Issue{Description: PersistVSDDState(
		&Issue{},
		&VSDDPhaseFields{
			Phase:          VSDDPhaseReview,
			SpecApproved:   true,
			TestsRed:       true,
			Implementation: true,
			ReviewVerdict:  "READY",
			ReviewApproved: true,
			ReviewSummary:  "Reviewed approved evidence.",
			ReviewEvidence: []string{"spec-1", "impl-1"},
			ReviewFresh:    true,
		},
		&VSDDArtifactFields{SpecArtifactID: "spec-1", SpecReviewArtifactID: "spec-review-1", TestPlanArtifactID: "test-1", RedTestEvidenceID: "red-1", ImplementationArtifactID: "impl-1", BuilderEvidenceID: "builder-1", ReviewArtifactID: "review-1"},
	)}
	inspection = InspectVSDDWorkflow(reviewed)
	if !inspection.ReviewContractOK || !inspection.ReviewFreshContext || inspection.ReviewSummary == "" {
		t.Fatalf("inspection = %#v", inspection)
	}
}

func containsAll(s string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
