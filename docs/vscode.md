# VS Code: GitHub Copilot with local Ollama

This guide sets up GitHub Copilot Chat in VS Code to use models served by Ollama on your machine, and turns off as much telemetry as VS Code allows.

## What runs where

| Feature | Local with this setup? | Notes |
| --- | --- | --- |
| Chat, agent mode, inline chat | ✅ Yes | Through the Ollama extension or a Custom Endpoint. |
| Utility tasks (chat titles, commit messages, PR descriptions, intent detection) | ✅ Yes, after [step 5](#step-5-route-utility-tasks-to-your-local-model) | Otherwise they use GitHub's built-in utility models. |
| Inline suggestions (ghost-text code completions) | ❌ No | Always GitHub-hosted. [Turn them off](#step-6-optional-turn-off-features-that-always-use-github) for a fully local setup. |
| Semantic search and embeddings-based features | ❌ No | Require a GitHub account. |

Source: [AI language models in VS Code](https://code.visualstudio.com/docs/agent-customization/language-models), which states "BYOK applies to the chat experience and utility tasks only."

> [!NOTE]
> VS Code also has **on-device models**, a separate feature where Copilot downloads and runs its own model (MAI Code 1.1 Flash) through llama.cpp. That feature requires GitHub sign-in, an NVIDIA GPU with 96 GB or more of VRAM, and about 55 GB of disk. This guide covers **BYOK** ("bring your own key") local models instead. BYOK works with any Ollama model and no sign-in.

## Requirements

- VS Code 1.127 or newer, which the Ollama extension requires.
- Ollama installed, running, and configured as described in [Shared setup](../README.md#shared-setup-install-ollama).
- At least one model that supports tool calling. VS Code hides models without tool calling from the agent model picker.
- **Copilot Business or Enterprise only:** your org admin must enable the **Bring Your Own Language Model Key in VS Code** policy. Free, Pro, and Pro+ users, and people who aren't signed in, can use BYOK without extra setup.

## Step 1: Start Ollama and pull a model

Complete [Shared setup](../README.md#shared-setup-install-ollama). In particular:

- Set `OLLAMA_CONTEXT_LENGTH` to at least 65536.
- Set `OLLAMA_NO_CLOUD=1`.
- Pull a tool-capable model, for example `ollama pull qwen3.6`.

## Step 2: Install the Ollama extension

VS Code's built-in Ollama provider is **deprecated**. VS Code now points you to the official Ollama extension, published by the Ollama team.

Install it from a terminal:

```shell
code --install-extension Ollama.ollama
```

Or open the Extensions view and search for **Ollama** (publisher: Ollama).

By default, the extension looks for Ollama at `http://127.0.0.1:11434`.

> [!IMPORTANT]
> If you previously set up the deprecated built-in Ollama provider, remove it from **Chat: Manage Language Models**. Also delete any `github.copilot.chat.byok.ollamaEndpoint` entry from your settings.

## Step 3: Pick your Ollama model in chat

1. Open the Chat view.
2. Open the model picker at the bottom of the chat input.
3. Choose a model from the **Ollama** section.

If no Ollama models appear:

1. Run `ollama list` to confirm the model is downloaded.
2. Open the Command Palette and run **Ollama: Refresh Models**.
3. If the models still don't appear, run **Ollama: Diagnose Models** and read the **Ollama** output channel.
4. Restart VS Code.

To choose which models appear in the picker, open the Command Palette and run **Chat: Manage Language Models**.

## Step 4: Let agent sessions use local models

Copilot harness sessions, in both the Chat view and the Agents window, only list BYOK models when you enable this setting.

Open **Preferences: Open User Settings (JSON)** and add:

```jsonc
"chat.agentHost.byokModels.enabled": true
```

## Step 5: Route utility tasks to your local model

VS Code runs background "utility" tasks, such as chat titles, commit messages, PR descriptions, and intent detection, on GitHub's utility models by default. Your chat model doesn't control them. To keep these tasks local, add:

```jsonc
"chat.byokUtilityModelDefault": "mainAgent"
```

With this setting, utility tasks use whichever BYOK model you've selected for chat.

To use a specific smaller model for utility tasks instead, set `chat.utilityModel` and `chat.utilitySmallModel` in the Settings UI. Each one is a dropdown that lists all your available models. A specific model in either setting takes precedence over `chat.byokUtilityModelDefault`.

If you aren't signed in to GitHub, VS Code shows a notice in the Chat view until you configure one of these options.

## Step 6 (optional): Turn off features that always use GitHub

Skip this step if you're happy to keep GitHub-hosted code completions. For a fully local setup, either stay signed out of GitHub in VS Code, or add these settings:

```jsonc
// Inline suggestions (ghost text) and next edit suggestions are always GitHub-hosted.
"github.copilot.enable": { "*": false },
"github.copilot.nextEditSuggestions.enabled": false
```

**Unverified:** Even when chat uses a local model, a signed-in Copilot extension still contacts GitHub for authentication and entitlement checks. Staying signed out is the only way to be sure.

## Step 7: Turn off telemetry and other phone-home features

The Copilot extension follows VS Code's global telemetry switch. In the installed extension, `vscode.env.isTelemetryEnabled` gates Copilot's telemetry.

Add these settings to your user `settings.json`:

```jsonc
// Telemetry and feedback
"telemetry.telemetryLevel": "off",
"telemetry.feedback.enabled": false,
"workbench.enableExperiments": false,

// Update checks and online lookups
"update.mode": "manual",
"update.showReleaseNotes": false,
"extensions.autoCheckUpdates": false,
"extensions.autoUpdate": false,
"extensions.ignoreRecommendations": true,
"npm.fetchOnlinePackageInfo": false,
"json.schemaDownload.enable": false,
"workbench.settings.enableNaturalLanguageSearch": false,

// Don't sync settings to the cloud
"settingsSync.enabled": false
```

What these settings do:

- `telemetry.telemetryLevel: "off"` turns off usage, error, and crash telemetry for VS Code and for extensions that respect it, including Copilot.
- `update.mode` can be `none` to block update checks completely. `manual` is a safer middle ground because you can still update on demand.
- Third-party extensions may collect their own telemetry, which this setting doesn't control. Check each extension's documentation.
- Turning off telemetry also turns off VS Code's A/B experiments.

Then complete the account-level privacy steps in [Shared privacy settings on GitHub.com](../README.md#shared-privacy-settings-on-githubcom).

## Step 8: Verify

1. Select your Ollama model in chat and send a prompt, for example: "List the files in this workspace."
2. In a terminal, run `ollama ps`. The model should be listed as loaded, with the context size you configured.
3. For agent mode, ask for a small edit and confirm that tool calls, such as reading and editing files, succeed.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| Model missing from the agent picker | The model doesn't advertise tool calling. Run `ollama show <model>` and look for `tools` under Capabilities, or pick another model. |
| Answers lose track of earlier context, or tool calls loop | Ollama's context is too small. Raise `OLLAMA_CONTEXT_LENGTH`, restart Ollama, and check the CONTEXT column in `ollama ps`. |
| Requests time out on a slow machine | Raise **Ollama: Inference Timeout Minutes** (`ollama.inferenceTimeoutMinutes`, from 1 to 60). |
| Model picker only shows **Auto** | The workspace isn't trusted. Trust it, or open a trusted folder. |

---

## Advanced: Switch to llama.cpp, or a remote server

### Option A: Remote Ollama

On the **server**, make Ollama listen on the network by setting `OLLAMA_HOST=0.0.0.0:11434`. Use the method in [Setting Ollama environment variables](../README.md#setting-ollama-environment-variables), then restart Ollama.

In VS Code on the **client**, point the extension at the server:

```jsonc
"ollama.endpoint": "http://192.168.1.50:11434"
```

If a reverse proxy in front of Ollama requires authentication, add the header with `"ollama.headers": { "Authorization": "Bearer <token>" }`.

### Option B: llama.cpp `llama-server`

`llama-server` provides an OpenAI-compatible API at `/v1/chat/completions`. Use VS Code's **Custom Endpoint** provider for it. Custom Endpoint replaces the deprecated "OpenAI Compatible" provider and the `github.copilot.chat.customOAIModels` setting.

**1. Start `llama-server`.** Install llama.cpp from the [llama.cpp releases page](https://github.com/ggml-org/llama.cpp/releases), or with `brew install llama.cpp` on macOS. Then start the server:

```shell
llama-server -hf ggml-org/gpt-oss-20b-GGUF --jinja -c 65536 --host 127.0.0.1 --port 8080 --alias gpt-oss-20b
```

- `--jinja` turns on the chat template engine that OpenAI-style tool calling needs. It's on by default in current builds, but setting it explicitly is harmless.
- `-c` sets the context size. `0` uses the model's own maximum.
- Always pass `--port`. The default port in current builds is `9931`, not `8080`.
- `--alias` sets the model name that clients send.
- Use `-m /path/to/model.gguf` instead of `-hf` to load a local file.

**2. Add the endpoint in VS Code.**

1. Open the Command Palette and run **Chat: Manage Language Models**.
2. Select **Add Models**, then **Custom Endpoint**.
3. Enter a group name and an API key. VS Code requires a non-empty key. If you didn't start `llama-server` with `--api-key`, any placeholder such as `none` works.
4. Set **API type** to **Chat Completions**.
5. VS Code opens `chatLanguageModels.json`. Edit it to match:

```json
[
  {
    "name": "llama.cpp",
    "vendor": "customendpoint",
    "apiKey": "${input:llamaApiKey}",
    "apiType": "chat-completions",
    "models": [
      {
        "id": "gpt-oss-20b",
        "name": "gpt-oss 20B (llama.cpp)",
        "url": "http://127.0.0.1:8080/v1/chat/completions",
        "toolCalling": true,
        "vision": false,
        "maxInputTokens": 57344,
        "maxOutputTokens": 8192
      }
    ]
  }
]
```

About these values:

- `id` must match the model name the server expects. Use the `--alias` value you set.
- `maxInputTokens + maxOutputTokens` must not exceed the server's `-c` value. You can set `contextWindow` instead of `maxInputTokens`.
- Give `url` the full path to `/v1/chat/completions` so there's no ambiguity. If you give only a base URL, VS Code appends `/v1/chat/completions` itself.
- Using `${input:...}` for `apiKey` keeps the key out of the file.

**3. For a remote server,** start `llama-server` on the remote machine with `--host 0.0.0.0 --api-key <secret>`, and change `url` to the server's address, for example `http://192.168.1.50:8080/v1/chat/completions` or `https://llm.example.com/v1/chat/completions`. VS Code accepts both `http://` and `https://` URLs and sends the key as `Authorization: Bearer <key>`.

### Security for remote servers

Neither Ollama nor `llama-server` encrypts traffic, and Ollama has no built-in authentication. Your prompts include source code. Choose one of these options:

- **SSH tunnel (simplest):** `ssh -N -L 8080:127.0.0.1:8080 user@server`. The server stays bound to `127.0.0.1`, and VS Code uses `http://127.0.0.1:8080`.
- **Private network:** run the server on a VPN or mesh network such as WireGuard or Tailscale, and firewall the port from everything else.
- **Reverse proxy with TLS,** such as Caddy or nginx, in front of the server, plus an API key (`llama-server --api-key`) or proxy authentication (`ollama.headers`).

Never expose port 11434 or 8080 directly to the internet.

## Sources

- [AI language models in VS Code (BYOK, Custom Endpoint, utility models)](https://code.visualstudio.com/docs/agent-customization/language-models)
- [Ollama for VS Code extension](https://marketplace.visualstudio.com/items?itemName=Ollama.ollama), with settings verified from the v0.0.13 package
- [VS Code telemetry](https://code.visualstudio.com/docs/configure/telemetry)
- [Ollama FAQ (environment variables, cloud opt-out)](https://github.com/ollama/ollama/blob/main/docs/faq.mdx) and [context length](https://github.com/ollama/ollama/blob/main/docs/context-length.mdx)
- [llama.cpp server README](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)
