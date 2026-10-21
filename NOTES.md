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

## Notable findings

- The Copilot CLI base URL must include `/v1` for Ollama. Without it, the CLI calls `/chat/completions`, which we confirmed with a mock server. The official docs example omits `/v1`.
- GitHub Desktop rejects plain `http://` URLs to non-local hosts. Use an SSH tunnel or HTTPS for remote servers.
- `llama-server`'s default port is now `9931`, so the guides always pass `--port`.
- In VS Code, inline completions and semantic search always use GitHub. Only chat and utility tasks can be local.
- Ollama's default context window, as small as 4k, is far too small for agents. Set `OLLAMA_CONTEXT_LENGTH`.

## Open questions and unverified items

- VS Code: whether a signed-in Copilot extension still sends any request content to GitHub when only BYOK models are used. The guide recommends staying signed out to be sure.
- GitHub Desktop: whether there's any way to turn off update checks.
- GitHub Copilot app: the telemetry opt-out location, and whether it accepts `http://` for remote hosts.
- Copilot CLI: where agentic memory is stored in BYOK or offline mode.
- Copilot CLI and BYOK under org policy for Business and Enterprise seats in offline mode.

## Candidate automation (next phase)

- A cross-platform script that checks for and installs Ollama, sets `OLLAMA_CONTEXT_LENGTH` and `OLLAMA_NO_CLOUD`, pulls a tool-capable model, and checks `ollama show` for tool support.
- VS Code: install `Ollama.ollama` and merge the privacy and BYOK keys into the user `settings.json`.
- Copilot CLI: write the environment block to the shell profile or PowerShell profile, run `copilot config autoUpdate false`, and optionally add a `copilot-local` alias.
- GitHub Desktop: no reliable automation, because settings live in app-internal storage. Provide a checklist, plus optional hosts-file blocking for the telemetry hosts.
- llama.cpp: a helper that starts `llama-server` with the right flags and prints the matching client config for each platform.
