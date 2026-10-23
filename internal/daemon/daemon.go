// Package daemon owns the run loop: one ticker goroutine per source, each
// cycle doing poll → diff against seen state → notify → persist.
//
// Ordering and failure semantics, per docs/adr/0004:
//   - First successful poll per source seeds the seen-set silently, so a fresh
//     install does not blast a backlog of notifications.
//   - Events are marked seen only after their notification is delivered, so a
//     failed notify retries next cycle.
//   - A source that keeps failing raises exactly one notification once it
//     crosses the failure threshold, and resets on recovery.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/PAumedes/polld/internal/config"
	"github.com/PAumedes/polld/internal/notify"
	"github.com/PAumedes/polld/internal/source"
	"github.com/PAumedes/polld/internal/state"
)

const (
	// defaultFailAfter is how many consecutive poll failures trigger a
	// "source unhealthy" notification.
	defaultFailAfter = 5
	httpTimeout      = 20 * time.Second
	// notifyTimeout bounds a single notification delivery: D-Bus service
	// activation can block for ~60s when the notification daemon is broken,
	// which must not stall the poll loop.
	notifyTimeout = 10 * time.Second
)

// runner pairs a source with its schedule and its health bookkeeping.
type runner struct {
	src      source.Source
	interval time.Duration

	mu       sync.Mutex
	failures int
	alerted  bool
}

// healthy reports whether the runner's last poll succeeded.
func (r *runner) healthy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failures == 0
}

// Daemon wires sources, dedup state, and the notifier together.
type Daemon struct {
	runners   []*runner
	store     *state.Store
	notifier  notify.Notifier
	template  notify.Notification
	logger    *slog.Logger
	failAfter int
}

// New builds sources for every config entry and prepares the run loop.
// Shared HTTP client: one connection pool for all adapters.
func New(
	cfg *config.Config,
	store *state.Store,
	notifier notify.Notifier,
	logger *slog.Logger,
) (*Daemon, error) {
	httpClient := &http.Client{Timeout: httpTimeout}
	d := &Daemon{
		store:     store,
		notifier:  notifier,
		logger:    logger,
		failAfter: defaultFailAfter,
		template: notify.Notification{
			AppName: cfg.Notifications.AppName,
			Urgency: cfg.Notifications.Urgency,
			Icon:    cfg.Notifications.Icon,
		},
	}
	for _, sc := range cfg.Sources {
		src, err := source.New(sc, httpClient)
		if err != nil {
			return nil, err
		}
		d.runners = append(d.runners, &runner{
			src:      src,
			interval: sc.IntervalOr(cfg.PollInterval.Duration),
		})
	}
	if len(d.runners) == 0 {
		return nil, errors.New("no sources configured")
	}
	return d, nil
}

// Run starts the per-source loops and blocks until ctx is cancelled, then
// persists state. It is the systemd-managed foreground mode.
func (d *Daemon) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, r := range d.runners {
		wg.Add(1)
		go func(r *runner) {
			defer wg.Done()
			d.loop(ctx, r)
		}(r)
	}
	<-ctx.Done()
	wg.Wait()
	return d.store.Save()
}

// RunOnce polls every source once and returns — used by -once for debugging
// and for timer-based deployments.
func (d *Daemon) RunOnce(ctx context.Context) error {
	var wg sync.WaitGroup
	errs := make([]error, len(d.runners))
	for i, r := range d.runners {
		wg.Add(1)
		go func(i int, r *runner) {
			defer wg.Done()
			d.poll(ctx, r)
			errs[i] = r.lastErr()
		}(i, r)
	}
	wg.Wait()
	if err := d.store.Save(); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}
	return errors.Join(errs...)
}

// loop polls immediately, then on every tick, until ctx is done.
func (d *Daemon) loop(ctx context.Context, r *runner) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	d.poll(ctx, r)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.poll(ctx, r)
		}
	}
}

// poll is one cycle for one source: fetch, diff, notify, record.
func (d *Daemon) poll(ctx context.Context, r *runner) {
	events, err := r.src.Poll(ctx)
	if err != nil {
		d.recordFailure(r, err)
		return
	}
	r.recordSuccess()
	name := r.src.Name()

	if !d.store.Initialized(name) {
		d.seed(name, events)
		return
	}
	fresh := d.store.Unseen(name, events)
	if len(fresh) == 0 {
		return
	}
	d.notifyFresh(name, fresh)
	if err := d.store.Save(); err != nil {
		d.logger.Warn("saving state", "source", name, "err", err)
	}
}

// seed marks a first poll's events as seen without notifying — the install
// moment is not a change the user needs to hear about one item at a time.
func (d *Daemon) seed(name string, events []source.Event) {
	for _, e := range events {
		d.store.Mark(name, e.ID, e.Fingerprint)
	}
	d.store.MarkInitialized(name)
	d.logger.Info("seeded source", "source", name, "events", len(events))
}

// notifyFresh delivers notifications oldest-first so a batch arrives in
// chronological order. Each event is marked only after its notification is
// delivered.
func (d *Daemon) notifyFresh(sourceName string, events []source.Event) {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		n := eventNotification(sourceName, e, d.template)
		ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
		err := d.notifier.Notify(ctx, n)
		cancel()
		if err != nil {
			d.logger.Warn("notify failed", "source", sourceName, "event", e.ID, "err", err)
			return // remaining events stay unseen; retried next cycle
		}
		d.store.Mark(sourceName, e.ID, e.Fingerprint)
	}
}

// eventNotification maps an event onto a desktop notification. The source
// name prefixes the summary so the user can tell sources apart at a glance.
func eventNotification(sourceName string, e source.Event, template notify.Notification) notify.Notification {
	n := template
	title := e.Title
	if title == "" {
		title = e.ID
	}
	n.Summary = fmt.Sprintf("[%s] %s", sourceName, title)
	n.Body = e.Body
	n.Link = e.Link
	return n
}

func (d *Daemon) recordFailure(r *runner, err error) {
	r.mu.Lock()
	r.failures++
	shouldAlert := r.failures >= d.failAfter && !r.alerted
	if shouldAlert {
		r.alerted = true
	}
	r.mu.Unlock()

	d.logger.Warn("poll failed", "source", r.src.Name(), "failures", r.failures, "err", err)
	if shouldAlert {
		n := d.template
		n.Summary = fmt.Sprintf("polld: source %q failing", r.src.Name())
		n.Body = err.Error()
		n.Urgency = "critical"
		nctx, ncancel := context.WithTimeout(context.Background(), notifyTimeout)
		nerr := d.notifier.Notify(nctx, n)
		ncancel()
		if nerr != nil {
			d.logger.Warn("failure alert could not be delivered", "err", nerr)
		}
	}
}

func (r *runner) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = 0
	r.alerted = false
}

func (r *runner) lastErr() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures > 0 {
		return fmt.Errorf("%d consecutive poll failures", r.failures)
	}
	return nil
}
