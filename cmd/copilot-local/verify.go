package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/launch"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/ollama"
	"github.com/artofthesmart/local-inference-setup/internal/system"
	"github.com/artofthesmart/local-inference-setup/internal/vscode"
)

var probeCopilot = launch.Probe

type verifyFlags struct {
	model      string
	context    int
	profile    string
	skipVSCode bool
	timeout    time.Duration
}

func newVerifyFlags(out io.Writer) (*flag.FlagSet, *verifyFlags) {
	f := &verifyFlags{}
	fs := newFlagSet("verify", helpFor("verify", ""), out)
	fs.StringVar(&f.model, "model", "", "model to check")
	fs.IntVar(&f.context, "context", 0, "expected context size")
	fs.StringVar(&f.profile, "profile", "", "VS Code profile to check")
	fs.BoolVar(&f.skipVSCode, "skip-vscode", false, "check CLI-only setup")
	fs.DurationVar(&f.timeout, "timeout", 3*time.Minute, "maximum time for inference tests")
	return fs, f
}

func runVerify(ctx context.Context, args []string, out io.Writer) int {
	fs, f := newVerifyFlags(out)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || f.context < 0 || f.context > 0 && f.context < 2048 || f.timeout <= 0 || f.skipVSCode && f.profile != "" {
		fmt.Fprintln(out, "Invalid arguments: context must be at least 2048, timeout must be positive, and --profile cannot be combined with --skip-vscode.")
		return 2
	}
	if f.model != "" {
		if err := models.ValidTag(f.model); err != nil {
			fmt.Fprintln(out, "Invalid model:", err)
			return 2
		}
		f.model = models.Canonical(f.model)
	}
	a, err := newApp(out)
	if err != nil {
		fmt.Fprintln(out, "Error:", err)
		return 1
	}
	var r ollama.Report
	add := func(name string, status ollama.Status, detail, fix string) {
		r.Checks = append(r.Checks, ollama.Check{Name: name, Status: status, Detail: detail, Fix: fix})
	}
	fmt.Fprintln(out, bold.Render("Checking the local setup..."))
	model, err := verificationModel(a.e.StateDir, *f)
	if err != nil {
		add("Selected model", ollama.Fail, err.Error(), "Run `copilot-local model`, then run verification again.")
	} else {
		add("Selected model", ollama.Pass, model.Tag, "")
	}
	want := model.Context
	if want == 0 {
		want = ollama.DefaultContextLength
	}
	a.e.WaitTimeout = 0
	ollamaReport := ollama.Verify(ctx, a.e, want)
	r.Checks = append(r.Checks, ollamaReport.Checks...)
	if model.Tag != "" {
		settings := launch.Settings{BaseURL: a.e.BaseURL, Model: model.Tag, Context: model.Context}
		r.Checks = append(r.Checks, verifyCLIEnvironment(settings, a.e)...)
		testCtx, cancel := context.WithTimeout(ctx, f.timeout)
		if ollamaReport.OK() {
			modelReport := a.qualify(testCtx, model.Tag, model.Context)
			r.Checks = append(r.Checks, modelReport.Checks...)
			if modelReport.OK() {
				verifyCopilotPrompt(testCtx, a, settings, add)
			} else {
				add("Copilot offline prompt", ollama.Fail, "Not run because the model checks failed", "Fix the model checks above, then run verification again.")
			}
		} else {
			add("Copilot offline prompt", ollama.Fail, "Not run because Ollama checks failed", "Fix the Ollama checks above, then run verification again.")
		}
		cancel()
	}
	if !f.skipVSCode {
		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		r.Checks = append(r.Checks, verifyVSCode(checkCtx, a.e, f.profile)...)
		cancel()
	}
	fmt.Fprintln(out, "\nManual checks (not verified automatically):")
	fmt.Fprintln(out, "  VS Code: select the local Ollama model in chat; workspace settings or policy can override user settings.")
	fmt.Fprintf(out, "  GitHub Desktop: Settings > Copilot > Providers: OpenAI-compatible, %s/v1, authentication None.\n", strings.TrimRight(a.e.BaseURL, "/"))
	fmt.Fprintln(out, "  Select the local model for BOTH commit messages and conflict resolution; generate a test commit message and confirm it with `ollama ps`.")
	fmt.Fprintln(out, "  Desktop: Settings > Advanced > Usage: turn off usage stats. Complete the GitHub.com account privacy controls in the README.")
	fmt.Fprintln(out, "  Copilot app: choose a local session and the Ollama model; see docs/github-desktop.md.")
	return report(out, r, "Automated checks passed. Complete the manual checks above; this is not a network-privacy audit.")
}

func verificationModel(dir string, f verifyFlags) (models.Qualified, error) {
	state, err := models.LoadState(dir)
	if err != nil {
		return models.Qualified{}, err
	}
	var model models.Qualified
	if f.model == "" {
		ordered := state.Ordered()
		if len(ordered) == 0 {
			return model, errors.New("no saved model; run `copilot-local model` or specify --model")
		}
		model = ordered[0]
	} else {
		model, _ = state.Get(f.model)
		model.Tag = f.model
	}
	if err := models.ValidTag(model.Tag); err != nil {
		return models.Qualified{}, err
	}
	model.Tag = models.Canonical(model.Tag)
	if f.context != 0 {
		model.Context = f.context
	} else if f.model != "" && model.Context == 0 {
		model.Context = ollama.DefaultContextLength
	}
	if model.Context < 2048 {
		return models.Qualified{}, errors.New("saved model context must be at least 2048")
	}
	return model, nil
}

func verifyCLIEnvironment(settings launch.Settings, e *ollama.Env) []ollama.Check {
	var checks []ollama.Check
	for _, variable := range settings.Vars() {
		name, want, _ := strings.Cut(variable, "=")
		check := ollama.Check{Name: name, Status: ollama.Pass}
		got := os.Getenv(name)
		switch {
		case got == "":
			check.Status = ollama.Warn
			check.Detail = "Unset in this terminal; the copilot-local launcher supplies it"
			check.Fix = "Use `copilot-local` to launch, or run `copilot-local configure` and open a new terminal."
		case got != want:
			check.Status = ollama.Fail
			// Never print inherited values: a URL may contain credentials.
			check.Detail = "Does not match the selected local model settings"
			check.Fix = "Run `copilot-local configure` and open a new terminal, or use `copilot-local` to launch."
		}
		checks = append(checks, check)
	}
	check := ollama.Check{Name: "Copilot provider overrides", Status: ollama.Pass}
	if launch.ProvidersWarning(os.Getenv, e.Home, e.Exists) != "" {
		check.Status = ollama.Fail
		check.Detail = "A providers.json override exists; the isolated probe will not use it"
		check.Fix = "Review COPILOT_PROVIDERS_CONFIG or providers.json in COPILOT_HOME (default ~/.copilot); rename the override to use environment settings."
	}
	for _, variable := range []struct{ name, want string }{
		{"COPILOT_PROVIDER_API_KEY_COMMAND", ""},
		{"COPILOT_PROVIDER_HEADERS", ""},
		{"COPILOT_PROVIDER_WIRE_MODEL", settings.Model},
		{"COPILOT_PROVIDER_MODEL_ID", settings.Model},
		{"COPILOT_PROVIDER_WIRE_API", "completions"},
		{"COPILOT_PROVIDER_TRANSPORT", "http"},
	} {
		if value := os.Getenv(variable.name); value != "" && value != variable.want {
			check.Status = ollama.Fail
			check.Detail = "Conflicting provider override: " + variable.name
			check.Fix = "Unset the conflicting override and retry; the isolated probe uses only the launcher settings."
			break
		}
	}
	checks = append(checks, check)
	return checks
}

func verifyCopilotPrompt(ctx context.Context, a *app, settings launch.Settings, add func(string, ollama.Status, string, string)) {
	path, err := findCopilot()
	if err == nil {
		fmt.Fprintln(a.out, "  Running an isolated offline Copilot prompt (no tools)...")
		err = probeCopilot(ctx, path, settings)
	}
	if err != nil {
		add("Copilot offline prompt", ollama.Fail, err.Error(), "Install/update Copilot CLI, then run `copilot-local model --check` and retry; use --timeout 5m on slow hardware.")
		return
	}
	add("Copilot offline prompt", ollama.Pass, "Fixed test reply received with isolated offline settings", "")
	loaded, err := a.api.Running(ctx)
	if err == nil {
		for _, m := range loaded {
			if m.Tag == settings.Model && m.Context >= uint64(settings.Context) {
				add("Loaded model context", ollama.Pass, fmt.Sprintf("%s: %d tokens", m.Tag, m.Context), "")
				return
			}
		}
		err = fmt.Errorf("%s is not loaded with at least %d tokens", settings.Model, settings.Context)
	}
	add("Loaded model context", ollama.Fail, err.Error(), fmt.Sprintf("Run `copilot-local ollama --context %d`, restart Ollama and retry; update Ollama if /api/ps omits context_length.", settings.Context))
}

func verifyVSCode(ctx context.Context, e *ollama.Env, name string) []ollama.Check {
	var r ollama.Report
	add := func(label string, err error, fix string) {
		check := ollama.Check{Name: label, Status: ollama.Pass}
		if err != nil {
			check.Status, check.Detail, check.Fix = ollama.Fail, err.Error(), fix
		}
		r.Checks = append(r.Checks, check)
	}
	code, err := findVSCode()
	if err != nil {
		add("VS Code installed", err, "Install VS Code and put `code` on PATH, or pass --skip-vscode for CLI-only setup.")
		return r.Checks
	}
	text, err := e.Runner.Run(ctx, system.Cmd{Name: code, Args: []string{"--version"}})
	if err == nil {
		version, ok := parseVSCodeVersion(text)
		if !ok || version[0] < 1 || version[0] == 1 && version[1] < 127 {
			err = errors.New("VS Code 1.127 or newer is required")
		}
	}
	add("VS Code version", err, "Update VS Code to 1.127 or newer.")
	userDir, err := vscodeUserDir(e)
	var profile vscode.Profile
	if err == nil {
		var profiles []vscode.Profile
		profiles, err = vscode.Profiles(userDir)
		if err == nil {
			// Verification must never prompt or silently choose among multiple profiles.
			profile, err = selectVSCodeProfile(ctx, profiles, name, true)
		}
	}
	if err != nil {
		add("VS Code profile", err, "Pass --profile with an existing profile name; repair malformed profile metadata in VS Code.")
		return r.Checks
	}
	args := []string{"--list-extensions"}
	if profile.Location != "" {
		args = append(args, "--profile", profile.Name)
	}
	text, err = e.Runner.Run(ctx, system.Cmd{Name: code, Args: args})
	if err == nil {
		found := false
		for _, extension := range strings.Fields(text) {
			if strings.EqualFold(extension, vscode.ExtensionID) {
				found = true
			}
		}
		if !found {
			err = errors.New("the Ollama extension is missing from the selected profile")
		}
	}
	fix := "Run `copilot-local vscode --profile " + fmt.Sprintf("%q", profile.Name) + "`."
	add("VS Code Ollama extension", err, fix)
	plans, err := buildVSCodeSettingsPlans(userDir, profile)
	if err == nil {
		var missing []string
		for _, plan := range plans {
			for _, change := range plan.changes {
				missing = append(missing, change.Key)
			}
		}
		if len(missing) > 0 {
			err = fmt.Errorf("missing or conflicting settings: %s", strings.Join(missing, ", "))
		}
	}
	add("VS Code local/privacy settings", err, fix)
	return r.Checks
}
