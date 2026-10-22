# Plan: one-command launcher with a model that just works (issue #3, plus the launch part of #4)

Tracks [issue #3](https://github.com/artofthesmart/local-inference-setup/issues/3). It also covers the "start Copilot CLI on a local model" part of [#4](https://github.com/artofthesmart/local-inference-setup/issues/4). It reuses the tested model checks and Gemini prompt from `artofthesmart/dotfiles-bootstrap`.

## The big idea

`copilot-local` starts **Copilot CLI connected to your local model**. Nothing else is needed.

- **First run:** it does all the setup itself. It sets up Ollama (from #2), picks a model that fits your computer, downloads it, tests it, then starts Copilot. You confirm once ("OK to set up? Downloads 12.8 GB").
- **Every later run:** a short menu where the **last model you used is first**, so you just press Enter. Your other working models are listed below it.

## What the user sees

```
$ copilot-local
Start Copilot with:
> gpt-oss:20b (last used)
  llama3.1:8b
  Get another model...
  Check / repair setup
  Quit
```

- Choosing a model starts `copilot` with these settings, for that run only: `COPILOT_OFFLINE=true`, `COPILOT_PROVIDER_BASE_URL=http://localhost:11434/v1`, `COPILOT_MODEL`, and the prompt and output token limits that match the context size. No shell profiles or `setx` are changed.
- Anything after `--` is passed through to Copilot, for example `copilot-local -- -p "fix the tests"`.
- **Get another model...** shows the shortlist models that fit this computer, each with its download size, and marks the recommended one. A "Help me choose with Gemini" option opens the Gemini prompt flow.
- If no terminal is attached (scripts or CI), it never shows a menu. It uses the last model, or `--model`.

## Picking the default model ("just works")

- **Shortlist:** one editable file, checked against Ollama's registry for download size and real context of 64k or more.

  | Model | Download | Context |
  | --- | --- | --- |
  | `llama3.1:8b` | 4.6 GiB | 128k |
  | `mistral-nemo:12b` | 6.6 GiB | 1M |
  | `gpt-oss:20b` | 12.8 GiB | 128k |
  | `devstral:24b` | 13.3 GiB | 128k |
  | `qwen3-coder:30b` | 17.3 GiB | 256k |
  | `qwen3.6:latest` | 21.1 GiB | 256k |

- **Auto-pick:** the best-ranked shortlist model whose estimated memory need fits safely. List order sets the ranking; models under `[menu-only]` (extra mid-size, large, and huge models) are offered in the menu only when they fit, and are never auto-picked. The estimate is the download size plus the 64k KV cache plus headroom, counted against the right memory pool:
  - **NVIDIA:** VRAM.
  - **Apple Silicon:** about 75% of unified memory.
  - **Windows iGPU:** 50% of RAM.
  - **CPU only, including Intel Macs:** RAM minus OS headroom.

  Memory pools are never added together, following the dotfiles-bootstrap rules. If nothing fits, the tool says so plainly and offers the smallest model anyway, with a warning.
- **Gemini is optional:** use "Help me choose with Gemini" in the menu, or `copilot-local model --ask-gemini`. The prompt is shown, saved to Documents, and copied to the clipboard, and gemini.google.com can be opened. You paste back `MODEL: tag`, and the tool only accepts tags from the checked shortlist.

## Steps

1. **Hardware detection** (`internal/hardware`): CPU, RAM, GPU and VRAM, and free disk space on the drive Ollama saves models to. All commands go through `Runner`, so every OS is unit-tested.
   - **Linux:** `/proc`, then `nvidia-smi`, then `lspci`.
   - **macOS:** `sysctl`.
   - **Windows:** the sum of `Win32_PhysicalMemory`, plus `Win32_VideoController` and `nvidia-smi`. `AdapterRAM` is never used.
2. **Shortlist and registry check** (`internal/models`):
   - Read the size from the manifest.
   - Range-read the GGUF header, parsed properly by key, to get `<arch>.context_length`.
   - Keep only models meeting the context target. Skip a model with a warning if its check fails.
   - Then the fit estimate and auto-pick.
3. **Download:**
   - Check free disk space against the download size plus 2 GiB.
   - Reuse the model if it's already in `/api/tags`; otherwise run `ollama pull` with live progress.
   - Then check `/api/show`: the `tools` capability is present and there's no `remote_host`.
4. **Test:**
   - Send one streaming `/v1/chat/completions` request with the `get_temperature` / "What is the temperature in New York?" example: 180-second limit, temperature 0, `max_tokens` 1024, retrying once at 2048 if the reply is cut off.
   - Pass only on a real structured tool call. JSON written as plain text fails.
   - Then `/api/ps` must show a context of at least the target, and the CPU/GPU split is printed.
   - A model is "qualified" only after it passes. Only qualified models appear in the launch menu.
5. **State file** (`StateDir/models.json`): qualified models with test date, last used model, context target. Models removed with `ollama rm` drop out automatically.
6. **Launcher:**
   - Find `copilot` on the PATH. If it's missing, say how to install it.
   - Warn if `~/.copilot/providers.json` exists, because it overrides the settings.
   - Run `copilot` with the environment above and pass through its exit code. Ctrl+C goes to Copilot.
7. **First-run flow:** if Ollama isn't set up, run the #2 Ollama setup first. Then auto-pick, download, test, save, and launch. One summary and one "OK?".
8. **Help:**
   - `copilot-local --help` and `-h`, plus `copilot-local help <command>` and `<command> --help`.
   - Each gives a plain-English description, examples, and every flag with its default.
   - CLI tests check that every flag appears in the help.

## Commands

```
copilot-local [--model TAG] [--yes] [-- copilot args...]   Start Copilot on a local model (first run sets everything up)
copilot-local model [--ask-gemini] [--model TAG] [--check] [--dry-run] [--yes]   Add, change, or re-test a model
copilot-local ollama [flags]                                Ollama install and tuning (from #2)
copilot-local version | help [command] | --help
```

- `--yes` never downloads more than the auto-picked model, and refuses to run Gemini because that needs a pasted answer.
- `--dry-run` shows the plan, the picked model and its size, and the Gemini prompt if asked, without changing anything.

## Out of scope (kept for later issues)

- Saving environment variables permanently in shell profiles or with `setx` (the rest of #4). The launcher makes this unnecessary for most people, so #4 will be updated to match.
- Starting the CLI against llama.cpp or a remote URL (`--endpoint`). The README's Advanced section covers it for now.
- VS Code (#5) and GitHub Desktop and the Copilot app (#6). They will read the same state file.

## Tests

- **Unit tests:**
  - GGUF parser.
  - Registry check using `httptest`.
  - Hardware parsing per OS with `FakeRunner`.
  - Fit estimate and auto-pick: an 8 GB laptop, 16 GB Mac, 24 GB GPU, iGPU, CPU-only, and a case where nothing fits.
  - Disk check.
  - Paste-back parsing.
  - Prompt rendering per OS.
  - Parsing the tool-call stream.
  - Context check in `/api/ps`.
  - State file reads and writes, including a model that was removed.
  - Building the launch environment, with a `providers.json` warning.
  - Menu order: last used comes first.
- **CLI tests:**
  - Help text for every command.
  - The full no-terminal flow against a fake Ollama server and a fake `copilot` binary that records its environment and arguments.
- **On-demand e2e (`e2e.yml` only):**
  - Run `copilot-local model --model llama3.1:8b --yes` and expect a pass.
  - Then launch with a stub `copilot` to check the environment it receives.

## Docs

- **README Quick start:** download the tool, run `copilot-local`, done.
- **`docs/copilot-cli.md`:** "The easy way: `copilot-local`" comes first. The manual steps stay below it.
- **`NOTES.md`:** the launcher decision, the shortlist plus auto-pick, Gemini as optional, and credit to dotfiles-bootstrap.
- Save this plan as `plans/issue-3-model-selection.md`, link it from #3, and add a comment on #4 explaining the narrower scope.

## Order of work

1. Save the plan in the repo and comment on #3 and #4.
2. Hardware detection.
3. Shortlist, GGUF, registry check, and auto-pick.
4. Download and test.
5. State file and launcher.
6. Menu, first-run flow, and help.
7. Gemini option.
8. Docs, e2e step, and the PR (base `main`, closes #3).
