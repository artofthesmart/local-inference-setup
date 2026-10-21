# Local inference for GitHub Copilot

Run GitHub Copilot's AI features on **your own machine** with [Ollama](https://ollama.com) instead of GitHub's cloud models, and turn off as much tracking and telemetry as each tool allows.

This repo covers three Copilot surfaces:

| Guide | What goes local | Sign-in needed? |
| --- | --- | --- |
| [VS Code](docs/vscode.md) | Chat, agent mode, inline chat, and utility tasks (titles, commit messages) | No (but inline completions and semantic search need GitHub) |
| [Copilot CLI](docs/copilot-cli.md) | Everything; it has a true offline mode | No |
| [GitHub Desktop and the GitHub Copilot app](docs/github-desktop.md) | Commit messages and conflict resolution (Desktop), agent sessions (Copilot app) | Yes, a GitHub account is still required |

Each guide ends with an **Advanced** section on switching from Ollama to [llama.cpp](https://github.com/ggml-org/llama.cpp)'s `llama-server`, either on the same machine or on another machine reached by IP address or URL.

## Shared setup: install Ollama

Every guide starts here.

1. Install Ollama.
   - macOS and Windows: download the app from <https://ollama.com/download>.
   - Linux: `curl -fsSL https://ollama.com/install.sh | sh`
2. Give coding agents enough context. Ollama's default context depends on your GPU memory (4k under 24 GB of VRAM), which is far too small for agent work. Ollama recommends at least 64k for coding tools. Set `OLLAMA_CONTEXT_LENGTH=65536` (see [Setting Ollama environment variables](#setting-ollama-environment-variables)).
3. Turn off Ollama's cloud features so nothing leaves your machine. Set `OLLAMA_NO_CLOUD=1`, or add `{"disable_ollama_cloud": true}` to `~/.ollama/server.json`. After a restart, Ollama's log shows `Ollama cloud disabled: true`.
4. Pull a model that supports **tool calling**. Copilot's agents need it. Examples:
   - `ollama pull qwen3.6`
   - `ollama pull gpt-oss:20b`
5. Check that the model lists `tools` under **Capabilities**: `ollama show qwen3.6`
6. Check that the server is running: `curl http://localhost:11434/v1/models`

### Setting Ollama environment variables

Ollama reads its settings from environment variables when the server starts. Restart Ollama after you change them.

- **Linux (systemd):** run `sudo systemctl edit ollama`, add the lines below, then run `sudo systemctl restart ollama`.

  ```ini
  [Service]
  Environment="OLLAMA_CONTEXT_LENGTH=65536"
  Environment="OLLAMA_NO_CLOUD=1"
  ```

- **macOS:** `launchctl setenv OLLAMA_CONTEXT_LENGTH 65536 && launchctl setenv OLLAMA_NO_CLOUD 1`, then quit and reopen the Ollama app.
- **Windows (PowerShell):** `setx OLLAMA_CONTEXT_LENGTH 65536; setx OLLAMA_NO_CLOUD 1`, then quit Ollama from the system tray and start it again.

Run `ollama ps` while a model is loaded. The **CONTEXT** column shows the context size actually in use.

## Shared privacy settings on GitHub.com

These account-level settings apply to every Copilot surface.

- On <https://github.com/settings/copilot>, under **Privacy**, turn off the option that lets GitHub use your data for product improvements and model training. This applies to Copilot Free, Pro, and Pro+. Business and Enterprise interaction data is excluded from that program by policy.
- On the same page, set **Suggestions matching public code** to **Block** if you want that filter. Organization-managed seats inherit this from org policy.

## Verification status

Facts in these guides were checked on 2026-10-21 against:

- Official docs from GitHub, VS Code, Ollama, and llama.cpp (linked in each guide).
- Installed software: VS Code 1.143.0 with the bundled GitHub Copilot extension 0.71.0, the `Ollama.ollama` VS Code extension 0.0.13, and Copilot CLI 1.0.101.
- A live test of Copilot CLI offline mode against a mock OpenAI-compatible server.

Claims that couldn't be verified are labeled **Unverified** where they appear. See [NOTES.md](NOTES.md) for project requirements, decisions, and open questions.
