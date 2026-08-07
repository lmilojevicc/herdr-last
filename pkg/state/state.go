package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// State tracks workspace and tab focus history in a thread-safe manner.
type State struct {
	mu                  sync.RWMutex
	CurrentWorkspaceID  string            `json:"current_workspace_id"`
	PreviousWorkspaceID string            `json:"previous_workspace_id"`
	CurrentTabPerWS     map[string]string `json:"current_tab_per_ws"`
	PreviousTabPerWS    map[string]string `json:"previous_tab_per_ws"`
}

// New initializes an empty State instance.
func New() *State {
	return &State{
		CurrentTabPerWS:  make(map[string]string),
		PreviousTabPerWS: make(map[string]string),
	}
}

// GetStateFilePath resolves the path to state.json using HERDR_PLUGIN_STATE_DIR
// with a fallback to ~/.config/herdr-last/state.json.
func GetStateFilePath() string {
	if dir := os.Getenv("HERDR_PLUGIN_STATE_DIR"); dir != "" {
		return filepath.Join(dir, "state.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "herdr-last", "state.json")
}

// WithLock serializes a complete state transaction across plugin processes.
func WithLock(fn func() error) error {
	path := GetStateFilePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("failed to open state lock: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to lock state: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN) //nolint:errcheck

	return fn()
}

// Load reads the state from disk, returning a new state if the file does not exist.
func Load() (*State, error) {
	path := GetStateFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	s := New()
	if err := json.Unmarshal(data, s); err != nil {
		return New(), fmt.Errorf("failed to unmarshal state: %w", err)
	}
	if s.CurrentTabPerWS == nil {
		s.CurrentTabPerWS = make(map[string]string)
	}
	if s.PreviousTabPerWS == nil {
		s.PreviousTabPerWS = make(map[string]string)
	}
	return s, nil
}

// Save writes state to disk atomically using a temp file and rename.
func (s *State) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := GetStateFilePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to write state temp file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to close state temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to replace state file: %w", err)
	}
	return nil
}

// OnWorkspaceFocused updates workspace tracking state and returns true if state changed.
func (s *State) OnWorkspaceFocused(wsID string) bool {
	if wsID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.CurrentWorkspaceID == wsID {
		return false
	}
	s.PreviousWorkspaceID = s.CurrentWorkspaceID
	s.CurrentWorkspaceID = wsID
	return true
}

// OnTabFocused updates tab focus tracking for a workspace and returns true if state changed.
func (s *State) OnTabFocused(wsID, tabID string) bool {
	if wsID == "" || tabID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	if s.CurrentWorkspaceID != wsID {
		s.PreviousWorkspaceID = s.CurrentWorkspaceID
		s.CurrentWorkspaceID = wsID
		changed = true
	}

	if s.CurrentTabPerWS == nil {
		s.CurrentTabPerWS = make(map[string]string)
	}
	if s.PreviousTabPerWS == nil {
		s.PreviousTabPerWS = make(map[string]string)
	}

	currentTab := s.CurrentTabPerWS[wsID]
	if currentTab != tabID {
		if currentTab != "" {
			s.PreviousTabPerWS[wsID] = currentTab
		}
		s.CurrentTabPerWS[wsID] = tabID
		changed = true
	}
	return changed
}

// GetPreviousWorkspace returns the ID of the previously focused workspace.
func (s *State) GetPreviousWorkspace() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.PreviousWorkspaceID
}

// GetPreviousTab returns the ID of the previously focused tab for a given workspace.
func (s *State) GetPreviousTab(wsID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.PreviousTabPerWS[wsID]
}

// GetCurrentWorkspace returns the ID of the currently focused workspace.
func (s *State) GetCurrentWorkspace() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentWorkspaceID
}
