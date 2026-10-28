# Minimum-version boundary schema

- Codex 0.122.0, Linux arm64, generated locally on 2026-10-28.
- Upstream tag rust-v0.122.0, commit `230dcadee609fa99d6162fe1107457030e5270a7`.
- Command: `codex app-server generate-json-schema --out <directory>`.
- Default generation; experimental opt-in not enabled.
- Complete generated bundle: 214 JSON files, unchanged.
- LICENSE/NOTICE copied from that exact upstream commit.
- Compare 0.121.0: RateLimitSnapshot lacks rateLimitReachedType.
- Feature introduction: openai/codex commit 139fa8b8f25408aeb3edea3ef50e1a8b3e3ff354.
- Boundary release list contains 0.121.0 then 0.122.0 with no intervening stable patch releases.
- Exact method/field inventory is recorded in surface.json; handshake is recorded separately.

The inventory establishes a schema compatibility floor, not successful live model,
authentication or sandbox behavior on every supported OS/version.
