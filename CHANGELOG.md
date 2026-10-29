# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

## [0.1.0] - 2026-10-29

### Added

- ChatGPT and OpenAI API-key connections for macOS, Linux and Windows on x64 and arm64.
- Private browser and device-code sign-in, isolated API-key configuration, and live connection status.
- Model discovery, reasoning effort selection, streaming conversations, cancellation and resume.
- Command and file approvals, crash recovery, and per-session turn budgets.
- ChatGPT quota windows, reset times, per-turn token counts, and resumable quota errors.
- Assigned stdio and HTTP MCP servers with per-tool permissions and one-call approvals.
- Managed workspace skills with automatic/manual invocation and source-preserving cleanup.
- Native subagents with inherited permissions, attributed output and bounded concurrency.
- PNG, JPEG and GIF image prompts with bounded in-memory transport.
- Read-only discovery of existing MCP definitions and user/project skills, with secret markers.
- Windows sandbox readiness diagnostics.
- Offline release conformance and weekly/manual Codex schema compatibility checks.

### Changed

- Keep provider skill catalogues separate and preserve original skills and other live session copies.
- **BREAKING:** ChatGPT connections require a separate Arvel sign-in. Recreate legacy shared-login connections and sign in through Arvel settings; personal terminal credentials are not reused.
- **BREAKING:** Require Codex CLI 0.164.0 or newer. Update the CLI before creating or reconnecting an Arvel connection.
