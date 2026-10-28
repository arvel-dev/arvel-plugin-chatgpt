# Codex schema provenance

- CLI: codex-cli 0.164.0.
- Generated locally on macOS arm64, 2026-10-28, with a temporary private CODEX_HOME.
- Command: `codex app-server generate-json-schema --out <staging-directory>`.
- No `--experimental` flag, no account or model calls.
- Complete generated JSON output is preserved unchanged: 320 files.
- Per-file hashes are in SHA256SUMS.
- Matching upstream tag: rust-v0.164.0.
- Tag resolves to commit `6aaac694803004a63c6f393d153c96d22521a6b9`.
- LICENSE and NOTICE copied unchanged from that commit in openai/codex.

Default generation still includes some explicitly UNSTABLE/internal variants.
Presence alone does not establish stable eligibility; inspect annotations and
matching versioned source before sending a method/field.

Relevant source:
- https://github.com/openai/codex/blob/6aaac694803004a63c6f393d153c96d22521a6b9/codex-rs/app-server-protocol/src/protocol/common.rs
- https://github.com/openai/codex/blob/6aaac694803004a63c6f393d153c96d22521a6b9/codex-rs/app-server-protocol/src/protocol/v2/permissions.rs

This is preflight schema evidence for G0. It does not establish minimum Codex
version or complete V1/V2, and no production vendor transport is implemented.
