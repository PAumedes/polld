# ADR 0003: Notification transport — D-Bus first, notify-send fallback

## Status

Accepted (2026-10-23)

## Context

Linux desktop notifications follow the freedesktop Notifications spec over
the session D-Bus bus. Options:

1. Shell out to `notify-send` — zero code, but a subprocess per notification
   and no programmatic access to the spec's hints.
2. Speak the protocol over D-Bus directly — one method call
   (`org.freedesktop.Notifications.Notify`), full control over urgency hints
   and markup, no subprocess.
3. cgo + libnotify — ruled out: defeats the static-binary property that keeps
   the daemon portable across Ubuntu releases.

## Decision

Two backends behind the `Notifier` interface:

- `dbus` — godbus/dbus client; the primary transport.
- `command` — `notify-send` subprocess.

`backend: auto` (the default) tries D-Bus and falls back to the command —
correct on a desktop session, still functional on minimal environments where
only the CLI exists. `backend: dbus` / `command` force one explicitly.

Per-notification markup is generated in `formatBody`: the body is
HTML-escaped first (the spec allows a markup subset), then an optional "Open"
anchor is appended for events carrying a link.

## Consequences

- `replaces_id` is always 0: every event is a distinct notification — the
  value of the tool is the stream, not a single updated bubble.
- Click-to-open-URL via spec *actions* is deliberately not implemented (it
  requires keeping a server-side action handler alive); links ride in the
  body markup where compliant daemons render them clickable.
