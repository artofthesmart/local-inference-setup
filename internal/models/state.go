package models

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Qualified is a model that passed every check on this computer.
type Qualified struct {
	Tag     string    `json:"tag"`
	Tested  time.Time `json:"tested"`
	Context int       `json:"context"`
}

// State is what copilot-local remembers between runs. Other tools (VS Code and Desktop setup) read the same file.
type State struct {
	LastUsed string      `json:"last_used,omitempty"`
	Models   []Qualified `json:"models"`
}

// StateFile is the file name inside the settings folder.
const StateFile = "models.json"

// LoadState reads the state file; a missing file is an empty state.
func LoadState(dir string) (State, error) {
	var s State
	b, err := os.ReadFile(filepath.Join(dir, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, errors.New("the saved model list is damaged; delete " + filepath.Join(dir, StateFile) + " and run again")
	}
	return s, nil
}

// Save writes the state file.
func (s State) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := filepath.Join(dir, StateFile+".tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, StateFile))
}

// Add records a model that passed, replacing any older result.
func (s *State) Add(q Qualified) {
	s.Remove(q.Tag)
	s.Models = append(s.Models, q)
}

// Remove forgets a model.
func (s *State) Remove(tag string) {
	s.Models = slices.DeleteFunc(s.Models, func(m Qualified) bool { return m.Tag == tag })
	if s.LastUsed == tag {
		s.LastUsed = ""
	}
}

// Get finds a qualified model.
func (s State) Get(tag string) (Qualified, bool) {
	for _, m := range s.Models {
		if m.Tag == tag {
			return m, true
		}
	}
	return Qualified{}, false
}

// Prune forgets models that are no longer downloaded. It reports whether anything changed.
func (s *State) Prune(installed []string) bool {
	before := len(s.Models)
	for _, m := range slices.Clone(s.Models) {
		if !slices.Contains(installed, m.Tag) {
			s.Remove(m.Tag)
		}
	}
	return len(s.Models) != before
}

// Ordered lists qualified models with the last-used one first, then newest tested first.
func (s State) Ordered() []Qualified {
	out := slices.Clone(s.Models)
	slices.SortStableFunc(out, func(a, b Qualified) int {
		switch {
		case a.Tag == s.LastUsed:
			return -1
		case b.Tag == s.LastUsed:
			return 1
		}
		return b.Tested.Compare(a.Tested)
	})
	return out
}
