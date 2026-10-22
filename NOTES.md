# Project notes

## Requirements (from kickoff, 2026-10-21)

- Offer a 3-part solution for local inference through GitHub Copilot on three surfaces: the VS Code IDE, Copilot CLI, and "the GitHub Desktop App."
- Use local Ollama instead of GitHub's cloud AI services.
- Wherever possible, turn off tracking, telemetry, and data collection.
- Automate where reasonable, but not everything.
- Phase 1, done: research and write one setup document per platform.
- Each document includes an **Advanced** section on switching from Ollama to llama.cpp, either locally or remotely by IP address or URL.

## Decisions

- **"GitHub Desktop App" is ambiguous.** It can mean GitHub Desktop, the Git GUI, or the newer GitHub Copilot app, a standalone agent app. Both support BYOK with Ollama, so [docs/github-desktop.md](docs/github-desktop.md) covers both. Revisit if only one was intended.
- **VS Code:** use the official `Ollama.ollama` extension, because the built-in Ollama provider is deprecated. Use **Custom Endpoint** for llama.cpp, because "OpenAI Compatible" and `customOAIModels` are deprecated.
- **Copilot CLI:** use the native environment variables (`COPILOT_PROVIDER_*`) with `COPILOT_OFFLINE=true`. No proxy or workaround is needed.
- Shared Ollama setup (context length and the cloud opt-out) lives once in the README. The platform guides link to it.
- Commands are written as single copy-paste lines.
- **Automation language: Go.** It builds one self-contained program per OS, so users need nothing installed first (no Python, no Bash on Windows). One codebase covers Linux, macOS, and Windows. Users get it from a one-line launcher (`curl … | sh` or `irm … | iex`). Plans live in [plans/](plans/), starting with [issue #2](plans/issue-2-ollama-setup.md).

## Notable findings

- The Copilot CLI base URL must include `/v1` for Ollama. Without it, the CLI calls `/chat/completions`, which we confirmed with a mock server. The official docs example omits `/v1`.
- GitHub Desktop rejects plain `http://` URLs to non-local hosts. Use an SSH tunnel or HTTPS for remote servers.
- `llama-server`'s default port is now `9931`, so the guides always pass `--port`.
- In VS Code, inline completions and semantic search always use GitHub. Only chat and utility tasks can be local.
- Ollama's default context window, as small as 4k, is far too small for agents. Set `OLLAMA_CONTEXT_LENGTH`.

## Findings from building `copilot-local` (issue #2)

- Linux systemd: Ollama runs as the `ollama` user (home `/usr/share/ollama`), so `~/.ollama/server.json` is ignored. The tool turns off cloud with `OLLAMA_NO_CLOUD=1` in its own drop-in, `/etc/systemd/system/ollama.service.d/50-copilot-local.conf`. An `override.conf` from `systemctl edit` sorts later and wins, and `--check` points this out.
- macOS: `launchctl setenv` is lost on reboot. The tool adds a LaunchAgent (`com.copilot-local.ollama-env`) that reapplies it at login.
- Windows: the tool sets a user environment variable rather than using `setx`. When it restarts Ollama, it copies the new value into the restart process, because child processes otherwise inherit the old environment.
- `GET /api/status` reports whether Ollama cloud is off. That's how the tool checks it.
- Work-managed Windows PCs: Defender's ASR rule "Use advanced protection against ransomware" (`C1DB55AB-C21A-4637-BB3F-A12568109D35`) blocks the unsigned `copilot-local.exe` after it's downloaded. Code-signing the Windows build fixes this.

## Decisions from issue #3 (model choice and launcher)

- **`copilot-local` is a launcher.** With no arguments, it starts Copilot CLI on a local model, setting everything up the first time. Settings are passed only to that Copilot run, so no shell profiles or `setx` are changed (the permanent setup in #4 is now optional). Plan: [plans/issue-3-model-selection.md](plans/issue-3-model-selection.md).
- **A default that just works.** A short [editable list](internal/models/shortlist.txt) is checked against Ollama's registry (download size, plus real context length read from the model file's header). The best-ranked model whose estimated memory need fits is picked automatically. The list's order sets the ranking. Models under `[menu-only]` (other mid-size, large, and huge models) can be chosen by hand but are never picked automatically, so a big Mac never quietly downloads a slow 70 GB model. Memory pools are never added together: GPU memory, about 2/3 to 3/4 of Apple unified memory, half of RAM for Windows built-in GPUs, or RAM minus headroom for CPU only.
- **Only tested models are offered.** A model must be local, claim tool support, accept the context size, make a real streamed tool call through `/v1/chat/completions`, and load with the full context (`/api/ps`). Results are saved in `models.json` in the settings folder; VS Code and Desktop setup can read it later.
- **Gemini is optional.** `model --ask-gemini` builds a prompt from the hardware facts and the checked list, and accepts only a model from that list. The prompt and fit rules come from the tested `dotfiles-bootstrap` setup.
- **Downloads are on demand in CI.** The e2e workflow downloads a small model only when started by hand.

## Findings from building the launcher (issue #3)

- On WSL using Ollama on Windows, the hardware that matters is the Windows host's, so the tool reads it through `powershell.exe` and `nvidia-smi.exe`.
- Ollama on Windows may load models with a smaller context than asked for. The `/api/ps` check catches this; `--context` can accept the smaller size.
- Hybrid models (Qwen 3.5/3.6, Qwen3-Next, Granite 4, Nemotron 3) keep a context cache on only some layers, and DeepSeek-style models (GLM-4.7-Flash) keep a compressed one. The memory estimate now accounts for both. Counting every layer had overestimated these models by up to 4 times. On a real load, `devstral:24b` at 32k used 18.2 GiB against an estimate of 19.3 GiB, which is a little high and so on the safe side.
- Newer models (Qwen 3.6, GLM-4.7-Flash, Nemotron 3, Qwen3-Coder-Next) have no chat template in the registry. Ollama handles their tool calls with a built-in renderer and parser named in the model config.
- Typical auto-picks at 64k context: `gpt-oss:20b` for a 24 GB GPU or 32 GB Mac, `qwen3.6` for 48 GB of VRAM or a 64 GB Mac, and `qwen3-coder-next` for a 96 GB Mac or bigger.

## Open questions and unverified items

- VS Code: whether a signed-in Copilot extension still sends any request content to GitHub when only BYOK models are used. The guide recommends staying signed out to be sure.
- GitHub Desktop: whether there's any way to turn off update checks.
- GitHub Copilot app: the telemetry opt-out location, and whether it accepts `http://` for remote hosts.
- Copilot CLI: where agentic memory is stored in BYOK or offline mode.
- Copilot CLI and BYOK under org policy for Business and Enterprise seats in offline mode.

## Candidate automation (next phase)

- ~~A cross-platform script that checks for and installs Ollama, sets `OLLAMA_CONTEXT_LENGTH` and `OLLAMA_NO_CLOUD`, pulls a tool-capable model, and checks `ollama show` for tool support.~~ Done in #2 and #3.
- VS Code: install `Ollama.ollama` and merge the privacy and BYOK keys into the user `settings.json`.
- Copilot CLI: write the environment block to the shell profile or PowerShell profile, run `copilot config autoUpdate false`, and optionally add a `copilot-local` alias.
- GitHub Desktop: no reliable automation, because settings live in app-internal storage. Provide a checklist, plus optional hosts-file blocking for the telemetry hosts.
- llama.cpp: a helper that starts `llama-server` with the right flags and prints the matching client config for each platform.
