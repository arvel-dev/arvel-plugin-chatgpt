# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Added

- Streaming agent sessions with resumable conversations, model and reasoning options, and local turn budgets.
- Command and file approval requests, graceful cancellation and shared-process crash recovery.

- ChatGPT browser and device-code sign-in, isolated API-key connections, and live connection status updates.
- Model discovery with reasoning levels, hidden-default handling, refresh, and a six-hour account-scoped cache.

- Initial provider scaffold and six-platform build configuration.
- Reusable Codex app-server transport with controlled process cleanup and safe default approval replies.

### Changed

- **BREAKING:** New ChatGPT connections require a separate Arvel sign-in; sign in again through the connection settings instead of relying on your personal Codex login.
- **BREAKING:** Require Codex 0.164.0 or newer for the verified session isolation controls; update the CLI before reconnecting.

- Clarify independent plugin development and the production host sandbox compatibility gate.

### Fixed

### Removed

### Security
