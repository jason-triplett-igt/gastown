package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/activity"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	runtimepkg "github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
)

func writeFetcherTestBD(t *testing.T, townRoot string, issues []struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Assignee string `json:"assignee"`
}) string {
	t.Helper()

	issuesJSON, err := json.Marshal(issues)
	if err != nil {
		t.Fatalf("Marshal(issues) error = %v", err)
	}

	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	script := fmt.Sprintf(`#!/bin/sh
for arg in "$@"; do
  if [ "$1" = "list" ] && [ "$arg" = "--status=in_progress" ]; then
    printf '%%s\n' '%s'
    exit 0
  fi
done
printf '[]\n'
`, strings.ReplaceAll(string(issuesJSON), "'", "'\\''"))
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}
	return bdPath
}

func setupWorkerFetcherExternalRuntime(t *testing.T, townRoot, rigName string) {
	t.Helper()
	oldRegistry := session.DefaultRegistry()
	t.Cleanup(func() { session.SetDefaultRegistry(oldRegistry) })

	if err := config.SaveRigsConfig(filepath.Join(townRoot, "mayor", "rigs.json"), &config.RigsConfig{
		Version: config.CurrentRigsVersion,
		Rigs: map[string]config.RigEntry{
			rigName: {BeadsConfig: &config.BeadsConfig{Prefix: "gt", Repo: "local"}},
		},
	}); err != nil {
		t.Fatalf("SaveRigsConfig() error = %v", err)
	}

	rigPath := filepath.Join(townRoot, rigName)
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{
		"copilot-external": {
			Provider: "copilot",
			Command:  "copilot",
			CLIURL:   "http://127.0.0.1:4321",
		},
	}
	settings.RoleAgents = map[string]string{
		constants.RolePolecat:  "copilot-external",
		constants.RoleRefinery: "copilot-external",
	}
	if err := os.MkdirAll(filepath.Join(rigPath, "settings"), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings) error = %v", err)
	}
	if err := config.SaveRigSettings(filepath.Join(rigPath, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}
	if err := session.InitRegistry(townRoot); err != nil {
		t.Fatalf("InitRegistry() error = %v", err)
	}
}

func TestFetchWorkers_IncludesExternalOwnerBindingWithoutTmuxSession(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())

	townRoot := t.TempDir()
	rigName := "gastown"
	setupWorkerFetcherExternalRuntime(t, townRoot, rigName)

	sessionName := "gt-toast"
	pid := os.Getpid()
	store := runtimepkg.NewFileSessionBindingStore(townRoot)
	binding := runtimepkg.SessionBinding{
		IssueID:          "slotmachine-123",
		Role:             constants.RolePolecat,
		RigName:          rigName,
		AgentName:        "toast",
		Provider:         "copilot-external",
		SessionName:      sessionName,
		RuntimeSessionID: "runtime-toast",
		WorkDir:          filepath.Join(townRoot, rigName, "polecats", "toast"),
		Metadata:         runtimepkg.OwnerBindingMetadata(runtimepkg.ExternalOwnerDir(townRoot, sessionName), pid),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := runtimepkg.WriteExternalOwnerStatus(townRoot, sessionName, runtimepkg.ExternalCopilotOwnerStatus{
		OwnerPID:         pid,
		RuntimeSessionID: binding.RuntimeSessionID,
		Ready:            runtimepkg.BoolPtr(true),
		Busy:             true,
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}

	bdPath := writeFetcherTestBD(t, townRoot, []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Assignee string `json:"assignee"`
	}{
		{ID: "slotmachine-123", Title: "Fix the dashboard", Assignee: "gastown/polecats/toast"},
	})

	f := &LiveConvoyFetcher{
		townRoot:       townRoot,
		bdBin:          bdPath,
		cmdTimeout:     5 * time.Second,
		tmuxCmdTimeout: 250 * time.Millisecond,
		staleThreshold: 5 * time.Minute,
		stuckThreshold: 30 * time.Minute,
	}

	workers, err := f.FetchWorkers()
	if err != nil {
		t.Fatalf("FetchWorkers() error = %v", err)
	}
	if len(workers) != 1 {
		t.Fatalf("len(workers) = %d, want 1 (%#v)", len(workers), workers)
	}

	worker := workers[0]
	if worker.Name != "toast" || worker.Rig != rigName || worker.SessionID != sessionName {
		t.Fatalf("worker identity = %#v", worker)
	}
	if worker.AgentType != constants.RolePolecat {
		t.Fatalf("AgentType = %q, want %q", worker.AgentType, constants.RolePolecat)
	}
	if worker.IssueID != "slotmachine-123" || worker.IssueTitle != "Fix the dashboard" {
		t.Fatalf("issue = %q / %q, want assigned issue", worker.IssueID, worker.IssueTitle)
	}
	if worker.WorkStatus != "working" {
		t.Fatalf("WorkStatus = %q, want %q", worker.WorkStatus, "working")
	}
	if worker.LastActivity.ColorClass == activity.ColorUnknown {
		t.Fatalf("LastActivity = %#v, want recent external-owner activity", worker.LastActivity)
	}
	if worker.StatusHint != "" {
		t.Fatalf("StatusHint = %q, want empty", worker.StatusHint)
	}
}

func TestFetchWorkers_IncludesDegradedExternalOwnerBindingWithoutTmuxSession(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())

	townRoot := t.TempDir()
	rigName := "gastown"
	setupWorkerFetcherExternalRuntime(t, townRoot, rigName)

	sessionName := "gt-nux"
	pid := os.Getpid()
	store := runtimepkg.NewFileSessionBindingStore(townRoot)
	binding := runtimepkg.SessionBinding{
		IssueID:          "slotmachine-456",
		Role:             constants.RolePolecat,
		RigName:          rigName,
		AgentName:        "nux",
		Provider:         "copilot-external",
		SessionName:      sessionName,
		RuntimeSessionID: "runtime-nux",
		WorkDir:          filepath.Join(townRoot, rigName, "polecats", "nux"),
		Metadata:         runtimepkg.OwnerBindingMetadata(runtimepkg.ExternalOwnerDir(townRoot, sessionName), pid),
		UpdatedAt:        time.Now().UTC(),
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := runtimepkg.WriteExternalOwnerStatus(townRoot, sessionName, runtimepkg.ExternalCopilotOwnerStatus{
		OwnerPID:         pid,
		RuntimeSessionID: binding.RuntimeSessionID,
		Ready:            runtimepkg.BoolPtr(false),
		Busy:             true,
		Error:            "owner degraded",
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}

	bdPath := writeFetcherTestBD(t, townRoot, []struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Assignee string `json:"assignee"`
	}{
		{ID: "slotmachine-456", Title: "Investigate degraded owner", Assignee: "gastown/polecats/nux"},
	})

	f := &LiveConvoyFetcher{
		townRoot:       townRoot,
		bdBin:          bdPath,
		cmdTimeout:     5 * time.Second,
		tmuxCmdTimeout: 250 * time.Millisecond,
		staleThreshold: 5 * time.Minute,
		stuckThreshold: 30 * time.Minute,
	}

	workers, err := f.FetchWorkers()
	if err != nil {
		t.Fatalf("FetchWorkers() error = %v", err)
	}
	sort.Slice(workers, func(i, j int) bool { return workers[i].Name < workers[j].Name })
	if len(workers) != 1 {
		t.Fatalf("len(workers) = %d, want 1 (%#v)", len(workers), workers)
	}

	worker := workers[0]
	if worker.Name != "nux" {
		t.Fatalf("Name = %q, want %q", worker.Name, "nux")
	}
	if worker.StatusHint != "External owner degraded" {
		t.Fatalf("StatusHint = %q, want degraded hint", worker.StatusHint)
	}
	if worker.WorkStatus != "stale" {
		t.Fatalf("WorkStatus = %q, want %q", worker.WorkStatus, "stale")
	}
	if worker.IssueID != "slotmachine-456" {
		t.Fatalf("IssueID = %q, want %q", worker.IssueID, "slotmachine-456")
	}
}

func TestCalculateWorkStatus(t *testing.T) {
	tests := []struct {
		name          string
		completed     int
		total         int
		activityColor string
		want          string
	}{
		{
			name:          "complete when all done",
			completed:     5,
			total:         5,
			activityColor: activity.ColorGreen,
			want:          "complete",
		},
		{
			name:          "complete overrides activity color",
			completed:     3,
			total:         3,
			activityColor: activity.ColorRed,
			want:          "complete",
		},
		{
			name:          "active when green",
			completed:     2,
			total:         5,
			activityColor: activity.ColorGreen,
			want:          "active",
		},
		{
			name:          "stale when yellow",
			completed:     2,
			total:         5,
			activityColor: activity.ColorYellow,
			want:          "stale",
		},
		{
			name:          "stuck when red",
			completed:     2,
			total:         5,
			activityColor: activity.ColorRed,
			want:          "stuck",
		},
		{
			name:          "waiting when unknown color",
			completed:     2,
			total:         5,
			activityColor: activity.ColorUnknown,
			want:          "waiting",
		},
		{
			name:          "waiting when empty color",
			completed:     0,
			total:         5,
			activityColor: "",
			want:          "waiting",
		},
		{
			name:          "waiting when no work yet",
			completed:     0,
			total:         0,
			activityColor: activity.ColorUnknown,
			want:          "waiting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateWorkStatus(tt.completed, tt.total, tt.activityColor)
			if got != tt.want {
				t.Errorf("calculateWorkStatus(%d, %d, %q) = %q, want %q",
					tt.completed, tt.total, tt.activityColor, got, tt.want)
			}
		})
	}
}

func TestDetermineCIStatus(t *testing.T) {
	tests := []struct {
		name   string
		checks []struct {
			State      string `json:"state"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		}
		want string
	}{
		{
			name:   "pending when no checks",
			checks: nil,
			want:   "pending",
		},
		{
			name: "pass when all success",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Conclusion: "success"},
				{Conclusion: "success"},
			},
			want: "pass",
		},
		{
			name: "pass with skipped checks",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Conclusion: "success"},
				{Conclusion: "skipped"},
			},
			want: "pass",
		},
		{
			name: "fail when any failure",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Conclusion: "success"},
				{Conclusion: "failure"},
			},
			want: "fail",
		},
		{
			name: "fail when cancelled",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Conclusion: "cancelled"},
			},
			want: "fail",
		},
		{
			name: "fail when timed_out",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Conclusion: "timed_out"},
			},
			want: "fail",
		},
		{
			name: "pending when in_progress",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Conclusion: "success"},
				{Status: "in_progress"},
			},
			want: "pending",
		},
		{
			name: "pending when queued",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Status: "queued"},
			},
			want: "pending",
		},
		{
			name: "fail from state FAILURE",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{State: "FAILURE"},
			},
			want: "fail",
		},
		{
			name: "pending from state PENDING",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{State: "PENDING"},
			},
			want: "pending",
		},
		{
			name: "failure takes precedence over pending",
			checks: []struct {
				State      string `json:"state"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			}{
				{Conclusion: "failure"},
				{Status: "in_progress"},
			},
			want: "fail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineCIStatus(tt.checks)
			if got != tt.want {
				t.Errorf("determineCIStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetermineMergeableStatus(t *testing.T) {
	tests := []struct {
		name      string
		mergeable string
		want      string
	}{
		{"ready when MERGEABLE", "MERGEABLE", "ready"},
		{"ready when lowercase mergeable", "mergeable", "ready"},
		{"conflict when CONFLICTING", "CONFLICTING", "conflict"},
		{"conflict when lowercase conflicting", "conflicting", "conflict"},
		{"pending when UNKNOWN", "UNKNOWN", "pending"},
		{"pending when empty", "", "pending"},
		{"pending when other value", "something_else", "pending"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineMergeableStatus(tt.mergeable)
			if got != tt.want {
				t.Errorf("determineMergeableStatus(%q) = %q, want %q",
					tt.mergeable, got, tt.want)
			}
		})
	}
}

func TestDetermineColorClass(t *testing.T) {
	tests := []struct {
		name      string
		ciStatus  string
		mergeable string
		want      string
	}{
		{"green when pass and ready", "pass", "ready", "mq-green"},
		{"red when CI fails", "fail", "ready", "mq-red"},
		{"red when conflict", "pass", "conflict", "mq-red"},
		{"red when both fail and conflict", "fail", "conflict", "mq-red"},
		{"yellow when CI pending", "pending", "ready", "mq-yellow"},
		{"yellow when merge pending", "pass", "pending", "mq-yellow"},
		{"yellow when both pending", "pending", "pending", "mq-yellow"},
		{"yellow for unknown states", "unknown", "unknown", "mq-yellow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := determineColorClass(tt.ciStatus, tt.mergeable)
			if got != tt.want {
				t.Errorf("determineColorClass(%q, %q) = %q, want %q",
					tt.ciStatus, tt.mergeable, got, tt.want)
			}
		})
	}
}

func TestGetRefineryStatusHint(t *testing.T) {
	// Create a minimal fetcher for testing
	f := &LiveConvoyFetcher{}

	tests := []struct {
		name            string
		mergeQueueCount int
		want            string
	}{
		{"idle when no PRs", 0, "Idle - Waiting for PRs"},
		{"singular PR", 1, "Processing 1 PR"},
		{"multiple PRs", 2, "Processing 2 PRs"},
		{"many PRs", 10, "Processing 10 PRs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := f.getRefineryStatusHint(tt.mergeQueueCount)
			if got != tt.want {
				t.Errorf("getRefineryStatusHint(%d) = %q, want %q",
					tt.mergeQueueCount, got, tt.want)
			}
		})
	}
}

func TestParseActivityTimestamp(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantUnix  int64
		wantValid bool
	}{
		{"valid timestamp", "1704312345", 1704312345, true},
		{"zero timestamp", "0", 0, false},
		{"empty string", "", 0, false},
		{"invalid string", "abc", 0, false},
		{"negative", "-123", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unix, valid := parseActivityTimestamp(tt.input)
			if valid != tt.wantValid {
				t.Errorf("parseActivityTimestamp(%q) valid = %v, want %v",
					tt.input, valid, tt.wantValid)
			}
			if valid && unix != tt.wantUnix {
				t.Errorf("parseActivityTimestamp(%q) = %d, want %d",
					tt.input, unix, tt.wantUnix)
			}
		})
	}
}

// --- calculateWorkerWorkStatus with configurable thresholds ---

func TestCalculateWorkerWorkStatus_DefaultThresholds(t *testing.T) {
	stale := 5 * time.Minute
	stuck := 30 * time.Minute

	tests := []struct {
		name       string
		age        time.Duration
		issueID    string
		workerName string
		want       string
	}{
		{"refinery always working", 1 * time.Hour, "gt-123", "refinery", "working"},
		{"refinery working even without issue", 0, "", "refinery", "working"},
		{"no issue means idle", 0, "", "dag", "idle"},
		{"no issue means idle even if active", 1 * time.Second, "", "nux", "idle"},
		{"very recent is working", 1 * time.Second, "gt-123", "dag", "working"},
		{"just under stale is working", stale - 1*time.Second, "gt-123", "dag", "working"},
		{"at stale boundary is stale", stale, "gt-123", "dag", "stale"},
		{"between stale and stuck is stale", 15 * time.Minute, "gt-123", "dag", "stale"},
		{"just under stuck is stale", stuck - 1*time.Second, "gt-123", "dag", "stale"},
		{"at stuck boundary is stuck", stuck, "gt-123", "dag", "stuck"},
		{"well past stuck is stuck", 2 * time.Hour, "gt-123", "dag", "stuck"},
		{"zero age with issue is working", 0, "gt-456", "nux", "working"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateWorkerWorkStatus(tt.age, tt.issueID, tt.workerName, stale, stuck)
			if got != tt.want {
				t.Errorf("calculateWorkerWorkStatus(%v, %q, %q, %v, %v) = %q, want %q",
					tt.age, tt.issueID, tt.workerName, stale, stuck, got, tt.want)
			}
		})
	}
}

func TestCalculateWorkerWorkStatus_CustomThresholds(t *testing.T) {
	// Use very different thresholds to prove they're actually used
	stale := 1 * time.Minute
	stuck := 5 * time.Minute

	tests := []struct {
		name    string
		age     time.Duration
		issueID string
		want    string
	}{
		{"30s is working with 1m stale", 30 * time.Second, "gt-1", "working"},
		{"90s is stale with 1m stale", 90 * time.Second, "gt-1", "stale"},
		{"3m is stale with 5m stuck", 3 * time.Minute, "gt-1", "stale"},
		{"6m is stuck with 5m stuck", 6 * time.Minute, "gt-1", "stuck"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateWorkerWorkStatus(tt.age, tt.issueID, "dag", stale, stuck)
			if got != tt.want {
				t.Errorf("calculateWorkerWorkStatus(%v, %q, dag, %v, %v) = %q, want %q",
					tt.age, tt.issueID, stale, stuck, got, tt.want)
			}
		})
	}
}

func TestCalculateWorkerWorkStatus_LargeThresholds(t *testing.T) {
	// Very large thresholds — everything should be "working"
	stale := 24 * time.Hour
	stuck := 48 * time.Hour

	got := calculateWorkerWorkStatus(12*time.Hour, "gt-1", "dag", stale, stuck)
	if got != "working" {
		t.Errorf("12h with 24h stale threshold should be working, got %q", got)
	}

	got = calculateWorkerWorkStatus(36*time.Hour, "gt-1", "dag", stale, stuck)
	if got != "stale" {
		t.Errorf("36h with 24h/48h thresholds should be stale, got %q", got)
	}
}

func TestCalculateWorkerWorkStatus_ZeroThresholds(t *testing.T) {
	// Zero thresholds: everything with an issue should be stuck
	got := calculateWorkerWorkStatus(0, "gt-1", "dag", 0, 0)
	if got != "stuck" {
		t.Errorf("0 age with 0/0 thresholds should be stuck, got %q", got)
	}
}

// --- NewConvoyHandler timeout ---

func TestNewConvoyHandler_StoresTimeout(t *testing.T) {
	mock := &MockConvoyFetcher{}
	timeout := 15 * time.Second

	handler, err := NewConvoyHandler(mock, timeout, "test-token")
	if err != nil {
		t.Fatalf("NewConvoyHandler: %v", err)
	}

	if handler.fetchTimeout != timeout {
		t.Errorf("fetchTimeout = %v, want %v", handler.fetchTimeout, timeout)
	}
}

func TestNewConvoyHandler_ZeroTimeout(t *testing.T) {
	mock := &MockConvoyFetcher{}
	handler, err := NewConvoyHandler(mock, 0, "test-token")
	if err != nil {
		t.Fatalf("NewConvoyHandler: %v", err)
	}

	if handler.fetchTimeout != 0 {
		t.Errorf("fetchTimeout = %v, want 0", handler.fetchTimeout)
	}
}

// --- NewAPIHandler timeout ---

func TestNewAPIHandler_StoresTimeouts(t *testing.T) {
	defTimeout := 45 * time.Second
	maxTimeout := 90 * time.Second

	handler := NewAPIHandler(defTimeout, maxTimeout, "test-token")
	if handler.defaultRunTimeout != defTimeout {
		t.Errorf("defaultRunTimeout = %v, want %v", handler.defaultRunTimeout, defTimeout)
	}
	if handler.maxRunTimeout != maxTimeout {
		t.Errorf("maxRunTimeout = %v, want %v", handler.maxRunTimeout, maxTimeout)
	}
}

// --- NewDashboardMux nil config ---

func TestNewDashboardMux_NilConfig(t *testing.T) {
	mock := &MockConvoyFetcher{}
	mux, err := NewDashboardMux(mock, nil)
	if err != nil {
		t.Fatalf("NewDashboardMux(nil config): %v", err)
	}
	if mux == nil {
		t.Fatal("NewDashboardMux returned nil handler")
	}
}

func TestRunCmd_SuccessAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based command test")
	}

	// Use generous timeout for success case — not testing timeout behavior here.
	// 500ms was flaky under CI load where process startup can take >1s.
	out, err := runCmd(30*time.Second, "sh", "-c", "printf 'ok'")
	if err != nil {
		t.Fatalf("runCmd success case failed: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "ok" {
		t.Fatalf("runCmd output = %q, want %q", got, "ok")
	}

	// Use "exec sleep" so sleep replaces the shell process — avoids orphan child
	// holding stdout open. Use 200ms timeout (not 30ms) for stability under load.
	_, err = runCmd(200*time.Millisecond, "sh", "-c", "exec sleep 10")
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
}

func TestRunBdCmd_ReturnsStdoutOnNonZeroAndTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-based command test")
	}

	binDir := t.TempDir()
	bdPath := filepath.Join(binDir, "bd")
	// Use "exec sleep" so the sleep process replaces the shell — no orphan
	// child processes that hold stdout open after the parent is killed.
	script := `#!/bin/sh
case "$1" in
  warn)
    echo "partial output"
    exit 1
    ;;
  sleep)
    exec sleep 10
    ;;
  *)
    echo "ok"
    exit 0
    ;;
esac
`
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	// Use bdBin with full path instead of t.Setenv("PATH", ...) to avoid
	// process-wide PATH mutation that can race under concurrent test suites.
	// Use generous 30s timeout — this fetcher tests exit-code behavior, not
	// timeouts. The 2s value was flaky under CI load (process startup alone
	// can take >1s under heavy contention).
	f := &LiveConvoyFetcher{cmdTimeout: 30 * time.Second, bdBin: bdPath}

	t.Run("non-zero exit with stdout returns output", func(t *testing.T) {
		stdout, err := f.runBdCmd(t.TempDir(), "warn")
		if err != nil {
			t.Fatalf("runBdCmd warn returned error: %v", err)
		}
		if got := strings.TrimSpace(stdout.String()); got != "partial output" {
			t.Fatalf("runBdCmd warn output = %q, want %q", got, "partial output")
		}
	})

	t.Run("timeout returns explicit error", func(t *testing.T) {
		// Use 200ms timeout (not 20ms) to avoid flakiness from process startup
		// overhead under system load. The script sleeps for 10s so the timeout
		// always fires first with wide margin.
		tf := &LiveConvoyFetcher{cmdTimeout: 200 * time.Millisecond, bdBin: bdPath}
		_, err := tf.runBdCmd(t.TempDir(), "sleep")
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected timeout error, got: %v", err)
		}
	})
}

func withMayorFetcherHooks(t *testing.T, sessionEnv func(sessionName, key string) (string, error), runCmdFunc func(time.Duration, string, ...string) (*bytes.Buffer, error)) {
	t.Helper()

	originalGetEnv := fetcherGetSessionEnv
	originalRunCmd := fetcherRunCmd
	t.Cleanup(func() {
		fetcherGetSessionEnv = originalGetEnv
		fetcherRunCmd = originalRunCmd
	})
	t.Cleanup(config.ResetRegistryForTesting)

	if sessionEnv != nil {
		fetcherGetSessionEnv = sessionEnv
	}
	if runCmdFunc != nil {
		fetcherRunCmd = runCmdFunc
	}
}

func TestResolveMayorRuntime(t *testing.T) {
	tests := []struct {
		name        string
		sessionEnv  func(sessionName, key string) (string, error)
		setup       func(t *testing.T, townRoot string)
		wantRuntime string
	}{
		{
			name: "uses session agent env",
			sessionEnv: func(sessionName, key string) (string, error) {
				if sessionName != "hq-mayor" || key != "GT_AGENT" {
					t.Fatalf("unexpected session env lookup: %s %s", sessionName, key)
				}
				return "codex", nil
			},
			wantRuntime: "codex",
		},
		{
			name: "falls back to town settings",
			sessionEnv: func(string, string) (string, error) {
				return "", os.ErrNotExist
			},
			setup: func(t *testing.T, townRoot string) {
				t.Helper()
				settings := config.NewTownSettings()
				settings.DefaultAgent = "codex"
				if err := config.SaveTownSettings(config.TownSettingsPath(townRoot), settings); err != nil {
					t.Fatalf("SaveTownSettings: %v", err)
				}
			},
			wantRuntime: "codex",
		},
		{
			name: "uses custom role agent alias",
			sessionEnv: func(string, string) (string, error) {
				return "", os.ErrNotExist
			},
			setup: func(t *testing.T, townRoot string) {
				t.Helper()
				settings := config.NewTownSettings()
				settings.RoleAgents[constants.RoleMayor] = "claude-sonnet"
				settings.Agents["claude-sonnet"] = &config.RuntimeConfig{
					Command: "claude",
					Args:    []string{"--dangerously-skip-permissions", "--model", "sonnet"},
				}
				if err := config.SaveTownSettings(config.TownSettingsPath(townRoot), settings); err != nil {
					t.Fatalf("SaveTownSettings: %v", err)
				}
			},
			wantRuntime: "claude/sonnet",
		},
		{
			name: "uses registry agent from session env",
			sessionEnv: func(sessionName, key string) (string, error) {
				if sessionName != "hq-mayor" || key != "GT_AGENT" {
					t.Fatalf("unexpected session env lookup: %s %s", sessionName, key)
				}
				return "mayor-registry", nil
			},
			setup: func(t *testing.T, townRoot string) {
				t.Helper()
				registry := &config.AgentRegistry{
					Version: config.CurrentAgentRegistryVersion,
					Agents: map[string]*config.AgentPresetInfo{
						"mayor-registry": {
							Name:    "mayor-registry",
							Command: "opencode",
							Args:    []string{"run", "--model", "gpt-5"},
						},
					},
				}
				if err := config.SaveAgentRegistry(config.DefaultAgentRegistryPath(townRoot), registry); err != nil {
					t.Fatalf("SaveAgentRegistry: %v", err)
				}
			},
			wantRuntime: "opencode/gpt-5",
		},
		{
			name: "uses ephemeral tier agent from session env",
			sessionEnv: func(sessionName, key string) (string, error) {
				if sessionName != "hq-mayor" || key != "GT_AGENT" {
					t.Fatalf("unexpected session env lookup: %s %s", sessionName, key)
				}
				return "claude-sonnet", nil
			},
			setup: func(t *testing.T, townRoot string) {
				t.Helper()
				if err := config.SaveTownSettings(config.TownSettingsPath(townRoot), config.NewTownSettings()); err != nil {
					t.Fatalf("SaveTownSettings: %v", err)
				}
				t.Setenv("GT_COST_TIER", "economy")
			},
			wantRuntime: "claude/sonnet",
		},
		{
			name: "uses provider only role agent alias",
			sessionEnv: func(string, string) (string, error) {
				return "", os.ErrNotExist
			},
			setup: func(t *testing.T, townRoot string) {
				t.Helper()
				settings := config.NewTownSettings()
				settings.RoleAgents[constants.RoleMayor] = "mayor-custom"
				settings.Agents["mayor-custom"] = &config.RuntimeConfig{
					Provider: "codex",
				}
				if err := config.SaveTownSettings(config.TownSettingsPath(townRoot), settings); err != nil {
					t.Fatalf("SaveTownSettings: %v", err)
				}
			},
			wantRuntime: "codex",
		},
		{
			name: "returns unknown alias verbatim when unresolved",
			sessionEnv: func(sessionName, key string) (string, error) {
				if sessionName != "hq-mayor" || key != "GT_AGENT" {
					t.Fatalf("unexpected session env lookup: %s %s", sessionName, key)
				}
				return "mystery-agent", nil
			},
			wantRuntime: "mystery-agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withMayorFetcherHooks(t, tt.sessionEnv, nil)

			townRoot := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, townRoot)
			}

			f := &LiveConvoyFetcher{townRoot: townRoot}
			if got := f.resolveMayorRuntime("hq-mayor"); got != tt.wantRuntime {
				t.Fatalf("resolveMayorRuntime() = %q, want %q", got, tt.wantRuntime)
			}
		})
	}
}

func TestRuntimeLabelFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		args     []string
		fallback string
		want     string
	}{
		{
			name:     "claude model flag",
			command:  "claude",
			args:     []string{"--dangerously-skip-permissions", "--model", "sonnet"},
			fallback: "claude-sonnet",
			want:     "claude/sonnet",
		},
		{
			name:     "short model flag",
			command:  "opencode",
			args:     []string{"run", "-m", "gpt-5"},
			fallback: "custom-opencode",
			want:     "opencode/gpt-5",
		},
		{
			name:     "cgroup wrap unwraps binary",
			command:  "cgroup-wrap",
			args:     []string{"/usr/local/bin/codex", "--dangerously-bypass-approvals-and-sandbox"},
			fallback: "codex",
			want:     "codex",
		},
		{
			name:     "empty command falls back to alias",
			command:  "",
			args:     nil,
			fallback: "mystery-agent",
			want:     "mystery-agent",
		},
		{
			name:     "empty command and fallback defaults to claude",
			command:  "",
			args:     nil,
			fallback: "",
			want:     "claude",
		},
		{
			name:     "model equals form long flag",
			command:  "claude",
			args:     []string{"--dangerously-skip-permissions", "--model=sonnet"},
			fallback: "claude-sonnet",
			want:     "claude/sonnet",
		},
		{
			name:     "model equals form short flag",
			command:  "opencode",
			args:     []string{"-m=gpt-5"},
			fallback: "custom",
			want:     "opencode/gpt-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeLabelFromConfig(tt.command, tt.args, tt.fallback); got != tt.want {
				t.Fatalf("runtimeLabelFromConfig(%q, %v, %q) = %q, want %q", tt.command, tt.args, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestFetchMayor_UsesResolvedRuntime(t *testing.T) {
	withMayorFetcherHooks(
		t,
		func(sessionName, key string) (string, error) {
			if sessionName != "hq-mayor" || key != "GT_AGENT" {
				t.Fatalf("unexpected session env lookup: %s %s", sessionName, key)
			}
			return "codex", nil
		},
		func(_ time.Duration, name string, args ...string) (*bytes.Buffer, error) {
			if name != "tmux" {
				t.Fatalf("unexpected command: %s %v", name, args)
			}
			return bytes.NewBufferString("hq-mayor:1731328320\nhq-deacon:1731328300\n"), nil
		},
	)

	f := &LiveConvoyFetcher{
		townRoot:             t.TempDir(),
		mayorActiveThreshold: 24 * time.Hour,
		tmuxCmdTimeout:       time.Second,
	}

	status, err := f.FetchMayor()
	if err != nil {
		t.Fatalf("FetchMayor: %v", err)
	}
	if !status.IsAttached {
		t.Fatal("expected mayor to be attached")
	}
	if status.SessionName != "hq-mayor" {
		t.Fatalf("SessionName = %q, want %q", status.SessionName, "hq-mayor")
	}
	if status.Runtime != "codex" {
		t.Fatalf("Runtime = %q, want %q", status.Runtime, "codex")
	}
	if status.LastActivity == "" {
		t.Fatal("expected LastActivity to be populated")
	}
}

func TestSessionRowState(t *testing.T) {
	tests := []struct {
		name   string
		status runtimepkg.SessionStatus
		want   string
	}{
		{name: "stopped", status: runtimepkg.SessionStatus{Alive: false}, want: "stopped"},
		{name: "degraded", status: runtimepkg.SessionStatus{Alive: true, Ready: false}, want: "degraded"},
		{name: "busy", status: runtimepkg.SessionStatus{Alive: true, Ready: true, Busy: true}, want: "busy"},
		{name: "running", status: runtimepkg.SessionStatus{Alive: true, Ready: true, Busy: false}, want: "running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionRowState(tt.status); got != tt.want {
				t.Fatalf("sessionRowState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManagedSessionStatusForRowReadsOwnerManagedBinding(t *testing.T) {
	townRoot := t.TempDir()
	sessionName := "gt-gastown-witness"
	pid := os.Getpid()
	rigPath := filepath.Join(townRoot, "gastown")
	settings := config.NewRigSettings()
	settings.Agents = map[string]*config.RuntimeConfig{
		"copilot-external": {
			Provider: "copilot",
			Command:  "copilot",
			CLIURL:   "http://127.0.0.1:4321",
		},
	}
	settings.RoleAgents = map[string]string{"witness": "copilot-external"}
	if err := os.MkdirAll(filepath.Join(rigPath, "settings"), 0o755); err != nil {
		t.Fatalf("MkdirAll(settings) error = %v", err)
	}
	if err := config.SaveRigSettings(filepath.Join(rigPath, "settings", "config.json"), settings); err != nil {
		t.Fatalf("SaveRigSettings() error = %v", err)
	}
	store := runtimepkg.NewFileSessionBindingStore(townRoot)
	binding := runtimepkg.SessionBinding{
		IssueID:          "slotmachine-910.7.3",
		Role:             "witness",
		RigName:          "gastown",
		AgentName:        "witness",
		Provider:         "copilot-external",
		SessionName:      sessionName,
		RuntimeSessionID: "runtime-xyz",
		WorkDir:          filepath.Join(rigPath, "witness"),
		Metadata: runtimepkg.OwnerBindingMetadata(
			runtimepkg.ExternalOwnerDir(townRoot, sessionName),
			pid,
		),
	}
	if err := store.Save(context.Background(), binding); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	ready := false
	if err := runtimepkg.WriteExternalOwnerStatus(townRoot, sessionName, runtimepkg.ExternalCopilotOwnerStatus{
		OwnerPID:         pid,
		RuntimeSessionID: "runtime-xyz",
		Ready:            &ready,
		Busy:             true,
		Error:            "owner degraded",
		UpdatedAt:        time.Now().UTC(),
	}); err != nil {
		t.Fatalf("WriteExternalOwnerStatus() error = %v", err)
	}
	f := &LiveConvoyFetcher{townRoot: townRoot}
	status, ok := f.managedSessionStatusForRow(SessionRow{Name: sessionName, Role: "witness", Rig: "gastown", Worker: "witness"})
	if !ok {
		t.Fatal("managedSessionStatusForRow() ok = false, want true")
	}
	if !status.Alive || status.Ready || status.Busy {
		t.Fatalf("status = %#v, want alive=true ready=false busy=false", status)
	}
}

// TestFetchHealth_DeaconHeartbeatFieldName verifies that FetchHealth reads the
// "timestamp" field written by heartbeat.go, not the old "last_heartbeat" field
// that caused dashboard to always show "no timestamp". (GH#2989)
func TestFetchHealth_DeaconHeartbeatFieldName(t *testing.T) {
	townRoot := t.TempDir()
	deaconDir := filepath.Join(townRoot, "deacon")
	if err := os.MkdirAll(deaconDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write heartbeat.json using the field name heartbeat.go actually writes.
	now := time.Now().UTC().Truncate(time.Second)
	heartbeatJSON := fmt.Sprintf(`{"timestamp":%q,"cycle":42,"healthy_agents":3,"unhealthy_agents":1}`,
		now.Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(deaconDir, "heartbeat.json"), []byte(heartbeatJSON), 0644); err != nil {
		t.Fatal(err)
	}

	f := &LiveConvoyFetcher{
		townRoot:                townRoot,
		heartbeatFreshThreshold: 5 * time.Minute,
	}

	health, err := f.FetchHealth()
	if err != nil {
		t.Fatalf("FetchHealth: %v", err)
	}

	// DeaconHeartbeat must NOT be "no timestamp" — the field was read correctly.
	if health.DeaconHeartbeat == "no timestamp" {
		t.Fatal("DeaconHeartbeat = \"no timestamp\": JSON field name mismatch (GH#2989)")
	}
	if health.DeaconHeartbeat == "no heartbeat" {
		t.Fatal("DeaconHeartbeat = \"no heartbeat\": heartbeat file was not read")
	}

	// Cycle and agent counts should be populated.
	if health.DeaconCycle != 42 {
		t.Errorf("DeaconCycle = %d, want 42", health.DeaconCycle)
	}
	if health.HealthyAgents != 3 {
		t.Errorf("HealthyAgents = %d, want 3", health.HealthyAgents)
	}
	if health.UnhealthyAgents != 1 {
		t.Errorf("UnhealthyAgents = %d, want 1", health.UnhealthyAgents)
	}

	// Heartbeat should be considered fresh (written just now).
	if !health.HeartbeatFresh {
		t.Error("HeartbeatFresh = false for a just-written heartbeat")
	}
}
