# ADR 0005: systemd user service, foreground process

## Status

Accepted (2026-10-23)

## Context

The daemon needs supervision (restart on crash, start at login, log capture)
and access to the desktop's notification bus.

## Decision

- **Foreground process managed by systemd**, not a self-daemonizing program:
  no double-fork, no PID file, no log handling — `Type=simple`,
  `Restart=on-failure`, `journald` capture. This is the modern systemd
  contract and keeps the binary honest (stdout is the log).
- **User service, not system service**: `org.freedesktop.Notifications` lives
  on the *session* bus. A root service would need `DBUS_SESSION_BUS_ADDRESS`
  hacks and uid juggling; a user unit (`WantedBy=default.target`) inherits
  the session environment for free. Multi-user deployments can template the
  unit per user later if ever needed.
- **`-once` flag** also supports the timer model (a `.timer` firing a
  oneshot) for people who prefer no resident process — both run the same
  code path.
- **Portability**: a static Go binary compiled on any recent toolchain runs
  on Ubuntu 24.04 and 26.04 alike — no interpreter or system-library
  coupling. This is the property that drove the language choice (Go).

## Consequences

- Logs: `journalctl --user -u polld`.
- Secrets reach the process via `~/.config/environment.d/` or unit
  `Environment=` overrides — same mechanism, user's choice.
