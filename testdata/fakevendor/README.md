# App-server fake vendor

G1's fake vendor is the Go test executable in `internal/appserver/client_test.go`.
The test's `TestMain` enters the fixed stdio fixture only when the parent supplies
`APP_SERVER_FAKE` through the SDK environment builder. Production always starts
the selected absolute binary with exactly `app-server`; no shell is involved.

Scenarios cover handshake, malformed or incomplete initialization, missing
responses, process EOF/crash, late responses after cancellation and environment
isolation. `peer_test.go` uses in-memory pipes for concurrent/out-of-order RPC,
reentrant notifications, ordered thread events and approval replies at shutdown.
No real Codex, account or network is accessed by unit tests. These synthetic
checks are separate from the 48 real G0 recordings and their versioned schemas.

Run `go test -race ./internal/appserver/...`.

G2 adds a self-executing synthetic vendor in
`internal/provider/integration_test.go`. Its fixed `--version` and `app-server`
entry points exercise production binary discovery, version checking, environment
construction, G1 transport, connection management and SDK host methods. It replays
the recorded model catalog, generates synthetic account/login responses and
emits login completion before the start reply. Tests cover shared/private homes,
proxy forwarding and credential stripping without network access. Connection
unit fakes additionally cover pagination, account polling/events, stale login
completion, cache expiry/invalidation, crashes, cancellation and owned shutdown.

Run `go test -race ./internal/connection ./internal/models ./internal/provider`.

The provider TestMain fake also serves historical rate-limit reads, sparse quota
pushes, token counters, retry advisories and typed quota/model failures. These
scenarios are synthetic, including quota exhaustion; they do not establish live
OpenAI billing, real-account quota rejection or host sandbox compatibility.

## Standalone conformance dependency

`cmd/fake-codex` and `internal/fakecodex` implement the separate synthetic
app-server used by `cmd/check-conformance`. Its embedded catalog, account
transitions, thread/approval/usage events and prompt-driven probe modes are
authored offline test data. They are not real vendor recordings and never
execute command text, MCP endpoints, model calls or read credential files.
Use the runner's generated environment, not your normal Codex account.
