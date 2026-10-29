# Bootstrap Findings

G0–G5 implementation and local evidence are complete under the owner-approved
scope. G6 release tooling is being verified against SDK v0.2.0. Historical
bootstrap sections below retain their original version/evidence context; they
are not the current implementation status. Native Windows/CI acceptance is
explicitly deferred until after v0.1.0, and real Arvel M5c sandbox compatibility
remains an independent integration check. No publication is implied.

## Evidence and authority

Recorded on 2026-10-28 with Go 1.27.2, SDK v0.1.2 and Codex 0.164.0 on macOS
arm64 and a headless Debian/Linux arm64 container. The tested Linux image is
`node:22-bookworm-slim`, digest
`sha256:3ed748ce9dea62d2f0f0205cdbb6823106d9f973a6f555c402fe2575f8d6e1ae`.
Linux authentication probes require the system `ca-certificates` package.

- SDK tag commit: `3001f396f74c833e2883c451087842cecaf95c73`.
- Codex 0.164.0 source commit: `6aaac694803004a63c6f393d153c96d22521a6b9`.
- Codex 0.122.0 source commit: `230dcadee609fa99d6162fe1107457030e5270a7`.
- Complete unmodified generated schemas, upstream LICENSE/NOTICE, hashes and
  provenance: `testdata/schema/0.122.0/` and `testdata/schema/0.164.0/`.
- Sanitized real RPC transcripts and per-recording metadata:
  `testdata/fixtures/index.json`. Historical recordings pin their own schema.
- Synthetic malformed messages, policy and schema examples live in Go tests.
  Discovery TOML/golden inputs are explicitly synthetic, not real transcripts.

The user authorized SDK compatibility adaptations, V1–V13 scope, the conditional
nested-sandbox exception, and necessary Codex compatibility decisions. Resolve
such differences from actual SDK/source/schema/spike evidence, preserving
security and isolation; do not ask for repeated API-shape approvals. Brief files
and SDK source remain unchanged.

Default schema generation can still contain descriptions marked UNSTABLE or
internal, notably `chatgptAuthTokens`. Presence alone is not permission to use
those variants. No experimental opt-in or internal auth method is sent.

## V1 — Minimum stable interface version

**Verified schema boundary: 0.122.0.** The root manifest uses that compatibility
floor; installed 0.164.0 was not simply declared the minimum.

Actual default schema generations on Linux compared 0.100.0, 0.115.0, 0.118.0,
0.120.0, 0.121.0, 0.122.0, 0.129.0 and 0.130.0. In 0.121.0 the required
`RateLimitSnapshot.rateLimitReachedType` is absent; in 0.122.0 it is present.
The other mandatory method and selected field inventory passes at 0.122.0.
There is no intervening stable patch release between 0.121.0 and 0.122.0 in the
published npm release list.

Upstream introduction commit
`139fa8b8f25408aeb3edea3ef50e1a8b3e3ff354` (Propagate rate limit reached type) is
an ancestor of rust-v0.122.0, not rust-v0.121.0. This agrees with the generated
schema boundary. A real 0.122.0 Linux handshake also passed.

Evidence: `fixtures/version-surface/comparison.json`,
`fixtures/handshake-minimum/`, and the versioned `surface.json` inventories.
This establishes the interface floor, not live authentication/model/sandbox
behavior on every OS and historical version.

## V2 — Handshake, framing and schema

**Observed.** Initialize request with clientInfo, successful response, then the
`initialized` notification. Newline-delimited JSON without the jsonrpc field is
accepted. A separate probe with `jsonrpc: "2.0"` on requests also succeeds.
An account request before initialization receives an RPC error, after which the
normal handshake succeeds. Private-home handshakes passed on macOS and Linux.

Codex 0.164.0's 320 generated JSON files compile offline with external loading
disabled. Regeneration with the same binary matched all generated files
byte-for-byte. The local validator checks direction, method schemas, response
correlation, exactly one result/error, unknown mappings and unindexed captures.

Evidence: `handshake`, `handshake-jsonrpc`, `preinitialize`, `handshake-linux`,
`handshake-minimum` recordings; `internal/spike/schema_test.go`.

## V3 — Streaming, items, images and turn outcomes

**Observed with explicit gaps.** Real recordings cover:

- text deltas: `item/agentMessage/delta`;
- `userMessage`, `agentMessage`, `reasoning`, `commandExecution`, `fileChange`,
  `mcpToolCall` and `webSearch` items through item/started and item/completed;
- turn/started, turn/completed and thread/tokenUsage/updated;
- completed, interrupted and failed terminal status values;
- local image input `{type: "localImage", path: <absolute path>}`, using a
  generated harmless one-pixel PNG;
- thread/resume after a completed, persisted first turn.

A deliberately nonexistent model produced a real failed turn. Its error has
`codexErrorInfo: "other"` with a nested JSON message containing model_not_found;
do not assume every model error has a dedicated top-level enum value.

`turn/plan/updated`, `turn/diff/updated` and exact reasoning delta names are in
the versioned ServerNotification schema. The default plan probe returned
tool-unavailable. With the stable `config.tools.update_plan.enabled = true`
override, plan-enabled emitted a real `turn/plan/updated` notification. The
default failure is retained alongside the successful observation. The bounded reasoning probe
uses an observed supported model with high effort and detailed summaries, and
records `item/reasoning/summaryTextDelta`. Raw/private reasoning is not requested;
G3 must obtain any still-missing real-event variants before implementing them.

**Resume edge:** immediately resuming a newly created thread before its first
turn returned `no rollout found`. After a first persisted turn, resume succeeds.
G3 must account for empty sessions rather than assume every start is immediately
resumable across process loss.

Evidence: text, reasoning, image, command, edit-*, mcp, web, interrupt, failed, plan, plan-enabled, resume,
and resume-before-first-turn recordings. Failed attempts remain labeled.

## V4 — Configuration overrides and instructions

**Observed/source-verified.** thread/start accepted a per-thread
`config.mcp_servers.arvel_probe` definition and
`sandbox_workspace_write.network_access: false`. The isolated fixture MCP server
was started and its tool returned MCP_PONG. This proves config delivery and MCP
startup. Separate native network-deny/network-allow probes executed one curl
against an owned loopback HTTP fixture: disabled access produced exit 7 and no
server request; enabled access returned NETWORK_PONG with exit 0. This verifies
the native setting on macOS, not the separate Arvel host/proxy matrix.

A developerInstructions marker was honored without sending baseInstructions.
Version-pinned core/context/developer_instructions.rs represents it as a
separate developer content item; base instructions have their own configuration
and provenance in core/session/world_state.rs. Do not replace the base prompt.

Shared-home threads also started personal MCP servers/hooks, even when the probe
injected its own server. An empty or partial map must not be assumed to replace
all personal configuration. G2/G5 must enforce the intended isolation explicitly.

## V5 — Read-only file approvals

**Observed.** In read-only mode an apply_patch edit produced
item/fileChange/requestApproval. Decline prevented file creation. Accept and
acceptForSession permitted exactly the scratch arvel_probe.txt. Cancel produced
an interrupted turn and no file. workspace-write created the scratch file
without the read-only approval path.

The probe approves only this exact observed scratch-file change, after
canonicalizing paths and rejecting symlinks. It never approves arbitrary
commands, external files or persistent policy amendments. Synthetic helper tests
also cover timeout, EOF, duplicate server requests and cleanup. The production
ACP approval bridge and cancellation-while-host-waiting matrix remain G3 work.

## V6 — Subagents

**Observed with stable V2 notifications.** `subagent-complete` records distinct
parent/child thread and turn IDs, child `subAgentActivity` started/completed
lifecycle events, the child CHILD_PONG response, a completed
`collabAgentToolCall` wait, and the parent's subsequent completed turn.
Activity events carry `agentThreadId`, `agentPath`, kind and optional model/effort;
collab items carry sender/receiver IDs and agentsStates. Not every wait item
populates receiverThreadIds, so G5 must combine observed attribution signals.

The source-informed bounded probe uses a persistent thread, stable multi_agent_v2
configuration, max_depth one and concurrency two (parent plus one child).
Earlier parent-only capacity and ephemeral attempts remain labeled unsuccessful.
The recorder formerly stopped at the first child turn/completed; a regression
test now proves it waits for the exact parent thread AND turn. Historical
subagent-capacity remains incomplete; it was not relabeled after this fix.

This establishes the G0 wire behavior, not production `_meta.arvel.subagent`
mapping or capability advertising. Those remain G5. No experimental opt-in is
used, and no AIF implementation agents or worktrees were spawned.

## V7 — Limits, reset times and errors

**Observed plus source evidence.** account/rateLimits/read returns rateLimits
and rateLimitsByLimitId. A real push update arrived during a turn. The observed
primary window was weekly (10080 minutes), not necessarily five hours: derive
labels from duration, never from primary/secondary position.

resetsAt is Unix **seconds**. Observed values align with the capture time and
window; upstream tui/src/status/rate_limits.rs converts them with
DateTime::from_timestamp(value.resets_at, 0). Convert to RFC3339 in G4.

The stable CodexErrorInfo enum includes usageLimitExceeded, mapped from the core
error in app-server-protocol/src/protocol/v2/shared.rs. No quota was deliberately
exhausted; a live mid-turn quota rejection was not manufactured. G4 must distinguish
this source-derived case from the real failed-model recording.

## V8 — API-key cost reporting

**Contract conclusion: spend remains unknown.** Thread token-usage structures
contain token counters, including cached and reasoning tokens, but no USD/cost
field. The plugin must not infer zero or estimate a price from those counters.
The intended API-key mapping remains costSource/spend source unknown.

No API key was supplied or inspected, and no live API-key authentication/spend
probe was performed. This limitation does not become a claimed live observation.
Source: app-server-protocol/src/protocol/v2/thread.rs TokenUsageBreakdown and the
recorded usage schemas. The pinned app-server turn_cost_worker.rs does query
cost backends but records them to session telemetry; it does not expose a stable
cost notification in the committed ServerNotification schema. Internal telemetry
is not a plugin data source.

## V9 — Environment and CODEX_HOME

**Source-verified, helper-tested and probed with synthetic sentinels.**
Versioned login/src/auth/manager.rs reads OPENAI_API_KEY, CODEX_API_KEY and
CODEX_ACCESS_TOKEN in its supported contexts. Crucially app-server lib.rs and
config_manager.rs initialize AuthManager with enable_codex_api_key_env=false;
the exec/CLI environment precedence is not an app-server authentication contract.
The real env-openai and env-codex private-home recordings confirm account/read
remains null with synthetic OPENAI_API_KEY/OPENAI_BASE_URL or CODEX_API_KEY.
No model request or external API-key verification was attempted with these
sentinels. OPENAI_BASE_URL also appears in the credential-broker context; it is
not assumed to be an unconditional app-server provider-base-URL switch.

The env-stripped probe starts with synthetic polluted OPENAI_/CODEX_ variables
in the parent; SDK EnvBuilder removes them before the real private-home vendor
starts, and account/read remains null. Strip these families and set only the
intended private CODEX_HOME explicitly; retain the real HOME per host contract.
The helper child tests independently prove credential/SSH/Git variables are
absent, private/shared home selection, RUST_LOG=warn and proxy construction.

private-api-key records stable API-key login/start, account type apiKey,
logout and return to a null account inside a temporary private home. The key
is explicitly synthetic: local acceptance proves wire/home behavior, not valid
remote authentication or spend. The real shared account remains ChatGPT and is
never logged out or mutated. Real separate-login success is now observed in the Linux device-login fixture. No credential file is read.

## V10 — Reasoning efforts

**Observed.** The current schema defines ReasoningEffort as a nonempty string,
not a closed universal enum. The real model catalog advertised low, medium,
high, xhigh, max and (on some models) ultra.

The catalog describes max as maximum reasoning depth and ultra as reasoning with
automatic task delegation. Offer the supported Arvel vocabulary through max;
do not map Arvel max to ultra or expose arbitrary new values. Model
multiAgentVersion is separate metadata, not the effort itself. Read the catalog
for each account/version; do not hardcode the observed model list.

Evidence: models/transcript.jsonl, ModelListResponse schema. Subagent execution
is a separate verification question (V6).

## V11 — Headless device-code login

**Verified end to end on Linux arm64.** In a headless Debian container with a
system CA bundle and a fresh private CODEX_HOME, account/login/start with
chatgptDeviceCode returned a challenge. The user completed it in a browser on
another device. The app-server emitted account/login/completed with success true
for the matching loginId; the subsequent account/read returned type chatgpt.
The bounded recorder completed successfully, exited, and ran temporary private
state cleanup. No credential file was read, copied or inspected.

Evidence: device-login-linux (completed true). Device codes, verification URLs,
login/account identifiers and email are scrubbed before capture persistence.
The deliberate terminal challenge is interactive output, not a saved log.

Cancellation is separately observed in device-cancel-linux-with-ca, with a
subsequent null account. The first complete-login attempt was stopped at the
user's pause request and remains device-login-linux-cancelled (completed false).
The earlier device-cancel-linux failure lacked a system CA bundle; installing
ca-certificates/libssl3/openssl in the test container fixed the real Rust HTTPS
path. A failed or cancelled attempt is never counted as successful sign-in.

## V12 — Skills and discovery

**Source-verified plus real Linux cwd discovery.** skills/list found a temporary
project skill under cwd/.agents/skills/arvel-probe with repo scope while using a
private Codex home. Built-in system skills also appear; a private home does not
mean the CLI has no built-in tools/skills.

Native explicit-only metadata is agents/openai.yaml with
`policy.allow_implicit_invocation: false`, parsed by skills/src/metadata.rs.
SDK v0.1.2 Materialize writes disable-model-invocation in SKILL.md; that generic
SDK metadata alone is not proof of Codex's native switch. G5 must materialize
through the SDK and add/preserve the Codex-specific metadata or the documented
explicit-request description fallback.

Private CODEX_HOME does **not** suppress the independent ~/.agents/skills root:
ext/skills/src/host_roots.rs adds both the user config skills root and the real
home's .agents/skills. Do not promise complete personal-skill isolation from
separateLogin alone. Verify/filter actual roots in G5 together with the host
sandbox; do not silently redirect HOME or read credential files.

Discovery source formats include config.toml mcp_servers, stdio env/env_vars and
HTTP bearer_token_env_var. Synthetic TOML plus secret/envRef golden expectations
are in fixtures/discovery; no personal config was read. `scripts/check-discovery-fixtures.py` checks the synthetic TOML/envRef/secret
projection with Python 3.11+ stdlib tomllib; the production parser stays in G5.

The real skills-selected recording proves explicit stable UserInput::Skill
invocation of the harmless project fixture with native allow_implicit_invocation
false, returning SKILL_PONG. Use the exact path from skills/list, after verifying
it identifies the intended materialized file: macOS /var and /private/var aliases
did not match the catalog in the retained failed skills-explicit attempt.
Source selection.rs explains exact catalog-path matching. No personal skill
content was opened.

## V13 — Nested Arvel sandbox and CONNECT proxy

**Deferred to the actual production M5c host boundary. Not verified.** Plugin
milestone development and release conformance continue independently against
SDK v0.1.2 and mockhost; neither M0 nor M5c completion is a prerequisite for those
stages. Real host/CLI compatibility and any conditional native-sandbox fallback
remain separate evidence gates.

On 2026-10-28 the owner reported M0 complete locally, with GitHub checks and merge
remaining. The inspected local host revision is
`f32141ba222fc3cdbda66bf935e247d79500f283` (`feat/m0-foundation`). Its accepted
ADR-14 and `docs/sandbox-contract.md` explicitly assign synthetic OS feasibility
to M0 and the production launcher/proxy to M5c. M0 artifacts are not a stable
plugin-launch API. The host's named-vendor runner was removed; its historical
CLI experiments cannot establish compatibility or justify a fallback.

The pinned SDK already supplies manifest declarations, `sandbox.mode` and
`sandbox.proxy`; no wire/SDK change is required. G2 tests verify proxy environment
forwarding through a synthetic vendor. Those tests and the standalone private-home
Codex smoke establish no actual CONNECT/allow-list, denied-path or nesting result.

Do not enable a danger-full-access fallback without a reproduced failure inside
a real effective M5c os/container sandbox on that OS, with isolation still
verified. Mode none always retains vendor sandboxing. Record native
macOS/Linux/Windows results, actual host/plugin/CLI revisions and sanitized proxy
logs when that production boundary exists. No M0 profile or generic Docker run
substitutes for this evidence. [The integration handoff](HOST-INTEGRATION.md)
defines the required matrix and current unverified cells.

The immutable SPEC's P9 wording is retained; this is a factual dependency
reconciliation to the owner-approved host contract, preserving security and
product semantics under the standing compatibility delegation. The development
sandbox probe now identifies M5c instead of promising a nonexistent P9 launcher.

## SDK manifest reconciliation

SPEC §3 line 60 used literal <dataDir>/codex-home entries in vendorPaths. SDK
v0.1.2 manifest/sandboxpaths.go rejects relative/unexpanded paths. The approved
fix is vendorPaths read ["~/.codex"], write [], relying on PROTOCOL §15.2's
existing plugin data-directory grant. Runtime private CODEX_HOME stays below
that data directory. The real root manifest passes official-platform validation.

## Approval contract reconciliation

SPEC §6.3 uses one decision vocabulary for every approval. Actual permissions
approvals instead require a granted permissions profile and turn/session scope;
command/file approvals use decision strings. The real command-approval probe
requests elevation for harmless pwd and records a client decline, with no
command permission grant. One-time permissions are turn-scoped,
not necessarily one tool call. Return an empty profile to deny/cancel; interrupt
on cancellation. Never grant more than the Arvel-approved request.

The pinned features/src/lib.rs marks the request_permissions_tool producer
UnderDevelopment and default false. It was not enabled for a live fixture.
Method-specific schema and synthetic denial tests do not substitute for live
producer coverage; that limitation remains explicit. G0 accepts the documented
source/contract conclusion under the standing vendor compatibility delegation.
G3 must obtain stable producer evidence before claiming live grants verified;
safe denial is covered by the synthetic method-specific tests.

CommandExecutionRequestApprovalParams.command is a nullable string, not argv.
Match the supplied string directly. Missing text cannot qualify for automatic
command allow-list matching. Do not synthesize it from display-only parsed actions.

MCP approvals additionally arrive as mcpServer/elicitation/request. A real failed
capture records the default rejection; the successful fixture-server probe
returns action accept with empty form content only for the controlled harmless
server. It does not persist the offered always/session choices. General MCP
policy/elicitation handling remains a production bridge requirement.

## Current verification and next checks

- SDK provider/bootstrap and official manifest tests; offline schema/transcript,
  redaction, process cleanup and environment tests are implemented.
- Pure-Go provider cross-builds passed for all six manifest platforms. These are
  compilation results, not native Windows runtime tests.
- At this checkpoint all 48 indexed recordings pass offline validation;
  `go test -race ./...`, `go vet ./...` and staticcheck v0.8.2 pass.
  Real e2e configuration/approval/network/MCP, environment, protocol,
  skills and usage groups pass. The subagent group hit the old 45-second bound;
  with a bounded 90-second allowance its targeted rerun passes in 18.35 seconds.
  A subsequent complete e2e command passes in 172.398 seconds. Earlier failed
  attempts remain documented rather than relabeled.
  Validation runs after each recording batch and explicit index update.
- The synthetic discovery TOML projection and native manual-policy/secret
  goldens pass; schema hashes match 214 historical and 320 current files.
- Recorder parent/child completion, echoed-marker rejection, device-challenge
  non-persistence and exact numeric validation have regression coverage.
- Full release conformance is G6, not an echo-model workaround for bootstrap.
- Remaining evidence must not be turned into passing flags or unchecked claims.
  V13 is an approved future integration gate. G0 closure still requires final
  verification and code/security review; release conformance remains G6.
- Final review corrected the README drift-job claim, included NOTICE in release
  archives and added per-recording SDK redactor registration so known challenges
  cannot leak through later free-text errors. Regression tests cover the latter.


## G1 — Runtime transport foundation

The production appserver package reuses SDK v0.1.2 jsonrpc.Peer through a Codex
lite envelope adapter. SDK decoding requires jsonrpc=2.0; the adapter adds it only
internally and removes it on outbound frames. It enforces the original vendor
64 MiB limit before normalization, preserves integer IDs without float conversion
and rejects duplicate active server requests. SDK scheduling preserves thread
notification order and permits callbacks to make reentrant RPC calls.

Client owns OS pipes, the SDK process tree and Serve worker. Initialization must
complete before initialized is sent. An individual Call cancellation leaves the
process alive and late replies are ignored; process closure fails pending calls.
Close cancels dispatch, unblocks reads, closes pipes, reaps the tree and waits for
its worker. Handlers must honor context. G3 owns semantic turn interrupt/resume
and restart decisions; there is no implicit transport retry.

The SDK peer's internal logger is discarded because its panic/error paths may
contain handler text. Outer diagnostics use SDK logx/redact, fixed method/status
context and bounded debug-only stderr lines. Long stderr lines are dropped whole;
registered secrets remain protected across split chunks. RPC error codes and
redacted messages are returned, while unvalidated error data is omitted. Login
keys and returned public challenges are registered with the shared redactor.

Known command/file requests default to decline, permissions to an empty turn
profile and MCP elicitation to decline. Unknown requests get method-not-found.
No persistent grants, experimental/internal auth, policy bypass or sandbox
fallback is introduced. These are synthetic contract tests, not new real
permissions-producer observations. Provider feature bits remain false; G2/G3
still own account actions, models and agent sessions.

Evidence: schema types re-emit recorded G0 requests against exact generated
schemas; synthetic Go fakevendor and pipe tests cover concurrent RPC, callback
reentrancy/order, cancellation/late responses, approval shutdown, invalid or empty
initialization, EOF/crash, 64 MiB frames, environment isolation and diagnostics.
Unit tests never invoke Codex, inspect credentials or use the network.

A gated G1 smoke test also passed against the installed real Codex app-server:
initialize/initialized, account/read with a fresh private CODEX_HOME, then owned
shutdown. It used no login or credential-file access and confirmed no inherited
account. This is distinct from synthetic fakevendor coverage.


## G2 — Connections, authentication and model discovery

SDK v0.1.2's `plugin.HostAware.SetHost` supplies `Host.StatusChanged` and the host
logger. Initialization only validates local connection metadata; executable
resolution, the minimum 0.122.0 version check and app-server startup happen lazily.
One manager owns the process and a cancellable refresh worker. Requests have a
15-second bound, including gate wait, and do not own the established process.
Vendor startup cancellation closes an incomplete process. Account notifications
are queued without RPC in the callback; authoritative `account/read` refresh and
30-second polling detect login/logout. Process death publishes an error, clears
the catalog revision and permits restart on the next explicit operation; no
request or model turn is automatically replayed.

The actual SDK initialization shape is `Connection.Config`/`Secrets`,
`Platform.Remote`, `DataDir`, and optional `Sandbox.Proxy`. The environment uses
SDK `EnvBuilder.Proxy` and preserves real HOME while removing inherited auth and
CODEX_HOME. Only default ChatGPT omits explicit CODEX_HOME; separate ChatGPT and
API-key connections use the connection data directory's `codex-home`. No
credential file is inspected. Existing private-home symlinks are rejected and
private directory permissions are restricted. Shared logout is unavailable.
Host V13 integration remains deferred; synthetic proxy forwarding is not proof
of host CONNECT, nesting, filesystem-write or cross-OS sandbox compatibility.

`account/read.account.planType` is stable in both committed versions, so G2 uses
it for the status plan without implementing G4's rate-limit mapping. Email labels
are masked. `requiresOpenaiAuth` is not a signed-out flag: it is also true for
recorded signed-in accounts. Readiness requires the connection's expected account
type. API keys are always applied from initialize secrets in the private home,
replacing any stale private account. Host key changes use a new plugin process;
the old persisted private login is logged out before applying the replacement.
As established by V9, local key acceptance proves configuration only, not valid
remote credentials or spend. The status deliberately says "API key configured
in Codex"; real key verification and model calls were not performed in G2.

Browser/device login returns only the SDK URL action and deliberate challenge
explanation. The plugin never launches a browser itself. A matching nonempty
completion loginId clears its pending attempt; stale or null-ID completions can
trigger account refresh but cannot cancel a newer login. Repeating login cancels
the old attempt. Failed/cancelled starts with no known loginId close the vendor
process to avoid an abandoned flow. Challenges and keys are registered with the
redactor and never logged by connection code.

A real read-only `models-paginated` recording on Codex 0.164.0/macOS arm64 verifies
`includeHidden:true`, `limit:1`, and the actual returned pagination cursors. All
pages are sanitized and indexed. It uses shared account discovery without login,
logout or a model turn. The development recorder's fixed models scenario now
walks this bounded pagination path. Stable request fields are schema-validated
against both 0.122.0 and 0.164.0; identical generated schemas were not regenerated.

The executable `model` slug becomes SDK `Model.ID`, and `isDefault` becomes the
catalog default. The recorder hashes all string fields named `id`, so the opaque
IDs in sanitized G0 catalogs do not prove that real vendor id/model values differ.
Using `model` avoids treating sanitized identifiers as executable model names.
The mapper retains a hidden default, filters unsupported effort values, offers
`max` independently of `xhigh` (V10), and excludes `ultra` delegation. Unsupported
default efforts are omitted; absent inputModalities uses the committed schema's
text/image default. Empty, duplicate, incomplete or ambiguous-default catalogs
fail instead of inventing a fallback model. Pagination has cursor-cycle, page and
model-count bounds. Complete catalogs are cached six hours, with defensive copies,
forced refresh and account/process revision invalidation; failed refresh does not
overwrite a valid complete cache.

Evidence: typed real-payload schema replay, real paginated capture, deterministic
connection/model tests and the self-executing provider fakevendor. The latter
exercises actual production discovery/version/transport/environment and SDK calls,
including an early login completion notification. Session features remain false,
sessions return an explicit unavailable error, and usage remains unknown until
G3/G4. No real interactive login was repeated, no API key was requested, and no
sandbox fallback was enabled.

G2 verification on Go 1.27.3 passed `go test -race ./...`, vet, staticcheck v0.8.2, offline
fixture validation and pure-Go builds (native macOS arm64, cross-build Windows
amd64/Linux arm64). govulncheck found zero reachable vulnerabilities and zero
affected imported packages. It reported two module-only advisories in the
pre-existing transitive `golang.org/x/text v0.14.0`: GO-2026-6629 and
GO-2026-5970. Their affected packages are not imported by this repository; G2
does not introduce a dependency override. No full native cross-OS or real Arvel
sandbox compatibility is claimed by these checks.

A review regression check found that failed login completion originally looked
identical to the previous signed-out status, so both manager and SDK deduplication
could suppress its notification. The fix preserves matched completion success,
emits a safe failure/cancellation message and a distinct pending message on retry.
It preserves authoritative account readiness even if a failed replacement login
leaves an existing account signed in. The manager regression failed before the
fix and passed afterward; a real-transport/SDK fakevendor test proves that two
consecutive failed attempts each emit status_changed. Raw vendor failure text
is never forwarded.


## Post-M0 handoff validation

The follow-up reconciles V13 with the host's accepted sandbox contract at
f32141ba222fc3cdbda66bf935e247d79500f283; it does not implement a private launcher
or grant new sandbox permissions. The regression for stale P9 launcher guidance
failed before the diagnostic fix and passed afterward, also checking that an
unavailable sandbox probe creates no output. Existing offline race, vet,
staticcheck v0.8.2, fixture validation and pure-Go build checks pass.

The first tagged e2e command inadvertently selected the entire package. It passed
in 132.209s and included real shared-account model calls; it completed before any
interrupt signal was sent. These are standalone CLI tests, not M5c integration.
The documented reproduction command now filters exactly
TestAppServer_TransportHandshake, with count=1 and a 45-second suite timeout, to
keep the private-home smoke separate from the broader model scenarios. The
filtered smoke also passed without sign-in or a model request. No resulting
recording is added to the committed fixtures; test temporary state is owned by
the existing harness. Real host/CLI compatibility remains unverified.


G2 native CI initially passed macOS/Linux and exposed two Windows-only test
assumptions in [run 40677198373](https://github.com/arvel-dev/arvel-plugin-chatgpt/actions/runs/40677198373).
SDK EnvBuilder.Proxy represents case-insensitive Windows aliases with one
spelling, so the connection test now compares normalized names and actual proxy,
loopback-exception and log-control values. Provider transport tests now allocate
their temporary directory before registering Shutdown cleanup: testing.T's LIFO
order then stops the owned child before removing its Windows cwd. These are test
portability fixes; production environment/process behavior and SDK code remain
unchanged. The existing G0 environment test already uses normalized names.
Native PR CI, rather than cross-compilation or POSIX directory removal, is the
Windows regression check.

## G3 — Session isolation preflight blocker

**Observed on 2026-10-28, Codex 0.164.0, macOS arm64.** Session runtime remains
unimplemented. The first G3 safety gate rejected the proposed effective-config
preflight and per-name MCP suppression recipe. This is independent of the
previously deferred M5c host sandbox matrix.

The development recorder now has two fixed private-home scenarios:
`config-isolation` and `config-isolation-race`. They create their own disposable
CODEX_HOME and project configuration with harmless marker executables. They do
not use shared authentication, read credential files, sign in, or send a model
turn. Both intentionally return failure rather than certify incomplete isolation.

The committed real `config-isolation-race` capture establishes this sequence:

1. `config/read {cwd, includeLayers:false}` exposes the synthetic global and
   project MCP definitions.
2. The client builds `enabled:false` overrides for those names, plus disabled
   plugin defaults, `features.hooks:false`, `notify:[]` and read-only/native
   sandbox controls.
3. The probe appends a **new** `late_probe` MCP definition to its own project
   config after preflight and before `thread/start`.
4. The real app-server accepts `thread/start`, and the new server executes the
   owned marker binary without an ACP/plugin command approval. The original
   known global/project marker files remain absent at the check. A subsequent
   `config/read` includes the new definition.

The metadata records `mcpAfterPreflightObserved:true`,
`securityGatePassed:false` and `completed:false`. The first value is an actual
owned-file observation by the recorder, not an invented wire notification.
The transcript is real app-server traffic and passes offline schema/correlation
validation. Synthetic helper tests establish the recorder's failure behavior;
they are not substitutes for that real executable observation. Temporary paths
are redacted before persistence and the temporary directory/process tree is
owned and cleaned up by the existing recorder.

This is a configuration read/start race: per-name suppression cannot prevent a
new inherited name from being loaded between the two requests. It also applies
to private login mode because project configuration is independent of
CODEX_HOME. Repeating the same enumeration does not make these operations
atomic. No authenticated model turn, hook execution, plugin-source coverage,
persisted resume, historical-version or native Windows/Linux isolation success
is claimed.

Source inspection at `6aaac694803004a63c6f393d153c96d22521a6b9` agrees with the
observation: `config/src/merge.rs` recursively merges tables, so an empty
`mcp_servers` table does not clear inherited entries. Config loading and
`ConfigManager::read` are separate from thread startup. `hooks` is a stable
feature, but legacy notify is separately passed into `HooksConfig` and plugin
hook sources have their own activation. Disabling only ordinary hooks is not a
complete suppression recipe. The inspected `codex app-server --help` exposes no
ignore-user/project-config flag. LoaderOverrides contains those controls, but
`load_default_config` uses them as an internal fallback; no supported public
atomic exclusion path has been established. Do not turn internal/debug-only
loader controls or malformed-configuration fallback into a production API.

Reproduce with a new output directory (the nonzero exit and explicit race
finding are expected):

```sh
ARVEL_E2E=1 go run ./cmd/record-fixtures record \
  --scenario config-isolation-race --home private \
  --schema testdata/schema/0.164.0 \
  --out /tmp/arvel-g3-isolation-reproduction --timeout 30s
```

G3 Task 1 remains blocked. Per the approved plan, do not enable sessions, proceed
to dependent runtime tasks, mark G3 done, or claim that the host sandbox makes
unassigned MCP execution acceptable. A supported atomic configuration-exclusion
mechanism must be established and verified, or a concrete product/security
contract change must be explicitly approved before the affected implementation.
No SDK, immutable brief, host policy or production provider behavior was changed
for this experiment.

### Follow-up: stable isolation alternatives and a concrete product choice

The owner authorized a further search rather than treating the failed per-name
recipe as proof that every integration is impossible. The immutable brief and
production provider remain unchanged.

Current official documentation was fetched on 2026-10-28:

- [Configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference):
  `projects.<path>.trust_level` skips project `.codex/` configuration, hooks and
  rules when untrusted; MCP enablement is per server.
- [Advanced configuration](https://learn.chatgpt.com/docs/config-file/config-advanced):
  user/system layers still load for an untrusted project; profiles add a layer
  over base user config; CODEX_HOME contains configuration and file-backed
  authentication state together.
- [App-server reference](https://learn.chatgpt.com/docs/app-server): compared with
  the actual default-generated thread start/resume schemas. Documentation is
  current guidance; measured results below identify the exact CLI version.

The following private-home probes use real CLI execution with synthetic owned
user/project configuration. They do not access the person's actual shared home,
perform a login or send a model turn. Nonzero exit remains intentional because
none certifies the complete G3 security gate:

| Candidate | Recording | Observation |
|---|---|---|
| `mcp_servers:{}` | `config-isolation-empty` / 0.164.0 | A known inherited marker executable ran; metadata records `inheritedExecutableObserved:true`. Empty tables do not clear inherited definitions. |
| `mcp_servers:null` | `config-isolation-null` / 0.164.0 | thread/start rejects an empty string where a map is required. Pinned json_to_toml converts JSON null into an empty TOML string. No runnable isolated session results. |
| `mcp_servers:[]` | `config-isolation-array` / 0.164.0 | thread/start rejects a sequence where a map is required. |
| Atomically mark cwd untrusted, then add project MCP | `config-isolation-untrusted-project` / 0.164.0 | thread/start succeeds; the late project marker is absent during the five-second startup observation. This supports project-layer exclusion, not complete user/hook/turn/resume isolation. |
| Untrusted cwd, then add user MCP | `config-isolation-untrusted-user` / 0.164.0 | The late owned user-config marker executes. Project trust does not exclude user configuration. |
| Same user-config race on newer official package | `config-isolation-untrusted-user-0165` / 0.165.0 | The late owned marker again executes. Updating to 0.165.0 alone does not fix the measured recipe. |

The untrusted candidate must enumerate **user-only** config with config/read
without cwd, then set the project trust override. Disabling project-only names
from an earlier cwd-aware read while excluding their underlying project layer
creates transport-less MCP definitions and is rejected. Initial failed attempts
established that detail; the committed untrusted recordings use the corrected
user-only preflight and have accepted thread/start responses.

The official npm 0.165.0 package was installed under a disposable `/tmp` prefix
with install scripts disabled. Its package metadata identifies the native binary
as `vendor/aarch64-apple-darwin/bin/codex`. The initial guessed older layout did
not launch a version probe and generated no capture; the metadata-resolved path
was used for the actual measurements. The user's installed CLI remains 0.164.0.
A fresh private-home default schema generation for 0.165.0 was compared in `/tmp`:
ThreadStartParams, ThreadResumeParams and InitializeParams add no new top-level
fields relative to the committed 0.164.0 schemas. CLI help likewise supplies no
ignore-user/project-config switch. The committed 0.165.0 capture was deliberately
recorded and validated against the committed 0.164.0 schema; its metadata says
`version:0.165.0`, `schemaVersion:0.164.0`. This establishes compatibility of that
specific transcript, not a full version/platform certification or an unchanged
entire schema. No temporary generated schema is promoted to the repository.

Pinned source provides further boundaries:

- The `codex app-server` CLI passes `LoaderOverrides::default()`; public profile
  configuration is additive, not a replacement for the base user file.
- User/project ignore controls and managed-requirements path overrides exist in
  Rust loader APIs/test helpers, but are not thereby public app-server controls.
- `features.plugins:false` is stable in 0.164.0 and can disable plugin activation
  independently of per-plugin names. Ordinary hooks and legacy notify still need
  their respective controls. There is no established general MCP disable flag in
  the inspected stable feature list.
- Managed MCP allowlists can enforce a stronger boundary, but their system/MDM
  sources are administrator-owned. This plugin must not alter `/etc`, spoof
  enterprise policy or use debug-only paths to make a session pass.
- Post-start config validation is too late to prevent already observed startup
  execution. Repeated preflight reads do not make startup atomic.

**Proposed product decision — not yet approved or implemented:** continue G3
agent-session work only for ChatGPT connections with separateLogin:true and for
API-key connections, using plugin-owned private CODEX_HOME plus the project
trust exclusion and verified runtime controls. Do not import/copy shared
credentials or silently turn the user's existing shared login into a private
one. Shared-home connections retain G2 status/models/authentication behavior;
NewSession/LoadSession must explicitly explain that a separate Arvel sign-in is
required. Full start/turn/resume, hooks, managed-policy and historical-version
checks still have to pass before enabling even this candidate in production.
A private home alone is insufficient: the original race was project-scoped.

This changes SPEC's shared-login session behavior and therefore requires the
owner's explicit choice, unlike routine method/field compatibility adaptation.
If shared-login agent sessions must remain mandatory, the verified candidate is
insufficient: keep runtime gated and seek a supported vendor contract that can
exclude or atomically replace inherited executable configuration while retaining
normal authentication. Such a vendor request would need tests for initial
start, resume, newly added user/project names, plugin/hooks, and concurrent
threads; no hypothetical flag or field is currently sent by this plugin. This
follow-up does not assert that all conceivable integration methods are
impossible and does not claim G3 complete.

### Owner approval: private onboarding and SDK settings

The owner approved required sign-in through Arvel or an API key and explicitly
asked to persist this in project rules/context. New ChatGPT connections now
default to private CODEX_HOME, and the manifest no longer offers shared-login
configuration for new connections. Legacy explicit separateLogin:false remains
parsable for G2 status/catalog migration but cannot be used for agent sessions.
No existing credentials are read, copied or silently migrated. An unauthenticated
connection reports needs_login and its public browser/device-code action; the
API-key field remains a required manifest secret supplied by Arvel.

SDK v0.1.2 supplies connections.config/secrets, Status (including Account),
Actions and status_changed. These are host-rendered connection settings/card
contracts, not an arbitrary plugin-owned settings webpage API. No custom UI or
new host protocol is added. The account label now retains a validated masked
local-part prefix/domain so different accounts can be recognized without
exposing their full email or logging identity. The native fakevendor/SDK test
checks new-connection private defaults, signed-out setup, login completion and
host-visible masked account status; account failures/retries retain G2's ordering
and deduplication regressions.

`config-isolation-private` completed a real 0.164.0/macOS arm64 thread/start,
two completed turns and a resume after thread/unsubscribe. It uses a fresh owned
home, atomically untrusted project, disabled hooks/plugins, empty notify and
native read-only/network controls. A new project MCP definition is inserted
after preflight; no global/project/late/notify marker executes. The model backend
is a bounded owned loopback Responses fixture derived from upstream test event
shapes. The metadata explicitly distinguishes real CLI lifecycle behavior from
that synthetic backend. No real account, OpenAI model, cost, other native OS or
M5c host sandbox certification is claimed. The transcript passes the committed
0.164.0 schema/correlation checks. This closes the revised private-home G3
configuration gate, not the rejected shared-home recipe or the entire milestone.

The runtime requirement is conservatively raised to Codex 0.164.0, the version
with the measured private lifecycle and stable hooks/plugins controls used here.
The 0.122.0 finding remains the historical schema-interface floor; it did not
certify these later runtime controls. No claim is made that 0.164.0 is the first
release technically capable of isolation. Older binaries now report outdated
with an actionable minimum rather than enabling unverified session controls.
The SDK version and immutable brief remain unchanged.

G3 mapping/policy reconciliation: ACP Diff represents whole-file old/new text,
not arbitrary unified hunks. Observed additions/deletions can use it directly;
other hunks are retained as textual tool content with path locations, and the
final unified diff stays on the last nested update metadata. Pinned core
`tools/orchestrator.rs` can retry approved commands without its original native
sandbox, so approval evaluation combines possible edit/network authority with
command rules; command allow patterns never override those denials. The plugin
still never sends a native sandbox bypass mode or persistent amendment.

### G3 managed configuration preflight

Codex 0.164.0 stable `configRequirements/read` was recorded in the successful private-home synthetic-backend lifecycle capture. It returns explicit `requirements:null` in the measured unmanaged environment. G3 refuses non-null/missing requirements and managed/unknown config origins rather than interpreting experimental managed fields or overriding administrator controls. User-only `config/read` must expose origins and no inherited MCP/custom provider. Both start and resume receive the same private-home/project-untrusted/hook/plugin/notify/native-sandbox recipe. Cwd must resolve to an existing directory outside (and not containing) private CODEX_HOME. The recording validator now maps this parameterless request to its explicit response schema; offline validation passes.

### G3 session lifecycle and host bridge

The plugin now borrows one generation-scoped connection process for all threads.
Notification queues and early-thread staging are bounded; overflow retires the
generation. Resume reserves the thread route before the vendor RPC, preventing a
duplicate local session from mutating another live thread. Only the exact recorded
`-32600: no rollout found for thread id <id>` condition can replace a locally known
empty thread. Once turn delivery is attempted, replacement is forbidden; ambiguous
prompts are never replayed. ACP identity remains stable across a permitted empty
replacement. SDK idle close uses stable `thread/unsubscribe` (recorded by the
private lifecycle probe); subsequent reload retains the in-memory vendor mapping,
next-turn options and local turn count. In-process recovery descriptors take
precedence over the SDK tombstone's original model/effort metadata; use SetOption
to change a retained conversation. At most 128 conversation descriptors are kept
per provider lifetime, matching the router's bounded capacity. No history/counter
reconstruction across plugin restarts is claimed.

Command escalation evaluates command, edit and network policy together because
pinned vendor source can retry an approved command without its native sandbox.
The host prompt explains this scope; deny wins before any host request. File
approvals require observed item paths, resolved existing ancestors and workspace
scope, with private-home overlap excluded. `grantRoot` alone never proves scope.
An independent `config/read` response establishes the SDK's earlier-notification
callback barrier before file metadata lookup. Native session decisions are used;
there is no plugin approval cache. Permissions profiles remain an empty grant at
runtime; the finite-scope grant encoder exists only in test code. MCP elicitation
remains a method-correct decline. No experimental producer or amendment is enabled.

SDK-wire tests use a synthetic child process to verify early notifications,
stream/final-diff ordering, command/file decision choices, explicit cancel after
actual reply writes, a streaming sibling while the host approval is pending,
process crash/resume without prompt replay, and idle reload preserving changed
reasoning effort and turn budget. These are host-protocol tests, not real account,
OpenAI model, spend, Windows native sandbox or Arvel M5c certification.

G3 review hardening: bounded caches count identifier bytes and path slot overhead,
not only path text. The transport also caps outstanding server requests and
registered notification backlogs before SDK dispatch (count/key/byte bounds),
with generation failure on overflow. This prevents the SDK's independently
scheduled handlers/notification queues from becoming an unbounded buffer in
front of the session router. Regression tests reproduce both pre-fix resource
budget bypasses; focused race tests pass after the fix. SDK-shaped provider
errors also carry the required safe vendorCode; no raw upstream error is exposed.

G3 network reconciliation: pinned stable Config.web_search accepts `disabled`;
core/src/tools/hosted_spec.rs returns no hosted web tool in Disabled mode. Hosted
search otherwise bypasses the native command approval boundary, so ask/deny
network policies now explicitly disable it on both start and resume. The real
0.164.0 private synthetic lifecycle probe records this override and inspects only
the synthetic Responses tool types, refusing any web_search tool in either turn.
Both turns and resume passed; no real OpenAI account or model was used. Network
allow retains the vendor default web mode. Native network approvals remain
method-specific; no unsupported hosted-web permission flow is invented.

G3 authorization-context hardening: successful private logout closes the borrowed
process and retires every thread/native session grant. When account/read observes
a different account principal (type/email, excluding plan-only changes) while a
runtime has been borrowed, the manager recreates the generation before it can
serve sessions for the new account. This leaves failed replacement-login account
preservation intact and requires fresh authorized acquisition on recovery. Both
logout and principal-change regressions were reproduced and verified offline.

## G4 — Usage and typed error contract reconciliation

The existing 0.122.0 and 0.164.0 committed schemas both expose the selected
rate-limit window fields and last/total token counters. The historical interface floor is
0.122.0; the production minimum remains 0.164.0 as established by G3 private-home
isolation (connection/config.go). cacheWriteInputTokens is newer and is not required or mapped to a host
field. No generated schema was modified. Real `limits`, `text` and `failed`
recordings establish the read, push, token and failed-model payloads.

The pinned 0.164.0 source `app-server-protocol/src/protocol/v2/shared.rs`
(`From<CoreCodexErrorInfo>`) maps UsageLimitExceeded, RateLimitExceeded and
FlexUnavailable separately. Only `usageLimitExceeded` is used as definite quota
proof. The following adapter cases are source/schema-derived synthetic coverage,
not live observations of quota exhaustion:

| Vendor terminal evidence | Arvel outcome |
|---|---|
| `usageLimitExceeded` | `rate_limited`, retryable |
| `contextWindowExceeded` | `context_overflow` |
| `unauthorized` | `auth_required` |
| `serverOverloaded`, `rateLimitExceeded`, `flexUnavailable` | `overloaded`, retryable; no quota claim |
| Recorded `other` with structured JSON `error.code=model_not_found` | `model_unavailable`, selected model |
| `sessionBudgetExceeded`, unknown string/object/null or malformed error | safe `provider_error`; no invented turns/USD dimension |

The schema describes account/rateLimits/updated as sparse rolling state. The
provider refetches the historical single-bucket account/rateLimits/read view;
it does not flatten arbitrary metered buckets. Null rolling metadata is not
recovery evidence. primary/secondary are slot IDs; labels follow duration,
including the observed weekly primary. Reset hints require current scoped,
fresh evidence and a unique fully used window with a future reset. Ambiguous
or stale evidence omits both windowId and resetsAt.

SDK v0.1.2 has TurnResult.Usage/Stop, protocol.TurnUsage and time.Time reset
fields, and Host.UsageUpdated already throttles notifications to five seconds.
There are no SDK reasoning/cache-write extensions. The last per-turn counters
are mapped without summing cached or reasoning subsets. Cost remains unknown.
API-key spend uses USD/source unknown; the SDK-required numeric sinceStart zero
is an unknown-source placeholder, never a measured zero. No real API-key
billing or forced live quota failure was observed.


## G5 — Assigned-skill isolation gate (blocked)

### Stable source contract

The pinned Codex 0.164.0 source at
`6aaac694803004a63c6f393d153c96d22521a6b9` does not establish a supported
atomic allowlist of assigned skill roots:

- `ext/skills/src/host_roots.rs`, `roots_from_layer_stack`, adds the real
  user home's `.agents/skills` independently of the private CODEX_HOME.
  `repo_agents_skill_roots` also adds `.agents/skills` between the repository
  root and cwd. Marking project configuration untrusted does not remove those
  skill roots.
- `config/src/skills_config.rs`, `SkillsConfig` and
  `SkillConfigRules::resolve_disabled_paths`, implements exact path or exact
  name rules. A name of `*` is not a wildcard. Disabling enumerated files
  cannot cover a different skill introduced after that inventory.
- `skills.include_instructions = false` controls the automatic instruction
  catalog. `core/src/session/turn.rs`, `build_skills_and_plugins`, still
  obtains the skills snapshot and resolves explicit mentions;
  `skills/src/selection.rs` includes textual and structured selection.
  Hiding the catalog is not an exclusion boundary.

This is a bounded source audit, not proof that every possible future supported
mechanism is impossible. No unverified flag, internal/experimental root API,
HOME redirection or SDK modification is an accepted workaround.

### Measured counterexample

`testdata/fixtures/skills-isolation-linux/` is real Codex 0.164.0 RPC traffic
from Linux arm64 in a fresh disposable `node:22-bookworm-slim` container. The
probe used the container's natural user home, a separate private CODEX_HOME,
owned repository/ancestor/user skill fixtures, and a synthetic loopback
Responses backend. The container's external network was disabled. It did not
mount the person's home, read their configuration/credentials, log in, contact
OpenAI or use a real model/account/spend. This is not the Arvel M5c sandbox.

The preflight `skills/list` enumerates three known unassigned skills. The probe
then creates four new unassigned skills with distinct body-only sentinel text.
Both threads use `approvalPolicy:on-request`, `sandbox:read-only`, untrusted
project/ancestor config, hooks/plugins/delegation disabled, network disabled,
`skills.include_instructions:false`, and exact-path disabled rules for the
original three skills. Neither prompt text nor structured selection contains
any sentinel from a skill body.

The backend reads only bounded synthetic requests in memory. It returns
`UNASSIGNED_SKILL_BODY_OBSERVED` only when the currently expected unique
body sentinel is present in the request. It executes no tools. Four turns
completed and each delivered the corresponding unassigned body:

| Turn | Thread | Selection | Unassigned source |
|---|---|---|---|
| 1 | Root | Textual `$arvel-late-0` | Disposable user `.agents/skills` |
| 2 | Concurrently open sibling | Structured `type:skill` | Project `.agents/skills` |
| 3 | Root, subsequent turn | Textual `$arvel-late-2` | Ancestor `.agents/skills` |
| 4 | Root after unsubscribe/resume | Textual `$arvel-late-3` | Disposable user `.agents/skills` |

The sibling is a second open thread; its turn is not simultaneous with the
root's turn. This suffices to disprove exclusion; it does not certify the full
concurrent-turn behavior matrix. New skills are added after the preflight
inventory but before thread/start, which already disproves the proposed
inventory-based recipe. Post-start watcher races need not be inferred.

Metadata deliberately records `completed:false`, `securityGatePassed:false`,
`unassignedSkillBodyObserved:true` and the four observation scopes. The
recording command exits nonzero after collecting all four counterexamples.
This is a successfully reproduced **negative** result, not successful isolation.
The transcript passes the committed 0.164.0 schema/correlation validator.

### Reproduction safety and continuation

Development-only scenario: `skills-isolation` in `cmd/record-fixtures`.
It requires `ARVEL_E2E=1`, `--home private`, and an explicitly owned disposable
natural home. In a fresh container only, provision
`.arvel-disposable-skill-home` in that natural home with the exact contents
`arvel-owned-disposable-skill-home` followed by a newline; set
`ARVEL_DISPOSABLE_SKILL_HOME` to that same absolute directory. The guard
refuses ordinary homes before executable discovery or output creation. Never
provision this marker in the person's actual home. No production HOME change
is involved.

Build the recorder for the container architecture, install official Codex
0.164.0 into a task-owned directory, provide the committed schemas in a real
directory ending in `0.164.0`, disable the container's external network, then run:

```text
record-fixtures record --scenario skills-isolation --home private
  --codex <absolute-owned-Codex-0.164.0-binary>
  --schema <absolute-schema-directory>/0.164.0
  --out <new-owned-output-directory> --timeout 90s
```

Arguments above form one invocation. On the measured result, expect exit 1
with the fixed unassigned-skill-body diagnostic. A schema-root symlink does
not work with this recorder's WalkDir-based loader; use an actual schema
copy/mount directory. The initial capture used a mount named `schema` and
therefore reported that basename as schemaVersion; it was not retained in the
repository. The committed capture was rerun with the correct schema-directory
basename and includes the final observation metadata.

G5 Task 1 remains blocked and unchecked. At the time of this measurement,
Tasks 2–20 had not started. The owner subsequently authorized independent G5
work; the dependency graph now permits that work without claiming strict skill
exclusion or full G5 completion. Closing the strict-skill guarantee needs an
evidenced supported vendor exclusion/allowlist contract,
or actual host filesystem enforcement that supplies this guarantee. The
existing host-integration deferral does not establish that enforcement or
permit a synthetic Docker probe to substitute for M5c. The G3 successful
configuration-isolation recording was expressly scoped to MCP/hooks/notify and
the lifecycle; it did not certify skill-root isolation. Current production
session setup still lacks this skill-exclusion guarantee. No new G5 capability
or sandbox weakening is enabled by this evidence-only change.


### Release and host dependency reconciliation after the counterexample

On 2026-10-28 the owner prioritized releasing a plugin consumable by the
application and approved continuing independent G5 work. The all-task dependency
on skill isolation was an overly broad plan constraint, not a necessary
technical dependency. It has been removed for independent features; native host
skill activation and full G5 completion remain gated.

Host ADR-14 and sandbox-contract.md were rechecked at revision
`183b2e0ea559a518ec3a84732c35d620c0c72532`: plugin release uses SDK/mockhost
conformance, while M3 and M5c core acceptance use generic echo/fake plugins.
The application therefore has no mandatory dependency on this provider release
for those tests. Actual provider compatibility remains a separate matrix.
No assumption is made that the current M5c scope supplies skill-root masking.
The proposed integration-beta release and any changed skill guarantees await
an explicit owner decision; neither a release waiver nor publication approval
is inferred. See HOST-INTEGRATION.md and the updated G5 plan.


G5 independent-contract work resumed with an exact Codex 0.164.0 schema
regeneration against the committed directory. The existing recorder compared
all generated JSON byte-for-byte and passed on 2026-10-28. No schema drift,
new version floor or new production protocol type is introduced by this check.
Task 2 remains in progress until its inventory/fixture requirements are met.


Owner decision (2026-10-28): no beta/prerelease; target full v0.1.0. The
application agent owns preparation of the generic strict assigned-skill
isolation contract. Independent plugin features and SDK/mockhost tests continue
while that contract is prepared. The preceding beta proposal is superseded.
Strict skill isolation and full G5/G6 completion remain unverified until the
actual contract and applicable evidence exist; sandbox.mode alone is not proof.


## G5 — Read-only discovery adapter

Implemented `internal/discovery` and SDK `plugin.Discoverer` on the provider.
The API is announced by the actual SDK interface; it runs only on an explicit
`_arvel/discover` request. Initialize does not resolve discovery roots, start
Codex, create a private home or authenticate. SDK-wire tests run signed out with
an intentionally missing executable, verify secret-free serialized results,
feature advertisement and source immutability. Native host execution is not
claimed by those offline tests.

The source is the user's default `CODEX_HOME/config.toml` (default
`~/.codex/config.toml`), currently restricted to a directory beneath the actual
user home; the provider rejects its own private state directory as a discovery
root. User skills come from `~/.agents/skills`, project skills only from each
explicit project's `.agents/skills`. No credential file, skill body or environment
secret value is returned. Source secret/header literals become SDK SecretField
markers; local environment references remain names, never getenv lookups.

The production parser is BurntSushi/toml v1.6.0 (pure Go, MIT), tested with the
existing synthetic discovery fixture and its SDK-shaped projection, quoted TOML
tables, multiline literals and header references. yaml.v3 reads skill front
matter only for output metadata; skill bodies are never returned. Parser source
lines are not exposed in errors. Known literal secrets duplicated in public
fields, recognized key formats, sensitive credential flags and HTTP URL
userinfo/query/fragment cause the whole server definition to be omitted.
Opaque user strings are not a general secret-detection oracle. Disabled,
remote-environment and header-helper definitions are not imported.

Limits: 1 MiB per source file, 8 MiB aggregate source/result bytes, 2,000 catalog
entries, 64 requested projects and 256 command arguments per server. Empty kinds
request nothing. Missing source directories/files return empty lists; malformed,
redirected, inaccessible or excessive sources fail with a fixed safe diagnostic.
Symlinked definitions/roots are conservatively rejected rather than followed.
Unix reads use pinned directory descriptors, no-follow openat and nonblocking
file opens before checking regular-file type; Windows uses pinned directory
handles without delete sharing, opening/rejecting reparse points themselves.
Native Windows execution still belongs to the OS CI gate; a cross-build alone
is not that evidence.

The manifest is unchanged: adding a global `~/.agents/skills` read grant would
also expose it to the shared vendor process. Discovery cannot bypass actual host
read denials, and it does not establish native skill-assignment isolation or
change the blocked Tasks 1/7. The application-owned contract must account for
explicit import discovery separately from session execution.


## G5 — Inline image inputs

The real `image-inline-linux` recording uses Codex 0.164.0 in a fresh disposable
Linux arm64 home with external networking disabled. A controlled Responses
backend inspects only bounded synthetic input and returns IMAGE_INPUT_OBSERVED
when the exact owned one-pixel PNG is present as an input_image data URL. One
turn completed. This establishes the stable `UserInput` image/url data-URL path,
not model vision quality, real account behavior, Windows execution or Arvel
sandbox compatibility. Metadata states the synthetic-backend scope explicitly.

The provider now advertises ACP image prompts through SDK Features.PromptImage.
Input accepts PNG/JPEG/GIF after strict base64 decoding and stdlib format/dimension
validation; PNG is live-fixture verified, JPEG/GIF format support is source-backed
by pinned utils/image. Limits are 2 MiB decoded image bytes per prompt, 16 images,
and 40 million pixels per image. The decoded byte limit accommodates Codex's
two echoed userMessage notifications inside the existing 8 MiB ingress/router
budgets. Text-only models fail before delivery/history consumption. URI-only
inputs, URI fetching, arbitrary local paths, audio and embedded context remain
unsupported. Optional URI metadata is not fetched.

Data URLs avoid temporary-image file lifetimes entirely; prompt data stays in
turn-local memory and does not enter Snapshot, logs or host output. SDK-wire
coverage asserts image capability and requires actual synthetic-vendor receipt;
unit coverage checks order, invalid data/MIME/signature, bounds and model modality.
This refines the initial localImage plan using measured stable behavior while
preserving host/native sandbox controls. No vendor API is called directly by
production code; all agent work still passes through Codex app-server.


Discovery review regression: an aliased plugin data directory could evade the
lexical private-state guard. The new alias test failed before correction and
passes after comparing canonical directory identities. No private state is
accepted as a user discovery source through that alias. Full local race/static/
vet/schema checks and six target cross-builds passed for the discovery/media
checkpoint; native Windows CI and actual Arvel compatibility remain pending.

## G5 — Native MCP policy probe checkpoint

Owned Linux recordings with Codex 0.164.0 and a synthetic Responses backend
exercise stdio/HTTP tool delivery, forced prompt approval, decline, deny, allow,
per-tool overrides, empty allowlists and repeated calls. These do not certify
real accounts, native Windows or application sandbox enforcement. The two
failed namespace experiments remain indexed as negative evidence.

Forced `default_tools_approval_mode = "prompt"` asks on each invocation.
The session-persistence experiment still asks twice despite an elicitation
response requesting persistence. Pinned source limits the session approval
cache to automatic approval mode; that mode may silently allow calls and cannot
implement the host's strict ask policy. The bridge consequently offers only
allow-once/reject, never a fabricated persistent grant. MCP requests use native
flattened form parameters and have no dependable item/tool identity; the host
permission identifier is session-local and is not guessed from display text.

Configuration projection, approval and output delivery now have unit and SDK-wire
coverage. Start/resume receive the same assigned definitions; default deny uses
an explicit enabled_tools list, including an empty list, while denied overrides
use disabled_tools. Native aliases are deterministic hashes of host identities;
only the actual host-assigned `arvel` definition gets the trusted-server bypass.
Concurrent sessions retain independent configuration and display-name maps.
SSE and unstable ACP transports are rejected. Raw
app-server stderr is omitted (byte counts only), since MCP children can print
session credentials not known to the process logger. A short unregistered
secret regression failed before this change and now passes. Full `go test ./...`
passes at this checkpoint; it is not final G5 verification.

`mcp-native-startup-failure-linux` records a missing owned executable producing
the stable `mcpServer/startupStatus/updated` failed status. It completes its
synthetic turn without MCP execution or permission requests. Method-specific
schema re-emission covers both this status and native approval forms. Threadless
status is ignored rather than attributed to another session; assigned failures
are delivered once per session generation on its next prompt. Raw vendor error
text is never displayed. Completed MCP result text is capped at 8 KiB and native
failed status is retained, including native isError conversion performed upstream.

The per-session literal filter includes short env/header values, supplied stdio
arguments and query values. Streaming text retains only a bounded raw suffix so
a known literal split across chunks is still removed before SDK delivery. It
also applies the SDK token patterns to display fields; this is not a claim that
arbitrary transformed or encoded secret exfiltration can be detected. Policy
evaluation uses original inputs, then sanitizes permission display fields. The
SDK fake detects duplicate responses and validates method-specific accept/cancel;
tests cover refusal, repeated asks, cancellation with a responsive sibling,
startup deduplication and secret-bearing result/delta payloads.

While subagent delivery is unfinished, the baseline explicitly disables agents,
multi_agent and multi_agent_v2 using the same stable configuration already
recorded in G5 probes. Rejecting host subagent enablement alone would leave
vendor defaults ambiguous. Enabled subagent routing remains unadvertised.


## G5 — Superseding owner decisions (2026-10-29)

The owner explicitly accepts a separate skill catalog for each provider, without
automatically merging providers' catalogs. Workspaces and roles select from the
corresponding provider catalog. External/global vendor
skills potentially visible to Codex. Arvel labels externally managed skills and
must not offer a nonfunctional disable control. Arvel-managed workspace skills
support enable/disable/removal while preserving external originals and other
live copies. Strict exclusion of all unassigned external skills is no longer a
release requirement. This supersedes the earlier blocked isolation decision;
the failed isolation recording remains failed evidence, not a passed guarantee.
Private authentication and filesystem/network/command boundaries are unchanged.
No immutable brief files were edited.

The owner delegated subagent model/effort policy. Configured model and effort
are defaults, with override behavior disclosed. `subagent-owned-linux` proves
one native V2 child inherits the configured model, low effort and parent
instructions, completes before the root's wait/turn completion, and has a
thread/read source identifying the parent and depth 1. The owned model backend
has no external networking or account/model-spend traffic. The override probe
remains completed:false: with model overrides hidden from the tool schema, a
synthetic spawn explicitly selected a different model/high effort successfully.
This corroborates child_config.rs precedence: explicit requests precede defaults.
It disproves strict model binding, not native sandbox/permission inheritance.
The later owner decision adopts defaults; no runtime producer is advertised
until child routing and SDK-wire verification are finished.

## G5 — Windows preparation checkpoint

The stable parameterless windowsSandbox/readiness response is recorded on Linux
and schema-replayed; that is not native Windows execution evidence. Windows
status processing reads readiness only after authentication, maps notConfigured
and updateRequired to safe degraded guidance, and treats errors/unknown values
as degraded. It never calls setup/start. The existing operation gate serializes
refreshes and the normal status-dedup path remains in use. Upstream readiness
may refresh an existing registered Core installation, as documented in pinned
request_processors/windows_sandbox_processor.rs; it is not strictly read-only.

Owned Windows-only tests cover standalone exe and npm-style cmd initialization,
version lookup, paths with spaces/ampersands, SDK argument rejection/quoting, and
Job Object termination of a spawned child. These test binaries cross-compile;
no native pass is claimed. The existing windows-latest matrix includes them
through go test -race ./.... Task 14/15 native acceptance remains open.


## G5 — SDK v0.1.3 reconciliation (2026-10-29)

The owner requested checking the updated SDK. Published v0.1.3 resolves to
92ac2e29b4da68bf8300fa1d641eeca29134c678 (the local SDK worktree was clean).
Go remains >=1.27.2, pluginApi remains 1, and Go/wire types are unchanged.
PROTOCOL §7.7 now explicitly requires a separate shared catalogue per provider,
with host-local provenance/ownership and enabled selections resolved into the
existing SkillRef list. Manual invocation is independent of enable/disable.
No new catalogue wire fields are introduced. This matches the owner's decision;
external/global CLI skills may remain visible with honest controls/disclosure.
The plugin dependency, manifest schema and release checker now pin v0.1.3.
The inspected application's host/go.mod still pins v0.1.2; host files were not
edited here, and wire compatibility does not depend on simultaneous upgrades.

Materialize now preserves all occupied names, including marked copies, and uses
a unique SDK copy-owner token to guard cleanup. The former independent-cleanup
reproducer now verifies the later copy survives; no stale-marker replacement is
assumed. The provider retains a catalog lease for identical ordered assignments
and rejects conflicting simultaneous managed selections in a shared cwd. It
still delegates actual copies, collision aliases, git excludes and ordinary
cleanup to the SDK. Codex-specific native YAML policy and native skill names are
applied only to those owned copies; external originals are untouched.

A coordination handle serializes this provider's workspace leases across its
connections/processes. Abandoned copies are never reclaimed automatically: an exclusive provider lock
does not prove that an orphaned CLI task has stopped. Their journal blocks
replacement until original cleanup or explicit host recovery. A marker alone
is never proof; conflicting/abandoned catalogs require a separate task directory. One small coordination inode persists in git metadata (or .agents for a
non-git directory), preventing lock-file unlink races; the OS handle is released
at last close. Native copies and bounded source-staging directories are removed.
Staging stays inside plugin data storage, reads only validated assigned sources,
rejects symlinks/special files and credential-home sources, and is bounded by
2000 entries/64 MiB. Sources are hashed before/after copying and are never edited.
SDK-managed excludes can survive a hard provider crash; recovery does not rewrite
unrelated user git metadata or claim abandoned-copy cleanup without proof.

`skills-managed-linux` was re-recorded with SDK v0.1.3 (metadata states sdkVersion)
and real Codex 0.164.0 against the owned Linux synthetic backend: a renamed manual
copy is discovered under its native alias, omitted from an implicit turn, loaded
by an explicit stable skill input, retained after the first shared lease closes,
and removed after the last lease closes. Source and repository collision remain.
The SDK wire test also exercises skill delivery across idle reload and app-server
crash recovery, preserving original source contents and conversation identity.
Full go test ./... passed after the SDK update; this is not native Windows or
full G5 acceptance. Ordinary explicit skill mentions are translated to the exact
owned native path, including collision aliases.

`subagent-owned-capacity-linux`, also recorded with SDK v0.1.3, holds the first
child's owned model request while the parent attempts a second spawn. Capacity
two admits the root plus one active child; the second child does not start.
The first child is then released, wait completes and root completion follows.
This measures active concurrency, not a cumulative spawn count or max_depth.


### G5 subagent delivery and final hardening (2026-10-29)

The production adapter uses stable V2 with V1 disabled. `maxParallel` (default
one, maximum sixteen) becomes native root-inclusive capacity `limit + 1`.
Owned `subagent-owned-capacity-linux` records the second concurrent child being
refused when root plus one child fills capacity. Model/effort remain defaults,
not immutable limits, under the owner's decision.

The real `subagent-owned-role-linux` fixture proves description-only native
role declarations retain the parent's developer instructions. Native role
config files otherwise replace those instructions (`core/src/agent/role.rs`).
The adapter shadows default/explorer/worker without config files and refuses
inherited role config files or replacement subagent developer instructions,
including aggregated agents/features origin keys that can conceal nested settings.
Native child configuration reapplies the parent's sandbox, approvals and cwd.

Generation-local child ownership requires both activity attribution and stable
`thread/read` source identity, exact effective cwd and parent/depth. Early
notifications remain bounded and undisclosed until proof. Each child has its
own turn identity, file-path cache, mapper and prefixed host tool IDs. Every
child update carries SDK SubagentMeta, including buffered text and diffs;
root usage and turn accounting remain separate. An acknowledged event reader
prevents prefetched root completion overtaking newly attributed child output.
Parent completion/cancellation drains permissions and interrupts active children;
unresponsive cleanup aborts the owned app-server generation. Unknown or
conflicting ownership never receives host permission. The general routing
budget remains 128 total retained threads; this can fail closed before native
capacity on long sessions with many historical children.

Offline session and actual SDK-transport tests cover early output, colliding
native tool IDs in two children, foreign-source rejection, child approval
cancellation with a responsive sibling, idle reload, and parent completion
while a child is active. Regression checks reproduced and fixed stale parent
activity reviving a completed child and connection-key fragments escaping
per-message redaction. The immutable session sanitizer now combines MCP and
connection credentials before mapping parent/child streams. Neither test uses
real accounts, model billing or the production Arvel launcher.

Windows-only tests cover the SDK's standalone executable and npm-style shim,
quoted arguments, initialization and Job Object process-tree termination. CI
also captures stable readiness using pinned Codex 0.164.0 in a fresh private
home, without login or setup/start. These tests are prepared, not certified
native passes. The prior Windows job 122881873396 on run 40707049140 has zero
steps; its GitHub annotation explicitly reports failed account payments or a
spending limit. A fresh rerun (attempt 2) repeats that exact allocation failure
on Windows job 123029541419 and Linux job 123029540965, with zero steps; this
rerun checks current runner availability at the older G4 revision, not G5 code. Cross-builds and the Linux readiness fixture do not replace
native Windows acceptance. G5 remains open until the required current-revision
native CI evidence is obtained.


## Owner acceptance and post-release native checks (2026-10-29)

The owner accepts G5 as complete on the verified local implementation and
explicitly moves native Windows/CI verification to after the v0.1.0 release.
This overrides the earlier pre-release native-CI completion requirement.
Prepared native tests remain in place; no native result is relabeled PASS.
G6 must not reintroduce this deferred check as a pre-release acceptance gate.
Other release-conformance requirements, private authorization, host sandbox
guarantees, and separate merge/publication approval remain unchanged.

G5 is accepted complete on branch feat/g5-host-tools-media, implementation
checkpoint c4f155b. Native CI run 40751856106 still has no executed steps;
this decision does not change its infrastructure-blocked result. The separate
post-release backlog records readiness, executable/shim and process-tree
checks. No beta, merge or publication is authorized by this acceptance.

## G6 — SDK v0.2.0 and configured release conformance

Released SDK v0.2.0 resolves to 97ffa3e0347293f0736554e3b412eafea08bd042.
Compared with v0.1.3, production plugin/protocol APIs, manifest schema,
Materialize ownership and Go >=1.27.2 are unchanged. Its checker adds --test-env,
explicit connection inputs, isolated data/workspace/home directories, prepared
startup environment and strict active cancel/busy probes. This repository now
pins v0.2.0 coherently; historical fixture sdkVersion fields remain unchanged.

The old TestProvider_FakeVendorThroughSDK unconditionally expected session
creation to fail. v0.1.3's cwd=/ masked that G2-era assertion; the v0.2.0 mockhost
uses a valid temporary workspace, so an authorized API-key session correctly
succeeds. The corrected test completes an API-key turn and retains typed session
denial for deliberate legacy shared-login status-only mode. No production
connection validation or private authentication was weakened.

The standalone offline fake models the recorded stable app-server interface
and accepts synthetic public login requests. The production executable uses
manifest codexPath, without ARVEL_DEV or a test-only runtime path. Its
connection environment continues dropping arbitrary inherited fixture-mode
variables. Concurrency/hold modes therefore use explicit synthetic prompt text.
Held turns emit more than one mapped update because the mapper deliberately
retains a trailing update and the sanitizer retains a bounded secret suffix.
Proxy checks use complete agentMessage items so the SDK's per-message NAME=VALUE
collector receives whole lines after production redaction, never a forged
startup proxy value.

The first full local configured SDK release run returned 77 PASS, 0 FAIL,
0 SKIP, including an actual overlapping two-thread barrier, active cancel/busy
and unpredictable proxy forwarding. These are synthetic host-interface checks,
not new real-Codex or model measurements. Final verification results are kept
with the local milestone reports.

The SDK YAML collector omits top-level session notification metadata, so child
attribution remains explicitly asserted by the existing raw SDK-wire tests.
Scenario initialization does not expose Platform.Remote; device-code behavior
remains covered by existing remote-login tests. No nonexistent action ID or
custom SDK field was added to work around these limits.

Schema-drift protects 40 production methods/notifications and explicit response
roots, resolving methods semantically and refs locally. It compares the full
reachable baseline contract conservatively. Historical byte-exact schema
reproducibility and fixture schema overrides remain intact; neither a schema
guard nor historical replay alone certifies latest runtime compatibility.

The added production-surface.json is an inventory array, not a generated schema.
A regression first reproduced the offline loader compiling it as JSON Schema.
The existing loader, schema-count test and byte-reproducibility file inventory
now exclude that exact metadata filename, like surface.json; all 320 generated
baseline schemas and historical fixture validation remain enforced.

Final configured release conformance after adding model switching and explicit
file denial returned 81 PASS, 0 FAIL, 0 SKIP across 19 scenarios and the built-in
release probes. The complete local race/static/fixture/discovery checks pass.
Latest npm lookup returned Codex 0.165.0; its freshly generated default schema
passed the 40-method guard locally. This adds schema-only evidence for 0.165.0,
not real-account/runtime support. The weekly/manual remote job still needs to
execute after the workflow reaches GitHub.

GoReleaser v2.19.0 (download checked against its published archive checksum)
validated the config and built a nonpublishing snapshot for all six targets.
Each archive contains only the correct provider binary, LICENSE, NOTICE and
the identical versioned manifest; seven SHA-256 checksum entries passed. With
no existing repository tags, this local snapshot is 0.0.0-SNAPSHOT-2eb5771; it
is not v0.1.0 and no tag, draft or publication was created. Full native matrix
acceptance remains post-release; runner availability must be checked on delivery.

Final review added regression checks for malformed synthetic MCP config,
checker-report streaming limits (including io.Copy's optimized interfaces),
cleanup error propagation and ordered schema tuples. Generic array equality
now preserves tuple/const/default positions; unordered matching is limited
to explicit schema sets/unions. The tuple regression failed before that fix
and the same check, full race/static checks and 0.165.0 comparison pass afterward.

## v0.1.0 release delivery override (2026-10-29)

The owner explicitly authorized releasing without waiting for CI, including
local assembly, tag creation and publication without further confirmations.
This supersedes the blocked remote-run/delivery requirement for this release.
The actual runner allocation failures remain infrastructure-blocked evidence;
no CI or native result is relabeled PASS. Use the locally verified source,
81/0/0 production-binary conformance and local 0.165.0 schema guard, and build
all release assets with the pinned GoReleaser v2.19.0 configuration. Upload
only its unmodified generated archives/checksums/manifest. Native verification
remains in the accepted post-release backlog, and the normal CI workflows stay
available for later releases. Real host M5c certification and interactive
browser/device-code/approval checks remain separate from synthetic conformance.

## Cross-process resume repair (2026-10-29)

Arvel's native darwin/arm64 integration with published v0.1.0 and Codex
0.164.0 reproduced a failure on the first prompt after restarting the provider:
`Codex returned inconsistent turn identities`. Loading the native thread itself
succeeded. A fresh provider process had no LastTurn snapshot and discarded the
terminal turn identities returned by stable `thread/resume`. Delayed historical
events could therefore bind the new turn to an old identity.

The provider now restores bounded terminal turn identities from that response
and excludes their delayed text, lifecycle and usage events from the next turn.
Unknown or inconsistent identities still fail closed. No SDK wire extension,
credential migration or vendor-sandbox bypass is involved. The regression first
failed for historical delta, lifecycle and usage cases, then passed after the
repair. A typed response test replays the existing sanitized resume fixture.
Freshly regenerated 0.164.0 schemas pass the production-surface drift guard
without changing the committed baseline.

A local 0.1.1 candidate from `fix/session-resume-turn-identity` was explicitly
installed through Arvel's normal checksum-verifying installer, retaining the
dedicated test connection's own login. The sequence new → prompt → provider
restart → load → prompt → cancel passed, including suppression of historical
feed updates. This is local candidate evidence, not a published 0.1.1 release
or native Linux/Windows certification. Full race, vet and static checks pass.
Offline release conformance passes 81 checks with no failures or skips; an
earlier invocation timed out during initialize and was rerun unchanged.
