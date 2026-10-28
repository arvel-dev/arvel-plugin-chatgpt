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
