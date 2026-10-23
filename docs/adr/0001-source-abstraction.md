# ADR 0001: Source abstraction and adapter registry

## Status

Accepted (2026-10-23)

## Context

polld must support GitLab today and arbitrary private services tomorrow —
including services we haven't seen yet — without the core loop knowing or
caring which services exist. Classic OO design would reach for a `Source`
base class; Go's answer is the small implicit interface plus a registry.

## Decision

```go
type Source interface {
    Name() string
    Poll(ctx context.Context) ([]Event, error)
}
```

- `internal/source` defines `Source`, the `Event` value type, and a registry:
  adapters call `source.Register("type-name", factory)` from `init()`.
- Each adapter is a subpackage (`internal/source/gitlab`,
  `internal/source/restapi`) owning its config shape, HTTP calls, and mapping
  to `Event`.
- `internal/source/all` is the single import site that blank-imports every
  adapter — the only file that knows the complete list.
- Adapter-specific YAML lands in `SourceConfig.Options` (`yaml:",inline"`)
  and each adapter decodes it into its own typed struct via `config.Decode`.
  The base schema never mentions `token_env`, `items`, or `watch`.

## Consequences

- Adding a service type = one new subpackage + one import line in `all`.
  Config parsing, the daemon, and existing adapters are untouched — the
  open/closed property the user asked for.
- Registration failure surfaces at startup as a panic (duplicate type) or a
  clear "unknown type" error listing the registered ones — never a silent
  skip.
- Adapters may be polled concurrently; the contract requires Poll to be safe
  for one caller per instance but not necessarily many.

## Alternatives considered

- **One switch statement in a monolithic `source` package** — simpler, but
  every new service edits core files and every adapter's imports compile
  into it. Rejected: that is exactly the coupling the registry removes.
- **Plugin .so loading** — true no-compile extensibility, but Go plugins are
  Linux-only, version-fragile, and overkill for a daemon of this size.
