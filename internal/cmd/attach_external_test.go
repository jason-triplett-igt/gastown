package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/runtime"
)

func TestHeadlessManagedAttachMessageIncludesOwnerLog(t *testing.T) {
	townRoot := t.TempDir()
	sessionName := "gt-refinery"
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-910",
		Role:             "refinery",
		RigName:          "gastown",
		AgentName:        "refinery",
		Provider:         "copilot-external",
		SessionName:      sessionName,
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(townRoot, "gastown", "refinery", "rig"),
		Metadata:         runtime.OwnerBindingMetadata(runtime.ExternalOwnerDir(townRoot, sessionName), os.Getpid()),
	}
	if err := runtime.NewFileSessionBindingStore(townRoot).Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := runtime.WriteExternalOwnerStatus(townRoot, sessionName, runtime.ExternalCopilotOwnerStatus{
		OwnerPID:         os.Getpid(),
		RuntimeSessionID: binding.RuntimeSessionID,
		Ready:            runtime.BoolPtr(true),
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}

	message, ok, err := headlessManagedAttachMessage(townRoot, "refinery", "gastown", "refinery", "gt refinery status gastown")
	if err != nil {
		t.Fatalf("headlessManagedAttachMessage() error = %v", err)
	}
	if !ok {
		t.Fatal("headlessManagedAttachMessage() ok = false, want true")
	}
	for _, want := range []string{
		"Refinery is running headlessly via external runtime",
		"`gt refinery status gastown`",
		runtime.ExternalOwnerLogPath(townRoot, sessionName),
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message = %q, want substring %q", message, want)
		}
	}
}

func TestHeadlessManagedAttachMessageReportsUnavailableOwner(t *testing.T) {
	townRoot := t.TempDir()
	sessionName := "gt-witness"
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-911",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "copilot-external",
		SessionName:      sessionName,
		RuntimeSessionID: "runtime-dead",
		WorkDir:          filepath.Join(townRoot, "gastown", "witness"),
		Metadata:         runtime.OwnerBindingMetadata(runtime.ExternalOwnerDir(townRoot, sessionName), 999999),
	}
	if err := runtime.NewFileSessionBindingStore(townRoot).Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	message, ok, err := headlessManagedAttachMessage(townRoot, "witness", "gastown", "witness", "gt witness status gastown")
	if err != nil {
		t.Fatalf("headlessManagedAttachMessage() error = %v", err)
	}
	if !ok {
		t.Fatal("headlessManagedAttachMessage() ok = false, want true")
	}
	for _, want := range []string{
		"Witness uses a headless external runtime",
		"owner missing; persisted session may be recoverable",
		"`gt witness status gastown`",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("message = %q, want substring %q", message, want)
		}
	}
}

func TestHeadlessManagedAttachMessageIgnoresTmuxManagedBindings(t *testing.T) {
	townRoot := t.TempDir()
	binding := runtime.SessionBinding{
		IssueID:          "slotmachine-912",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "claude",
		SessionName:      "gt-witness",
		RuntimeSessionID: "gt-witness",
		WorkDir:          filepath.Join(townRoot, "gastown", "witness"),
	}
	if err := runtime.NewFileSessionBindingStore(townRoot).Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	message, ok, err := headlessManagedAttachMessage(townRoot, "witness", "gastown", "witness", "gt witness status gastown")
	if err != nil {
		t.Fatalf("headlessManagedAttachMessage() error = %v", err)
	}
	if ok {
		t.Fatalf("headlessManagedAttachMessage() = %q, true, want no headless attach fallback", message)
	}
}
