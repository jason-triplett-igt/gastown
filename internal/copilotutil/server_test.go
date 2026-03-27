package copilotutil

import (
	"context"
	"strings"
	"testing"
)

func TestEnsureServerStartsLocalServerWithoutManagedState(t *testing.T) {
	t.Parallel()
	oldStatus := statusServer
	oldStart := startServer
	t.Cleanup(func() {
		statusServer = oldStatus
		startServer = oldStart
	})
	statusServer = func(townRoot string) (*ServerStatus, error) {
		return &ServerStatus{CLIURL: "http://127.0.0.1:4321", Managed: false, Healthy: false}, nil
	}
	called := false
	startServer = func(ctx context.Context, townRoot string) (*ServerStatus, error) {
		called = true
		return &ServerStatus{CLIURL: "http://127.0.0.1:4321", Managed: true, Healthy: true, State: "running"}, nil
	}
	status, err := EnsureServer(context.Background(), "/tmp/town")
	if err != nil {
		t.Fatalf("EnsureServer() error = %v", err)
	}
	if !called {
		t.Fatal("EnsureServer() did not start local server")
	}
	if status == nil || !status.Healthy {
		t.Fatalf("EnsureServer() status = %#v, want healthy status", status)
	}
}

func TestEnsureServerRejectsUnmanagedRemoteServer(t *testing.T) {
	t.Parallel()
	oldStatus := statusServer
	oldStart := startServer
	t.Cleanup(func() {
		statusServer = oldStatus
		startServer = oldStart
	})
	statusServer = func(townRoot string) (*ServerStatus, error) {
		return &ServerStatus{CLIURL: "https://copilot.example", Managed: false, Healthy: false}, nil
	}
	startServer = func(ctx context.Context, townRoot string) (*ServerStatus, error) {
		t.Fatal("EnsureServer() unexpectedly attempted to start remote server")
		return nil, nil
	}
	_, err := EnsureServer(context.Background(), "/tmp/town")
	if err == nil {
		t.Fatal("EnsureServer() error = nil, want remote unmanaged failure")
	}
	if !strings.Contains(err.Error(), "unhealthy and not Gastown-managed") {
		t.Fatalf("EnsureServer() error = %v, want unmanaged remote error", err)
	}
}

func TestEnsureServerForCLIURLStartsLocalServerWithoutTownConfig(t *testing.T) {
	t.Parallel()
	oldStatus := statusServer
	oldStart := startServer
	t.Cleanup(func() {
		statusServer = oldStatus
		startServer = oldStart
	})
	statusServer = oldStatus
	startServer = oldStart

	status, err := EnsureServerForCLIURL(context.Background(), "/tmp/town", "http://127.0.0.1:4321")
	if err == nil {
		if status == nil || status.CLIURL != "http://127.0.0.1:4321" {
			t.Fatalf("EnsureServerForCLIURL() status = %#v, want matching CLI URL", status)
		}
		return
	}

	// If the port is not actually listening in this environment, the helper still
	// must not fail with the old town-config resolution error.
	if strings.Contains(err.Error(), "town is not configured for copilot external") {
		t.Fatalf("EnsureServerForCLIURL() error = %v, want explicit CLI URL path", err)
	}
}
