// Command copilot-local sets up GitHub Copilot to use local AI models.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/artofthesmart/local-inference-setup/internal/ollama"
)

// version is set at release time.
var version = "dev"

const usage = `copilot-local sets up GitHub Copilot to run on local AI models.

Usage:
  copilot-local                 Open the guided menu
  copilot-local ollama [flags]  Install Ollama and tune it for Copilot
  copilot-local version         Print the version

Flags for "ollama":
  --context N     Context length in tokens (default 65536)
  --yes           Don't ask before making changes
  --dry-run       Show what would change, and change nothing
  --check         Only check the current setup, and change nothing
  --undo          Remove the settings this tool added (Ollama stays installed)
  --skip-install  Don't install Ollama if it's missing

Guides: https://github.com/artofthesmart/local-inference-setup
`

var (
	bold  = lipgloss.NewStyle().Bold(true)
	green = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	amber = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	red   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dim   = lipgloss.NewStyle().Faint(true)
)

type ollamaFlags struct {
	context     int
	yes         bool
	dryRun      bool
	check       bool
	undo        bool
	skipInstall bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout))
}

func run(ctx context.Context, args []string, out io.Writer) int {
	if len(args) == 0 {
		return menu(ctx, out)
	}
	switch args[0] {
	case "ollama":
		f, err := parseOllamaFlags(args[1:], out)
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		return runOllama(ctx, f, out)
	case "version", "--version":
		fmt.Fprintln(out, "copilot-local", version)
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return 0
	default:
		fmt.Fprintf(out, "Unknown command %q.\n\n%s", args[0], usage)
		return 2
	}
}

func parseOllamaFlags(args []string, out io.Writer) (ollamaFlags, error) {
	var f ollamaFlags
	fs := flag.NewFlagSet("ollama", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { fmt.Fprint(out, usage) }
	fs.IntVar(&f.context, "context", ollama.DefaultContextLength, "context length")
	fs.BoolVar(&f.yes, "yes", false, "don't ask")
	fs.BoolVar(&f.dryRun, "dry-run", false, "show changes only")
	fs.BoolVar(&f.check, "check", false, "check only")
	fs.BoolVar(&f.undo, "undo", false, "remove our settings")
	fs.BoolVar(&f.skipInstall, "skip-install", false, "don't install Ollama")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if f.context < 2048 {
		fmt.Fprintln(out, "--context must be at least 2048.")
		return f, errors.New("bad context")
	}
	return f, nil
}

func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func menu(ctx context.Context, out io.Writer) int {
	if !interactive() {
		fmt.Fprint(out, usage)
		return 2
	}
	var choice string
	ctxLen := fmt.Sprint(ollama.DefaultContextLength)
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("What would you like to do?").
			Options(
				huh.NewOption("Set up Ollama for Copilot (install if needed, tune settings)", "setup"),
				huh.NewOption("Preview the Ollama changes without making them", "dry-run"),
				huh.NewOption("Undo the Ollama changes this tool made", "undo"),
				huh.NewOption("Quit", "quit"),
			).
			Value(&choice),
	), huh.NewGroup(
		huh.NewSelect[string]().
			Title("How much context should Ollama give each model?").
			Description("Copilot's agents need a lot. Bigger uses more memory.").
			Options(
				huh.NewOption("32k: smaller computers (16 GB of memory or less)", "32768"),
				huh.NewOption("64k: recommended", "65536"),
				huh.NewOption("128k: big GPUs (24 GB of video memory or more)", "131072"),
			).
			Value(&ctxLen),
	).WithHideFunc(func() bool { return choice == "undo" || choice == "quit" }))
	if err := form.RunWithContext(ctx); err != nil {
		return 130
	}
	f := ollamaFlags{context: ollama.DefaultContextLength}
	fmt.Sscan(ctxLen, &f.context)
	switch choice {
	case "setup":
	case "dry-run":
		f.dryRun = true
	case "undo":
		f.undo = true
	default:
		return 0
	}
	return runOllama(ctx, f, out)
}

func runOllama(ctx context.Context, f ollamaFlags, out io.Writer) int {
	e, err := ollama.NewEnv(out)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	if f.check {
		// Nothing was restarted, so there's no need to wait long for the server.
		e.WaitTimeout = 2 * time.Second
		fmt.Fprintln(out, bold.Render("Checking Ollama..."))
		return report(out, ollama.Verify(ctx, e, f.context))
	}
	fmt.Fprintln(out, bold.Render("Checking your computer..."))
	s, err := ollama.Detect(ctx, e)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	printState(out, e, s)

	var steps []ollama.Step
	if f.undo {
		steps = ollama.Undo(e, s)
		if len(steps) == 0 {
			fmt.Fprintln(out, "\nNothing to undo. This tool hasn't changed any Ollama settings here.")
			return 0
		}
	} else {
		steps, err = ollama.Plan(e, s, ollama.Options{ContextLength: f.context, SkipInstall: f.skipInstall})
		if err != nil {
			fmt.Fprintln(out, "\n"+amber.Render("Can't continue:"), err)
			return 1
		}
		if len(steps) == 0 {
			e.WaitTimeout = 2 * time.Second
			fmt.Fprintln(out, "\n"+green.Render("Everything is already set up.")+" Checking it works...")
			return report(out, ollama.Verify(ctx, e, f.context))
		}
	}

	fmt.Fprintln(out, "\n"+bold.Render("Here's what will change:"))
	printSteps(out, steps)
	if f.dryRun {
		fmt.Fprintln(out, "\n"+dim.Render("Dry run: nothing was changed."))
		return 0
	}
	if !f.yes {
		if !interactive() {
			fmt.Fprintln(out, "\nRun again with --yes to make these changes, or --dry-run to only preview them.")
			return 2
		}
		ok := false
		if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("OK to continue?").Affirmative("Yes").Negative("No").Value(&ok))).RunWithContext(ctx); err != nil || !ok {
			fmt.Fprintln(out, "Cancelled. Nothing was changed.")
			return 1
		}
	}

	if code := apply(ctx, out, steps); code != 0 {
		return code
	}
	if steps[0].Install {
		// The installer creates the service and app, so plan the settings now that they exist.
		fmt.Fprintln(out, "\n"+bold.Render("Ollama is installed. Now tuning its settings..."))
		if s, err = ollama.Detect(ctx, e); err == nil {
			steps, err = ollama.Plan(e, s, ollama.Options{ContextLength: f.context, SkipInstall: true})
		}
		if err != nil {
			fmt.Fprintln(out, red.Render("Error:"), err)
			return 1
		}
		printSteps(out, steps)
		if code := apply(ctx, out, steps); code != 0 {
			return code
		}
	}

	if f.undo {
		fmt.Fprintln(out, "\n"+green.Render("Done.")+" The settings this tool added are gone. Ollama itself is still installed.")
		return 0
	}
	fmt.Fprintln(out, "\n"+bold.Render("Checking it works..."))
	return report(out, ollama.Verify(ctx, e, f.context))
}

func printState(out io.Writer, e *ollama.Env, s *ollama.State) {
	line := func(label, value string) { fmt.Fprintf(out, "  %-18s %s\n", label, value) }
	osName := map[string]string{"linux": "Linux", "darwin": "macOS", "windows": "Windows"}[e.Info.OS]
	if e.Info.WSL {
		osName += " (WSL)"
	}
	line("System:", osName+" "+e.Info.Arch)
	if s.Installed() {
		line("Ollama:", strings.TrimSpace(s.Version+" at "+s.BinaryPath))
	} else {
		line("Ollama:", "not installed")
	}
	if s.ServerUp {
		line("Server:", "running at "+e.BaseURL)
	} else {
		line("Server:", "not running")
	}
}

func printSteps(out io.Writer, steps []ollama.Step) {
	for i, st := range steps {
		fmt.Fprintf(out, "  %d. %s\n", i+1, st.Title)
		for _, d := range st.Details {
			for _, l := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
				fmt.Fprintln(out, dim.Render("       "+l))
			}
		}
	}
}

func apply(ctx context.Context, out io.Writer, steps []ollama.Step) int {
	for _, st := range steps {
		fmt.Fprintf(out, "\n%s %s\n", bold.Render("→"), st.Title)
		err := st.Apply(ctx)
		switch {
		case errors.Is(err, ollama.ErrManualRestart):
			fmt.Fprintln(out, "  "+amber.Render("Action needed:"), "stop `ollama serve`, then start it again in a new terminal.")
		case err != nil:
			fmt.Fprintln(out, "  "+red.Render("Failed:"), err)
			fmt.Fprintln(out, "  Nothing after this step was changed. Fix the problem above and run again; it's safe to repeat.")
			return 1
		default:
			fmt.Fprintln(out, "  "+green.Render("Done"))
		}
	}
	return 0
}

func report(out io.Writer, r ollama.Report) int {
	for _, c := range r.Checks {
		tag := map[ollama.Status]lipgloss.Style{ollama.Pass: green, ollama.Warn: amber, ollama.Fail: red}[c.Status].Render(c.Status.String())
		fmt.Fprintf(out, "  %s  %s", tag, c.Name)
		if c.Detail != "" {
			fmt.Fprint(out, dim.Render(" ("+c.Detail+")"))
		}
		fmt.Fprintln(out)
		if c.Fix != "" && c.Status != ollama.Pass {
			fmt.Fprintln(out, "        Fix: "+c.Fix)
		}
	}
	if !r.OK() {
		fmt.Fprintln(out, "\n"+red.Render("Some checks failed.")+" Try the fixes above, then run again.")
		return 1
	}
	fmt.Fprintln(out, "\n"+green.Render("Ollama is ready for Copilot.")+" Next: pull a model (see the README).")
	return 0
}
