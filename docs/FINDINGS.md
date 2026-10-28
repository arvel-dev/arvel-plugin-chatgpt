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
