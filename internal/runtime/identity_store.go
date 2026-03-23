package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SessionBinding struct {
	IssueID          string            `json:"issue_id"`
	Role             string            `json:"role"`
	RigName          string            `json:"rig_name,omitempty"`
	AgentName        string            `json:"agent_name,omitempty"`
	Provider         string            `json:"provider,omitempty"`
	SessionName      string            `json:"session_name"`
	RuntimeSessionID string            `json:"runtime_session_id,omitempty"`
	WorkDir          string            `json:"work_dir,omitempty"`
	LifecycleState   string            `json:"lifecycle_state,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

const (
	SessionLifecycleStarting = "starting"
	SessionLifecycleRunning  = "running"
	SessionLifecycleStopping = "stopping"
	SessionLifecycleStopped  = "stopped"
	SessionLifecycleUnknown  = "unknown"

	SessionLifecycleStartingGrace = 5 * time.Second
)

type SessionBindingStore interface {
	Save(ctx context.Context, binding SessionBinding) error
	Load(ctx context.Context, issueID, role, rigName, agentName string) (*SessionBinding, error)
	List(ctx context.Context, role, rigName string) ([]SessionBinding, error)
	Delete(ctx context.Context, issueID, role, rigName, agentName string) error
}

type FileSessionBindingStore struct {
	rootDir string
}

func NewFileSessionBindingStore(townRoot string) *FileSessionBindingStore {
	return &FileSessionBindingStore{rootDir: filepath.Join(townRoot, ".runtime", "session-bindings")}
}

func (s *FileSessionBindingStore) Save(_ context.Context, binding SessionBinding) error {
	if binding.Role == "" {
		return fmt.Errorf("role is required")
	}
	if binding.SessionName == "" {
		return fmt.Errorf("session name is required")
	}
	if err := os.MkdirAll(s.rootDir, 0755); err != nil {
		return fmt.Errorf("creating binding directory: %w", err)
	}
	if binding.UpdatedAt.IsZero() {
		binding.UpdatedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(binding, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding binding: %w", err)
	}
	if err := os.WriteFile(s.filePath(binding.IssueID, binding.Role, binding.RigName, binding.AgentName), append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("writing binding: %w", err)
	}
	return nil
}

func (s *FileSessionBindingStore) Load(_ context.Context, issueID, role, rigName, agentName string) (*SessionBinding, error) {
	if issueID == "" || rigName == "" || agentName == "" {
		return s.scan(issueID, role, rigName, agentName)
	}
	binding, err := s.readBinding(s.filePath(issueID, role, rigName, agentName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &binding, nil
}

func (s *FileSessionBindingStore) List(_ context.Context, role, rigName string) ([]SessionBinding, error) {
	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading binding directory: %w", err)
	}
	bindings := make([]SessionBinding, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		binding, err := s.readBinding(filepath.Join(s.rootDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if role != "" && binding.Role != role {
			continue
		}
		if rigName != "" && binding.RigName != rigName {
			continue
		}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].UpdatedAt.After(bindings[j].UpdatedAt)
	})
	return bindings, nil
}

func (s *FileSessionBindingStore) scan(issueID, role, rigName, agentName string) (*SessionBinding, error) {
	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading binding directory: %w", err)
	}
	matches := make([]SessionBinding, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		binding, err := s.readBinding(filepath.Join(s.rootDir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if issueID != "" && binding.IssueID != issueID {
			continue
		}
		if role != "" && binding.Role != role {
			continue
		}
		if rigName != "" && binding.RigName != rigName {
			continue
		}
		if agentName != "" && binding.AgentName != agentName {
			continue
		}
		matches = append(matches, binding)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].UpdatedAt.After(matches[j].UpdatedAt)
	})
	selected := matches[0]
	return &selected, nil
}

func (s *FileSessionBindingStore) Delete(_ context.Context, issueID, role, rigName, agentName string) error {
	if err := os.Remove(s.filePath(issueID, role, rigName, agentName)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing binding: %w", err)
	}
	return nil
}

func (s *FileSessionBindingStore) readBinding(path string) (SessionBinding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SessionBinding{}, fmt.Errorf("reading binding: %w", err)
	}
	var binding SessionBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		return SessionBinding{}, fmt.Errorf("decoding binding: %w", err)
	}
	return binding, nil
}

func (s *FileSessionBindingStore) filePath(issueID, role, rigName, agentName string) string {
	return filepath.Join(s.rootDir, bindingKey(issueID, role, rigName, agentName)+".json")
}

func bindingKey(issueID, role, rigName, agentName string) string {
	parts := []string{sanitizeBindingPart(issueID), sanitizeBindingPart(role)}
	if rigName != "" {
		parts = append(parts, sanitizeBindingPart(rigName))
	}
	if agentName != "" {
		parts = append(parts, sanitizeBindingPart(agentName))
	}
	return strings.Join(parts, "--")
}

func sanitizeBindingPart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "none"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "@", "-", ".", "-")
	value = replacer.Replace(value)
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	return strings.Trim(value, "-")
}
