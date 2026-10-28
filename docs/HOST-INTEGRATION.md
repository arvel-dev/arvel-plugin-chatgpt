# Host integration handoff

Plugin development continues against the released SDK and mock host. Completing
Arvel M0 does not provide the production launcher needed for this plugin's V13
sandbox compatibility measurements. The actual boundary is implemented in host
M5c, independently of provider names or releases.

## Evidence and authority

Inspected on 2026-10-28:

- Local host branch: `feat/m0-foundation`, revision
  `f32141ba222fc3cdbda66bf935e247d79500f283`.
- Shared SDK: `github.com/arvel-dev/arvel-sdk v0.1.2` in both repositories.
- Owner-approved [host sandbox contract at the inspected revision](https://github.com/arvel-dev/arvel/blob/f32141ba222fc3cdbda66bf935e247d79500f283/docs/sandbox-contract.md),
  especially “Independent acceptance stages” and “Handoff for a plugin repository”.
- [Host ADR-14 at the inspected revision](https://github.com/arvel-dev/arvel/blob/f32141ba222fc3cdbda66bf935e247d79500f283/docs/adr/0014-provider-independent-sandbox-acceptance.md)
  assigns generic OS feasibility to M0 and production enforcement to M5c.
- Host `host/` contains foundation lifecycle/RPC/platform code; its M0 labs are
  explicitly excluded from provider launching. They are not a stable executable
  sandbox API.

The owner reports M0 complete locally, with GitHub checks/merge remaining. That
report does not change which runtime interface exists. Host PR/CI completion and
plugin compatibility are separate acceptance stages; neither a milestone label
nor a green synthetic probe certifies real CLI compatibility. No host files were
modified for this handoff.

The immutable plugin SPEC V13 originally refers to the “app spike P9”. This
reference now identifies the historical dependency, not a launcher interface.
The accepted host contract resolves that factual drift while preserving the
plugin's security requirements and conditional fallback gate. No protocol field
or SDK release is required for this reconciliation.

## Work that can continue now

| Stage | Plugin responsibility | Gate |
|---|---|---|
| Independent development | Implement G0–G6 against pinned SDK types, committed Codex schemas/fixtures and fakevendor/mockhost. | Applicable unit, schema, race and static checks. |
| Plugin release | Complete G6 release conformance and disclose remaining integration limits. | Applicable `arvel-plugin-check` release suite and repository release workflow; M0/M5c completion is not a prerequisite. |
| Actual host/plugin compatibility | Run this plugin and its real CLI under the production M5c launcher/proxy. | Pinned revisions, native per-OS evidence and the matrix below. |

G2 supplies lazy process ownership, isolated authentication, model discovery and
proxy environment forwarding. Agent sessions/approvals are still G3 and usage is
G4; their tests must not be claimed before implementation. Existing synthetic
proxy tests establish environment construction only. The gated real CLI smoke
uses a fresh private CODEX_HOME and no sign-in, network model call or credential
file access; it establishes standalone transport/account isolation, not V13.

## Shared interface

Use the actual pinned SDK contract. The host validates manifest declarations and
supplies `InitMeta.Sandbox.Mode/Proxy`; the plugin forwards the SDK proxy
environment to its CLI. Vendor paths/hosts/keychain requests remain manifest
data. Vendor command lines, authentication and recovery belong to this plugin.
No host registry entry, hardcoded provider preset or new host exception is needed.

M0 Seatbelt/bubblewrap fixtures and manually assembled Docker profiles must not
be adapted into an unofficial plugin-launch API. Once M5c provides the real
launcher, use its documented production path without copying host enforcement
into the plugin or patching the SDK to bypass isolation.

## Required V13 evidence after M5c

Every run must identify host revision, plugin revision, SDK version, Codex version,
OS/architecture, actual isolation mode, connection kind and shared/private home
selection. Use synthetic files and owned endpoints for denial checks. Account
steps require the user's interactive sign-in or supplied test credentials; never
read, copy or log credential files. Store sanitized evidence in plugin-owned
fixtures/FINDINGS rather than altering host policy to accommodate a vendor.

| Native target | Host boundary | Required observations | Current V13 result |
|---|---|---|---|
| macOS | Actual M5c `os` launcher and egress proxy | Plugin + descendant CLI startup, declared state permissions, denied outside paths/credential/socket access, approved/refused CONNECT, direct egress denial and native Codex sandbox nesting/workspace-write. | Not run; M0's generic Seatbelt lab is not this boundary. |
| Linux | Actual M5c `os` or `container` launcher and egress proxy | The same file/network/descendant/nesting checks, plus namespace/LSM preflight and real CLI HTTPS through the actual proxy. | Not run; synthetic bubblewrap or standalone Docker is insufficient. |
| Windows | Actual M5c container boundary, when available | Declared state, denied paths, descendants, approved/refused proxy traffic and real CLI sandbox behavior under the actual container launcher. | Not run; the architectural container choice is not an implementation. |
| Any supported OS | Explicit host `none` mode | Report the unisolated host mode honestly and retain the vendor sandbox. | Never qualifies for a vendor-sandbox fallback. |

Within each implemented host mode, test the default ChatGPT connection,
separate-login ChatGPT connection and API-key private home where supported.
Record allow and deny outcomes, not just successful startup or a successful API
request. Keep real authentication/model/API measurements distinct from synthetic
keys, mockhost outputs and schema-only evidence. Native cross-OS runs cannot be
replaced by cross-compilation.

## Conditional nested-sandbox fallback

Keep Codex's native sandbox enabled. A fallback can be considered only after a
reproduced nesting failure on that OS inside the actual effective M5c `os` or
`container` boundary, with file/network isolation and approval enforcement still
verified. Mode `none` never qualifies. This handoff enables no bypass and makes
no real-host compatibility claim. Failed historical M0 vendor experiments are
not proof that the production boundary needs a fallback.

## Reproduction available today

```sh
go test -race ./...
go vet ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...
go run ./cmd/record-fixtures verify
ARVEL_E2E=1 go test -tags e2e ./e2e/... -run '^TestAppServer_TransportHandshake$' -count=1 -timeout 45s
```

The first four checks are offline. The anchored test filter is intentional: other
tests in the e2e package use shared authentication and perform real model calls.
The last command starts the installed real Codex with
a temporary private home and requires explicit real-probe opt-in. The
`record-fixtures sandbox` command deliberately fails before executable discovery
or output creation while the production launcher/native target is unavailable;
its diagnostic identifies M5c and this handoff. This command is an explicit
unavailable stub, not automatic host-launcher detection. Implement its real
adapter only against the documented M5c invocation/lifecycle once that interface
exists. Adding M0 completion evidence
alone must not turn that unavailable probe into a reported pass.
