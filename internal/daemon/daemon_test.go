package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PAumedes/polld/internal/notify"
	"github.com/PAumedes/polld/internal/source"
	"github.com/PAumedes/polld/internal/state"
)

// fakeSource returns scripted results; each call pops the next script entry.
type fakeSource struct {
	name    string
	mu      sync.Mutex
	script  [][]source.Event
	errs    []error
	calls   int
	callSig chan struct{}
}

func (f *fakeSource) Name() string { return f.name }

func (f *fakeSource) Poll(context.Context) ([]source.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	f.calls++
	if f.callSig != nil {
		select {
		case f.callSig <- struct{}{}:
		default:
		}
	}
	var events []source.Event
	if i < len(f.script) {
		events = f.script[i]
	}
	var err error
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return events, err
}

type fakeNotifier struct {
	mu   sync.Mutex
	sent []notify.Notification
	err  error
	sig  chan struct{}
}

func (f *fakeNotifier) Notify(_ context.Context, n notify.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, n)
	if f.sig != nil {
		select {
		case f.sig <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeNotifier) Close() error { return nil }

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func newDaemon(t *testing.T, srcs []source.Source, n *fakeNotifier, interval time.Duration) (*Daemon, *state.Store) {
	t.Helper()
	store, err := state.Open(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{
		store:     store,
		notifier:  n,
		logger:    testLogger(),
		failAfter: 3,
		template:  notify.Notification{AppName: "polld", Urgency: "normal"},
	}
	for _, s := range srcs {
		d.runners = append(d.runners, &runner{src: s, interval: interval})
	}
	return d, store
}

func TestFirstPollSeedsSilently(t *testing.T) {
	src := &fakeSource{name: "a", script: [][]source.Event{
		{{ID: "e1"}, {ID: "e2"}},
	}}
	n := &fakeNotifier{}
	d, store := newDaemon(t, []source.Source{src}, n, time.Minute)

	d.poll(context.Background(), d.runners[0])
	if n.count() != 0 {
		t.Fatalf("first poll should seed silently, sent %d", n.count())
	}
	if !store.Initialized("a") {
		t.Error("source should be marked initialized")
	}
	if got := store.Unseen("a", []source.Event{{ID: "e1"}}); len(got) != 0 {
		t.Error("seeded events should be marked seen")
	}
}

func TestSecondPollNotifies(t *testing.T) {
	src := &fakeSource{name: "a", script: [][]source.Event{
		{{ID: "e1"}},
		{{ID: "e1"}, {ID: "e2"}, {ID: "e3"}},
	}}
	n := &fakeNotifier{}
	d, _ := newDaemon(t, []source.Source{src}, n, time.Minute)

	d.poll(context.Background(), d.runners[0]) // seed
	d.poll(context.Background(), d.runners[0]) // e2, e3 are new
	if n.count() != 2 {
		t.Fatalf("sent %d, want 2", n.count())
	}
	if n.sent[0].Summary == "" || n.sent[0].AppName == "" {
		t.Error("notification should carry summary and app name")
	}
}

func TestFingerprintChangeRenotifies(t *testing.T) {
	src := &fakeSource{name: "a", script: [][]source.Event{
		{{ID: "mr-1", Fingerprint: "v1"}},
		{{ID: "mr-1", Fingerprint: "v2"}},
	}}
	n := &fakeNotifier{}
	d, _ := newDaemon(t, []source.Source{src}, n, time.Minute)
	d.poll(context.Background(), d.runners[0])
	d.poll(context.Background(), d.runners[0])
	if n.count() != 1 {
		t.Fatalf("sent %d, want 1", n.count())
	}
}

func TestFailedNotifyRetriesNextCycle(t *testing.T) {
	src := &fakeSource{name: "a", script: [][]source.Event{
		{},
		{{ID: "e1"}},
		{{ID: "e1"}}, // sources re-emit current items; e1 was never marked seen
	}}
	n := &fakeNotifier{err: errors.New("dbus down")}
	d, _ := newDaemon(t, []source.Source{src}, n, time.Minute)
	d.poll(context.Background(), d.runners[0]) // seeds
	d.poll(context.Background(), d.runners[0]) // e1 fresh, notify fails
	if n.count() != 0 {
		t.Fatalf("notify failed → should not count as sent, got %d", n.count())
	}
	n.err = nil
	d.poll(context.Background(), d.runners[0]) // e1 still unseen → retried
	if n.count() != 1 {
		t.Fatalf("e1 should be retried, sent %d", n.count())
	}
}

func TestSourceErrorThresholdNotifiesOnce(t *testing.T) {
	src := &fakeSource{name: "a", errs: []error{
		errors.New("x"), errors.New("x"), errors.New("x"), errors.New("x"),
	}}
	n := &fakeNotifier{}
	d, _ := newDaemon(t, []source.Source{src}, n, time.Minute)
	for i := 0; i < 4; i++ {
		d.poll(context.Background(), d.runners[0])
	}
	// failAfter=3: polls 3 triggers one alert; poll 4 must not duplicate it.
	if n.count() != 1 {
		t.Fatalf("sent %d failure alerts, want exactly 1", n.count())
	}
	if n.sent[0].Summary == "" {
		t.Error("failure alert has no summary")
	}
}

func TestRecoveryClearsFailureAlert(t *testing.T) {
	src := &fakeSource{name: "a",
		errs:   []error{errors.New("x"), errors.New("x"), errors.New("x"), nil},
		script: [][]source.Event{nil, nil, nil, {}},
	}
	n := &fakeNotifier{}
	d, _ := newDaemon(t, []source.Source{src}, n, time.Minute)
	for i := 0; i < 4; i++ {
		d.poll(context.Background(), d.runners[0])
	}
	if n.count() != 1 {
		t.Fatalf("sent %d, want 1 (failure alert only)", n.count())
	}
	if !d.runners[0].healthy() {
		t.Error("runner should be healthy after a successful poll")
	}
}

func TestRunOnceConcurrentAndOrdered(t *testing.T) {
	srcA := &fakeSource{name: "a", script: [][]source.Event{nil, {{ID: "a1", Title: "A1", OccurredAt: time.Now()}}}}
	srcB := &fakeSource{name: "b", script: [][]source.Event{nil, {{ID: "b1", Title: "B1"}}}}
	n := &fakeNotifier{}
	d, _ := newDaemon(t, []source.Source{srcA, srcB}, n, time.Minute)

	// First RunOnce seeds.
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second RunOnce notifies.
	if err := d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.count() != 2 {
		t.Fatalf("sent %d, want 2", n.count())
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	src := &fakeSource{name: "a", callSig: make(chan struct{}, 10)}
	n := &fakeNotifier{}
	d, store := newDaemon(t, []source.Source{src}, n, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()

	select {
	case <-src.callSig:
	case <-time.After(2 * time.Second):
		t.Fatal("expected at least one poll")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if !store.Initialized("a") {
		t.Error("state should be saved on shutdown")
	}
}

func TestEventToNotification(t *testing.T) {
	e := source.Event{
		Kind:  "merge_request",
		Title: "MR !1 opened: X",
		Body:  "ada",
		Link:  "https://x",
	}
	n := eventNotification("src", e, notify.Notification{AppName: "polld", Urgency: "normal"})
	if n.Summary != "[src] MR !1 opened: X" || n.Body != "ada" || n.Link != "https://x" {
		t.Errorf("notification = %+v", n)
	}
}

func TestEventToNotificationFallsBackToID(t *testing.T) {
	e := source.Event{ID: "evt-99"}
	n := eventNotification("src", e, notify.Notification{})
	if n.Summary != "[src] evt-99" {
		t.Errorf("Summary = %q", n.Summary)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
