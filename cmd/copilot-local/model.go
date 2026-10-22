package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/artofthesmart/local-inference-setup/internal/hardware"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/ollama"
)

type modelFlags struct {
	tag       string
	askGemini bool
	check     bool
	context   int
	dryRun    bool
	yes       bool
	// auto skips the pick-a-model menu, for first-time setup.
	auto bool
	// launcher means the launcher is asking, which has no --dry-run.
	launcher bool
}

func newModelFlags(out io.Writer) (*flag.FlagSet, *modelFlags) {
	f := &modelFlags{}
	fs := newFlagSet("model", helpFor("model", shortlistPath()), out)
	fs.StringVar(&f.tag, "model", "", "model tag")
	fs.BoolVar(&f.askGemini, "ask-gemini", false, "ask Gemini")
	fs.BoolVar(&f.check, "check", false, "re-test only")
	fs.IntVar(&f.context, "context", 0, "context length")
	fs.BoolVar(&f.dryRun, "dry-run", false, "download nothing")
	fs.BoolVar(&f.yes, "yes", false, "don't ask")
	return fs, f
}

func runModel(ctx context.Context, args []string, out io.Writer) int {
	fs, f := newModelFlags(out)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(out, "Unexpected %q. Use --model to name a model.\n", fs.Arg(0))
		return 2
	}
	if f.context != 0 && f.context < 2048 {
		fmt.Fprintln(out, "--context must be at least 2048.")
		return 2
	}
	if f.yes && f.askGemini {
		fmt.Fprintln(out, "--ask-gemini needs you to paste Gemini's answer, so it can't be used with --yes.")
		return 2
	}
	a, err := newApp(out)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	installed, err := a.installed(ctx)
	if err != nil && !f.dryRun {
		fmt.Fprintf(out, "%s Ollama isn't answering at %s.\nRun `copilot-local ollama` to set it up, or `copilot-local ollama --check` to see what's wrong.\n", red.Render("Can't continue:"), a.e.BaseURL)
		return 1
	}
	f.context = a.contextTarget(ctx, f.context)
	st, err := models.LoadState(a.e.StateDir)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	if installed != nil && st.Prune(installed) {
		_ = st.Save(a.e.StateDir)
	}

	if f.check {
		tag := models.Canonical(f.tag)
		if tag == "" && len(st.Models) > 0 {
			tag = st.Ordered()[0].Tag
		}
		if tag == "" {
			fmt.Fprintln(out, "No model has been set up yet. Run `copilot-local model` to pick one.")
			return 1
		}
		fmt.Fprintf(out, "%s %s\n", bold.Render("Testing"), tag)
		return a.testAndRecord(ctx, &st, tag, f.context)
	}

	p, code := a.chooseModel(ctx, *f, installed)
	if code != 0 || p.Tag == "" {
		return code
	}
	return a.confirmInstall(ctx, &st, p, *f)
}

// modelPlan is a model about to be downloaded and tested.
type modelPlan struct {
	Tag       string
	Info      *models.Info // nil when the registry doesn't know it, such as a model built on this computer
	Installed bool
	Fits      bool
	Budget    hardware.Budget
	FreeDisk  uint64
	Context   int
	Named     bool // chosen with --model rather than from the list
}

// chooseModel works out which model to set up: the one named, the user's pick, Gemini's pick, or the automatic pick. An empty Tag with code 0 means stop quietly.
func (a *app) chooseModel(ctx context.Context, f modelFlags, installed []string) (modelPlan, int) {
	out := a.out
	fmt.Fprintln(out, bold.Render("Checking your computer..."))
	h := a.hardware(ctx)
	a.printHardware(h)
	p := modelPlan{Budget: h.Budget(), FreeDisk: h.FreeDisk, Context: f.context}
	want := uint64(f.context)

	if f.tag != "" {
		tag := models.Canonical(f.tag)
		if err := models.ValidTag(tag); err != nil {
			fmt.Fprintln(out, red.Render("Can't use that model:"), err)
			return p, 2
		}
		p.Tag, p.Installed, p.Named = tag, slices.Contains(installed, tag), true
		fmt.Fprintf(out, "\n%s %s\n", bold.Render("Checking"), tag+" with Ollama's registry...")
		info, err := a.reg.Inspect(ctx, tag)
		switch {
		case err == nil && info.Context < want:
			fmt.Fprintf(out, "%s %s supports only %d tokens of context, and Copilot needs %d.\n", red.Render("Can't use that model:"), tag, info.Context, want)
			return p, 1
		case err == nil:
			p.Info, p.Fits = &info, info.Fits(p.Budget.Bytes, want)
		case p.Installed:
			fmt.Fprintf(out, "  %s couldn't check it (%v), so the downloaded copy will be tested as is.\n", amber.Render("Note:"), err)
			p.Fits = true
		default:
			fmt.Fprintln(out, red.Render("Can't use that model:"), err)
			return p, 1
		}
		return p, 0
	}

	list, source, err := models.LoadShortlist(shortlistPath())
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return p, 1
	}
	if source != "built-in list" {
		source = "your list (" + source + ")"
	}
	fmt.Fprintf(out, "\n%s\n", bold.Render(fmt.Sprintf("Checking %d models from the %s with Ollama's registry...", len(list), source)))
	checked := a.reg.CheckShortlist(ctx, list, want)
	for _, s := range checked.Skipped {
		fmt.Fprintln(out, dim.Render("  Skipped "+s))
	}
	if len(checked.OK) == 0 {
		fmt.Fprintln(out, red.Render("Can't continue:")+" none of the models could be checked. Are you online? You can also name one with --model.")
		return p, 1
	}
	pick, fits := models.Pick(checked.OK, p.Budget.Bytes, want)

	var tag string
	switch {
	case f.askGemini:
		var code int
		if tag, code = a.askGemini(ctx, h, checked.OK, f); code != 0 || tag == "" {
			return p, code
		}
	case interactive() && !f.yes && !f.auto:
		var err error
		if tag, err = a.selectModel(ctx, checked.OK, pick, fits, p.Budget, want, installed); err != nil || tag == "" {
			fmt.Fprintln(out, "Cancelled. Nothing was downloaded.")
			return p, 1
		}
		if tag == geminiChoice {
			var code int
			if tag, code = a.askGemini(ctx, h, checked.OK, f); code != 0 || tag == "" {
				return p, code
			}
		}
	default:
		tag = pick.Tag
	}
	for _, m := range checked.OK {
		if m.Tag == tag {
			p.Tag, p.Info, p.Fits = m.Tag, &m, m.Fits(p.Budget.Bytes, want)
		}
	}
	p.Installed = slices.Contains(installed, p.Tag)
	return p, 0
}

const geminiChoice = "\x00gemini"

// menuModels orders the menu: the pick first, then biggest to smallest. Menu-only models that don't fit are left out unless already downloaded.
func menuModels(list []models.Info, pick models.Info, b hardware.Budget, want uint64, installed []string) []models.Info {
	ordered := slices.DeleteFunc(slices.Clone(list), func(m models.Info) bool {
		return m.MenuOnly && !m.Fits(b.Bytes, want) && m.Tag != pick.Tag && !slices.Contains(installed, m.Tag)
	})
	slices.SortStableFunc(ordered, func(x, y models.Info) int {
		switch {
		case x.Tag == pick.Tag:
			return -1
		case y.Tag == pick.Tag:
			return 1
		}
		return int(int64(y.Size)/1e6 - int64(x.Size)/1e6)
	})
	return ordered
}

func (a *app) selectModel(ctx context.Context, list []models.Info, pick models.Info, fits bool, b hardware.Budget, want uint64, installed []string) (string, error) {
	var opts []huh.Option[string]
	for _, m := range menuModels(list, pick, b, want, installed) {
		label := fmt.Sprintf("%-24s %s", m.Tag, hardware.Size(m.Size))
		var tags []string
		if m.Tag == pick.Tag && fits {
			tags = append(tags, "recommended")
		}
		if slices.Contains(installed, m.Tag) {
			tags = append(tags, "downloaded")
		}
		if !m.Fits(b.Bytes, want) {
			tags = append(tags, "too big to run fully on your hardware, so slower")
		}
		if len(tags) > 0 {
			label += "  (" + strings.Join(tags, ", ") + ")"
		}
		if m.Note != "" {
			label += "  " + m.Note
		}
		opts = append(opts, huh.NewOption(label, m.Tag))
	}
	opts = append(opts, huh.NewOption("Help me choose with Gemini", geminiChoice), huh.NewOption("Cancel", ""))
	choice := pick.Tag
	err := huh.NewForm(huh.NewGroup(huh.NewSelect[string]().Title("Which model?").Description("Bigger models are smarter but need more memory. Sizes are downloads.").Options(opts...).Value(&choice))).RunWithContext(ctx)
	return choice, err
}

func (a *app) printModelPlan(p modelPlan) {
	a.line("Model:", p.Tag)
	switch {
	case p.Installed:
		a.line("Download:", "none, it's already downloaded")
	case p.Info != nil:
		a.line("Download:", hardware.Size(p.Info.Size))
	}
	if p.Info == nil {
		return
	}
	need := p.Info.MemoryNeed(uint64(p.Context))
	a.line("Memory needed:", fmt.Sprintf("about %s, including %d tokens of context", hardware.Size(need), p.Context))
	if !p.Fits {
		note := fmt.Sprintf("this is more than the %s your computer has for models, so part of it will run more slowly on the CPU.", hardware.Size(p.Budget.Bytes))
		if !p.Named {
			note += " It's the best fit on the list."
		}
		fmt.Fprintln(a.out, "  "+amber.Render("Note:")+" "+note)
	}
}

// diskOK refuses a download that won't fit on the disk, leaving 2 GB spare.
func (a *app) diskOK(p modelPlan) bool {
	if p.Installed || p.Info == nil || p.FreeDisk == 0 {
		return true
	}
	need := p.Info.Size + 2*hardware.GiB
	if p.FreeDisk >= need {
		return true
	}
	fmt.Fprintf(a.out, "%s %s needs %s of free disk space (including 2 GB spare), and only %s is free. Free up space, or pick a smaller model.\n", red.Render("Not enough disk space:"), p.Tag, hardware.Size(need), hardware.Size(p.FreeDisk))
	return false
}

// confirm asks a yes/no question, or explains how to say yes when there's no terminal.
func confirm(ctx context.Context, out io.Writer, yes, canDryRun bool, question string) int {
	if yes {
		return 0
	}
	if !interactive() {
		if canDryRun {
			fmt.Fprintln(out, "\nRun again with --yes to go ahead, or --dry-run to only preview.")
		} else {
			fmt.Fprintln(out, "\nRun again with --yes to go ahead.")
		}
		return 2
	}
	ok := false
	if err := huh.NewForm(huh.NewGroup(huh.NewConfirm().Title(question).Affirmative("Yes").Negative("No").Value(&ok))).RunWithContext(ctx); err != nil || !ok {
		fmt.Fprintln(out, "Cancelled. Nothing was downloaded.")
		return 1
	}
	return 0
}

func (a *app) confirmInstall(ctx context.Context, st *models.State, p modelPlan, f modelFlags) int {
	fmt.Fprintln(a.out, "\n"+bold.Render("Here's the plan:"))
	a.printModelPlan(p)
	if !a.diskOK(p) {
		return 1
	}
	if f.dryRun {
		fmt.Fprintln(a.out, "\n"+dim.Render("Dry run: nothing was downloaded."))
		return 0
	}
	q := "OK to download and test it?"
	if p.Installed {
		q = "OK to test it?"
	}
	if code := confirm(ctx, a.out, f.yes, !f.launcher, q); code != 0 {
		return code
	}
	return a.installAndTest(ctx, st, p)
}

func (a *app) installAndTest(ctx context.Context, st *models.State, p modelPlan) int {
	if !p.Installed {
		fmt.Fprintf(a.out, "\n%s %s\n", bold.Render("Downloading"), p.Tag)
		if err := a.api.Pull(ctx, p.Tag, a.progressPrinter()); err != nil {
			fmt.Fprintln(a.out, "\n"+red.Render("Failed:"), err)
			fmt.Fprintln(a.out, "Run the same command again to pick up where it stopped.")
			return 1
		}
		if interactive() {
			fmt.Fprintln(a.out)
		}
	}
	return a.testAndRecord(ctx, st, p.Tag, p.Context)
}

// progressPrinter shows download progress: one updating line on a terminal, every 10% otherwise.
func (a *app) progressPrinter() func(models.Progress) {
	tty := interactive()
	lastPct, lastStatus := -1, ""
	return func(p models.Progress) {
		if p.Total == 0 {
			if p.Status != lastStatus {
				if tty && lastPct >= 0 {
					fmt.Fprintln(a.out)
				}
				fmt.Fprintln(a.out, dim.Render("  "+p.Status))
				lastStatus, lastPct = p.Status, -1
			}
			return
		}
		if p.Total < 100<<20 {
			// Small parts like the license or template finish instantly; skip their progress.
			return
		}
		pct := int(p.Completed * 100 / p.Total)
		if tty {
			fmt.Fprintf(a.out, "\r  %3d%%  %s of %s   ", pct, hardware.Size(p.Completed), hardware.Size(p.Total))
			lastPct = pct
			return
		}
		if pct/10 != lastPct/10 || lastPct < 0 {
			fmt.Fprintf(a.out, "  %3d%%  %s of %s\n", pct, hardware.Size(p.Completed), hardware.Size(p.Total))
			lastPct = pct
		}
		lastStatus = p.Status
	}
}

// toolTestTimeout covers loading a big model from disk the first time.
var toolTestTimeout = 3 * time.Minute

// qualify checks a downloaded model works for Copilot: local, tool-capable, big enough context, and a real tool call.
func (a *app) qualify(ctx context.Context, tag string, want int) ollama.Report {
	var r ollama.Report
	add := func(name string, st ollama.Status, detail, fix string) {
		r.Checks = append(r.Checks, ollama.Check{Name: name, Status: st, Detail: detail, Fix: fix})
	}
	show, err := a.api.Show(ctx, tag)
	if err != nil {
		add("Downloaded", ollama.Fail, err.Error(), "Run `copilot-local model --model "+tag+"` to download it.")
		return r
	}
	add("Downloaded", ollama.Pass, tag, "")
	if show.RemoteHost != "" {
		add("Runs on this computer", ollama.Fail, "it's a cloud model hosted at "+show.RemoteHost, "Pick a local model.")
		return r
	}
	add("Runs on this computer", ollama.Pass, "", "")
	if !show.HasTools() {
		add("Supports tool calling", ollama.Fail, "Ollama says it can't call tools, and Copilot needs that", "Pick another model.")
		return r
	}
	add("Supports tool calling", ollama.Pass, "", "")
	switch {
	case show.Context == 0:
		add(fmt.Sprintf("Supports %d tokens of context", want), ollama.Warn, "the model doesn't say", "")
	case show.Context < uint64(want):
		add(fmt.Sprintf("Supports %d tokens of context", want), ollama.Fail, fmt.Sprintf("only %d", show.Context), "Pick a model with a bigger context window.")
		return r
	default:
		add(fmt.Sprintf("Supports %d tokens of context", want), ollama.Pass, fmt.Sprintf("up to %d", show.Context), "")
	}

	fmt.Fprintln(a.out, dim.Render("  Loading the model and asking it to call a test tool. The first time can take a minute or two..."))
	res, err := a.toolTest(ctx, tag, 1024)
	if err == nil && res.Truncated {
		fmt.Fprintln(a.out, dim.Render("  The model ran out of room to answer; trying once more with more room..."))
		res, err = a.toolTest(ctx, tag, 2048)
	}
	switch {
	case err != nil:
		add("Tool-call test", ollama.Fail, err.Error(), "If it ran out of memory or time, pick a smaller model.")
		return r
	case !res.Passed:
		add("Tool-call test", ollama.Fail, res.Reason, "This model can't drive Copilot's tools reliably. Pick another model.")
		return r
	default:
		add("Tool-call test", ollama.Pass, fmt.Sprintf("called get_temperature for New York in %.0fs", res.Elapsed.Seconds()), "")
	}

	loaded, err := a.api.Running(ctx)
	var lm *models.Loaded
	for i := range loaded {
		if loaded[i].Tag == tag {
			lm = &loaded[i]
		}
	}
	switch {
	case err != nil || lm == nil:
		add("Context in use", ollama.Warn, "couldn't see the loaded model", "Run `ollama ps` and check the CONTEXT column.")
	case lm.Context == 0:
		add("Context in use", ollama.Warn, "this Ollama version doesn't report it", "Run `ollama ps` and check the CONTEXT column.")
	case lm.Context < uint64(want):
		add("Context in use", ollama.Fail, fmt.Sprintf("Ollama gave the model %d tokens, not %d", lm.Context, want), fmt.Sprintf("Run `copilot-local ollama` to set the context size, then `copilot-local model --check`. Or accept the smaller size by running again with --context %d.", lm.Context))
	default:
		add("Context in use", ollama.Pass, fmt.Sprintf("%d tokens", lm.Context), "")
	}
	if lm != nil {
		switch share := lm.GPUShare(); {
		case share >= 100:
			add("Runs on the GPU", ollama.Pass, "100% on the GPU", "")
		case share == 0:
			add("Runs on the GPU", ollama.Warn, "runs on the CPU only, so answers will be slow", "A smaller model, or a smaller --context, may fit on the GPU.")
		default:
			add("Runs on the GPU", ollama.Warn, fmt.Sprintf("%d%% on the GPU, the rest on the CPU, so answers will be slower", share), "A smaller model, or a smaller --context, may fit fully on the GPU.")
		}
	}
	return r
}

func (a *app) toolTest(ctx context.Context, tag string, maxTokens int) (models.ToolTest, error) {
	ctx, cancel := context.WithTimeout(ctx, toolTestTimeout)
	defer cancel()
	res, err := a.api.TestToolCall(ctx, tag, maxTokens)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return res, fmt.Errorf("no answer within %s", toolTestTimeout)
	}
	return res, err
}

// testAndRecord tests a model and remembers whether it passed.
func (a *app) testAndRecord(ctx context.Context, st *models.State, tag string, want int) int {
	fmt.Fprintln(a.out, "\n"+bold.Render("Testing it works with Copilot..."))
	r := a.qualify(ctx, tag, want)
	if r.OK() {
		st.Add(models.Qualified{Tag: tag, Tested: time.Now().UTC(), Context: want})
	} else {
		st.Remove(tag)
	}
	if err := st.Save(a.e.StateDir); err != nil {
		fmt.Fprintln(a.out, amber.Render("Couldn't save the result:"), err)
	}
	return report(a.out, r, tag+" is ready for Copilot.")
}
