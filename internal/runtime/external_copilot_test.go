package runtime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/toolapi"
)

func TestExternalOwnerLookupForManagedBinding(t *testing.T) {
	t.Parallel()
	connector := copilotExternalSessionConnector{}
	metadata := OwnerBindingMetadata("/tmp/gastown/.runtime/copilot-owner/hq-mayor", 4321)
	req := SessionLookupRequest{
		IssueID:      "hq-mayor",
		Role:         "mayor",
		SessionName:  "hq-mayor",
		SessionID:    "runtime-123",
		TownRoot:     "/tmp/gastown",
		WorkDir:      "/tmp/gastown/mayor",
		Metadata:     metadata,
		AllowedTools: []string{"send_mail"},
		ReadOnly:     true,
	}

	sess, err := connector.Lookup(context.Background(), req, &config.RuntimeConfig{CLIURL: "http://127.0.0.1:4321"}, "copilot-external")
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	external, ok := sess.(*externalCopilotManagedSession)
	if !ok {
		t.Fatalf("Lookup() session = %T, want *externalCopilotManagedSession", sess)
	}
	if external.ID() != "runtime-123" {
		t.Fatalf("ID() = %q, want runtime-123", external.ID())
	}
	if !IsExternalOwnerBinding(&SessionBinding{SessionName: external.sessionName, Metadata: external.metadata}) {
		t.Fatalf("session metadata = %#v, want owner-managed binding", external.metadata)
	}
	if external.toolPolicy.AvailableTools == nil || len(external.toolPolicy.AvailableTools) != 1 || external.toolPolicy.AvailableTools[0] != "send_mail" {
		t.Fatalf("tool policy = %#v, want legacy policy from allowed tools", external.toolPolicy)
	}
	if OwnerPIDFromMetadata(external.metadata) != 4321 {
		t.Fatalf("owner pid = %d, want 4321", OwnerPIDFromMetadata(external.metadata))
	}
}

func TestCopilotExternalLookupRequiresRuntimeSessionIDWhenNotOwnerManaged(t *testing.T) {
	t.Parallel()
	connector := copilotExternalSessionConnector{}
	_, err := connector.Lookup(context.Background(), SessionLookupRequest{
		Role:        "witness",
		SessionName: "gt-witness",
		TownRoot:    "/tmp/gastown",
		WorkDir:     "/tmp/gastown/witness",
	}, &config.RuntimeConfig{CLIURL: "http://127.0.0.1:4321"}, "copilot-external")
	if err == nil {
		t.Fatal("Lookup() error = nil, want missing runtime session id error")
	}
	if got := err.Error(); got != "runtime session id is required for external lookup" {
		t.Fatalf("Lookup() error = %q, want runtime session id is required for external lookup", got)
	}
}

func TestResolveExternalToolPolicyPrefersExplicitPolicy(t *testing.T) {
	t.Parallel()
	explicit := &config.ToolPolicy{AvailableTools: []string{"send_mail"}, ExcludedTools: []string{"bash"}}
	resolved := resolveExternalToolPolicy("mayor", "/tmp/town", "/tmp/rig", "/tmp/work", "", map[string]string{"session_kind": "review"}, explicit, []string{"grep"}, true)
	if len(resolved.AvailableTools) != 1 || resolved.AvailableTools[0] != "send_mail" {
		t.Fatalf("AvailableTools = %#v, want explicit policy", resolved.AvailableTools)
	}
	if len(resolved.ExcludedTools) != 1 || resolved.ExcludedTools[0] != "bash" {
		t.Fatalf("ExcludedTools = %#v, want explicit policy", resolved.ExcludedTools)
	}
}

func TestResolveExternalToolPolicyUsesLegacyAllowedToolsWithoutSessionKind(t *testing.T) {
	t.Parallel()
	resolved := resolveExternalToolPolicy("crew", "/tmp/town", "/tmp/rig", "/tmp/work", "", nil, nil, []string{"send_mail", "nudge_agent"}, true)
	if len(resolved.AvailableTools) != 2 || resolved.AvailableTools[0] != "send_mail" || resolved.AvailableTools[1] != "nudge_agent" {
		t.Fatalf("AvailableTools = %#v, want legacy allowed tools", resolved.AvailableTools)
	}
}

func TestExternalCopilotManagedSessionSendRequiresOwnerManagedBinding(t *testing.T) {
	t.Parallel()
	sess := &externalCopilotManagedSession{
		provider:    "copilot-external",
		role:        "witness",
		sessionName: "gt-witness",
		runtimeID:   "runtime-1",
		townRoot:    "/tmp/gastown",
		metadata:    map[string]string{"session_kind": "review"},
		toolHooks:   toolapi.Callbacks{},
	}
	err := sess.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("Send() error = nil, want owner-managed failure")
	}
	if got := err.Error(); got != "external copilot session is not owner-managed" {
		t.Fatalf("Send() error = %q, want external copilot session is not owner-managed", got)
	}
}

func TestExternalCopilotManagedSessionStatusReflectsOwnerBusyBit(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	sessionName := "hq-mayor"
	pid := os.Getpid()
	if err := WriteExternalOwnerStatus(townRoot, sessionName, ExternalCopilotOwnerStatus{OwnerPID: pid, RuntimeSessionID: "runtime-1", Busy: true, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}
	sess := &externalCopilotManagedSession{
		provider:    "copilot-external",
		role:        "mayor",
		sessionName: sessionName,
		runtimeID:   "runtime-1",
		townRoot:    townRoot,
		metadata:    OwnerBindingMetadata(ExternalOwnerDir(townRoot, sessionName), pid),
	}
	status, err := sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Alive || !status.Ready || !status.Busy {
		t.Fatalf("status = %#v, want alive+ready+busy", status)
	}
}

func TestExternalCopilotManagedSessionStatusTracksOwnerBusyIdleTransitions(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	sessionName := "hq-mayor"
	pid := os.Getpid()
	sess := &externalCopilotManagedSession{
		provider:    "copilot-external",
		role:        "mayor",
		sessionName: sessionName,
		runtimeID:   "runtime-1",
		townRoot:    townRoot,
		metadata:    OwnerBindingMetadata(ExternalOwnerDir(townRoot, sessionName), pid),
	}

	for _, tc := range []struct {
		name     string
		busy     bool
		wantBusy bool
	}{
		{name: "idle before request", busy: false, wantBusy: false},
		{name: "busy while processing", busy: true, wantBusy: true},
		{name: "idle after completion", busy: false, wantBusy: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := WriteExternalOwnerStatus(townRoot, sessionName, ExternalCopilotOwnerStatus{OwnerPID: pid, RuntimeSessionID: "runtime-1", Busy: tc.busy, UpdatedAt: time.Now().UTC()}); err != nil {
				t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
			}
			status, err := sess.Status(context.Background())
			if err != nil {
				t.Fatalf("Status() error = %v", err)
			}
			if !status.Alive || !status.Ready || status.Busy != tc.wantBusy {
				t.Fatalf("status = %#v, want busy=%t with alive+ready", status, tc.wantBusy)
			}
		})
	}
}

func TestExternalCopilotManagedSessionStatusReflectsUpdatedOwnerStatusOverTime(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	sessionName := "hq-mayor"
	pid := os.Getpid()
	sess := &externalCopilotManagedSession{
		provider:    "copilot-external",
		role:        "mayor",
		sessionName: sessionName,
		runtimeID:   "runtime-1",
		townRoot:    townRoot,
		metadata:    OwnerBindingMetadata(ExternalOwnerDir(townRoot, sessionName), pid),
	}

	if err := WriteExternalOwnerStatus(townRoot, sessionName, ExternalCopilotOwnerStatus{OwnerPID: pid, RuntimeSessionID: "runtime-1", Busy: false, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus(idle) error = %v", err)
	}
	status, err := sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status(idle) error = %v", err)
	}
	if status.Busy {
		t.Fatalf("Status(idle) = %#v, want Busy=false", status)
	}

	if err := WriteExternalOwnerStatus(townRoot, sessionName, ExternalCopilotOwnerStatus{OwnerPID: pid, RuntimeSessionID: "runtime-1", Busy: true, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus(busy) error = %v", err)
	}
	status, err = sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status(busy) error = %v", err)
	}
	if !status.Busy {
		t.Fatalf("Status(busy) = %#v, want Busy=true", status)
	}

	if err := WriteExternalOwnerStatus(townRoot, sessionName, ExternalCopilotOwnerStatus{OwnerPID: pid, RuntimeSessionID: "runtime-1", Busy: false, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus(idle-2) error = %v", err)
	}
	status, err = sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status(idle-2) error = %v", err)
	}
	if status.Busy {
		t.Fatalf("Status(idle-2) = %#v, want Busy=false", status)
	}
}

func TestExternalCopilotManagedSessionStatusFailsClosedForDeadOwnerBusyBit(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	sessionName := "hq-mayor"
	deadPID := 99999999
	if err := WriteExternalOwnerStatus(townRoot, sessionName, ExternalCopilotOwnerStatus{OwnerPID: deadPID, RuntimeSessionID: "runtime-1", Busy: true, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}
	sess := &externalCopilotManagedSession{
		provider:    "copilot-external",
		role:        "mayor",
		sessionName: sessionName,
		runtimeID:   "runtime-1",
		townRoot:    townRoot,
		metadata:    OwnerBindingMetadata(ExternalOwnerDir(townRoot, sessionName), deadPID),
	}
	status, err := sess.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Alive || status.Ready || status.Busy {
		t.Fatalf("status = %#v, want alive=false ready=false busy=false", status)
	}
}
