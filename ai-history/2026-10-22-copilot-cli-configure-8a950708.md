# Persistent Copilot CLI local configuration

- Date: 2026-10-22 (America/Los_Angeles)
- Session: 8a950708-10be-447a-b552-932c54fe7521
- Outcome: Issue #4 implemented and merged.
- References: issue #4, PR #11, commit `fc5ebd1`, merge commit `3cfc9f4`

## Summary

Added `copilot-local configure` to persist the last qualified local model, offline-mode setting, and context-derived token limits. macOS/Linux receive a managed settings block in both bash and zsh profiles; Windows uses `setx`. The command disables Copilot CLI auto-update, warns about an overriding `providers.json`, and is safe to rerun. Updated command help, the README, and the Copilot CLI guide.

## Decisions

- Reuse the last tested model and its saved context; if none exists, direct the user to `copilot-local model`.
- Keep permanent configuration opt-in and leave the normal one-run launcher behavior unchanged.
- Replace only the tool-managed shell-profile block, preserving unrelated profile content.

## Verification and follow-up

- Local Go tests, `go vet`, Windows/macOS cross-builds, and `git diff --check` passed.
- PR #11 merged to `main` on 2026-10-22. Its Ubuntu, macOS, and Windows CI jobs all passed.
- The feature branch remains available; it can be deleted after confirming no other worktree or session needs it.
