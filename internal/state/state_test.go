package state

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PAumedes/polld/internal/source"
)

func ev(id, fp string) source.Event {
	return source.Event{ID: id, Fingerprint: fp, OccurredAt: time.Now()}
}

func TestUnseenOnEmptyStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	fresh := s.Unseen("gitlab", []source.Event{ev("a", ""), ev("b", "v1")})
	if len(fresh) != 2 {
		t.Fatalf("Unseen returned %d events, want 2", len(fresh))
	}
	if s.Initialized("gitlab") {
		t.Error("source should not be initialized")
	}
}

func TestMarkFiltersSeenAndDetectsFingerprintChange(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Mark("gitlab", "a", "")
	s.Mark("gitlab", "b", "v1")

	fresh := s.Unseen("gitlab", []source.Event{ev("a", ""), ev("b", "v1"), ev("c", "")})
	if len(fresh) != 1 || fresh[0].ID != "c" {
		t.Fatalf("Unseen = %v, want only c", fresh)
	}

	fresh = s.Unseen("gitlab", []source.Event{ev("b", "v2")})
	if len(fresh) != 1 {
		t.Fatalf("fingerprint change should re-report: got %v", fresh)
	}
}

func TestStateIsPerSource(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Mark("a", "x", "")
	if fresh := s.Unseen("b", []source.Event{ev("x", "")}); len(fresh) != 1 {
		t.Fatal("same ID in a different source should be unseen")
	}
}

func TestSaveAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "dir", "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Mark("gitlab", "a", "v1")
	s.MarkInitialized("gitlab")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s2.Initialized("gitlab") {
		t.Error("initialized flag not persisted")
	}
	if fresh := s2.Unseen("gitlab", []source.Event{ev("a", "v1")}); len(fresh) != 0 {
		t.Error("seen event re-reported after reload")
	}
	if fresh := s2.Unseen("gitlab", []source.Event{ev("a", "v2")}); len(fresh) != 1 {
		t.Error("changed fingerprint not detected after reload")
	}
}

func TestCorruptFileBackedUpNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("corrupt state should not be fatal: %v", err)
	}
	if fresh := s.Unseen("a", []source.Event{ev("x", "")}); len(fresh) != 1 {
		t.Error("store should be empty after corruption")
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Errorf("corrupt file not preserved for inspection")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxPerSource+10; i++ {
		s.MarkAt("a", id(i), "", time.Now().Add(time.Duration(i)*time.Second))
	}
	if n := s.count("a"); n > maxPerSource {
		t.Errorf("count = %d, want <= %d", n, maxPerSource)
	}
	if _, ok := s.get("a", id(0)); ok {
		t.Error("oldest entry should have been pruned")
	}
	if _, ok := s.get("a", id(maxPerSource+9)); !ok {
		t.Error("newest entry should have been kept")
	}
}

func id(i int) string {
	return fmt.Sprintf("id-%04d", i)
}
