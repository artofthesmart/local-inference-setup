# Local inference guides and the copilot-local Ollama tool

- Date: 2026-10-21 (America/Los_Angeles)
- Session: 84d7fab6-c71a-4479-941e-a35b6eac817b
- Outcome: Platform guides merged. Issue #2 implemented, with CI green on 3 OSes.
- References: PR #1, PR #7, issues #2–#8

## Summary

Researched and wrote guides for running GitHub Copilot on local Ollama, with an Advanced section on llama.cpp, for 3 tools: VS Code, Copilot CLI, and GitHub Desktop with the Copilot app. Each guide also turns off telemetry where possible. Created the repo and filed issues #2–#6 for automation ideas.

Planned and built `copilot-local`, a Go program that does the following:

- Installs Ollama.
- Sets a 64k context length.
- Turns off Ollama cloud.
- Restarts Ollama.
- Shows a pass/fail report with fixes.

It also supports `--dry-run`, `--check`, and `--undo`. Added one-line launchers that check the download's checksum, GoReleaser config, and a README quick start.

## Decisions

- **Go:** one self-contained program per OS, so users install nothing first.
- **Runtime OS branching:** chosen over build tags so every OS's commands are unit-tested everywhere.
- **Linux systemd:** turn off cloud with `OLLAMA_NO_CLOUD` in our own drop-in file, because the service ignores the user's `server.json`.
- **macOS:** a LaunchAgent, because `launchctl setenv` doesn't survive a reboot.
- **Desktop app fallback:** if the app doesn't start the server, run `ollama serve` directly.
- **Full install test:** moved to an on-demand `e2e` workflow at the user's request, because it downloads several GB.

## Verification and follow-up

- Unit tests pass on Linux, macOS, and Windows in CI.
- A one-time real install passed on all 3 OSes.
- `install.sh` was tested against a local release, including refusing a tampered download.
- On a real Windows PC, only `--dry-run` and `--check` were run, so no changes were made there.
- Follow-ups:
  - The one-line launchers need the repo or its releases to be public.
  - Code-signing the Windows build (#8): Defender ASR blocks the unsigned program on managed PCs.
  - Issues #3–#6 remain open.
