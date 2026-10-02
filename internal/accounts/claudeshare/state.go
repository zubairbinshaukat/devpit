package claudeshare

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// stateFileName is Devpit's record of what an account shares, kept in the
// account's own folder so it travels with it. It holds names and paths
// only. It is on the deny-list, so it is never shared itself.
const stateFileName = ".devpit-share.json"

// State is what an account shares with the default account.
type State struct {
	Version int `json:"version"`
	// Source is the default home the links point into.
	Source string `json:"source,omitempty"`
	// Shared lists the rows shared by link or import.
	Shared []Kind `json:"shared,omitempty"`
	// Local lists skills this account keeps as its own on purpose; the
	// launch-time EnsureSkillLinks never links over them.
	Local []string `json:"local,omitempty"`
	// Copied lists what Devpit copied ("skills/foo", "settings"), so a
	// later difference is shown as "changed since the copy".
	Copied []string `json:"copied,omitempty"`
}

func statePath(target string) string { return filepath.Join(target, stateFileName) }

// readState reads the account's record. A missing file is an empty record.
func readState(target string) (State, error) {
	b, err := readFile(statePath(target))
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: 1}, nil
	}
	if err != nil {
		return State{Version: 1}, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{Version: 1}, fmt.Errorf("Devpit's sharing record %s could not be read: %w", statePath(target), err) //nolint:revive,staticcheck // a product name
	}
	if s.Version == 0 {
		s.Version = 1
	}
	return s, nil
}

func (s State) clone() State {
	s.Shared = slices.Clone(s.Shared)
	s.Local = slices.Clone(s.Local)
	s.Copied = slices.Clone(s.Copied)
	return s
}

// encode is the file's bytes: sorted, so equal records are equal bytes.
func (s State) encode() []byte {
	c := s.clone()
	c.Version = 1
	slices.Sort(c.Shared)
	c.Shared = slices.Compact(c.Shared)
	sortFoldUnique(&c.Local)
	sortFoldUnique(&c.Copied)
	b, _ := json.MarshalIndent(c, "", "  ")
	return append(b, '\n')
}

func sortFoldUnique(v *[]string) {
	slices.SortFunc(*v, func(a, b string) int { return strings.Compare(strings.ToUpper(a), strings.ToUpper(b)) })
	*v = slices.CompactFunc(*v, strings.EqualFold)
}

func (s State) shares(k Kind) bool { return slices.Contains(s.Shared, k) }

func (s *State) setShared(k Kind, on bool) {
	s.Shared = slices.DeleteFunc(s.Shared, func(x Kind) bool { return x == k })
	if on {
		s.Shared = append(s.Shared, k)
	}
}

func (s State) isLocal(name string) bool {
	return slices.ContainsFunc(s.Local, func(x string) bool { return strings.EqualFold(x, name) })
}

func (s *State) setLocal(name string, on bool) {
	s.Local = slices.DeleteFunc(s.Local, func(x string) bool { return strings.EqualFold(x, name) })
	if on {
		s.Local = append(s.Local, name)
	}
}

func (s State) wasCopied(key string) bool {
	return slices.ContainsFunc(s.Copied, func(x string) bool { return strings.EqualFold(x, key) })
}

func (s *State) setCopied(key string, on bool) {
	s.Copied = slices.DeleteFunc(s.Copied, func(x string) bool { return strings.EqualFold(x, key) })
	if on {
		s.Copied = append(s.Copied, key)
	}
}
