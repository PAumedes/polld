# ADR 0002: YAML configuration with env-var secret references

## Status

Accepted (2026-10-23)

## Context

The daemon is configured by hand-edited text that describes each service.
Requirements: comments for documentation, tolerant syntax for humans, and a
hard rule that secrets never live in the file.

## Decision

- **YAML** (`gopkg.in/yaml.v3`) over JSON and TOML: comments, anchors, and the
  lightest syntax for the nested source blocks this config needs. JSON was
  rejected for its no-comments rule — a config file you can't annotate is a
  support burden. TOML was rejected on ubiquity: nothing about it buys
  anything YAML doesn't already give the target audience.
- **Two-level schema**: common keys (`name`, `type`, `url`, `interval`) are
  typed; adapter-specific keys flow into `SourceConfig.Options` via
  `yaml:",inline"` and each adapter decodes them with `config.Decode`. The
  root schema can't rot when adapters evolve.
- **Secrets by reference only**: `${VAR}` expansion inside any option string
  (resolved at load), plus `token_env` on the gitlab adapter naming the
  environment variable outright. The file can be committed safely.
- **XDG defaults**: `~/.config/polld/config.yaml`,
  `~/.local/state/polld/state.json`; `POLLD_CONFIG` overrides the config
  path for testing and multi-instance use.

## Consequences

- Validation is split: `Load` (syntax), `Validate` (semantics), adapter-level
  `New` (per-type fields). Errors point at the specific source by name.
- `${VAR}` expansion happens in values only — a literal `${VAR}` in the
  middle of a string is intentional (e.g. `"Bearer ${TOKEN}"`).
