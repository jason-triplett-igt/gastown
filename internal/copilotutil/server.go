package copilotutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/util"
	"github.com/steveyegge/gastown/internal/workspace"
)

type ServerStatus struct {
	CLIURL    string    `json:"cli_url"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	PID       int       `json:"pid,omitempty"`
	LogPath   string    `json:"log_path,omitempty"`
	State     string    `json:"state"`
	Managed   bool      `json:"managed"`
	Healthy   bool      `json:"healthy"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type serverState struct {
	CLIURL    string    `json:"cli_url"`
	PID       int       `json:"pid,omitempty"`
	LogPath   string    `json:"log_path,omitempty"`
	Managed   bool      `json:"managed"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

var (
	statusServer = Status
	startServer  = Start
)

func ResolveCLIURLForTown(townRoot string) (string, error) {
	rc := config.ResolveRoleAgentConfig("mayor", townRoot, "")
	if rc == nil || !strings.EqualFold(strings.TrimSpace(rc.Provider), "copilot") || strings.TrimSpace(rc.CLIURL) == "" {
		return "", fmt.Errorf("town is not configured for copilot external")
	}
	return strings.TrimSpace(rc.CLIURL), nil
}

func IsManagedCLIURL(raw string) bool {
	host, _, err := parseCLIURL(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func EnsureServer(ctx context.Context, townRoot string) (*ServerStatus, error) {
	status, err := statusServer(townRoot)
	if err != nil {
		return nil, err
	}
	if status.Healthy {
		return status, nil
	}
	if !status.Managed && !IsManagedCLIURL(status.CLIURL) {
		return nil, fmt.Errorf("copilot server at %s is unhealthy and not Gastown-managed", status.CLIURL)
	}
	return startServer(ctx, townRoot)
}

func EnsureServerForCLIURL(ctx context.Context, townRoot, cliURL string) (*ServerStatus, error) {
	status, err := statusForCLIURL(townRoot, cliURL)
	if err != nil {
		return nil, err
	}
	if status.Healthy {
		return status, nil
	}
	if !status.Managed && !IsManagedCLIURL(status.CLIURL) {
		return nil, fmt.Errorf("copilot server at %s is unhealthy and not Gastown-managed", status.CLIURL)
	}
	return startForCLIURL(ctx, townRoot, cliURL)
}

func Start(ctx context.Context, townRoot string) (*ServerStatus, error) {
	cliURL, err := ResolveCLIURLForTown(townRoot)
	if err != nil {
		return nil, err
	}
	return startForCLIURL(ctx, townRoot, cliURL)
}

func startForCLIURL(ctx context.Context, townRoot, cliURL string) (*ServerStatus, error) {
	cliURL = strings.TrimSpace(cliURL)
	if !IsManagedCLIURL(cliURL) {
		return nil, fmt.Errorf("copilot server %s is not local; refusing to auto-start", cliURL)
	}
	if healthy, _ := health(cliURL); healthy {
		status := loadState(townRoot, cliURL)
		if status.PID == 0 {
			status.Managed = false
		}
		status.Healthy = true
		status.State = "running"
		return status, nil
	}
	host, port, err := parseCLIURL(cliURL)
	if err != nil {
		return nil, err
	}
	logPath := filepath.Join(serverDir(townRoot), "copilot-server.log")
	if err := os.MkdirAll(serverDir(townRoot), 0o755); err != nil {
		return nil, fmt.Errorf("creating copilot server dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening copilot server log: %w", err)
	}
	cmd := exec.CommandContext(ctx, "copilot", "--headless", "--port", strconv.Itoa(port), "--no-auto-update", "--log-level", "error")
	cmd.Dir = townRoot
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	util.SetDetachedProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("starting copilot server: %w", err)
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
		_ = cmd.Process.Release()
	}
	state := serverState{CLIURL: cliURL, PID: pid, LogPath: logPath, Managed: true, UpdatedAt: time.Now().UTC()}
	if err := saveState(townRoot, state); err != nil {
		return nil, err
	}
	if err := waitHealthy(ctx, cliURL); err != nil {
		return nil, err
	}
	return &ServerStatus{CLIURL: cliURL, Host: host, Port: port, PID: pid, LogPath: logPath, Managed: true, Healthy: true, State: "running", UpdatedAt: state.UpdatedAt}, nil
}

func Stop(townRoot string) (*ServerStatus, error) {
	cliURL, err := ResolveCLIURLForTown(townRoot)
	if err != nil {
		return nil, err
	}
	return stopForCLIURL(townRoot, cliURL)
}

func stopForCLIURL(townRoot, cliURL string) (*ServerStatus, error) {
	cliURL = strings.TrimSpace(cliURL)
	status := loadState(townRoot, cliURL)
	if status.PID > 0 {
		if proc, err := os.FindProcess(status.PID); err == nil {
			_ = proc.Kill()
		}
	}
	_ = os.Remove(statePath(townRoot))
	status.Healthy = false
	status.State = "stopped"
	return status, nil
}

func Status(townRoot string) (*ServerStatus, error) {
	cliURL, err := ResolveCLIURLForTown(townRoot)
	if err != nil {
		return nil, err
	}
	return statusForCLIURL(townRoot, cliURL)
}

func statusForCLIURL(townRoot, cliURL string) (*ServerStatus, error) {
	cliURL = strings.TrimSpace(cliURL)
	status := loadState(townRoot, cliURL)
	healthy, _ := health(cliURL)
	status.Healthy = healthy
	if healthy {
		status.State = "running"
	} else if status.PID > 0 {
		status.State = "stale"
	} else {
		status.State = "stopped"
	}
	return status, nil
}

func serverDir(townRoot string) string {
	return filepath.Join(townRoot, ".runtime", "copilot-server")
}

func statePath(townRoot string) string {
	return filepath.Join(serverDir(townRoot), "state.json")
}

func loadState(townRoot, cliURL string) *ServerStatus {
	host, port, _ := parseCLIURL(cliURL)
	status := &ServerStatus{CLIURL: cliURL, Host: host, Port: port}
	data, err := os.ReadFile(statePath(townRoot))
	if err != nil {
		return status
	}
	var state serverState
	if err := jsonUnmarshal(data, &state); err != nil {
		return status
	}
	status.PID = state.PID
	status.LogPath = state.LogPath
	status.Managed = state.Managed
	status.UpdatedAt = state.UpdatedAt
	return status
}

func saveState(townRoot string, state serverState) error {
	data, err := jsonMarshal(state)
	if err != nil {
		return err
	}
	if err := os.WriteFile(statePath(townRoot), data, 0o644); err != nil {
		return fmt.Errorf("writing copilot server state: %w", err)
	}
	return nil
}

func waitHealthy(ctx context.Context, cliURL string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		healthy, err := health(cliURL)
		if healthy && err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for copilot server health: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func health(cliURL string) (bool, error) {
	host, port, err := parseCLIURL(cliURL)
	if err != nil {
		return false, err
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return false, err
	}
	_ = conn.Close()
	return true, nil
}

func parseCLIURL(raw string) (string, int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", 0, fmt.Errorf("cli_url is required")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", 0, fmt.Errorf("parsing cli_url: %w", err)
	}
	host := u.Hostname()
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 {
		return "", 0, fmt.Errorf("invalid cli_url port: %s", raw)
	}
	return host, port, nil
}

func TownRootOrError() (string, error) {
	return workspace.FindFromCwdOrError()
}

func jsonMarshal(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}

func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
