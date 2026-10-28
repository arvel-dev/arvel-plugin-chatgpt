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
sandbox measurements without the actual production Arvel M5c launcher/native
target. M0 P9 is a feasibility lab, not a plugin-launch API; see the
[host integration handoff](../../docs/HOST-INTEGRATION.md). Per-recording
schemaOverrides in index.json pin historical CLI captures to their exact bundle.

The device-login scenario requires a private home and deliberately displays the
one-time challenge on the interactive terminal only. It persists only scrubbed
RPC messages, waits for the matching successful completion and confirms a
ChatGPT account with account/read. Temporary private state is removed on exit.
Use a sufficiently long --timeout and never redirect the challenge into a file.
Synthetic-key environment/private-account probes do not verify remote API-key
authentication or spend; their metadata/scenario names distinguish them.

### Session isolation preflight failure

`config-isolation-race` is a real Codex 0.164.0 private-home recording, with no
account/model operation. It deliberately adds an owned project MCP definition
between `config/read` and `thread/start`. The new server executes a harmless
marker despite per-name disable overrides built from the previous read.
Metadata records that file observation separately from RPC traffic, and marks
both probe completion and the security gate false. The recording command is
expected to exit nonzero; see FINDINGS “G3 — Session isolation preflight blocker”.
The `config-isolation` startup-only scenario also refuses to certify untested
hooks, plugin sources, completed turns or persisted resume. Neither scenario
honors `--home shared`. Marker executables only create a new `isolation-*.sentinel`
file and never overwrite an existing path.

The follow-up isolation fixtures compare empty/null/array root replacement,
untrusted project configuration, and a late user-config definition. All retain
`securityGatePassed:false`. `config-isolation-untrusted-project` is only a bounded
startup observation; its missing marker is not certified end-to-end isolation.
`config-isolation-untrusted-user-0165` is real official Codex 0.165.0 traffic,
recorded against the committed 0.164.0 validation schema as its metadata states.
The executable/configuration inputs remain synthetic and private to each probe;
no real account or model was used. See FINDINGS for the outcome matrix and the
unapproved private-session product proposal.

`config-isolation-private` is the successful revised private-home lifecycle gate:
real Codex starts a thread, completes two turns and resumes an unloaded thread
without executing the owned project/notify sentinels. Its model responses come
from a synthetic loopback fixture and require no account or OpenAI model usage.
Its successful gate label applies only to that explicitly scoped configuration
measurement, not to full G3 completion or production host compatibility.
