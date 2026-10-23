// Package state persists which events have already been reported, so the
// daemon notifies each change exactly once across restarts.
//
// The store is a single JSON document written atomically (temp file + rename).
// A corrupt file is renamed aside and the store starts empty rather than
// failing the daemon — losing dedup history causes one duplicate notification
// per item, while failing to start loses every notification.
//
// See docs/adr/0004.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/PAumedes/polld/internal/source"
)

// maxPerSource bounds the state file: a noisy source can't grow it forever.
const maxPerSource = 500

type entry struct {
	Fingerprint string    `json:"fingerprint,omitempty"`
	At          time.Time `json:"at"`
}

type fileData struct {
	Sources     map[string]map[string]entry `json:"sources"`
	Initialized map[string]bool             `json:"initialized,omitempty"`
}

// Store is the dedup ledger. All methods are safe for concurrent use.
type Store struct {
	path string
	now  func() time.Time

	mu   sync.Mutex
	data fileData
}

// Open loads the state file at path. A missing file yields an empty store;
// a corrupt file is moved to path+".corrupt-<timestamp>" and the store
// starts empty.
func Open(path string) (*Store, error) {
	s := &Store{path: path, now: time.Now}
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		s.data = emptyData()
	case err != nil:
		return nil, fmt.Errorf("reading state file: %w", err)
	default:
		if err := json.Unmarshal(data, &s.data); err != nil {
			backup := fmt.Sprintf("%s.corrupt-%d", path, s.now().Unix())
			if rerr := os.Rename(path, backup); rerr != nil {
				return nil, fmt.Errorf("state file corrupt (%w) and could not be moved aside: %w", err, rerr)
			}
			s.data = emptyData()
		}
	}
	if s.data.Sources == nil {
		s.data.Sources = map[string]map[string]entry{}
	}
	if s.data.Initialized == nil {
		s.data.Initialized = map[string]bool{}
	}
	return s, nil
}

func emptyData() fileData {
	return fileData{
		Sources:     map[string]map[string]entry{},
		Initialized: map[string]bool{},
	}
}

// Initialized reports whether the source has completed a first poll. The
// daemon uses this to seed history silently instead of notifying a burst of
// backlog events on first run.
func (s *Store) Initialized(sourceName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Initialized[sourceName]
}

// MarkInitialized records that the source's first poll completed.
func (s *Store) MarkInitialized(sourceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Initialized[sourceName] = true
}

// Unseen returns the events the store does not yet know — new IDs, or known
// IDs whose fingerprint changed. It does not mark them; callers mark after
// the notification actually goes out, so a failed notify retries next cycle.
func (s *Store) Unseen(sourceName string, events []source.Event) []source.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := s.data.Sources[sourceName]
	var fresh []source.Event
	for _, e := range events {
		prev, ok := seen[e.ID]
		if !ok || (e.Fingerprint != "" && e.Fingerprint != prev.Fingerprint) {
			fresh = append(fresh, e)
		}
	}
	return fresh
}

// Mark records an event as reported, stamped with the current time.
func (s *Store) Mark(sourceName, id, fingerprint string) {
	s.MarkAt(sourceName, id, fingerprint, s.now())
}

// MarkAt is Mark with an explicit timestamp; exists for tests.
func (s *Store) MarkAt(sourceName, id, fingerprint string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Sources[sourceName] == nil {
		s.data.Sources[sourceName] = map[string]entry{}
	}
	s.data.Sources[sourceName][id] = entry{Fingerprint: fingerprint, At: at}
	s.pruneLocked(sourceName)
}

// pruneLocked drops the oldest entries beyond maxPerSource.
func (s *Store) pruneLocked(sourceName string) {
	entries := s.data.Sources[sourceName]
	if len(entries) <= maxPerSource {
		return
	}
	type kv struct {
		id string
		at time.Time
	}
	ordered := make([]kv, 0, len(entries))
	for id, e := range entries {
		ordered = append(ordered, kv{id, e.At})
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].at.Before(ordered[j].at) })
	for _, old := range ordered[:len(ordered)-maxPerSource] {
		delete(entries, old.id)
	}
}

// Save writes the store atomically: a temp file in the same directory, then a
// rename, so a crash mid-write can never leave a truncated state file.
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".polld-state-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// count and get expose internals to tests in this package.
func (s *Store) count(sourceName string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data.Sources[sourceName])
}

func (s *Store) get(sourceName, id string) (entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data.Sources[sourceName][id]
	return e, ok
}
