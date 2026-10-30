# Gemini chat, agentic, and clipboard queries

- Date: 2026-10-30 (UTC-07:00)
- Session: 966921ff-3b06-4626-aead-f7bbd9dc6b08
- Outcome: Broader standalone recommendations and optional ENTER-to-copy interaction.
- References: #16 (merged), #17 (clipboard and session history).

## Summary

Reviewed the difference between the standalone Gemini query scripts and the built-in model picker. The built-in picker restricts Gemini to verified shortlist candidates; the standalone scripts already request current Ollama discovery but previously considered only agentic coding models.

Updated both standalone prompts to request CHAT and AGENTIC groups, each with speed, balanced, and intelligence recommendations. The user scoped this work to the caller scripts, leaving the automated setup pipeline unchanged. Published the recommendation changes in #16, which was subsequently merged.

Added an interactive separator and ENTER-to-copy action. Clipboard contents contain only the generated query, not terminal instructions. The shell script supports macOS, WSL, Wayland, and common X11 clipboard utilities; PowerShell uses Set-Clipboard. Missing or failing clipboard access produces a manual-copy fallback.

## Decisions

CHAT does not require tools or large agent context. AGENTIC requires supported, hardware-feasible 131072-token context and evidence of reliable tool calling. These are overlapping use cases, not mutually exclusive model classes. Chat-only behavior is controlled by the client withholding tools.

## Verification and follow-up

Local `go test ./...`, `go vet ./...`, shell syntax, formatting, and whitespace checks passed. Shell clipboard fixtures cover five backends and failures. Real terminal checks confirmed ENTER-to-copy for direct and piped execution without including UI text in the copied query. Windows host execution policy blocked local script execution; Windows CI passed the PowerShell fixtures and clipboard interaction unit tests. Initial macOS CI exposed older-shell parsing of a captured here-document; prompt rendering was moved into a function, with final CI pending. No live model downloads or Gemini answers were tested.

Keep both branches until their work and history are integrated and no session needs them. Archive readiness requires final remote preservation and activity checks; host archive semantics are not established by this record.
