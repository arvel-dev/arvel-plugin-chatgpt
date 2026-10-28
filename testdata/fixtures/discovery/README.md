# Discovery format inputs

Synthetic TOML and expected redacted JSON for the G5 discovery implementation.
They are not real RPC recordings and do not count as live fixture coverage.
The source formats are verified against Codex rust-v0.164.0 config/MCP types.
Read only config and skill definitions; never credential files.
No production discovery parser is implemented in G0.

Run `python3.12 scripts/check-discovery-fixtures.py` from the repository root
(Python 3.11+). It checks only these synthetic inputs using stdlib tomllib.
