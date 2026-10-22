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
	// doneMsg replaces the closing message, for first-time setup.
	doneMsg string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout))
}

func run(ctx context.Context, args []string, out io.Writer) int {
	if len(args) == 0 || (strings.HasPrefix(args[0], "-") && !isHelpFlag(args[0]) && args[0] != "--version") {
		return runLaunch(ctx, args, out)
	}
	switch args[0] {
	case "model":
		return runModel(ctx, args[1:], out)
	case "configure":
		return runConfigure(ctx, args[1:], out)
	case "vscode":
		return runVSCode(ctx, args[1:], out)
	case "verify":
		return runVerify(ctx, args[1:], out)
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
	case "help", "-h", "-help", "--help":
		topic := ""
		if args[0] == "help" && len(args) > 1 {
			topic = args[1]
		}
		h := helpFor(topic, shortlistPath())
		if h == "" {
			fmt.Fprintf(out, "No help for %q.\n\n%s", topic, topUsage)
			return 2
		}
		fmt.Fprint(out, h)
		return 0
	default:
		fmt.Fprintf(out, "Unknown command %q.\n\n%s", args[0], topUsage)
		return 2
	}
}

func isHelpFlag(s string) bool {
	return s == "-h" || s == "-help" || s == "--help"
}

func newOllamaFlags(out io.Writer) (*flag.FlagSet, *ollamaFlags) {
	f := &ollamaFlags{}
	fs := newFlagSet("ollama", helpFor("ollama", ""), out)
	fs.IntVar(&f.context, "context", ollama.DefaultContextLength, "context length")
	fs.BoolVar(&f.yes, "yes", false, "don't ask")
	fs.BoolVar(&f.dryRun, "dry-run", false, "show changes only")
	fs.BoolVar(&f.check, "check", false, "check only")
	fs.BoolVar(&f.undo, "undo", false, "remove our settings")
	fs.BoolVar(&f.skipInstall, "skip-install", false, "don't install Ollama")
	return fs, f
}

func parseOllamaFlags(args []string, out io.Writer) (ollamaFlags, error) {
	fs, f := newOllamaFlags(out)
	if err := fs.Parse(args); err != nil {
		return *f, err
	}
	if f.context < 2048 {
		fmt.Fprintln(out, "--context must be at least 2048.")
		return *f, errors.New("bad context")
	}
	return *f, nil
}

// interactive is replaced in tests.
var interactive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func runOllama(ctx context.Context, f ollamaFlags, out io.Writer) int {
	done := ollamaReady
	if f.doneMsg != "" {
		done = f.doneMsg
	}
	e, err := newEnv(out)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	if f.check {
		// Nothing was restarted, so there's no need to wait long for the server.
		e.WaitTimeout = 2 * time.Second
		fmt.Fprintln(out, bold.Render("Checking Ollama..."))
		return report(out, ollama.Verify(ctx, e, f.context), done)
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
			return report(out, ollama.Verify(ctx, e, f.context), done)
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
	return report(out, ollama.Verify(ctx, e, f.context), done)
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

const ollamaReady = "Ollama is ready for Copilot. Next: run `copilot-local` to pick a model and start Copilot."

func report(out io.Writer, r ollama.Report, okMsg string) int {
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
	if okMsg != "" {
		fmt.Fprintln(out, "\n"+green.Render(okMsg))
	}
	return 0
}
