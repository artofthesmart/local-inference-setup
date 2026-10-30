# Hardware-aware Gemini model queries

- Date: 2026-10-30 (UTC-07:00)
- Session: d63995ba-2c29-4f88-a52d-129b36115981
- Outcome: Completed standalone query generators and three-tier recommendations.
- References: Review branch `feat/gemini-query-options`, based on `main`.

## Summary

Added standalone PowerShell and POSIX shell scripts that inspect hardware and print a prompt for the user to paste into Gemini. README one-liners cover remote execution, local execution, and saving output. The scripts do not contact Gemini, install software, or download models.

The prompt requests speed, balanced, and intelligence-first options that remain usable on the detected hardware. It asks for current exact Ollama tags, source links, tool-calling evidence, realistic performance estimates, and memory planning including the cache at 65,536 tokens.

## Decisions

Unknown hardware remains explicitly unverified. Shared memory is not added to RAM, and WSL users are directed to inspect the Windows host when appropriate. If fewer than three practical models fit, Gemini should explain reused choices or return NONE rather than invent a fit.

The existing Go Gemini flow also requests three tiers while preserving its single `MODEL:` copy-back contract using the balanced option.

## Verification and follow-up

Passed the full Go test suite and `go vet ./...`, shell syntax checks, PowerShell parsing and execution, and piped invocations. Tests cover mocked Linux/NVIDIA, Apple unified memory, Windows hardware, missing probes, unsupported platforms, and copy-back compatibility. Native hardware detection on Windows and macOS was not exercised locally; CI provides platform checks. No feature work was deferred. Closure publishes the changes and this history on the review branch; merge and branch deletion remain separate actions.
