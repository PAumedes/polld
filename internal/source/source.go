// Package source defines the polling abstraction every service adapter
// implements, plus the registry that wires config types to adapters.
//
// Extensibility model (see docs/adr/0001): adapters live in subpackages,
// register a Factory under a type name in init(), and are linked in by a
// blank import in internal/source/all. Adding support for a new service
// means adding one subpackage — nothing in the core changes.
package source

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/PAumedes/polld/internal/config"
)

// Event is one notification-worthy change observed by a source.
//
// ID must be stable across polls for the same underlying item (e.g. a merge
// request's project-scoped IID). Fingerprint is an optional change token: when
// it differs from the stored value the item is re-reported even if the ID was
// seen (e.g. an MR's updated_at). Events that are inherently unique — like
// GitLab project events — leave Fingerprint empty and dedup on ID alone.
type Event struct {
	ID          string
	Fingerprint string
	Kind        string
	Title       string
	Body        string
	Link        string
	OccurredAt  time.Time
}

// Source polls one service and returns the current candidate events.
// Implementations must be safe for a single poller goroutine at a time;
// different sources may be polled concurrently.
type Source interface {
	// Name identifies this source in logs, state, and notifications.
	Name() string
	// Poll fetches the latest items. It must honor ctx cancellation.
	Poll(ctx context.Context) ([]Event, error)
}

// Factory builds a Source from its configuration. httpClient is the shared,
// timeout-configured client owned by the daemon; factories may wrap it with
// per-source transports but must not mutate it.
type Factory func(cfg config.SourceConfig, httpClient *http.Client) (Source, error)

var (
	registryMu sync.RWMutex
	registry   = map[string]Factory{}
)

// Register adds an adapter under its type name. Called from adapter init()
// functions. Panics on duplicate registration — a programmer error caught
// at startup, not a runtime condition.
func Register(typ string, f Factory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[typ]; exists {
		panic(fmt.Sprintf("source type %q registered twice", typ))
	}
	registry[typ] = f
}

// New builds the Source for a config entry, or an error naming the unknown
// type and listing the registered ones.
func New(cfg config.SourceConfig, httpClient *http.Client) (Source, error) {
	registryMu.RLock()
	f, ok := registry[cfg.Type]
	registryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source %q: unknown type %q (registered: %v)", cfg.Name, cfg.Type, Types())
	}
	src, err := f(cfg, httpClient)
	if err != nil {
		return nil, fmt.Errorf("source %q: %w", cfg.Name, err)
	}
	return src, nil
}

// Types returns the registered adapter type names, sorted.
func Types() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for t := range registry {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
