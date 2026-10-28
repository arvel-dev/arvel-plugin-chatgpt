# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- Native subagents with inherited permissions, child output attribution and cancellation.
- Windows sandbox readiness diagnostics and SDK executable/shim launch checks.

- Managed workspace skills with automatic/manual invocation, source-preserving copies and safe session cleanup.

- Assigned stdio and HTTP MCP servers with per-tool permissions, one-call approvals, bounded results and visible startup failures.

- Bounded PNG, JPEG and GIF prompts for image-capable models, sent without temporary files.

- Read-only discovery of existing Codex MCP definitions and user/project skills, with secret markers and bounded source reads.

- ChatGPT quota windows, reset times, live usage updates and per-turn token counts.
- Typed quota, authentication, context and model errors with resumable conversations after quota exhaustion.

- Streaming agent sessions with resumable conversations, model and reasoning options, and local turn budgets.
- Command and file approval requests, graceful cancellation and shared-process crash recovery.

- ChatGPT browser and device-code sign-in, isolated API-key connections, and live connection status updates.
- Model discovery with reasoning levels, hidden-default handling, refresh, and a six-hour account-scoped cache.

- Initial provider scaffold and six-platform build configuration.
- Reusable Codex app-server transport with controlled process cleanup and safe default approval replies.

### Changed

- Upgrade to arvel-sdk v0.1.3 with provider-specific skill catalogues and ownership-safe materialization.

- **BREAKING:** New ChatGPT connections require a separate Arvel sign-in; sign in again through the connection settings instead of relying on your personal Codex login.
- **BREAKING:** Require Codex 0.164.0 or newer for the verified session isolation controls; update the CLI before reconnecting.

- Clarify independent plugin development and the production host sandbox compatibility gate.

### Fixed

### Removed

### Security
