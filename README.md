# ChatGPT for Arvel

[![CI](https://github.com/arvel-dev/arvel-plugin-chatgpt/actions/workflows/ci.yml/badge.svg)](https://github.com/arvel-dev/arvel-plugin-chatgpt/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/arvel-dev/arvel-plugin-chatgpt)](https://github.com/arvel-dev/arvel-plugin-chatgpt/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

This provider plugin lets [Arvel](https://github.com/arvel-dev/arvel) roles run on **OpenAI models**. It works through the official **Codex CLI** and its app-server, the integration surface OpenAI provides for apps like this one. You can pay with your **ChatGPT plan** or an **OpenAI API key**.

Manage the plugin in Arvel’s **Settings → Plugins**.

---

## What you need

| | |
|---|---|
| Codex CLI (`codex`) | see [Compatibility](#compatibility) for the minimum version |
| Arvel | 1.0 or newer (plugin API 1) |
| OS | macOS, Linux and Windows on x64 and arm64. It also runs on a Linux server that Arvel connects to. |

Install the CLI:

```bash
npm i -g @openai/codex          # macOS, Linux, Windows
# or on macOS: brew install --cask codex
```

## Two ways to connect

| Connection | Billing | What Arvel shows |
|---|---|---|
| **ChatGPT plan (via Codex CLI)** | Your ChatGPT Plus / Pro / Business / Enterprise / Edu plan | Plan windows (e.g. 5-hour and weekly) with reset times |
| **OpenAI API key** | Pay per token on your OpenAI Platform account | Token usage per task |

## Signing in

**ChatGPT plan, on your computer.** After adding the connection, click **Sign in** in Arvel's plugin settings. Complete the browser flow; Arvel then shows **Ready** and a masked account label. Each connection uses its own Codex login, separate from your terminal login.

**ChatGPT plan, on a server.** Arvel shows a link and a short code. Open the link on any device, sign in and enter the code. No browser is needed on the server.

**API key.** Enter the key in the connection's secret field in Arvel settings. Until a key is supplied, the connection reports **Sign in required** with setup guidance. The key is applied through Codex's public login API inside the private connection directory.

Arvel renders the connection configuration, secret fields, authorization status,
account summary and login/logout actions provided by the SDK. The plugin does
not create a separate settings webpage or request credentials through chat.
It never reads stored Codex tokens or copies your personal login. Both connection
types use `codex-home` inside the connection's plugin data directory; inherited
`CODEX_HOME` and authentication environment variables are stripped.

**Migration:** legacy shared-login connections can still report their connection
status, but cannot start agent sessions. Recreate the connection and sign in for
Arvel, or provide an API key. Signing out affects only the private Arvel login;
your shared terminal login remains untouched. Signing out stops active Arvel
sessions; switching the account also retires the old session authorization context.

Codex accepts an API key locally when it configures the connection. A **Ready**
status means the key is configured, not that OpenAI has validated it remotely.
Replacing a key restarts the connection with the new host-supplied secret and
replaces the previous private Codex login.

## Models and reasoning effort

The model list comes from Codex (`model/list`), together with the reasoning levels each model supports.

| Arvel | Codex |
|---|---|
| Auto | the model's default effort |
| Low · Medium · High | `low` · `medium` · `high` |
| Extra high | `xhigh`, when offered |
| Max | `max`, when offered; automatic task delegation (`ultra`) is excluded |

The catalog is cached for six hours. Refresh bypasses the cache; account changes
and app-server restarts invalidate it. Hidden models are omitted unless they are
the account default. Connection changes are pushed to Arvel, with an additional
account check every 30 seconds to detect sign-in changes made outside Arvel.

Model and effort changes apply from the next message. Sessions can resume after
a process restart or an idle close without replaying history into the live feed.
In-process recovery retains the selected options and local turn budget; these
local counters are not reconstructed across plugin restarts.

## Permissions and safety

Codex starts every session with its native read-only or workspace-write sandbox.
Arvel's host sandbox remains in force. The plugin maps role rules onto the native
baseline and the approval requests Codex emits:

| Role rule | What happens |
|---|---|
| Edit files: **Yes** | Codex may write inside the task folder without asking |
| Edit files: **Ask** / **No** | Read-only sandbox; every change is asked / refused |
| Internet: **Yes** | Network allowed inside the sandbox |
| Internet: **Ask** / **No** | Network off inside the sandbox; hosted web search disabled because it has no supported approval callback |
| Commands | Codex asks before commands outside its trusted set. The plugin evaluates the exact supplied command; deny takes precedence. A sandbox escalation also checks file-write and network rules before asking the host. |
| MCP tools | Only assigned stdio/HTTP servers are configured. Native per-tool rules apply allow/ask/deny before execution; Arvel's assigned `arvel` server is allowed. Ask offers **Allow once** or **Reject** for each invocation. |

The plugin never selects an unrestricted sandbox, disables approvals, or saves
permanent allow rules in Codex configuration. Codex may retry an approved command
outside its native sandbox; the host permission request describes that scope, and
Arvel's host sandbox still applies. "Allow for session" uses Codex's native
conversation-scoped decision and does not transfer to a replacement thread.

MCP startup failures appear in the session feed on its next turn. MCP tool text
is bounded; supplied environment, argument and header values are redacted from
displayed output. SSE is unsupported. Raw vendor stderr is omitted from logs.

Limitation: Codex can always read files inside the task folder, so the *Read files: No* rule cannot be enforced with this provider. Arvel warns you about this when you assign it.

## Skills and MCP servers

Host-delivered MCP servers support stdio and HTTP with native per-tool rules.
Managed skills are copied through the pinned SDK and support automatic or manual
invocation. Subagents inherit task instructions and permissions, with a bounded native
concurrency limit. Model and effort settings are defaults that native spawn
requests can override. Child output and approvals stay attributed to the child;
parent completion and cancellation stop remaining children.
Image prompts accept
PNG, JPEG and GIF data for models that support images: up to 2 MiB of decoded
image data per prompt, 16 images and 40 million pixels per image. Images stay in
memory; supplied image URLs are never fetched. Audio and embedded resource
prompts remain unsupported. Agent sessions use
a private Codex home, exclude project `.codex` configuration through an untrusted
project override, and disable inherited hooks, plugins and notifications. Inherited
MCP definitions, custom model providers and managed/unknown configuration layers
are rejected before a thread starts or resumes. Personal or project skill discovery
outside that configuration mechanism remains available according to Codex behavior.

Arvel maintains a separate skill catalog for each provider; spaces and roles
select from that provider's catalog. Turning off an Arvel copy affects subsequent
sessions, with existing copies retained until their tasks close. It does not
turn off an independently available external/global original or erase instructions
already present in conversation history. Unsupported global exclusion has no
working toggle. Source folders and other live copies are preserved. Different
simultaneous managed selections require separate working directories; this
provider rejects conflicting selections in a shared directory.

On request, Arvel can discover MCP definitions in your default Codex
`config.toml` and skills in `~/.agents/skills` or the requested projects'
`.agents/skills` directories. Discovery is read-only: it does not start Codex,
sign in, install or enable definitions. Literal environment/header secrets are
returned as markers; environment references are returned by name. Definitions
with detected credentials in arguments or URLs are omitted. Symlinked sources
are rejected, and a blocked or malformed source returns an error. The default
Codex configuration directory must be beneath your user home. Host filesystem
permissions still apply; discovery adds no global access to personal skill roots.

Unsupported permission-profile requests receive an empty grant. Only recorded
native MCP tool-approval forms reach Arvel; other forms and URL elicitations are
declined. The plugin does not enable experimental producers for these capabilities.

## Usage and limits

ChatGPT connections report quota windows with duration-based labels, reset times
and warning/exhausted states. Limits are read after sign-in, refreshed on vendor
updates and every ten minutes while turns run. Unknown observations remain
explicitly unknown; the host receives coalesced usage updates.

Each turn reports its last observed input, output and cached input token counts.
API-key connections have no subscription windows. USD spend and token cost remain
unknown; no prices are estimated and a nonzero USD budget is rejected. A local
`maxTurns` budget survives in-process recovery and idle reload.

Explicit quota failures return `rate_limited` and keep the thread resumable after
reset. Reset hints are included only when a fresh matching window is unambiguous.
Authentication, context, model and transient overload errors have distinct codes.
Live quota exhaustion and API-key billing have not been measured; quota recovery
is covered by source-derived offline tests.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| "codex not found" | CLI not installed or not on PATH | `npm i -g @openai/codex`, or set *Path to codex* |
| Connection stuck on "Sign in" | Browser sign-in was not finished | Click Sign in again; on a server use the code flow |
| API request rejects the key | Invalid, revoked or unusable API key; Ready only means configured locally | Check the key and account on platform.openai.com |
| Windows: sandbox warning | Codex's Windows sandbox is not set up | Follow the hint in Arvel, or run Codex once in a terminal |
| Runs stop with "limit reached" | Plan window exhausted | Wait for reset, or add a fallback executor |

## Privacy

The plugin talks only to the `codex` program on the same machine. Codex talks to OpenAI as it always does. The plugin makes no other network connections and has no telemetry. The default log level contains no prompts and no keys.

## Compatibility

| Plugin | Codex CLI tested | Plugin API |
|---|---|---|
| 0.1.x (in development) | 0.164.0 minimum and exercised session baseline | 1 |

The weekly and manually triggered `schema-drift` workflow checks the production
schema surface against the latest CLI. A schema pass does not establish tested
runtime support for that version. Native Windows/CI acceptance is deferred until
after v0.1.0; cross-builds are not native verification.

## Disclaimer

Independent open-source project, not affiliated with or endorsed by OpenAI. "OpenAI", "ChatGPT" and "Codex" are trademarks of OpenAI.

## Development

Requires Go 1.27.2 or newer for arvel-sdk v0.2.0. Connections, private login,
streaming sessions, approvals, recovery, usage, host tools, managed skills,
images and native subagents are implemented. Offline release conformance runs
the production executable against a separate synthetic app-server. Native host
sandbox certification still requires the Arvel M5c integration matrix; synthetic
CLI probes do not certify real account/model behavior. See [findings](docs/FINDINGS.md)
and [conformance](docs/CONFORMANCE.md) for evidence and limits. Minimal Linux
containers need a system CA bundle.
`ARVEL_LOG_LEVEL=debug` enables redacted development diagnostics on stderr.
`ARVEL_CODEX_BIN` overrides binary discovery only with `ARVEL_DEV=1`; production
connections use `codexPath` or executable discovery.

```bash
go test -race ./...
go build -o dist/arvel-plugin-chatgpt ./cmd/arvel-plugin-chatgpt
go run ./cmd/check-conformance --report /tmp/chatgpt-conformance.json
codex app-server generate-json-schema --out testdata/schema/$(codex --version | awk '{print $NF}')
```

Release conformance is a required release gate; it uses synthetic inputs without real accounts or model requests. Synthetic discovery goldens can be checked with `python3.12 scripts/check-discovery-fixtures.py` (Python 3.11+).

Fixtures: see [`testdata/fixtures/README.md`](testdata/fixtures/README.md). Validate them offline with `go run ./cmd/record-fixtures verify`. E2E tests: `ARVEL_E2E=1 go test -tags e2e ./e2e/...`.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). The files in `testdata/schema/` are generated from [openai/codex](https://github.com/openai/codex), which is also Apache-2.0.
