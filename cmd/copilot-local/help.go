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
  ollama    Install Ollama and tune it for Copilot (part of first-time setup)
  version   Print the version
  help      Show help for a command, for example: copilot-local help model

Examples:
  copilot-local                          Set up (first time), then start Copilot
  copilot-local --model llama3.1:8b      Start Copilot with a specific model
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

// helpFor returns the help text for a command, or "" if there's no such command.
func helpFor(cmd, shortlistPath string) string {
	switch cmd {
	case "", "copilot-local":
		return topUsage
	case "model":
		return fmt.Sprintf(modelUsage, shortlistPath)
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
