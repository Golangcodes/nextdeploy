package daemon

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

type State struct {
	Ports map[string]int `json:"ports"` // appName -> port
}

type StateManager struct {
	path  string
	mu    sync.RWMutex
	state State
}

func NewStateManager(path string) *StateManager {
	sm := &StateManager{
		path: path,
		state: State{
			Ports: make(map[string]int),
		},
	}
	if err := sm.load(); err != nil {
		log.Printf("[state] Warning: failed to load state: %v", err)
	}
	return sm
}

func (sm *StateManager) load() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, err := os.Stat(sm.path); os.IsNotExist(err) {
		return nil
	}

	data, err := os.ReadFile(sm.path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, &sm.state)
}

func (sm *StateManager) Save() error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(sm.path), 0750); err != nil {
		return err
	}

	data, err := json.MarshalIndent(sm.state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(sm.path, data, 0600)
}

func (sm *StateManager) GetPort(appName string) int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.Ports[appName]
}

func (sm *StateManager) SetPort(appName string, port int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.state.Ports[appName] = port
}
