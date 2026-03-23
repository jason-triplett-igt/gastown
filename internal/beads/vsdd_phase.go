package beads

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type VSDDPhase string

const (
	VSDDPhaseSpec           VSDDPhase = "spec"
	VSDDPhaseTests          VSDDPhase = "tests"
	VSDDPhaseImplementation VSDDPhase = "implementation"
	VSDDPhaseReview         VSDDPhase = "review"
	VSDDPhaseConvergence    VSDDPhase = "convergence"
	VSDDPhaseDone           VSDDPhase = "done"
)

type VSDDRejectionReason string

const (
	VSDDRejectSpecRequired      VSDDRejectionReason = "spec_required"
	VSDDRejectTestsRequired     VSDDRejectionReason = "tests_required"
	VSDDRejectImplementationReq VSDDRejectionReason = "implementation_required"
	VSDDRejectReviewRequired    VSDDRejectionReason = "review_required"
	VSDDRejectReviewMalformed   VSDDRejectionReason = "review_malformed"
	VSDDRejectInvalidTransition VSDDRejectionReason = "invalid_transition"
	VSDDRejectReviewFailed      VSDDRejectionReason = "review_failed"
)

type VSDDPhaseFields struct {
	Phase          VSDDPhase
	SpecApproved   bool
	TestsRed       bool
	Implementation bool
	ReviewVerdict  string
	ReviewApproved bool
	ReviewSummary  string
	ReviewEvidence []string
	ReviewFindings []string
	ReviewFresh    bool
	LastRejection  string
	LastTransition string
}

type VSDDReviewVerdictInput struct {
	Verdict          string
	Summary          string
	EvidenceRefs     []string
	Findings         []string
	FreshContext     bool
	ReviewArtifactID string
}

type VSDDArtifactRequirements struct {
	SpecArtifact           bool
	SpecReviewArtifact     bool
	TestPlanArtifact       bool
	RedTestEvidence        bool
	ImplementationArtifact bool
	BuilderEvidence        bool
	ReviewArtifact         bool
	FormalEvidence         bool
	ConvergenceEvidence    bool
}

type VSDDArtifactFields struct {
	SpecArtifactID           string
	SpecReviewArtifactID     string
	TestPlanArtifactID       string
	RedTestEvidenceID        string
	ImplementationArtifactID string
	BuilderEvidenceID        string
	ReviewArtifactID         string
	FormalEvidenceID         string
	ConvergenceEvidenceID    string
}

func ParseVSDDPhaseFields(issue *Issue) *VSDDPhaseFields {
	if issue == nil || issue.Description == "" {
		return nil
	}

	fields := &VSDDPhaseFields{}
	hasFields := false
	for _, line := range strings.Split(issue.Description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "vsdd_phase":
			fields.Phase = VSDDPhase(value)
			hasFields = true
		case "vsdd_spec_approved":
			fields.SpecApproved = strings.EqualFold(value, "true")
			hasFields = true
		case "vsdd_tests_red":
			fields.TestsRed = strings.EqualFold(value, "true")
			hasFields = true
		case "vsdd_implementation_done":
			fields.Implementation = strings.EqualFold(value, "true")
			hasFields = true
		case "vsdd_review_verdict":
			fields.ReviewVerdict = value
			hasFields = true
		case "vsdd_review_approved":
			fields.ReviewApproved = strings.EqualFold(value, "true")
			hasFields = true
		case "vsdd_review_summary":
			fields.ReviewSummary = value
			hasFields = true
		case "vsdd_review_evidence":
			fields.ReviewEvidence = parseReviewList(value)
			hasFields = true
		case "vsdd_review_findings":
			fields.ReviewFindings = parseReviewList(value)
			hasFields = true
		case "vsdd_review_fresh_context":
			fields.ReviewFresh = strings.EqualFold(value, "true")
			hasFields = true
		case "vsdd_last_rejection":
			fields.LastRejection = value
			hasFields = true
		case "vsdd_last_transition":
			fields.LastTransition = value
			hasFields = true
		}
	}
	if !hasFields {
		return nil
	}
	return fields
}

func FormatVSDDPhaseFields(fields *VSDDPhaseFields) string {
	if fields == nil {
		return ""
	}
	lines := []string{}
	if fields.Phase != "" {
		lines = append(lines, "vsdd_phase: "+string(fields.Phase))
	}
	lines = append(lines, "vsdd_spec_approved: "+boolString(fields.SpecApproved))
	lines = append(lines, "vsdd_tests_red: "+boolString(fields.TestsRed))
	lines = append(lines, "vsdd_implementation_done: "+boolString(fields.Implementation))
	if fields.ReviewVerdict != "" {
		lines = append(lines, "vsdd_review_verdict: "+fields.ReviewVerdict)
	}
	lines = append(lines, "vsdd_review_approved: "+boolString(fields.ReviewApproved))
	if fields.ReviewSummary != "" {
		lines = append(lines, "vsdd_review_summary: "+fields.ReviewSummary)
	}
	if len(fields.ReviewEvidence) > 0 {
		lines = append(lines, "vsdd_review_evidence: "+formatReviewList(fields.ReviewEvidence))
	}
	if len(fields.ReviewFindings) > 0 {
		lines = append(lines, "vsdd_review_findings: "+formatReviewList(fields.ReviewFindings))
	}
	lines = append(lines, "vsdd_review_fresh_context: "+boolString(fields.ReviewFresh))
	if fields.LastRejection != "" {
		lines = append(lines, "vsdd_last_rejection: "+fields.LastRejection)
	}
	if fields.LastTransition != "" {
		lines = append(lines, "vsdd_last_transition: "+fields.LastTransition)
	}
	return strings.Join(lines, "\n")
}

func SetVSDDPhaseFields(issue *Issue, fields *VSDDPhaseFields) string {
	knownKeys := map[string]bool{
		"vsdd_phase":                true,
		"vsdd_spec_approved":        true,
		"vsdd_tests_red":            true,
		"vsdd_implementation_done":  true,
		"vsdd_review_verdict":       true,
		"vsdd_review_approved":      true,
		"vsdd_review_summary":       true,
		"vsdd_review_evidence":      true,
		"vsdd_review_findings":      true,
		"vsdd_review_fresh_context": true,
		"vsdd_last_rejection":       true,
		"vsdd_last_transition":      true,
	}
	var otherLines []string
	if issue != nil && issue.Description != "" {
		for _, line := range strings.Split(issue.Description, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				otherLines = append(otherLines, line)
				continue
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 || !knownKeys[strings.ToLower(strings.TrimSpace(parts[0]))] {
				otherLines = append(otherLines, line)
			}
		}
	}
	formatted := FormatVSDDPhaseFields(fields)
	for len(otherLines) > 0 && strings.TrimSpace(otherLines[0]) == "" {
		otherLines = otherLines[1:]
	}
	for len(otherLines) > 0 && strings.TrimSpace(otherLines[len(otherLines)-1]) == "" {
		otherLines = otherLines[:len(otherLines)-1]
	}
	if formatted == "" {
		return strings.Join(otherLines, "\n")
	}
	if len(otherLines) == 0 {
		return formatted
	}
	return formatted + "\n\n" + strings.Join(otherLines, "\n")
}

func (f *VSDDPhaseFields) Normalize() {
	if f == nil {
		return
	}
	if f.Phase == "" {
		f.Phase = VSDDPhaseSpec
	}
	f.ReviewVerdict = normalizeReviewVerdict(f.ReviewVerdict)
	if f.ReviewVerdict == "READY" {
		f.ReviewApproved = true
	}
	if f.ReviewVerdict == "NOT READY" {
		f.ReviewApproved = false
	}
	if f.Phase == VSDDPhaseDone {
		f.ReviewApproved = true
	}
	if f.Phase == VSDDPhaseImplementation || f.Phase == VSDDPhaseReview || f.Phase == VSDDPhaseConvergence || f.Phase == VSDDPhaseDone {
		f.SpecApproved = true
		f.TestsRed = true
	}
	if f.Phase == VSDDPhaseReview || f.Phase == VSDDPhaseConvergence || f.Phase == VSDDPhaseDone {
		f.Implementation = true
	}
	if f.Phase == VSDDPhaseConvergence || f.Phase == VSDDPhaseDone {
		f.ReviewApproved = true
		if f.ReviewVerdict == "" {
			f.ReviewVerdict = "READY"
		}
	}
}

func ValidateVSDDTransition(current *VSDDPhaseFields, nextPhase VSDDPhase) error {
	state := *current
	state.Normalize()
	switch nextPhase {
	case VSDDPhaseSpec:
		return nil
	case VSDDPhaseTests:
		if !state.SpecApproved {
			return fmt.Errorf("%s", VSDDRejectSpecRequired)
		}
		return nil
	case VSDDPhaseImplementation:
		if !state.SpecApproved {
			return fmt.Errorf("%s", VSDDRejectSpecRequired)
		}
		if !state.TestsRed {
			return fmt.Errorf("%s", VSDDRejectTestsRequired)
		}
		return nil
	case VSDDPhaseReview:
		if !state.Implementation {
			return fmt.Errorf("%s", VSDDRejectImplementationReq)
		}
		return nil
	case VSDDPhaseConvergence:
		if !state.ReviewApproved {
			if strings.EqualFold(state.ReviewVerdict, "NOT READY") {
				return fmt.Errorf("%s", VSDDRejectReviewFailed)
			}
			return fmt.Errorf("%s", VSDDRejectReviewRequired)
		}
		return nil
	case VSDDPhaseDone:
		if !state.ReviewApproved {
			return fmt.Errorf("%s", VSDDRejectReviewRequired)
		}
		return nil
	default:
		return fmt.Errorf("%s", VSDDRejectInvalidTransition)
	}
}

func NextVSDDState(current *VSDDPhaseFields, nextPhase VSDDPhase) (*VSDDPhaseFields, error) {
	if current == nil {
		current = &VSDDPhaseFields{}
	}
	if err := ValidateVSDDTransition(current, nextPhase); err != nil {
		next := *current
		next.Normalize()
		next.LastRejection = err.Error()
		next.LastTransition = fmt.Sprintf("blocked:%s", nextPhase)
		return &next, err
	}
	next := *current
	next.Normalize()
	next.Phase = nextPhase
	next.LastRejection = ""
	next.LastTransition = fmt.Sprintf("entered:%s", nextPhase)
	next.Normalize()
	return &next, nil
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func RequiredArtifactsForPhase(phase VSDDPhase) VSDDArtifactRequirements {
	switch phase {
	case VSDDPhaseSpec:
		return VSDDArtifactRequirements{SpecArtifact: true}
	case VSDDPhaseTests:
		return VSDDArtifactRequirements{SpecArtifact: true, SpecReviewArtifact: true, TestPlanArtifact: true}
	case VSDDPhaseImplementation:
		return VSDDArtifactRequirements{SpecArtifact: true, SpecReviewArtifact: true, TestPlanArtifact: true, RedTestEvidence: true}
	case VSDDPhaseReview:
		return VSDDArtifactRequirements{SpecArtifact: true, SpecReviewArtifact: true, TestPlanArtifact: true, RedTestEvidence: true, ImplementationArtifact: true, BuilderEvidence: true}
	case VSDDPhaseConvergence:
		return VSDDArtifactRequirements{SpecArtifact: true, SpecReviewArtifact: true, TestPlanArtifact: true, RedTestEvidence: true, ImplementationArtifact: true, BuilderEvidence: true, ReviewArtifact: true}
	case VSDDPhaseDone:
		return VSDDArtifactRequirements{SpecArtifact: true, SpecReviewArtifact: true, TestPlanArtifact: true, RedTestEvidence: true, ImplementationArtifact: true, BuilderEvidence: true, ReviewArtifact: true, ConvergenceEvidence: true}
	default:
		return VSDDArtifactRequirements{}
	}
}

func (f VSDDArtifactFields) Satisfies(req VSDDArtifactRequirements) (bool, []string) {
	missing := make([]string, 0, 8)
	if req.SpecArtifact && f.SpecArtifactID == "" {
		missing = append(missing, "spec_artifact")
	}
	if req.SpecReviewArtifact && f.SpecReviewArtifactID == "" {
		missing = append(missing, "spec_review_artifact")
	}
	if req.TestPlanArtifact && f.TestPlanArtifactID == "" {
		missing = append(missing, "test_plan_artifact")
	}
	if req.RedTestEvidence && f.RedTestEvidenceID == "" {
		missing = append(missing, "red_test_evidence")
	}
	if req.ImplementationArtifact && f.ImplementationArtifactID == "" {
		missing = append(missing, "implementation_artifact")
	}
	if req.BuilderEvidence && f.BuilderEvidenceID == "" {
		missing = append(missing, "builder_evidence")
	}
	if req.ReviewArtifact && f.ReviewArtifactID == "" {
		missing = append(missing, "review_artifact")
	}
	if req.FormalEvidence && f.FormalEvidenceID == "" {
		missing = append(missing, "formal_evidence")
	}
	if req.ConvergenceEvidence && f.ConvergenceEvidenceID == "" {
		missing = append(missing, "convergence_evidence")
	}
	return len(missing) == 0, missing
}

func ParseVSDDArtifactFields(issue *Issue) *VSDDArtifactFields {
	if issue == nil || issue.Description == "" {
		return nil
	}
	fields := &VSDDArtifactFields{}
	hasFields := false
	for _, line := range strings.Split(issue.Description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		switch key {
		case "vsdd_spec_artifact":
			fields.SpecArtifactID = value
			hasFields = true
		case "vsdd_spec_review_artifact":
			fields.SpecReviewArtifactID = value
			hasFields = true
		case "vsdd_test_plan_artifact":
			fields.TestPlanArtifactID = value
			hasFields = true
		case "vsdd_red_test_evidence":
			fields.RedTestEvidenceID = value
			hasFields = true
		case "vsdd_implementation_artifact":
			fields.ImplementationArtifactID = value
			hasFields = true
		case "vsdd_builder_evidence":
			fields.BuilderEvidenceID = value
			hasFields = true
		case "vsdd_review_artifact":
			fields.ReviewArtifactID = value
			hasFields = true
		case "vsdd_formal_evidence":
			fields.FormalEvidenceID = value
			hasFields = true
		case "vsdd_convergence_evidence":
			fields.ConvergenceEvidenceID = value
			hasFields = true
		}
	}
	if !hasFields {
		return nil
	}
	return fields
}

func FormatVSDDArtifactFields(fields *VSDDArtifactFields) string {
	if fields == nil {
		return ""
	}
	lines := []string{}
	appendIf := func(key, value string) {
		if value != "" {
			lines = append(lines, key+": "+value)
		}
	}
	appendIf("vsdd_spec_artifact", fields.SpecArtifactID)
	appendIf("vsdd_spec_review_artifact", fields.SpecReviewArtifactID)
	appendIf("vsdd_test_plan_artifact", fields.TestPlanArtifactID)
	appendIf("vsdd_red_test_evidence", fields.RedTestEvidenceID)
	appendIf("vsdd_implementation_artifact", fields.ImplementationArtifactID)
	appendIf("vsdd_builder_evidence", fields.BuilderEvidenceID)
	appendIf("vsdd_review_artifact", fields.ReviewArtifactID)
	appendIf("vsdd_formal_evidence", fields.FormalEvidenceID)
	appendIf("vsdd_convergence_evidence", fields.ConvergenceEvidenceID)
	return strings.Join(lines, "\n")
}

func SetVSDDArtifactFields(issue *Issue, fields *VSDDArtifactFields) string {
	knownKeys := map[string]bool{
		"vsdd_spec_artifact":           true,
		"vsdd_spec_review_artifact":    true,
		"vsdd_test_plan_artifact":      true,
		"vsdd_red_test_evidence":       true,
		"vsdd_implementation_artifact": true,
		"vsdd_builder_evidence":        true,
		"vsdd_review_artifact":         true,
		"vsdd_formal_evidence":         true,
		"vsdd_convergence_evidence":    true,
	}
	var otherLines []string
	if issue != nil && issue.Description != "" {
		for _, line := range strings.Split(issue.Description, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				otherLines = append(otherLines, line)
				continue
			}
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) != 2 || !knownKeys[strings.ToLower(strings.TrimSpace(parts[0]))] {
				otherLines = append(otherLines, line)
			}
		}
	}
	formatted := FormatVSDDArtifactFields(fields)
	for len(otherLines) > 0 && strings.TrimSpace(otherLines[0]) == "" {
		otherLines = otherLines[1:]
	}
	for len(otherLines) > 0 && strings.TrimSpace(otherLines[len(otherLines)-1]) == "" {
		otherLines = otherLines[:len(otherLines)-1]
	}
	if formatted == "" {
		return strings.Join(otherLines, "\n")
	}
	if len(otherLines) == 0 {
		return formatted
	}
	return formatted + "\n\n" + strings.Join(otherLines, "\n")
}

func ValidateDispatchGate(phase *VSDDPhaseFields, artifacts *VSDDArtifactFields, target VSDDPhase) error {
	if phase == nil {
		phase = &VSDDPhaseFields{}
	}
	phase.Normalize()
	if err := ValidateVSDDTransition(phase, target); err != nil {
		return err
	}
	if artifacts == nil {
		artifacts = &VSDDArtifactFields{}
	}
	required := RequiredArtifactsForPhase(target)
	ok, missing := artifacts.Satisfies(required)
	if ok {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("missing_artifacts:%s", strings.Join(missing, ","))
}

func PersistVSDDState(issue *Issue, phase *VSDDPhaseFields, artifacts *VSDDArtifactFields) string {
	description := ""
	if issue != nil {
		description = issue.Description
	}
	updated := SetVSDDPhaseFields(&Issue{Description: description}, phase)
	return SetVSDDArtifactFields(&Issue{Description: updated}, artifacts)
}

type VSDDWorkflowInspection struct {
	Phase              VSDDPhase
	LastTransition     string
	LastRejection      string
	ReviewVerdict      string
	ReviewApproved     bool
	ReviewSummary      string
	ReviewEvidence     []string
	ReviewFindings     []string
	ReviewFreshContext bool
	ReviewContractOK   bool
	MissingArtifacts   []string
	RequiredArtifacts  []string
	SatisfiedArtifacts []string
	DispatchReady      bool
}

func InspectVSDDWorkflow(issue *Issue) *VSDDWorkflowInspection {
	phase := ParseVSDDPhaseFields(issue)
	artifacts := ParseVSDDArtifactFields(issue)
	if phase == nil && artifacts == nil {
		return nil
	}
	if phase == nil {
		phase = &VSDDPhaseFields{}
	}
	phase.Normalize()
	if artifacts == nil {
		artifacts = &VSDDArtifactFields{}
	}
	required := RequiredArtifactsForPhase(phase.Phase)
	_, missing := artifacts.Satisfies(required)
	result := &VSDDWorkflowInspection{
		Phase:              phase.Phase,
		LastTransition:     phase.LastTransition,
		LastRejection:      phase.LastRejection,
		ReviewVerdict:      phase.ReviewVerdict,
		ReviewApproved:     phase.ReviewApproved,
		ReviewSummary:      phase.ReviewSummary,
		ReviewEvidence:     append([]string(nil), phase.ReviewEvidence...),
		ReviewFindings:     append([]string(nil), phase.ReviewFindings...),
		ReviewFreshContext: phase.ReviewFresh,
		MissingArtifacts:   append([]string(nil), missing...),
		RequiredArtifacts:  requiredArtifactNames(required),
		SatisfiedArtifacts: satisfiedArtifactNames(*artifacts),
	}
	result.ReviewContractOK = ValidateReviewVerdictContract(phase) == nil
	result.DispatchReady = len(result.MissingArtifacts) == 0 && result.LastRejection == ""
	sort.Strings(result.RequiredArtifacts)
	sort.Strings(result.SatisfiedArtifacts)
	sort.Strings(result.MissingArtifacts)
	sort.Strings(result.ReviewEvidence)
	sort.Strings(result.ReviewFindings)
	return result
}

func PersistReviewVerdict(issue *Issue, input VSDDReviewVerdictInput) (string, error) {
	phase := ParseVSDDPhaseFields(issue)
	if phase == nil {
		phase = &VSDDPhaseFields{}
	}
	artifacts := ParseVSDDArtifactFields(issue)
	if artifacts == nil {
		artifacts = &VSDDArtifactFields{}
	}
	phase.ReviewVerdict = input.Verdict
	phase.ReviewSummary = strings.TrimSpace(input.Summary)
	phase.ReviewEvidence = append([]string(nil), input.EvidenceRefs...)
	phase.ReviewFindings = append([]string(nil), input.Findings...)
	phase.ReviewFresh = input.FreshContext
	phase.Normalize()
	if err := ValidateReviewVerdictContract(phase); err != nil {
		phase.LastRejection = err.Error()
		phase.LastTransition = "blocked:review-contract"
		return PersistVSDDState(issue, phase, artifacts), err
	}
	if input.ReviewArtifactID != "" {
		artifacts.ReviewArtifactID = input.ReviewArtifactID
	}
	phase.LastRejection = ""
	phase.LastTransition = "recorded:review"
	return PersistVSDDState(issue, phase, artifacts), nil
}

func ValidateReviewVerdictContract(phase *VSDDPhaseFields) error {
	if phase == nil {
		return fmt.Errorf("%s:missing_review", VSDDRejectReviewRequired)
	}
	phase.Normalize()
	if phase.ReviewVerdict != "READY" && phase.ReviewVerdict != "NOT READY" {
		return fmt.Errorf("%s:verdict", VSDDRejectReviewMalformed)
	}
	if strings.TrimSpace(phase.ReviewSummary) == "" {
		return fmt.Errorf("%s:summary", VSDDRejectReviewMalformed)
	}
	if len(phase.ReviewEvidence) == 0 {
		return fmt.Errorf("%s:evidence", VSDDRejectReviewMalformed)
	}
	if !phase.ReviewFresh {
		return fmt.Errorf("%s:fresh_context", VSDDRejectReviewMalformed)
	}
	if phase.ReviewVerdict == "NOT READY" && len(phase.ReviewFindings) == 0 {
		return fmt.Errorf("%s:findings", VSDDRejectReviewMalformed)
	}
	return nil
}

func normalizeReviewVerdict(value string) string {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	switch normalized {
	case "READY":
		return "READY"
	case "NOT READY", "NOT_READY", "NOTREADY":
		return "NOT READY"
	default:
		return normalized
	}
}

func parseReviewList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var parsed []string
	if json.Unmarshal([]byte(value), &parsed) == nil {
		return filterEmptyStrings(parsed)
	}
	parts := strings.Split(value, ",")
	return filterEmptyStrings(parts)
}

func formatReviewList(values []string) string {
	encoded, err := json.Marshal(filterEmptyStrings(values))
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func filterEmptyStrings(values []string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}

func requiredArtifactNames(req VSDDArtifactRequirements) []string {
	names := make([]string, 0, 9)
	appendIf := func(ok bool, name string) {
		if ok {
			names = append(names, name)
		}
	}
	appendIf(req.SpecArtifact, "spec_artifact")
	appendIf(req.SpecReviewArtifact, "spec_review_artifact")
	appendIf(req.TestPlanArtifact, "test_plan_artifact")
	appendIf(req.RedTestEvidence, "red_test_evidence")
	appendIf(req.ImplementationArtifact, "implementation_artifact")
	appendIf(req.BuilderEvidence, "builder_evidence")
	appendIf(req.ReviewArtifact, "review_artifact")
	appendIf(req.FormalEvidence, "formal_evidence")
	appendIf(req.ConvergenceEvidence, "convergence_evidence")
	return names
}

func satisfiedArtifactNames(fields VSDDArtifactFields) []string {
	names := make([]string, 0, 9)
	appendIf := func(value, name string) {
		if value != "" {
			names = append(names, name)
		}
	}
	appendIf(fields.SpecArtifactID, "spec_artifact")
	appendIf(fields.SpecReviewArtifactID, "spec_review_artifact")
	appendIf(fields.TestPlanArtifactID, "test_plan_artifact")
	appendIf(fields.RedTestEvidenceID, "red_test_evidence")
	appendIf(fields.ImplementationArtifactID, "implementation_artifact")
	appendIf(fields.BuilderEvidenceID, "builder_evidence")
	appendIf(fields.ReviewArtifactID, "review_artifact")
	appendIf(fields.FormalEvidenceID, "formal_evidence")
	appendIf(fields.ConvergenceEvidenceID, "convergence_evidence")
	return names
}
