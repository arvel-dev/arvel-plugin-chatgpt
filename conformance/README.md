# Synthetic release scenarios

Run `go run ./cmd/check-conformance` from the repository root. It prepares the
root `conformance-env.yaml` template and these SDK v0.2.0 YAML scenarios for the
actual production executable with a separate, offline fake Codex process.
Synthetic constants and source markers in these files are test inputs only.

See [the conformance guide](../docs/CONFORMANCE.md) for fixture ownership,
required probes, supplementary raw-wire tests and evidence limitations.
