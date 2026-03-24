package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/util"
)

const (
	ExternalOwnerModeMetadataKey = "owner_mode"
	ExternalOwnerPIDMetadataKey  = "owner_pid"
	ExternalOwnerDirMetadataKey  = "owner_dir"
	ExternalOwnerHeartbeatWindow = 15 * time.Second

	ExternalOwnerModeQueue = "copilot-queue"

	ExternalOwnerRequestKindSend = "send"
	ExternalOwnerRequestKindAsk  = "ask"

	externalOwnerPollInterval = 200 * time.Millisecond
)

type ExternalCopilotOwnerConfig struct {
	IssueID          string            `json:"issue_id"`
	Role             string            `json:"role"`
	RigName          string            `json:"rig_name,omitempty"`
	RigPath          string            `json:"rig_path,omitempty"`
	AgentName        string            `json:"agent_name,omitempty"`
	Provider         string            `json:"provider,omitempty"`
	SessionName      string            `json:"session_name"`
	SessionKind      string            `json:"session_kind,omitempty"`
	TownRoot         string            `json:"town_root"`
	WorkDir          string            `json:"work_dir"`
	RuntimeConfigDir string            `json:"runtime_config_dir,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	ToolPolicy       config.ToolPolicy `json:"tool_policy"`
	StartupPrompt    string            `json:"startup_prompt,omitempty"`
	RequestedModel   string            `json:"requested_model,omitempty"`
	ReasoningEffort  string            `json:"reasoning_effort,omitempty"`
	ResumeSessionID  string            `json:"resume_session_id,omitempty"`
}

type ExternalCopilotOwnerStatus struct {
	OwnerPID         int       `json:"owner_pid,omitempty"`
	RuntimeSessionID string    `json:"runtime_session_id,omitempty"`
	Error            string    `json:"error,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ExternalCopilotOwnerDiscovery struct {
	Binding       *SessionBinding             `json:"-"`
	OwnerDir      string                      `json:"owner_dir,omitempty"`
	OwnerPID      int                         `json:"owner_pid,omitempty"`
	Status        *ExternalCopilotOwnerStatus `json:"status,omitempty"`
	OwnerAlive    bool                        `json:"owner_alive"`
	HeartbeatLive bool                        `json:"heartbeat_live"`
	Recoverable   bool                        `json:"recoverable"`
	NeedsRecovery bool                        `json:"needs_recovery"`
	Reason        string                      `json:"reason,omitempty"`
}

type ExternalCopilotOwnerRequest struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Message   string    `json:"message"`
	TimeoutMS int64     `json:"timeout_ms,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type ExternalCopilotOwnerResponse struct {
	ID        string    `json:"id"`
	Content   string    `json:"content,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func ExternalOwnerDir(townRoot, sessionName string) string {
	safe := strings.ReplaceAll(strings.TrimSpace(sessionName), "/", "_")
	return filepath.Join(townRoot, ".runtime", "copilot-owner", safe)
}

func ExternalOwnerConfigPath(townRoot, sessionName string) string {
	return filepath.Join(ExternalOwnerDir(townRoot, sessionName), "config.json")
}

func ExternalOwnerStatusPath(townRoot, sessionName string) string {
	return filepath.Join(ExternalOwnerDir(townRoot, sessionName), "status.json")
}

func ExternalOwnerLogPath(townRoot, sessionName string) string {
	return filepath.Join(ExternalOwnerDir(townRoot, sessionName), "owner.log")
}

func externalOwnerRequestsDir(townRoot, sessionName string) string {
	return filepath.Join(ExternalOwnerDir(townRoot, sessionName), "requests")
}

func externalOwnerResponsesDir(townRoot, sessionName string) string {
	return filepath.Join(ExternalOwnerDir(townRoot, sessionName), "responses")
}

func WriteExternalOwnerConfig(cfg ExternalCopilotOwnerConfig) error {
	if strings.TrimSpace(cfg.TownRoot) == "" {
		return fmt.Errorf("town root is required")
	}
	if strings.TrimSpace(cfg.SessionName) == "" {
		return fmt.Errorf("session name is required")
	}
	if err := os.MkdirAll(ExternalOwnerDir(cfg.TownRoot, cfg.SessionName), 0o755); err != nil {
		return fmt.Errorf("creating owner dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding owner config: %w", err)
	}
	if err := os.WriteFile(ExternalOwnerConfigPath(cfg.TownRoot, cfg.SessionName), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing owner config: %w", err)
	}
	return nil
}

func ReadExternalOwnerConfig(path string) (*ExternalCopilotOwnerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading owner config: %w", err)
	}
	var cfg ExternalCopilotOwnerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decoding owner config: %w", err)
	}
	return &cfg, nil
}

func WriteExternalOwnerStatus(townRoot, sessionName string, status ExternalCopilotOwnerStatus) error {
	if err := os.MkdirAll(ExternalOwnerDir(townRoot, sessionName), 0o755); err != nil {
		return fmt.Errorf("creating owner dir: %w", err)
	}
	if status.UpdatedAt.IsZero() {
		status.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding owner status: %w", err)
	}
	if err := os.WriteFile(ExternalOwnerStatusPath(townRoot, sessionName), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing owner status: %w", err)
	}
	return nil
}

func DeleteExternalOwnerStatus(townRoot, sessionName string) error {
	if err := os.Remove(ExternalOwnerStatusPath(townRoot, sessionName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing owner status: %w", err)
	}
	return nil
}

func StopExternalOwnerProcess(townRoot, sessionName string) error {
	status, err := ReadExternalOwnerStatus(townRoot, sessionName)
	if err != nil {
		if strings.Contains(err.Error(), "reading owner status") {
			return nil
		}
		return nil
	}
	if status != nil && status.OwnerPID > 0 && isProcessRunning(status.OwnerPID) {
		if proc, findErr := os.FindProcess(status.OwnerPID); findErr == nil {
			_ = proc.Kill()
		}
	}
	return nil
}

func ClearExternalOwnerResponses(townRoot, sessionName string) error {
	entries, err := os.ReadDir(externalOwnerResponsesDir(townRoot, sessionName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading owner response dir: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(externalOwnerResponsesDir(townRoot, sessionName), entry.Name())
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing owner response: %w", err)
		}
	}
	return nil
}

func ReadExternalOwnerStatus(townRoot, sessionName string) (*ExternalCopilotOwnerStatus, error) {
	data, err := os.ReadFile(ExternalOwnerStatusPath(townRoot, sessionName))
	if err != nil {
		return nil, fmt.Errorf("reading owner status: %w", err)
	}
	var status ExternalCopilotOwnerStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("decoding owner status: %w", err)
	}
	return &status, nil
}

func DiscoverExternalOwner(townRoot string, binding *SessionBinding) (*ExternalCopilotOwnerDiscovery, error) {
	if binding == nil {
		return nil, fmt.Errorf("binding is required")
	}
	discovery := &ExternalCopilotOwnerDiscovery{
		Binding:  binding,
		OwnerDir: OwnerDirFromBinding(townRoot, binding),
		OwnerPID: OwnerPIDFromMetadata(binding.Metadata),
	}
	if strings.TrimSpace(discovery.OwnerDir) == "" {
		discovery.Reason = "owner dir unavailable"
		return discovery, nil
	}
	status, err := ReadExternalOwnerStatus(townRoot, binding.SessionName)
	if err == nil {
		discovery.Status = status
		if status.OwnerPID > 0 {
			discovery.OwnerPID = status.OwnerPID
		}
	}
	discovery.OwnerAlive = isProcessRunning(discovery.OwnerPID)
	if status != nil && !status.UpdatedAt.IsZero() {
		discovery.HeartbeatLive = time.Since(status.UpdatedAt) <= ExternalOwnerHeartbeatWindow
	}
	discovery.Recoverable = strings.TrimSpace(binding.RuntimeSessionID) != "" && !discovery.OwnerAlive
	discovery.NeedsRecovery = discovery.Recoverable
	switch {
	case discovery.OwnerAlive && discovery.HeartbeatLive:
		discovery.Reason = "owner alive"
	case discovery.OwnerAlive:
		discovery.Reason = "owner alive but heartbeat stale"
	case discovery.Recoverable:
		discovery.Reason = "owner missing; persisted session may be recoverable"
	default:
		discovery.Reason = "owner unavailable"
	}
	return discovery, nil
}

func MarkExternalOwnerRecovered(ctx context.Context, store SessionBindingStore, binding *SessionBinding) error {
	if store == nil || binding == nil {
		return nil
	}
	if townRoot := townRootFromBinding(binding); townRoot != "" {
		_ = DeleteExternalOwnerStatus(townRoot, binding.SessionName)
		_ = ClearExternalOwnerRequests(townRoot, binding.SessionName)
	}
	updated := *binding
	updated.LifecycleState = SessionLifecycleStopped
	updated.UpdatedAt = time.Now().UTC()
	if updated.Metadata == nil {
		updated.Metadata = make(map[string]string)
	}
	delete(updated.Metadata, ExternalOwnerPIDMetadataKey)
	delete(updated.Metadata, ExternalOwnerModeMetadataKey)
	delete(updated.Metadata, ExternalOwnerDirMetadataKey)
	return store.Save(ctx, updated)
}

func townRootFromBinding(binding *SessionBinding) string {
	if binding == nil || strings.TrimSpace(binding.WorkDir) == "" {
		return ""
	}
	if binding.RigName != "" {
		marker := "/" + binding.RigName + "/"
		if idx := strings.Index(binding.WorkDir, marker); idx >= 0 {
			return binding.WorkDir[:idx]
		}
	}
	parts := strings.Split(strings.TrimSuffix(binding.WorkDir, "/"), "/")
	if len(parts) > 1 {
		return strings.Join(parts[:len(parts)-1], "/")
	}
	return ""
}

func IsExternalOwnerBinding(binding *SessionBinding) bool {
	if binding == nil || binding.Metadata == nil {
		return false
	}
	return strings.TrimSpace(binding.Metadata[ExternalOwnerModeMetadataKey]) == ExternalOwnerModeQueue
}

func OwnerBindingMetadata(ownerDir string, ownerPID int) map[string]string {
	metadata := map[string]string{ExternalOwnerModeMetadataKey: ExternalOwnerModeQueue}
	if strings.TrimSpace(ownerDir) != "" {
		metadata[ExternalOwnerDirMetadataKey] = ownerDir
	}
	if ownerPID > 0 {
		metadata[ExternalOwnerPIDMetadataKey] = fmt.Sprintf("%d", ownerPID)
	}
	return metadata
}

func OwnerPIDFromMetadata(metadata map[string]string) int {
	if metadata == nil {
		return 0
	}
	value := strings.TrimSpace(metadata[ExternalOwnerPIDMetadataKey])
	if value == "" {
		return 0
	}
	pid, err := strconvAtoi(value)
	if err != nil {
		return 0
	}
	return pid
}

func OwnerDirFromBinding(townRoot string, binding *SessionBinding) string {
	if binding == nil {
		return ""
	}
	if binding.Metadata != nil {
		if value := strings.TrimSpace(binding.Metadata[ExternalOwnerDirMetadataKey]); value != "" {
			return value
		}
	}
	if strings.TrimSpace(townRoot) == "" {
		return ""
	}
	return ExternalOwnerDir(townRoot, binding.SessionName)
}

func AskExternalOwner(ctx context.Context, townRoot string, binding *SessionBinding, message string) (string, error) {
	request, err := enqueueExternalOwnerRequest(ctx, townRoot, binding, ExternalOwnerRequestKindAsk, message)
	if err != nil {
		return "", err
	}
	response, err := waitForExternalOwnerResponse(ctx, townRoot, binding, request.ID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(response.Error) != "" {
		return "", fmt.Errorf("owner ask failed: %s", strings.TrimSpace(response.Error))
	}
	return strings.TrimSpace(response.Content), nil
}

func SendExternalOwner(townRoot string, binding *SessionBinding, message string) error {
	_, err := enqueueExternalOwnerRequest(context.Background(), townRoot, binding, ExternalOwnerRequestKindSend, message)
	return err
}

func enqueueExternalOwnerRequest(ctx context.Context, townRoot string, binding *SessionBinding, kind, message string) (*ExternalCopilotOwnerRequest, error) {
	if binding == nil {
		return nil, fmt.Errorf("binding is required")
	}
	ownerDir := OwnerDirFromBinding(townRoot, binding)
	if strings.TrimSpace(ownerDir) == "" {
		return nil, fmt.Errorf("owner dir unavailable")
	}
	if err := os.MkdirAll(filepath.Join(ownerDir, "requests"), 0o755); err != nil {
		return nil, fmt.Errorf("creating owner request dir: %w", err)
	}
	request := &ExternalCopilotOwnerRequest{
		ID:        externalOwnerID("req"),
		Kind:      strings.TrimSpace(kind),
		Message:   strings.TrimSpace(message),
		CreatedAt: time.Now().UTC(),
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			request.TimeoutMS = remaining.Milliseconds()
		}
	}
	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding owner request: %w", err)
	}
	filename := fmt.Sprintf("%d-%s.json", request.CreatedAt.UnixNano(), request.ID)
	path := filepath.Join(ownerDir, "requests", filename)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return nil, fmt.Errorf("writing owner request: %w", err)
	}
	return request, nil
}

func waitForExternalOwnerResponse(ctx context.Context, townRoot string, binding *SessionBinding, requestID string) (*ExternalCopilotOwnerResponse, error) {
	ownerDir := OwnerDirFromBinding(townRoot, binding)
	if strings.TrimSpace(ownerDir) == "" {
		return nil, fmt.Errorf("owner dir unavailable")
	}
	responsePath := filepath.Join(ownerDir, "responses", requestID+".json")
	ticker := time.NewTicker(externalOwnerPollInterval)
	defer ticker.Stop()
	for {
		data, err := os.ReadFile(responsePath)
		if err == nil {
			var response ExternalCopilotOwnerResponse
			if err := json.Unmarshal(data, &response); err != nil {
				return nil, fmt.Errorf("decoding owner response: %w", err)
			}
			if strings.TrimSpace(response.Content) == "" && strings.TrimSpace(response.Error) == "" {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-ticker.C:
				}
				continue
			}
			_ = os.Remove(responsePath)
			return &response, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading owner response: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func NextExternalOwnerRequests(townRoot, sessionName string) ([]string, error) {
	entries, err := os.ReadDir(externalOwnerRequestsDir(townRoot, sessionName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading owner request dir: %w", err)
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		paths = append(paths, filepath.Join(externalOwnerRequestsDir(townRoot, sessionName), entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}

func ClaimExternalOwnerRequest(path string) (string, error) {
	claimPath := path + ".claimed-" + externalOwnerID("claim")
	if err := os.Rename(path, claimPath); err != nil {
		return "", err
	}
	return claimPath, nil
}

func ReadExternalOwnerRequest(path string) (*ExternalCopilotOwnerRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading owner request: %w", err)
	}
	var request ExternalCopilotOwnerRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, fmt.Errorf("decoding owner request: %w", err)
	}
	return &request, nil
}

func RemoveExternalOwnerRequest(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing owner request: %w", err)
	}
	return nil
}

func WriteExternalOwnerResponse(townRoot, sessionName string, response ExternalCopilotOwnerResponse) error {
	if err := os.MkdirAll(externalOwnerResponsesDir(townRoot, sessionName), 0o755); err != nil {
		return fmt.Errorf("creating owner response dir: %w", err)
	}
	if response.CreatedAt.IsZero() {
		response.CreatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding owner response: %w", err)
	}
	path := filepath.Join(externalOwnerResponsesDir(townRoot, sessionName), response.ID+".json")
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing owner response: %w", err)
	}
	return nil
}

func ClearExternalOwnerRequests(townRoot, sessionName string) error {
	paths, err := NextExternalOwnerRequests(townRoot, sessionName)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing owner request: %w", err)
		}
	}
	return nil
}

func ResetExternalOwnerState(townRoot, sessionName string) error {
	_ = StopExternalOwnerProcess(townRoot, sessionName)
	if err := DeleteExternalOwnerStatus(townRoot, sessionName); err != nil {
		return err
	}
	if err := ClearExternalOwnerRequests(townRoot, sessionName); err != nil {
		return err
	}
	if err := ClearExternalOwnerResponses(townRoot, sessionName); err != nil {
		return err
	}
	return nil
}

func LaunchExternalOwnerProcess(ctx context.Context, cfg ExternalCopilotOwnerConfig) (*ExternalCopilotOwnerStatus, error) {
	if err := WriteExternalOwnerConfig(cfg); err != nil {
		return nil, err
	}
	if err := ResetExternalOwnerState(cfg.TownRoot, cfg.SessionName); err != nil {
		return nil, err
	}
	gtBin, err := os.Executable()
	if err != nil {
		gtBin = "gt"
	}
	cmd := exec.Command(gtBin, "external-copilot-owner", "--config", ExternalOwnerConfigPath(cfg.TownRoot, cfg.SessionName))
	cmd.Dir = cfg.TownRoot
	cmd.Env = os.Environ()
	if err := os.MkdirAll(ExternalOwnerDir(cfg.TownRoot, cfg.SessionName), 0o755); err != nil {
		return nil, fmt.Errorf("creating owner dir: %w", err)
	}
	logFile, err := os.OpenFile(ExternalOwnerLogPath(cfg.TownRoot, cfg.SessionName), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	util.SetDetachedProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting external owner process: %w", err)
	}
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if err := WriteExternalOwnerStatus(cfg.TownRoot, cfg.SessionName, ExternalCopilotOwnerStatus{OwnerPID: pid, UpdatedAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return waitForExternalOwnerReady(ctx, cfg.TownRoot, cfg.SessionName, pid)
}

func waitForExternalOwnerReady(ctx context.Context, townRoot, sessionName string, pid int) (*ExternalCopilotOwnerStatus, error) {
	ticker := time.NewTicker(externalOwnerPollInterval)
	defer ticker.Stop()
	for {
		status, err := ReadExternalOwnerStatus(townRoot, sessionName)
		if err == nil {
			if strings.TrimSpace(status.Error) != "" {
				return nil, fmt.Errorf("%s", status.Error)
			}
			if status.OwnerPID == pid && strings.TrimSpace(status.RuntimeSessionID) != "" {
				return status, nil
			}
		}
		if pid > 0 && !isProcessRunning(pid) {
			return nil, fmt.Errorf("external owner process exited before becoming ready")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func externalOwnerID(prefix string) string {
	var bytes [4]byte
	_, _ = rand.Read(bytes[:])
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UnixNano(), hex.EncodeToString(bytes[:]))
}

func strconvAtoi(value string) (int, error) {
	return strconv.Atoi(value)
}
