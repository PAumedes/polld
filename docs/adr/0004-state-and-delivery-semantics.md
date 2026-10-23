# ADR 0004: Dedup state and delivery semantics

## Status

Accepted (2026-10-23)

## Context

A poller that re-reports everything every cycle is useless; one that loses
state across restarts is fragile. The store must also decide what "new"
means for two different event shapes: transient items (GitLab events have
unique IDs; once seen they never recur) and persistent items (merge requests
stay listed; their *update* is the news).

## Decisions

1. **Ledger file**: `$XDG_STATE_HOME/polld/state.json`, written atomically
   (temp file in the same directory + `rename`). A torn write can never
   produce a half-JSON file.
2. **Corruption is not fatal**: a corrupt file is renamed to
   `*.corrupt-<ts>` and the store starts empty. Worst case is one duplicate
   notification per item — strictly better than a dead daemon.
3. **Two-part dedup key**: `(source, ID)` identifies an item; `fingerprint`
   identifies its revision. Transient events leave fingerprint empty (ID
   alone dedups). Persistent items set fingerprint to a change token (MR:
   `updated_at`) so the same item re-notifies when it materially changes.
4. **Mark after delivery**: an event is recorded only after `Notify` returns
   nil. A failed notification leaves the event unseen → retried next cycle.
   This gives at-least-once delivery with exactly-once intent.
5. **Seed on first poll**: a source with no history marks everything seen
   silently rather than flooding the user with backlog. The ledger records
   `initialized` per source to make this explicit.
6. **Bounded growth**: each source keeps at most 500 entries, pruned oldest-
   first by record time — a noisy source cannot grow the file forever.
7. **Failure self-report**: 5 consecutive poll failures raise one critical
   notification; the counter and alert flag reset on the next success.

## Consequences

- Restarting polld never re-notifies; deleting the state file re-seeds
  silently (by design — see 5).
- Delivery is at-least-once at the edge (crash between notify and mark can
  duplicate one notification) — the correct trade for never missing one.
