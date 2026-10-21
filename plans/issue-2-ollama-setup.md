# Plan: automate Ollama install and tuning (issue #2)

Tracks [issue #2](https://github.com/artofthesmart/local-inference-setup/issues/2). This is the first scripting issue, so it also sets up the Go project that #3–#6 build on.

## What the user experiences

1. They paste one line to download and start the tool.
2. It asks a few simple questions, shows what it will change, and asks "OK to continue?"
3. It installs Ollama if missing, sets the context length, turns off Ollama cloud, restarts Ollama, and checks everything worked.
4. It ends with a pass/fail list. Anything that failed comes with the fix.

Running it again is safe: if everything is already set, it changes nothing.

## Command shape

- `copilot-local` with no arguments opens the guided menu (later issues add steps to it like model selection).
- `copilot-local ollama` runs only this step.
- Flags: `--context 65536` (default 64k), `--yes` (no prompts), `--dry-run`, `--undo`, `--skip-install`, `--help` (including a link to the repo).

## Steps

1. **Detect:** the OS, CPU architecture, RAM/VRAM, WSL, and other system details. Check whether Ollama is installed (`ollama --version` and the default install paths). Check whether the server answers at `127.0.0.1:11434/api/version`. On Linux, check whether systemd manages Ollama.
2. **Install** (only if missing, after the user confirms): run Ollama's official installers so they keep owning GPU drivers, services, and PATH. Linux and macOS use `https://ollama.com/install.sh`. Windows uses `https://ollama.com/install.ps1`. We download to a temp file first, rather than piping into a shell.
3. **Configure:**
   - **Cloud off, the same on every OS:** merge `"disable_ollama_cloud": true` into `~/.ollama/server.json`. This keeps other keys, makes a backup first, and needs no admin rights.
   - **Context length:** set `OLLAMA_CONTEXT_LENGTH`, which works differently on each OS:
     - Linux with systemd: our own drop-in file `/etc/systemd/system/ollama.service.d/50-copilot-local.conf`, then `daemon-reload` (needs sudo).
     - Linux without systemd: a marked block in `~/.profile`.
     - macOS: `launchctl setenv`, plus a LaunchAgent so the setting survives reboot. The README's manual steps currently miss the reboot problem, so we'll fix that too.
     - Windows: a user environment variable in `HKCU\Environment`, then notify running apps.
4. **Restart Ollama:**
   - Linux: `systemctl restart ollama`.
   - macOS: quit the app, then `open -a Ollama`.
   - Windows: stop the Ollama processes, then start `ollama app.exe`.
5. **Verify:** wait up to 30 seconds for the server, then call `/v1/models`. Read the server log to confirm both settings took effect. Log locations:
   - Linux: `journalctl -u ollama`
   - macOS: `~/.ollama/logs/server.log`
   - Windows: `%LOCALAPPDATA%\Ollama\server.log`
6. **Undo:** remove only what we added and restart Ollama. Never uninstall Ollama itself.

## Code layout

Go module with `cmd/copilot-local` and `internal/system`, which covers OS detection and a `Runner` interface for shell commands. Plus `internal/ollama`:

- `detect.go`, `install.go`, `serverjson.go`, `verify.go`
- Per-OS files for environment settings and restart.

The guided menu uses charmbracelet/huh. Because every command goes through `Runner`, tests and `--dry-run` can see what would run without changing the machine.

## Tests

- **Unit tests,** run on Linux, macOS, and Windows in CI:
  - The `server.json` merge: no file, an empty file, existing keys, and bad JSON.
  - The drop-in file and plist contents.
  - Detection, using fake command output.
  - Re-running changes nothing.
  - Undo restores the original state.
  - The exact commands run on each OS.

## Distribution

GoReleaser builds programs for Linux, macOS, and Windows on both x64 and arm64. Users paste one line:

- Mac and Linux: `curl -fsSL …/install.sh | sh`
- Windows: `irm …/install.ps1 | iex`

That small launcher downloads the right program, checks its checksum, and runs it. Nothing is installed globally (except Ollama). The README gets a "Quick start (automated)" section.

## Open questions

- **WSL:** if Ollama already runs on the Windows side, configure that one rather than installing a second copy in WSL? Answer: Ask the user which environment they plan to use, default to using the Windows hosted Ollama instance.
- **Existing `OLLAMA_HOST=0.0.0.0`:** leave it alone, but warn that the server is exposed to the network.
- **Private repo:** the one-line installers need a public repo or public releases. This repo will be made public before release.

## Order of work

1. Scaffolding: `go.mod`, the CLI skeleton, `Runner`, and unit-test CI on 3 OSes.
2. Detection and the `server.json` merge.
3. Context length for each OS, plus restart.
4. Verification and the pass/fail report.
5. Install via the official scripts.
6. `--undo` and `--dry-run`.
7. GoReleaser, the launcher scripts, and the README quick start.

Also: record the Go decision in `NOTES.md`, save this plan in the repo at `plans/issue-2-ollama-setup.md`, and link it from issue #2.

## Changes made while building

- **Choosing per-OS behavior at runtime, not compile time.** Behavior depends on the detected OS rather than Go build tags. Because of that, every OS's commands are unit-tested on every OS. Windows settings go through PowerShell via `Runner`.
- **Cloud off on Linux with systemd uses `OLLAMA_NO_CLOUD=1` in our drop-in.** The service runs as the `ollama` user, so `~/.ollama/server.json` is ignored there. Other systems use `server.json` as planned.
- **Added `--check`,** which only checks and changes nothing.
- **WSL with Ollama on the Windows side:** the tool stops and asks the user to run it from Windows PowerShell, so it doesn't install a second copy.
- **No end-to-end install test in CI.** It downloaded several GB of Ollama on every run, and Ollama's own installer is trusted to work. Unit tests cover every OS's commands. A one-time real run on all 3 OSes passed before this was removed.
- **Code-signing for Windows is a follow-up.** Defender's ransomware ASR rule blocks the unsigned build on managed PCs.
