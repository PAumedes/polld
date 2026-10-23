# Contributing

This codebase aims for boring excellence: small functions, explicit errors,
tests that document behavior. Two commitments keep it that way:

## 1. Test-driven development

Red → green → refactor, in that order. Concretely:

1. Write a failing test that names the behavior you want
   (`TestPollMapsItemsToEvents`, not `TestItWorks`).
2. Write the simplest code that passes.
3. Refactor under the safety of the green bar.

Tests are executable documentation. A reader should understand a package's
contract by reading only its `*_test.go`.

Conventions:

- **Table-driven tests** for input→output matrices (see `config_test.go`).
- **`httptest` servers** for HTTP — never mock `net/http` internals; test
  against a real socket (see `gitlab_test.go`).
- **Fakes over frameworks** — `fakeSource`, `fakeNotifier` are handwritten
  structs satisfying the interface; no mocking library.
- **`-race` is mandatory**: `make test` runs it; CI enforces it.
- Test the edge: empty responses, error statuses, fingerprint changes,
  corrupt state files. The interesting bugs live there.

## 2. Continuous refactoring (Fowler)

We treat the codebase as always-in-refactoring. The catalog we actually
apply, with where it shows up:

| Refactoring | Applied as |
| --- | --- |
| Extract Function | `gitlab.pollMRs` / `gitlab.pollProjectEvents` — two watchers, one shape |
| Replace Conditional with Polymorphism | `source.Source` interface; adapters register, never `switch` on type |
| Introduce Parameter Object | `config.SourceConfig` carries adapter options as an opaque map |
| Encapsulate Collection | `state.Store` owns its maps; callers get `Unseen`/`Mark`, not the map |
| Rename | mercilessly — names explain *why* (`seed`, `fingerprint`) |
| Remove Dead Code | `unused` linter in CI; delete it, don't comment it out |

Rules of engagement:

- **Refactor before adding a feature** when the current shape fights you —
  but land refactors in their own commits, separate from behavior changes.
- **Never refactor and change behavior in the same commit.** Tests must
  stay green through a pure refactor.
- When a design decision is made (not just a mechanic), record it as an
  [ADR](docs/adr/).

## Pull request checklist

- [ ] `make all` passes locally (fmt, vet, tests with `-race`, build)
- [ ] New behavior has tests written before the implementation
- [ ] Config/schema changes update `configs/config.example.yaml` and README
- [ ] Design decisions have an ADR in `docs/adr/`
- [ ] Commit messages are imperative and describe *why*, not just *what*
