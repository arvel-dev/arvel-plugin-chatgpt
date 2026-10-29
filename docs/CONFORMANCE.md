# Provider conformance and schema drift

## Offline release checks

Run from the repository root with Go 1.27.2 or newer:

```sh
go run ./cmd/check-conformance --report /tmp/chatgpt-conformance.json
```

The runner builds the production provider, a separate `fake-codex` and the
checker selected by the pinned SDK v0.2.0 module. All three use CGO-free builds.
It prepares a temporary copy of `conformance-env.yaml` and `conformance/*.yaml`,
inserting the absolute fake `codexPath` and synthetic skill source. The checked-in
environment file is a template; it is not a ready connection to a real CLI.
The runner then invokes:

```text
arvel-plugin-check --json --level release --manifest <manifest> \
  --scenarios <prepared scenarios> --test-env <prepared environment> -- <provider>
```

The SDK generates separate private data/workspace/home directories per check.
Explicit fixture precedence is SDK defaults, root environment, named check,
scenario override, then the checker-controlled canary and proxy. Changing a
connection kind clears inherited config/secrets; generated ChatGPT scenarios
repeat the fake path and perform their own synthetic public login.

No real vendor program, credential files, user account, model API or MCP server
is accessed. The fake begins unauthenticated and accepts the synthetic test key
through the same public app-server login method used by production. Its protocol
responses model the stable recorded interface; they are not new vendor evidence.
No production development override or authentication shortcut is installed.

The fixture is self-contained and can run from an arbitrary generated data
cwd. Fixture modes use prompt text because the production environment allow-list
correctly drops arbitrary inherited control variables. The fake never executes
command text or supplied MCP endpoints. The concurrency mode uses a two-thread
barrier; cancel/busy modes stream updates and stay active until interruption.
Proxy mode emits complete message items with only the relevant child's actual
proxy variables, allowing the checker to verify its unpredictable injected proxy.

| Coverage | Evidence |
|---|---|
| Initialization, status, models, shutdown, canary | SDK basic checks with explicit API-key fixture |
| Streaming, resume, model/effort options | Release scenarios; idle/crash recovery also has SDK-wire tests |
| Private login/logout, API-key isolation | Release scenarios and existing credential stripping/login tests |
| Command/file approval choices and deny | Release scenarios plus permission-count and cancellation SDK-wire tests |
| MCP policies, skills and images | Release scenarios; ownership and no-execution boundaries also have dedicated tests |
| Child output | Release scenario plus raw SDK-wire attribution/ownership tests |
| Usage, typed quota stop, resume | Release scenarios plus usage/notification tests |
| Concurrency, active cancellation/busy, proxy | Dedicated required checker probes |

The runner requires PASS for every returned assertion and all mandatory probes;
a SKIP or an empty scenario suite is an error. A nonzero checker exit, malformed
report, or teardown failure cannot be converted into success. Only the SDK's
sanitized report is exported. Raw captured RPC, config maps and environment maps
must not be added to diagnostics. Owned processes finish before temporary
resources are removed; unconfirmed teardown retains scratch resources.

The SDK scenario collector stores update objects without their top-level
notification metadata. Child attribution is therefore asserted in the existing
raw SDK-wire tests, not inferred from a YAML text assertion. Device-code login
is selected by `InitMeta.Platform.Remote`, which this version's scenario
initialization does not expose; existing remote-login wire tests cover that
branch. No fictitious `login-device` action is added.

A release conformance PASS proves the provider's SDK interface against synthetic
dependencies. It does not certify real OpenAI account/model behavior, spend,
Windows native execution or the production Arvel M5c sandbox.

## Schema drift

```sh
go run ./cmd/schema-drift --baseline testdata/schema/0.164.0 \
  --candidate /absolute/generated/schema \
  --surface testdata/schema/0.164.0/production-surface.json
```

The versioned inventory selects all 40 production methods/notifications and
explicit response roots. Methods are found by semantic name, never a remembered
`oneOf` array index. Local references and cycles are resolved without network
access. The guard checks the complete reachable baseline contract, including
optional properties; it may flag a change in a field this plugin does not use.

Descriptions, titles, union order, equivalent definition names, additional
optional properties and compatible enum/union alternatives are allowed.
Removed methods/properties/alternatives, changed types/nullability/constraints,
changed required guarantees, new required fields and ambiguous union matching
require review. Malformed schemas and unresolved/external references fail input
validation. The comparator is conservative; it is not a proof of general JSON
Schema inclusion or runtime compatibility.

Output is sorted JSON findings (`method`, `path`, `reason`). Exit codes are 0
for compatible, 1 for structural drift and 2 for invalid input. Test the guard
without a CLI or network by comparing the recorded baseline with itself.

The `schema-drift` workflow runs weekly and on manual dispatch. It installs the
latest Codex into runner temporary storage, records the actual version, generates
its default stable schema and uploads candidate/version/report even on failure.
It does not log in, make model calls, enable experimental APIs, overwrite the
baseline or automatically claim support for a newer CLI. Historical fixture
schema overrides remain unchanged; replaying those fixtures alone does not
prove compatibility with a latest schema.

## Release and deferred native checks

Release quality requires Linux vet/staticcheck/race, recorded fixture/discovery
checks and the real production-binary conformance runner. Packaging produces
six CGO-free archives plus checksums and a versioned manifest, including licenses.
CI creates a draft; merge and publication each retain their separate approval.

The full macOS/Linux/Windows CI matrix remains available for PR/manual runs.
The owner explicitly deferred native Windows/CI acceptance until after v0.1.0.
Cross-builds and synthetic recordings are not native passes. Historical GitHub
billing/runner allocation failures must be rechecked for actual delivery.
Real host-sandbox certification remains subject to [host integration](HOST-INTEGRATION.md).
