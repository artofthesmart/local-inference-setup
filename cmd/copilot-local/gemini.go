package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/huh"

	"github.com/artofthesmart/local-inference-setup/internal/hardware"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/system"
)

// copyText puts text on the clipboard; replaced in tests.
var copyText = clipboard.WriteAll

// askGemini writes a prompt for Gemini and reads back its pick. An empty tag with code 0 means stop quietly.
func (a *app) askGemini(ctx context.Context, h hardware.Hardware, candidates []models.Info, f modelFlags) (string, int) {
	out := a.out
	prompt := models.Prompt(h, candidates, f.context)
	fmt.Fprintln(out, "\n"+bold.Render("Your prompt for Gemini:"))
	fmt.Fprintln(out, dim.Render(strings.Repeat("-", 60)))
	fmt.Fprintln(out, prompt)
	fmt.Fprintln(out, dim.Render(strings.Repeat("-", 60)))
	if f.dryRun {
		fmt.Fprintln(out, dim.Render("Dry run: the prompt wasn't saved or copied."))
		return "", 0
	}

	if path, err := a.savePrompt(prompt); err == nil {
		fmt.Fprintln(out, "Saved to "+path)
	} else {
		fmt.Fprintln(out, amber.Render("Couldn't save the prompt:"), err)
	}
	if err := a.copyPrompt(ctx, prompt); err == nil {
		fmt.Fprintln(out, "Copied to your clipboard.")
	}

	if !interactive() {
		fmt.Fprintf(out, "\nPaste it into Gemini at %s, then run:\n  copilot-local model --model <the model Gemini picks>\n", models.GeminiURL)
		return "", 0
	}
	open := true
	if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("Open Gemini in your browser?").Description(models.GeminiURL).Affirmative("Yes").Negative("No").Value(&open))).RunWithContext(ctx); err != nil {
		fmt.Fprintln(out, "Cancelled. Nothing was downloaded.")
		return "", 1
	}
	if open {
		if err := a.openBrowser(ctx, models.GeminiURL); err != nil {
			fmt.Fprintf(out, "Couldn't open the browser. Go to %s yourself.\n", models.GeminiURL)
		}
	}

	allowed := make([]string, len(candidates))
	for i, m := range candidates {
		allowed[i] = m.Tag
	}
	var answer string
	input := huh.NewInput().
		Title("Paste Gemini's answer (the line starting with MODEL:)").
		Description("Allowed: " + strings.Join(allowed, ", ")).
		Value(&answer).
		Validate(func(s string) error {
			if _, err := models.ParseAnswer(s, allowed); err != nil && !errors.Is(err, models.ErrNone) {
				return err
			}
			return nil
		})
	if err := huh.NewForm(huh.NewGroup(input)).RunWithContext(ctx); err != nil {
		fmt.Fprintln(out, "Cancelled. Nothing was downloaded.")
		return "", 1
	}
	tag, err := models.ParseAnswer(answer, allowed)
	if err != nil {
		fmt.Fprintln(out, "Gemini thinks none of these models will run well on this computer. You can still pick one yourself with `copilot-local model`, but expect slow answers.")
		return "", 1
	}
	fmt.Fprintln(out, "Gemini picked "+bold.Render(tag)+".")
	return tag, 0
}

// savePrompt writes the prompt to Documents, or the home folder if there isn't one.
func (a *app) savePrompt(prompt string) (string, error) {
	dir := filepath.Join(a.e.Home, "Documents")
	if !a.e.Exists(dir) {
		dir = a.e.Home
	}
	path := filepath.Join(dir, "copilot-local-gemini-prompt.txt")
	return path, os.WriteFile(path, []byte(prompt+"\n"), 0o644)
}

func (a *app) copyPrompt(ctx context.Context, prompt string) error {
	if a.e.Info.WSL {
		_, err := a.e.Runner.Run(ctx, system.Cmd{Name: "clip.exe", Stdin: prompt})
		return err
	}
	return copyText(prompt)
}

func (a *app) openBrowser(ctx context.Context, url string) error {
	var c system.Cmd
	switch {
	case a.e.Info.OS == "darwin":
		c = system.Cmd{Name: "open", Args: []string{url}}
	case a.e.Info.OS == "windows":
		c = system.Cmd{Name: "rundll32", Args: []string{"url.dll,FileProtocolHandler", url}}
	case a.e.Info.WSL:
		// explorer.exe opens the page but always reports failure.
		_, _ = a.e.Runner.Run(ctx, system.Cmd{Name: "explorer.exe", Args: []string{url}})
		return nil
	default:
		c = system.Cmd{Name: "xdg-open", Args: []string{url}}
	}
	_, err := a.e.Runner.Run(ctx, c)
	return err
}
