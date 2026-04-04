package persistence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"flush-detector/internal/flush"
)

type State struct {
	Alerts []flush.Alert `json:"alerts"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load() (State, error) {
	var state State
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	err = json.Unmarshal(data, &state)
	return state, err
}

func (s *Store) SaveAlerts(alerts []flush.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := State{Alerts: alerts}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil && filepath.Dir(s.path) != "." {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
