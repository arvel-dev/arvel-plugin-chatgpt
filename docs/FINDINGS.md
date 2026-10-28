# Bootstrap Findings

G0 bootstrap evidence is complete under the approved plugin-first scope; the
plugin is not release-ready. The SDK scaffold, recording tools and
local evidence are implemented. Headless Linux device-code sign-in is verified. The user explicitly deferred
Arvel sandbox/P9 measurements to future host integration so plugin development
can proceed first. The production provider
intentionally reports an incomplete bootstrap; agent execution starts in later
milestones.

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
