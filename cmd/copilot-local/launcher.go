package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/charmbracelet/huh"

	"github.com/artofthesmart/local-inference-setup/internal/launch"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/ollama"
)

type launchFlags struct {
	tag     string
	context int
	yes     bool
}

func newLaunchFlags(out io.Writer) (*flag.FlagSet, *launchFlags) {
	f := &launchFlags{}
	fs := newFlagSet("copilot-local", topUsage, out)
	fs.StringVar(&f.tag, "model", "", "model tag")
	fs.IntVar(&f.context, "context", 0, "context length")
	fs.BoolVar(&f.yes, "yes", false, "don't ask")
	return fs, f
}

// runLaunch starts Copilot on a local model, setting everything up the first time.
func runLaunch(ctx context.Context, args []string, out io.Writer) int {
	fs, f := newLaunchFlags(out)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.context != 0 && f.context < 2048 {
		fmt.Fprintln(out, "--context must be at least 2048.")
		return 2
	}
	path, err := findCopilot()
	if err != nil {
		fmt.Fprintln(out, err)
		fmt.Fprintln(out, "\nYou can set up a model now and start Copilot later with: copilot-local model")
		return 1
	}
	a, err := newApp(out)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	if w := launch.ProvidersWarning(os.Getenv, a.e.Home, a.e.Exists); w != "" {
		fmt.Fprintln(out, amber.Render("Warning:")+" "+w)
	}
	st, err := models.LoadState(a.e.StateDir)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	installed, ollamaErr := a.installed(ctx)
	if ollamaErr != nil && len(st.Models) > 0 {
		// Set up before, but Ollama isn't answering now: repair it, then carry on.
		fmt.Fprintf(out, "%s Ollama isn't answering at %s. Let's fix that first.\n\n", amber.Render("Note:"), a.e.BaseURL)
		if code := runOllama(ctx, ollamaFlags{context: a.contextTarget(ctx, f.context), yes: f.yes, doneMsg: "Ollama is ready."}, out); code != 0 {
			return code
		}
		installed, ollamaErr = a.installed(ctx)
	}
	if ollamaErr == nil && st.Prune(installed) {
		_ = st.Save(a.e.StateDir)
	}

	var tag string
	code := 0
	switch {
	case ollamaErr != nil || len(st.Models) == 0:
		tag, code = a.firstRun(ctx, &st, *f, installed)
	case f.tag != "":
		tag, code = a.useNamed(ctx, &st, *f, installed)
	case !interactive() || f.yes:
		tag = st.Ordered()[0].Tag
	default:
		tag, code = a.launchMenu(ctx, &st, *f)
	}
	if code != 0 || tag == "" {
		return code
	}

	want := f.context
	if q, ok := st.Get(tag); ok && want == 0 {
		want = q.Context
	}
	if want == 0 {
		want = a.contextTarget(ctx, 0)
	}
	st.LastUsed = tag
	if err := st.Save(a.e.StateDir); err != nil {
		fmt.Fprintln(out, amber.Render("Couldn't save your choice:"), err)
	}
	fmt.Fprintf(out, "\n%s %s %s\n\n", green.Render("Starting Copilot with"), bold.Render(tag), dim.Render("(on this computer, offline)"))
	code, err = startCopilot(path, fs.Args(), launch.Settings{BaseURL: a.e.BaseURL, Model: tag, Context: want})
	if err != nil {
		fmt.Fprintln(out, red.Render("Couldn't start Copilot:"), err)
		return 1
	}
	return code
}

// firstRun sets up Ollama and a model, asking once.
func (a *app) firstRun(ctx context.Context, st *models.State, f launchFlags, installed []string) (string, int) {
	out := a.out
	fmt.Fprintln(out, bold.Render("Welcome! Let's get Copilot running on this computer."))
	want := a.contextTarget(ctx, f.context)
	s, err := ollama.Detect(ctx, a.e)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return "", 1
	}
	var steps []ollama.Step
	if s.WindowsSideServer {
		fmt.Fprintln(out, dim.Render("Using the Ollama running on Windows. To change its settings, run copilot-local ollama in PowerShell."))
	} else {
		steps, err = ollama.Plan(a.e, s, ollama.Options{ContextLength: want})
	}
	if err != nil {
		fmt.Fprintln(out, amber.Render("Can't continue:"), err)
		return "", 1
	}
	p, code := a.chooseModel(ctx, modelFlags{tag: f.tag, context: want, yes: f.yes, auto: true}, installed)
	if code != 0 || p.Tag == "" {
		return "", code
	}

	fmt.Fprintln(out, "\n"+bold.Render("Here's the plan:"))
	if len(steps) > 0 {
		fmt.Fprintln(out, "  Set up Ollama:")
		printSteps(out, steps)
	}
	a.printModelPlan(p)
	a.line("Then:", "start Copilot with it")
	if !a.diskOK(p) {
		return "", 1
	}
	if code := confirm(ctx, out, f.yes, false, "OK to set up?"); code != 0 {
		return "", code
	}
	if len(steps) > 0 {
		fmt.Fprintln(out)
		if code := runOllama(ctx, ollamaFlags{context: want, yes: true, doneMsg: "Ollama is ready."}, out); code != 0 {
			return "", code
		}
		if now, err := a.installed(ctx); err == nil {
			p.Installed = slices.Contains(now, p.Tag)
		}
	}
	if code := a.installAndTest(ctx, st, p); code != 0 {
		return "", code
	}
	return p.Tag, 0
}

// useNamed starts a model named with --model, setting it up first if it hasn't passed the tests here.
func (a *app) useNamed(ctx context.Context, st *models.State, f launchFlags, installed []string) (string, int) {
	tag := models.Canonical(f.tag)
	if _, ok := st.Get(tag); ok {
		return tag, 0
	}
	fmt.Fprintln(a.out, tag+" hasn't been set up on this computer yet.")
	p, code := a.chooseModel(ctx, modelFlags{tag: tag, context: a.contextTarget(ctx, f.context), yes: f.yes}, installed)
	if code != 0 {
		return "", code
	}
	if code := a.confirmInstall(ctx, st, p, modelFlags{yes: f.yes, launcher: true}); code != 0 {
		return "", code
	}
	return tag, 0
}

const (
	menuAnother = "\x00another"
	menuRepair  = "\x00repair"
)

// launchMenu lists the models that passed, last used first.
func (a *app) launchMenu(ctx context.Context, st *models.State, f launchFlags) (string, int) {
	var opts []huh.Option[string]
	for _, q := range st.Ordered() {
		label := q.Tag
		if q.Tag == st.LastUsed {
			label += " (last used)"
		}
		opts = append(opts, huh.NewOption(label, q.Tag))
	}
	opts = append(opts,
		huh.NewOption("Get another model...", menuAnother),
		huh.NewOption("Check / repair setup", menuRepair),
		huh.NewOption("Quit", ""))
	choice := st.Ordered()[0].Tag
	if err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Start Copilot with:").Options(opts...).Value(&choice))).RunWithContext(ctx); err != nil {
		return "", 0
	}
	switch choice {
	case menuAnother:
		installed, _ := a.installed(ctx)
		want := a.contextTarget(ctx, f.context)
		p, code := a.chooseModel(ctx, modelFlags{context: want}, installed)
		if code != 0 || p.Tag == "" {
			return "", code
		}
		if code := a.confirmInstall(ctx, st, p, modelFlags{}); code != 0 {
			return "", code
		}
		return p.Tag, 0
	case menuRepair:
		return "", runOllama(ctx, ollamaFlags{context: a.contextTarget(ctx, f.context)}, a.out)
	}
	return choice, 0
}
