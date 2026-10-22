package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

const guideURL = "https://github.com/artofthesmart/local-inference-setup"

const topUsage = `copilot-local starts GitHub Copilot CLI on an AI model that runs on your own computer.
Nothing you type is sent to GitHub or any cloud AI service.

Usage:
  copilot-local [flags] [-- copilot arguments]
  copilot-local <command> [flags]

The first time, copilot-local sets everything up: it installs and tunes Ollama, picks a model
that fits your computer, downloads it, tests it, and starts Copilot. After that, it shows a
short menu with the model you used last at the top, so you can just press Enter.

Flags:
  --model TAG     Use this model instead of picking (for example gpt-oss:20b)
  --context N     Context size in tokens for first-time setup (default 65536)
  --yes           Don't ask before setting up or downloading; skip the menu

Commands:
  model     Add, change, or re-test a model (can ask Gemini to help you choose)
  configure Save the local model settings for future terminals
  vscode    Install the Ollama VS Code extension and configure local generation/privacy
  verify    Check the whole setup, including a real offline Copilot test prompt
  ollama    Install Ollama and tune it for Copilot (part of first-time setup)
  version   Print the version
  help      Show help for a command, for example: copilot-local help model

Examples:
  copilot-local                          Set up (first time), then start Copilot
  copilot-local --model llama3.1:8b      Start Copilot with a specific model
  copilot-local vscode                   Set up VS Code to use local Ollama models
  copilot-local -- -p "fix the tests"    Pass arguments to Copilot after --
  copilot-local model --ask-gemini       Get Gemini's advice on which model to use

Guides: ` + guideURL + "\n"

const modelUsage = `copilot-local model adds, changes, or re-tests the model Copilot uses.

Usage:
  copilot-local model [flags]

It reads your computer's memory and graphics card, checks a short list of models with
Ollama's registry (each must exist and handle a big enough context), and helps you pick one.
Then it downloads the model and tests that it can really call tools, the way Copilot does.
Only models that pass are offered when you start Copilot.

Flags:
  --model TAG     Skip choosing and use this model (it still has to pass every test)
  --ask-gemini    Make a prompt describing your computer for Gemini, then paste back its pick.
                  The prompt holds only hardware facts and the model list.
  --check         Re-test the model you used last (or --model), and change nothing
  --context N     Context size to test for, in tokens (default: Ollama's setting, or 65536)
  --dry-run       Show the pick, its size, and any Gemini prompt; download nothing
  --yes           Don't ask before downloading; picks automatically without --model

Examples:
  copilot-local model                         Choose from the list that fits your computer
  copilot-local model --model gpt-oss:20b     Download and test one model
  copilot-local model --check                 Re-test the last model

The model list is in a text file you can edit: copy it to %s
to use your own. To remove a model, run: ollama rm <tag>
`

const ollamaUsage = `copilot-local ollama installs Ollama if needed and tunes it for Copilot:
a big context window, and Ollama's cloud features turned off.

Usage:
  copilot-local ollama [flags]

Flags:
  --context N     Context length in tokens (default 65536)
  --yes           Don't ask before making changes
  --dry-run       Show what would change, and change nothing
  --check         Only check the current setup, and change nothing
  --undo          Remove the settings this tool added (Ollama stays installed)
  --skip-install  Don't install Ollama if it's missing
`

const configureUsage = `copilot-local configure saves your selected local model settings for future terminals.

Usage:
  copilot-local configure

It writes the offline provider settings to both ~/.bashrc and ~/.zshrc on macOS/Linux,
or saves them with setx on Windows. It also disables Copilot CLI auto-update. Safe to re-run.
`

const vscodeUsage = `copilot-local vscode installs the Ollama VS Code extension and configures
the selected VS Code profile for local Copilot generation and the privacy settings in the guide.

Usage:
  copilot-local vscode [--profile NAME] [--yes] [--dry-run]

Flags:
  --profile NAME  Configure this VS Code profile; if omitted, choose interactively when needed
  --yes           Don't ask before installing or replacing settings
  --dry-run       Show planned settings changes without applying them

The command requires VS Code 1.127 or newer. With multiple profiles, choose one or pass
--profile; --yes requires an explicit profile when the choice is ambiguous. Profile-specific
settings go in that profile, while application-wide telemetry and update settings go in
Default Profile settings. It backs up each file before editing, preserves unrelated settings
and JSONC comments, and asks before replacing conflicting values. GitHub.com account-level
privacy controls are separate.
`

const verifyUsage = `copilot-local verify checks the local setup and prints PASS/FAIL results with fixes.

Usage:
  copilot-local verify [flags]

Flags:
  --model TAG     Check this model (default: the last selected, qualified model)
  --context N     Expected context size (default: saved model context, or 65536)
  --profile NAME  VS Code profile to check (required when multiple profiles exist)
  --skip-vscode   Check CLI-only setup without requiring VS Code
  --timeout D     Maximum time for model and Copilot tests (default 3m)

It checks Ollama, cloud opt-out, a real tool call, loaded context, CLI environment,
and VS Code's extension and privacy settings. It runs a fixed Copilot test prompt
offline in a temporary workspace with isolated settings and no tools, then deletes
the temporary files. No installation, download, or settings changes are made.
Models may be loaded into memory. Unset CLI variables are warnings, since the
launcher supplies them; conflicting variables and provider files are failures.
Desktop configuration and VS Code's selected chat model still need manual checks.
Exit codes: 0 = no failures (warnings/manual checks may remain), 1 = failed, 2 = bad arguments.
`

// helpFor returns the help text for a command, or "" if there's no such command.
func helpFor(cmd, shortlistPath string) string {
	switch cmd {
	case "", "copilot-local":
		return topUsage
	case "model":
		return fmt.Sprintf(modelUsage, shortlistPath)
	case "configure":
		return configureUsage + "\nGuides: " + guideURL + "\n"
	case "vscode":
		return vscodeUsage + "\nGuides: " + guideURL + "\n"
	case "verify":
		return verifyUsage + "\nGuides: " + guideURL + "\n"
	case "ollama":
		return ollamaUsage + "\nGuides: " + guideURL + "\n"
	case "version":
		return "copilot-local version prints the version.\n"
	case "help":
		return "copilot-local help <command> shows help for a command.\n"
	}
	return ""
}

func newFlagSet(name, help string, out io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, help) }
	return fs
}

// undocumentedFlags lists flags missing from a help text. Tests use it to keep help complete.
func undocumentedFlags(fs *flag.FlagSet, help string) []string {
	var missing []string
	fs.VisitAll(func(f *flag.Flag) {
		if !strings.Contains(help, "--"+f.Name+" ") && !strings.Contains(help, "--"+f.Name+"\n") {
			missing = append(missing, f.Name)
		}
	})
	return missing
}
