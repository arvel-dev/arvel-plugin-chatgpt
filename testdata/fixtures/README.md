# Codex evidence fixtures

Recordings are made by `cmd/record-fixtures` against the exact schema version in
`index.json`. Unit tests never run a real CLI or access the network.

```sh
go run ./cmd/record-fixtures --help
ARVEL_E2E=1 go run ./cmd/record-fixtures record --scenario handshake --schema testdata/schema/0.164.0 --out testdata/fixtures/handshake
go run ./cmd/record-fixtures verify
```

The shell and PowerShell wrappers forward the same arguments. Live Go tests also
require `-tags e2e`. `ARVEL_CODEX_BIN` is honored only with `ARVEL_DEV=1`; an
explicit `--codex` is an absolute executable path. Defaults use a temporary private
CODEX_HOME and scratch cwd, deleted after the probe. `--home shared` is explicit
and never performs login/logout or credential mutation.

Each JSONL line has `direction` (`client_to_server` or `server_to_client`) and a
`message` object. Every recording has version/OS/scenario metadata. Messages are
sanitized in memory before any disk write, including ignored staging paths.
No credential file is read, and app-server stderr is discarded. Never add raw
personal transcripts, keys, tokens, auth URL parameters or device codes.

`index.json` lists every recording and pins its schema path relative to this
directory. RPC errors have a numeric `code` and string `message`; successful
results are matched to the exact method-specific response schema using the
request ID and direction. Requests in both directions must receive exactly one
response. Unknown mappings, unresolved references and unindexed recordings fail
offline verification. Failed probes are evidence of failure, not successful
feature verification. Synthetic contract checks live in Go tests and are never
presented as recorded transcripts.

Approval encodings follow each vendor request type: command/file use `decision`;
permissions use `permissions` and `scope`. The recording harness declines command
and permissions requests and, by default, file requests. Explicit edit decision
scenarios accept or cancel only an observed fileChange targeting exactly the
scratch arvel_probe.txt, with canonicalized paths. No other path is approved.
It never enables experimental APIs or persistent policy rules and refuses
sandbox measurements without the actual Arvel P9 environment. Per-recording
schemaOverrides in index.json pin historical CLI captures to their exact bundle.

The device-login scenario requires a private home and deliberately displays the
one-time challenge on the interactive terminal only. It persists only scrubbed
RPC messages, waits for the matching successful completion and confirms a
ChatGPT account with account/read. Temporary private state is removed on exit.
Use a sufficiently long --timeout and never redirect the challenge into a file.
Synthetic-key environment/private-account probes do not verify remote API-key
authentication or spend; their metadata/scenario names distinguish them.
