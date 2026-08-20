package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SlotEntry is the persisted recency record for one slot.
type SlotEntry struct {
	LastUsed time.Time `json:"last_used"`
}

// State is the exact on-disk shape of .bare/wt.json.
type State struct {
	Version int                  `json:"version"`
	Slots   map[string]SlotEntry `json:"slots"`
}

// LoadState reads .bare/wt.json. A missing file is not an error — it just means no slot
// has recorded recency yet. Slot names in the file that no longer correspond to a real
// worktree are simply never looked up again; callers tolerate them by construction.
func LoadState(root string) (*State, error) {
	path := filepath.Join(root, ".bare", "wt.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{Version: 1, Slots: map[string]SlotEntry{}}, nil
		}
		return nil, fmt.Errorf("reading state file: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	if s.Slots == nil {
		s.Slots = map[string]SlotEntry{}
	}

	return &s, nil
}

// SaveState writes .bare/wt.json atomically: a temp file is written first, then renamed
// over the target, so a crash mid-write never leaves a corrupt state file.
func SaveState(root string, s *State) error {
	path := filepath.Join(root, ".bare", "wt.json")
	tmpPath := path + ".tmp"

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state file: %w", err)
	}

	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("finalizing state file: %w", err)
	}

	return nil
}
