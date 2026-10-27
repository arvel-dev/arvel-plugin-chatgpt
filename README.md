# ChatGPT for Arvel

[![CI](https://github.com/arvel-dev/arvel-plugin-chatgpt/actions/workflows/ci.yml/badge.svg)](https://github.com/arvel-dev/arvel-plugin-chatgpt/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/arvel-dev/arvel-plugin-chatgpt)](https://github.com/arvel-dev/arvel-plugin-chatgpt/releases)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

This provider plugin lets [Arvel](https://github.com/arvel-dev/arvel) roles run on **OpenAI models**. It works through the official **Codex CLI** and its app-server, the integration surface OpenAI provides for apps like this one. You can pay with your **ChatGPT plan** or an **OpenAI API key**.

It ships with Arvel. You can turn it off or remove it in **Settings → Plugins**, and restore it later.

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

**ChatGPT plan, on your computer.** If `codex` is already signed in on this machine, the connection is ready at once. Otherwise click **Sign in** in Arvel → Settings → ChatGPT. Your browser opens the ChatGPT sign-in page. When you finish, Arvel turns the connection **Ready**.

**ChatGPT plan, on a server.** Arvel shows a link and a short code. Open the link on any device, sign in and enter the code. No browser is needed on the server.

**Use a separate sign-in for Arvel.** This option keeps Arvel's Codex login apart from the one you use in your own terminal.

**API key.** Paste the key in Arvel → Settings → ChatGPT → **Add API key**. Arvel stores it in your system keychain (on servers: encrypted host storage). The plugin uses it only inside a private Codex folder, so your personal Codex setup is untouched.

The plugin never reads Codex's stored tokens. Sign-in is done by Codex itself.

## Models and reasoning effort

The model list comes from Codex (`model/list`), together with the reasoning levels each model supports.

| Arvel | Codex |
|---|---|
| Auto | the model's default effort |
| Low · Medium · High | `low` · `medium` · `high` |
| Max | the highest single-agent level the model offers (e.g. `xhigh`) |

Model and effort changes apply from the next message of the conversation.

## Permissions and safety

Codex runs every session in its **sandbox**, limited to the task's working folder. The plugin maps the Arvel role's rules onto it:

| Role rule | What happens |
|---|---|
| Edit files: **Yes** | Codex may write inside the task folder without asking |
| Edit files: **Ask** / **No** | Read-only sandbox; every change is asked / refused |
| Internet: **Yes** | Network allowed inside the sandbox |
| Internet: **Ask** / **No** | Network off inside the sandbox |
| Commands | Codex asks before running commands outside its trusted set. Arvel's allow and deny lists answer automatically. Everything else comes to you in Arvel or the Telegram bot. |

The plugin **never**:
- turns the sandbox off;
- disables approvals;
- saves "always allow" rules into your Codex configuration. "Allow for session" lasts only for that conversation.

Limitation: Codex can always read files inside the task folder, so the *Read files: No* rule cannot be enforced with this provider. Arvel warns you about this when you assign it.

## Skills and MCP servers

You manage skills and MCP servers in Arvel (**Agent tools → MCP servers / Skills**) and assign them to roles. For each session the plugin places that role's skills where Codex looks for project skills, inside the task's working folder and hidden from git. It passes only that role's MCP servers to Codex.

If you use the shared Codex sign-in (the default), Codex may also load your personal skills. Turn on *Use a separate Codex sign-in just for Arvel* to keep Arvel sessions clean.

## Usage and limits

| | ChatGPT plan | API key |
|---|---|---|
| Window usage % and reset time | Yes, from Codex | — |
| Tokens per task | Yes | Yes |
| Cost in USD | — | Only if Codex reports it |
| At the limit | The run stops cleanly and stays resumable. Arvel applies the role's rule: wait, switch, or ask you. | Same |

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
| 0.1.x | see release notes | 1 |

A weekly CI job checks new Codex releases for protocol changes.

## Disclaimer

Independent open-source project, not affiliated with or endorsed by OpenAI. "OpenAI", "ChatGPT" and "Codex" are trademarks of OpenAI.

## Development

```bash
go test -race ./...
go build -o dist/arvel-plugin-chatgpt ./cmd/arvel-plugin-chatgpt
arvel-plugin-check --level release --scenarios ./conformance -- ./dist/arvel-plugin-chatgpt
codex app-server generate-json-schema --out testdata/schema/$(codex --version | awk '{print $NF}')
```

Fixtures: see `testdata/fixtures/README.md`. E2E tests: `ARVEL_E2E=1 go test -tags e2e ./e2e/...`.

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). The files in `testdata/schema/` are generated from [openai/codex](https://github.com/openai/codex), which is also Apache-2.0.
