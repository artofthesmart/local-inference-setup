package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/artofthesmart/local-inference-setup/internal/launch"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/ollama"
	"github.com/artofthesmart/local-inference-setup/internal/system"
)

func runConfigure(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("configure", helpFor("configure", ""), out)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(out, "Unexpected argument %q.\n\n%s", fs.Arg(0), helpFor("configure", ""))
		return 2
	}

	e, err := newEnv(out)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	state, err := models.LoadState(e.StateDir)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	qualified := state.Ordered()
	if len(qualified) == 0 {
		fmt.Fprintln(out, "No qualified model has been saved yet. Run `copilot-local model` first, then run `copilot-local configure`.")
		return 1
	}
	model := qualified[0]
	if err := models.ValidTag(model.Tag); err != nil {
		fmt.Fprintf(out, "The saved model name %q is invalid. Run `copilot-local model` to select a valid model.\n", model.Tag)
		return 1
	}
	if model.Context < 2048 {
		fmt.Fprintf(out, "The saved context for %s is invalid. Run `copilot-local model --check` to repair it.\n", model.Tag)
		return 1
	}
	copilotPath, err := findCopilot()
	if err != nil {
		fmt.Fprintln(out, err)
		return 1
	}
	settings := launch.Settings{BaseURL: e.BaseURL, Model: model.Tag, Context: model.Context}
	if warning := launch.ProvidersWarning(os.Getenv, e.Home, e.Exists); warning != "" {
		fmt.Fprintln(out, amber.Render("Warning:")+" "+warning)
	}

	if _, err := e.Runner.Run(ctx, system.Cmd{Name: copilotPath, Args: []string{"config", "autoUpdate", "false"}}); err != nil {
		fmt.Fprintln(out, red.Render("Couldn't disable Copilot auto-update:"), err)
		return 1
	}
	if err := persistCopilotSettings(ctx, e, settings); err != nil {
		fmt.Fprintln(out, red.Render("Couldn't persist all Copilot settings:"), err)
		fmt.Fprintln(out, "Run `copilot-local configure` again after fixing the problem; it is safe to re-run.")
		return 1
	}
	fmt.Fprintf(out, "%s Copilot is configured to use %s offline.\n", green.Render("Done."), model.Tag)
	if e.Info.OS == "windows" {
		fmt.Fprintln(out, "Open a new terminal for the saved environment settings to take effect.")
	} else {
		fmt.Fprintln(out, "The settings are in both ~/.bashrc and ~/.zshrc; open a new terminal to use them.")
	}
	return 0
}

func persistCopilotSettings(ctx context.Context, e *ollama.Env, settings launch.Settings) error {
	switch e.Info.OS {
	case "linux", "darwin":
		return launch.UpdateShellProfiles(e.Home, settings)
	case "windows":
		for _, variable := range settings.Vars() {
			name, value, _ := strings.Cut(variable, "=")
			if _, err := e.Runner.Run(ctx, system.Cmd{Name: "setx", Args: []string{name, value}}); err != nil {
				return fmt.Errorf("setx failed for %s (earlier variables may already be saved): %w", name, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("persistent configuration isn't supported on %s", e.Info.OS)
	}
}
