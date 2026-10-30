# Local inference for GitHub Copilot

Run GitHub Copilot's AI features on **your own machine** with [Ollama](https://ollama.com) instead of GitHub's cloud models, and turn off as much tracking and telemetry as each tool allows.

This repo covers three Copilot surfaces:

| Guide | What goes local | Sign-in needed? |
| --- | --- | --- |
| [VS Code](docs/vscode.md) | Chat, agent mode, inline chat, and utility tasks (titles, commit messages) | No (but inline completions and semantic search need GitHub) |
| [Copilot CLI](docs/copilot-cli.md) | Everything; it has a true offline mode | No |
| [GitHub Desktop and the GitHub Copilot app](docs/github-desktop.md) | Commit messages and conflict resolution (Desktop), agent sessions (Copilot app) | Yes, a GitHub account is still required |

Each guide ends with an **Advanced** section on switching from Ollama to [llama.cpp](https://github.com/ggml-org/llama.cpp)'s `llama-server`, either on the same machine or on another machine reached by IP address or URL.

## Quick start (automated)

The `copilot-local` tool starts Copilot CLI on a model running on your own computer. The first time, it sets everything up for you and asks once before changing anything:

1. Installs Ollama if needed, sets a big enough context size, and turns off Ollama cloud.
2. Checks your memory and graphics card, and picks the best model that fits from a [tested list](internal/models/shortlist.txt). The list runs from 8 GB laptops up to 48 GB graphics cards and 128 GB Macs. Extra mid-size and large models can be chosen from the menu.
3. Downloads the model and tests that it can really call tools, the way Copilot does.
4. Starts Copilot, connected only to that model.

After that, it shows a short menu with the model you used last at the top, so you just press Enter.

- **macOS and Linux:** `curl -fsSL https://raw.githubusercontent.com/artofthesmart/local-inference-setup/main/install.sh | sh`
- **Windows (PowerShell):** `irm https://raw.githubusercontent.com/artofthesmart/local-inference-setup/main/install.ps1 | iex`

Each command downloads the right program for your computer, checks its checksum, and runs it from a temporary folder. Nothing gets installed except Ollama and the model. You need [Copilot CLI](docs/copilot-cli.md#requirements) installed first.

Useful options, for example `curl -fsSL …/install.sh | sh -s -- model --ask-gemini`:

| Option | What it does |
| --- | --- |
| `--model gpt-oss:20b` | Start Copilot with this model (it's downloaded and tested first if needed) |
| `-- -p "fix the tests"` | Pass anything after `--` to Copilot |
| `model` | Add, change, or re-test a model |
| `configure` | Save your selected local model settings for future terminals |
| `model --ask-gemini` | Make a prompt about your computer to ask Gemini which model to use, then paste back its answer |
| `model --check` | Re-test the model you used last |
| `vscode` | Install the Ollama VS Code extension and configure local generation and privacy settings |
| `verify` | Check Ollama, the selected model, an offline Copilot prompt, and VS Code; print fixes for failures |
| `verify --skip-vscode` | Verify a CLI-only setup without requiring VS Code |
| `ollama --dry-run` | Show what the Ollama setup would change, and change nothing |
| `ollama --context 131072` | Use a different context length (default 65536) |
| `ollama --undo` | Remove the Ollama settings the tool added (Ollama stays installed) |
| `--yes` | Don't ask questions (for scripts) |
| `--help` | Help for any command, for example `model --help` |

> [!NOTE]
> On some work-managed Windows PCs, Microsoft Defender's ransomware protection blocks new, unsigned programs, and you'll see "Access is denied". Use the manual steps below, or ask your IT team. Signed Windows builds are tracked in the issues.

The manual steps below do the same thing by hand.

### Ask Gemini for model choices (no installation)

These standalone scripts print a prompt with your CPU, RAM, GPU information, and free disk space. Paste the output into Gemini to ask for three local Ollama choices: **speed**, **balanced**, and **intelligence** (better reasoning over speed, but still usable on your hardware). The prompt asks Gemini to check current exact tags, tool calling, and memory needs at 64k context, and to say when no practical model fits.

- **macOS and Linux:** `curl -fsSL https://raw.githubusercontent.com/artofthesmart/local-inference-setup/main/gemini-query.sh | sh`
- **Windows (PowerShell):** `irm https://raw.githubusercontent.com/artofthesmart/local-inference-setup/main/gemini-query.ps1 | iex`

From a local checkout, use `sh ./gemini-query.sh` or `powershell -NoProfile -ExecutionPolicy Bypass -File .\gemini-query.ps1`. In an interactive terminal, press **ENTER** after the separator to copy just the query to your clipboard. If clipboard access is unavailable, copy it manually. Noninteractive runs skip clipboard interaction. To save the output instead, use `sh ./gemini-query.sh > gemini-query.txt` or `powershell -NoProfile -ExecutionPolicy Bypass -File .\gemini-query.ps1 > gemini-query.txt`.

The scripts need no Go, Ollama, or Gemini API key. They do not contact Gemini, install anything, download models, or change settings; the remote one-liners only fetch the script itself. Missing hardware details are marked unknown. Check the output before sharing it with Gemini; no usernames, model paths, or project code are intentionally included. In WSL, run the PowerShell version on Windows if that is where Ollama runs. The existing `copilot-local model --ask-gemini` flow also requests all three choices, while keeping the balanced choice in its `MODEL:` copy-back line.

### Check the whole setup

Run `copilot-local verify` after setup, or pass `verify` to either launcher above. It prints **PASS**, **WARN**, or **FAIL** for Ollama, cloud opt-out, a real model tool call, the context actually loaded, Copilot CLI environment variables, and VS Code's Ollama extension and documented user-level privacy settings. A fixed test prompt also runs through the real Copilot CLI with offline settings, no available tools, and an isolated temporary home and workspace. It does not install software, download models, or change your settings; it may load the selected model into memory.

Use `copilot-local verify --skip-vscode` for CLI-only setup, `--profile "Local AI"` to check a particular VS Code profile, `--model gpt-oss:20b --context 65536` to override the saved selection, or `--timeout 5m` on slow hardware. Missing CLI environment variables are warnings because the launcher supplies them. Conflicting variables, provider overrides, failed tests, and missing VS Code configuration are failures, each with a suggested fix. The isolated prompt tests launcher-provided settings, not your saved Copilot settings. Exit status is **0** with no failures (warnings and manual steps may remain), **1** with failures, or **2** for invalid arguments.

GitHub Desktop and the Copilot app still need the manual checks printed at the end. VS Code's selected chat model and workspace/policy overrides also need checking by hand. Passing this command is **not** proof that every application makes zero external requests.

## Shared setup: install Ollama

Every guide starts here.

1. Install Ollama.
   - macOS and Windows: download the app from <https://ollama.com/download>.
   - Linux: `curl -fsSL https://ollama.com/install.sh | sh`
2. Give coding agents enough context. Ollama's default context depends on your GPU memory (4k under 24 GB of VRAM), which is far too small for agent work. Ollama recommends at least 64k for coding tools. Set `OLLAMA_CONTEXT_LENGTH=65536` (see [Setting Ollama environment variables](#setting-ollama-environment-variables)).
3. Turn off Ollama's cloud features so nothing leaves your machine. Set `OLLAMA_NO_CLOUD=1`, or add `{"disable_ollama_cloud": true}` to `~/.ollama/server.json`. After a restart, Ollama's log shows `Ollama cloud disabled: true`, and `curl http://localhost:11434/api/status` reports `"disabled":true`. On Linux with systemd, Ollama runs as its own `ollama` user, so your `~/.ollama/server.json` is ignored. Use the environment variable there.
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

- **macOS:** `launchctl setenv OLLAMA_CONTEXT_LENGTH 65536 && launchctl setenv OLLAMA_NO_CLOUD 1`, then quit and reopen the Ollama app. These settings are lost when you restart your Mac. To keep them, run the commands again after each restart, or use the automated tool, which adds a small login item that reapplies them.
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

### Development checks

Run `go test ./...` and `go vet ./...`. With Copilot CLI installed, `COPILOT_LOCAL_CLI_TEST=1 go test ./internal/launch ./cmd/copilot-local -run 'TestProbeWithInstalledCLI|TestVerifyWithInstalledCLI' -count=1` additionally runs the actual CLI against local mock APIs, including wrong/empty-reply failures. It uses no GitHub credentials or real model downloads. The manually triggered e2e workflow exercises a real Ollama model.
