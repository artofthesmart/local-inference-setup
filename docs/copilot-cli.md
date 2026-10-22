# Copilot CLI: local Ollama, fully offline

GitHub Copilot CLI (the `copilot` command) has built-in support for your own model provider ("BYOK") and a true **offline mode**. With both turned on, the CLI talks only to your local Ollama server. It skips GitHub sign-in, telemetry, web tools, the GitHub MCP server, and auto-update.

This guide was checked against Copilot CLI 1.0.101 (`copilot help providers`, `copilot help environment`, `copilot help config`) and the [official BYOK docs](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models). The setup was also tested end to end against a mock server, using a fresh config directory and no GitHub login.

## The easy way: `copilot-local`

Run the one-line command from the [README Quick start](../README.md#quick-start-automated). It sets up Ollama, picks and tests a model that fits your computer, and starts Copilot with the offline settings below, for that run only. Next time, run it again and press Enter to reuse your last model. It doesn't change your shell profile, so plain `copilot` still uses GitHub.

The rest of this guide does the same thing by hand, and covers making the settings permanent.

## What runs where

| Feature | Local with this setup? |
| --- | --- |
| Agent conversation, tool calls, file edits | ✅ Yes |
| Background tasks (session naming, prompt refinement) | ✅ Yes, sent to the same provider |
| GitHub authentication | ✅ Not needed |
| Telemetry, auto-update, web fetch and search, GitHub MCP server | ✅ Off in offline mode |
| GitHub features (issues, PRs, `/delegate`, cloud agents) | ❌ Not available offline |

## Requirements

- Copilot CLI. Install it with any one of these commands:
  - `npm install -g @github/copilot`
  - `brew install --cask copilot-cli`
  - `winget install GitHub.Copilot`
  - `curl -fsSL https://gh.io/copilot-install | bash`
- Ollama configured as described in [Shared setup](../README.md#shared-setup-install-ollama).
- A model that supports **tool calling and streaming**. The CLI returns an error otherwise. GitHub recommends a context window of at least 128k tokens. In practice, 64k works for small tasks.
- No Copilot plan or GitHub account is needed in BYOK mode.

> [!NOTE]
> On some managed machines, an org policy may block Copilot CLI or BYOK. **Unverified:** the exact policy behavior for local-only offline use wasn't tested.

## Step 1: Prepare Ollama

Complete [Shared setup](../README.md#shared-setup-install-ollama). For agent work, raise the context length further if your hardware allows it, for example `OLLAMA_CONTEXT_LENGTH=131072`. Then pull a model:

```shell
ollama pull qwen3.6
```

## Step 2: Try it once

**macOS and Linux (bash or zsh):**

```shell
COPILOT_OFFLINE=true COPILOT_PROVIDER_BASE_URL=http://localhost:11434/v1 COPILOT_MODEL=qwen3.6 copilot
```

**Windows (PowerShell):**

```powershell
$env:COPILOT_OFFLINE="true"; $env:COPILOT_PROVIDER_BASE_URL="http://localhost:11434/v1"; $env:COPILOT_MODEL="qwen3.6"; copilot
```

> [!IMPORTANT]
> **Include `/v1` in the base URL.** The CLI appends `/chat/completions` to whatever base URL you give it. We confirmed this against a mock server. Ollama's OpenAI-compatible API lives at `/v1/chat/completions`. The official docs example uses `http://localhost:11434` without `/v1`, which makes the CLI call `/chat/completions`, a path Ollama doesn't serve.

The variables used:

| Variable | Value | Why |
| --- | --- | --- |
| `COPILOT_PROVIDER_BASE_URL` | `http://localhost:11434/v1` | Turns on BYOK mode and points the CLI at Ollama. |
| `COPILOT_MODEL` | `qwen3.6` | The Ollama model name, exactly as `ollama list` shows it. Required. |
| `COPILOT_OFFLINE` | `true` | Blocks all GitHub network access: auth, telemetry, web tools, GitHub MCP, and auto-update. |
| `COPILOT_PROVIDER_TYPE` | *(not needed)* | Defaults to `openai`, the right type for Ollama and llama.cpp. |
| `COPILOT_PROVIDER_API_KEY` | *(not needed)* | Ollama has no authentication. |

## Step 3: Tell the CLI your real context size

The CLI doesn't recognize most Ollama model names, so it falls back to "safe defaults" for token limits. Set the limits to match your Ollama context so the CLI compacts history before Ollama silently truncates it. For a context of 65536:

```shell
export COPILOT_PROVIDER_MAX_PROMPT_TOKENS=57344 COPILOT_PROVIDER_MAX_OUTPUT_TOKENS=8192
```

Keep the sum of the two values at or below `OLLAMA_CONTEXT_LENGTH`.

## Step 4: Make it permanent

**macOS and Linux:** add the settings to `~/.bashrc` or `~/.zshrc` in one command, then open a new terminal:

```shell
printf '\n# Copilot CLI -> local Ollama\nexport COPILOT_OFFLINE=true COPILOT_PROVIDER_BASE_URL=http://localhost:11434/v1 COPILOT_MODEL=qwen3.6 COPILOT_PROVIDER_MAX_PROMPT_TOKENS=57344 COPILOT_PROVIDER_MAX_OUTPUT_TOKENS=8192\n' >> ~/.bashrc
```

If you'd rather keep normal cloud Copilot as the default, add an alias instead:

```shell
echo "alias copilot-local='COPILOT_OFFLINE=true COPILOT_PROVIDER_BASE_URL=http://localhost:11434/v1 COPILOT_MODEL=qwen3.6 copilot'" >> ~/.bashrc
```

**Windows (PowerShell, persists for your user):**

```powershell
setx COPILOT_OFFLINE true; setx COPILOT_PROVIDER_BASE_URL http://localhost:11434/v1; setx COPILOT_MODEL qwen3.6; setx COPILOT_PROVIDER_MAX_PROMPT_TOKENS 57344; setx COPILOT_PROVIDER_MAX_OUTPUT_TOKENS 8192
```

Open a new terminal afterwards.

## Step 5: Turn off telemetry and auto-update for non-offline sessions too

Offline mode already turns off telemetry. If you sometimes run without `COPILOT_OFFLINE`, for example to use GitHub features with a local model, also apply these settings. Each command writes to `~/.copilot/settings.json`:

```shell
copilot config autoUpdate false
```

- Launch with `copilot --disable-client-telemetry` to turn off telemetry sent by the CLI. This doesn't affect server-side telemetry that GitHub records when you use GitHub-hosted models.
- Optionally run `copilot config memory false` to turn off cross-session agentic memory. **Unverified:** where the CLI stores memories in BYOK mode.
- Complete [Shared privacy settings on GitHub.com](../README.md#shared-privacy-settings-on-githubcom).

## Step 6: Verify

1. Start `copilot` with the variables set.
2. Ask: "List the files in this folder and summarize the README."
3. In another terminal, run `ollama ps`. Your model should be loaded.
4. To prove nothing goes to GitHub, start the CLI with a clean config directory, for example `COPILOT_HOME=/tmp/copilot-test copilot`. It should start without asking you to sign in.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `Connection timed out to provider` | Ollama isn't running, or the URL is wrong. Test it with `curl http://localhost:11434/v1/models`. |
| Your `COPILOT_PROVIDER_*` variables seem to be ignored | A `~/.copilot/providers.json` file (or one named by `COPILOT_PROVIDERS_CONFIG`) takes precedence over these variables ([docs](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference)). Rename or remove it. |
| 404 errors from the provider | `/v1` is missing from `COPILOT_PROVIDER_BASE_URL`. |
| Error about tools or streaming | The model doesn't support tool calling. Check `ollama show <model>` for `tools` under Capabilities. |
| Agent forgets earlier steps, or loops | The context is too small. Raise `OLLAMA_CONTEXT_LENGTH` and `COPILOT_PROVIDER_MAX_PROMPT_TOKENS` together. |
| Asked to log in | `COPILOT_PROVIDER_BASE_URL` isn't set in this shell. Check it with `echo $COPILOT_PROVIDER_BASE_URL`. |

---

## Advanced: Switch to llama.cpp, or a remote server

The CLI treats any OpenAI-compatible server the same way. Switching only changes the environment variables.

### Option A: Remote Ollama

On the server, set `OLLAMA_HOST=0.0.0.0:11434` and restart Ollama. See [Setting Ollama environment variables](../README.md#setting-ollama-environment-variables). On the client:

```shell
COPILOT_OFFLINE=true COPILOT_PROVIDER_BASE_URL=http://192.168.1.50:11434/v1 COPILOT_MODEL=qwen3.6 copilot
```

> [!IMPORTANT]
> Offline mode only guarantees isolation from GitHub. Your prompts and code still travel over the network to the remote server. See [Security for remote servers](#security-for-remote-servers).

### Option B: llama.cpp `llama-server`, local

Start the server. Always pass `--port`, because the default port in current builds is `9931`.

```shell
llama-server -hf ggml-org/gpt-oss-20b-GGUF --jinja -c 131072 --host 127.0.0.1 --port 8080 --alias gpt-oss-20b
```

Then point the CLI at it:

```shell
COPILOT_OFFLINE=true COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:8080/v1 COPILOT_MODEL=gpt-oss-20b COPILOT_PROVIDER_MAX_PROMPT_TOKENS=122880 COPILOT_PROVIDER_MAX_OUTPUT_TOKENS=8192 copilot
```

- `COPILOT_MODEL` must match the server's `--alias`.
- `--jinja` turns on the chat template that tool calling needs. It's on by default in current builds.

### Option C: llama.cpp `llama-server`, remote by IP address or URL

On the server, add an API key and listen on the network:

```shell
llama-server -hf ggml-org/gpt-oss-20b-GGUF --jinja -c 131072 --host 0.0.0.0 --port 8080 --alias gpt-oss-20b --api-key YOUR_SECRET
```

On the client:

```shell
COPILOT_OFFLINE=true COPILOT_PROVIDER_BASE_URL=https://llm.example.com/v1 COPILOT_PROVIDER_API_KEY=YOUR_SECRET COPILOT_MODEL=gpt-oss-20b copilot
```

The CLI sends `COPILOT_PROVIDER_API_KEY` as `Authorization: Bearer ...`, which is the header `llama-server --api-key` expects. Other useful variables:

- `COPILOT_PROVIDER_API_KEY_COMMAND` runs a command that prints the key, such as a password-manager lookup, so the key isn't stored in your shell profile.
- `COPILOT_PROVIDER_HEADERS` adds extra headers, for example for a gateway. Separate multiple headers with a newline. The CLI sends these headers only to your provider, never to GitHub.

### Security for remote servers

Neither Ollama nor `llama-server` encrypts traffic, and Ollama has no built-in authentication. Your prompts include source code. Choose one of these options:

- **SSH tunnel (simplest):** `ssh -N -L 8080:127.0.0.1:8080 user@server`, then use `http://127.0.0.1:8080/v1`.
- **Private network:** use a VPN or mesh network such as WireGuard or Tailscale, and firewall the port from everything else.
- **Reverse proxy with TLS,** such as Caddy or nginx, in front of the server, plus `--api-key` or proxy authentication.

Never expose port 11434 or 8080 directly to the internet.

## Sources

- [Adding LLM models to GitHub Copilot CLI (BYOK and offline mode)](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-byok-models)
- Built-in help in Copilot CLI 1.0.101: `copilot help providers`, `copilot help environment`, `copilot help config`
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
- [llama.cpp server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
