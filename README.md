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

The plugin never selects an unrestricted sandbox, disables approvals, or saves
permanent allow rules in Codex configuration. Codex may retry an approved command
outside its native sandbox; the host permission request describes that scope, and
Arvel's host sandbox still applies. "Allow for session" uses Codex's native
conversation-scoped decision and does not transfer to a replacement thread.

Limitation: Codex can always read files inside the task folder, so the *Read files: No* rule cannot be enforced with this provider. Arvel warns you about this when you assign it.

## Skills and MCP servers

Host-delivered skills, MCP servers, subagents and media prompts are not implemented
yet. Nonempty requests for these capabilities fail explicitly. Agent sessions use
a private Codex home, exclude project `.codex` configuration through an untrusted
project override, and disable inherited hooks, plugins and notifications. Inherited
MCP definitions, custom model providers and managed/unknown configuration layers
are rejected before a thread starts or resumes. Personal or project skill discovery
outside that configuration mechanism remains a vendor limitation; host skill
materialization belongs to the later tools milestone.

Unsupported permission-profile requests receive an empty grant, and MCP elicitation
requests are declined. The plugin does not enable experimental producers to obtain
these capabilities.

## Usage and limits

Usage and typed quota/error mapping are planned for the next milestone. Currently
connection usage is unknown, and no token cost or USD spend is inferred. A nonzero
USD budget is rejected. A local `maxTurns` budget stops before the next turn and
survives in-process recovery and idle reload.

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| "codex not found" | CLI not installed or not on PATH | `npm i -g @openai/codex`, or set *Path to codex* |
| Connection stuck on "Sign in" | Browser sign-in was not finished | Click Sign in again; on a server use the code flow |
| "Key rejected" | Wrong or revoked key | Create a new key on platform.openai.com |
| Windows: sandbox warning | Codex's Windows sandbox is not set up | Follow the hint in Arvel, or run Codex once in a terminal |
| Runs stop with "limit reached" | Plan window exhausted | Wait for reset, or add a fallback executor |

## Privacy

The plugin talks only to the `codex` program on the same machine. Codex talks to OpenAI as it always does. The plugin makes no other network connections and has no telemetry. The default log level contains no prompts and no keys.

## Compatibility

| Plugin | Codex CLI tested | Plugin API |
|---|---|---|
| 0.1.x (in development) | 0.164.0 minimum and exercised session baseline | 1 |

Weekly checks of new Codex releases are planned for the conformance milestone.

## Disclaimer

Independent open-source project, not affiliated with or endorsed by OpenAI. "OpenAI", "ChatGPT" and "Codex" are trademarks of OpenAI.

## Development

Requires Go 1.27.2 or newer for arvel-sdk v0.1.2. Connection status, browser and
device-code sign-in, isolated API-key configuration and model discovery are
implemented, together with streaming sessions, command/file approvals, cancellation
and recovery. Usage mapping, host tool delivery and release conformance remain
unimplemented. Native host sandbox certification is still pending the Arvel host
integration matrix; synthetic-backend CLI probes do not certify real account or
model behavior. See [bootstrap findings](docs/FINDINGS.md) for measured CLI behavior and
outstanding verification. Minimal Linux containers need a system CA bundle.
`ARVEL_LOG_LEVEL=debug` enables redacted development diagnostics on stderr.
`ARVEL_CODEX_BIN` overrides binary discovery only with `ARVEL_DEV=1`; production
connections use `codexPath` or executable discovery.

```bash
go test -race ./...
go build -o dist/arvel-plugin-chatgpt ./cmd/arvel-plugin-chatgpt
arvel-plugin-check --level release --scenarios ./conformance -- ./dist/arvel-plugin-chatgpt
codex app-server generate-json-schema --out testdata/schema/$(codex --version | awk '{print $NF}')
```

Release conformance is a G6 gate; the bootstrap does not pass it yet. Synthetic discovery goldens can be checked with `python3.12 scripts/check-discovery-fixtures.py` (Python 3.11+).

Fixtures: see [`testdata/fixtures/README.md`](testdata/fixtures/README.md). Validate them offline with `go run ./cmd/record-fixtures verify`. E2E tests: `ARVEL_E2E=1 go test -tags e2e ./e2e/...`.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). The files in `testdata/schema/` are generated from [openai/codex](https://github.com/openai/codex), which is also Apache-2.0.
