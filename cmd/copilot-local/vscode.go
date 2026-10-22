package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/artofthesmart/local-inference-setup/internal/ollama"
	"github.com/artofthesmart/local-inference-setup/internal/system"
	"github.com/artofthesmart/local-inference-setup/internal/vscode"
	"github.com/charmbracelet/huh"
)

var (
	findVSCode = func() (string, error) {
		path, err := exec.LookPath("code")
		if err != nil {
			return "", err
		}
		return resolveVSCodeCommand(path, runtime.GOOS)
	}
	askVSCodeProfile = func(ctx context.Context, profiles []vscode.Profile) (string, error) {
		var options []huh.Option[string]
		for _, profile := range profiles {
			options = append(options, huh.NewOption(profile.Name, profile.Name))
		}
		choice := profiles[0].Name
		err := huh.NewForm(huh.NewGroup(
			huh.NewSelect[string]().Title("Which VS Code profile should be configured?").Options(options...).Value(&choice),
		)).RunWithContext(ctx)
		return choice, err
	}
	confirmVSCode = func(ctx context.Context, question string) (bool, error) {
		ok := false
		err := huh.NewForm(huh.NewGroup(
			huh.NewConfirm().Title(question).Affirmative("Yes").Negative("No").Value(&ok),
		)).RunWithContext(ctx)
		return ok, err
	}
)

type vscodeFlags struct {
	profile string
	yes     bool
	dryRun  bool
}

type vscodeSettingsPlan struct {
	path    string
	current []byte
	changes []vscode.SettingChange
	updated []byte
}

func runVSCode(ctx context.Context, args []string, out io.Writer) int {
	fs := newFlagSet("vscode", helpFor("vscode", ""), out)
	f := vscodeFlags{}
	fs.StringVar(&f.profile, "profile", "", "VS Code profile name (required when selecting non-interactively)")
	fs.BoolVar(&f.yes, "yes", false, "don't ask before installing or replacing settings")
	fs.BoolVar(&f.dryRun, "dry-run", false, "show planned changes without applying them")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(out, "Unexpected argument %q.\n\n%s", fs.Arg(0), helpFor("vscode", ""))
		return 2
	}

	e, err := newEnv(out)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	code, err := findVSCode()
	if err != nil {
		fmt.Fprintf(out, "%s VS Code's `code` command wasn't found or couldn't be run: %v. Install VS Code and add its command-line launcher to PATH.\n", red.Render("Error:"), err)
		return 1
	}
	versionText, err := e.Runner.Run(ctx, system.Cmd{Name: code, Args: []string{"--version"}})
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), "couldn't read the VS Code version:", err)
		return 1
	}
	version, ok := parseVSCodeVersion(versionText)
	if !ok {
		fmt.Fprintln(out, red.Render("Error:"), "couldn't understand the version returned by `code --version`.")
		return 1
	}
	if version[0] < 1 || version[0] == 1 && version[1] < 127 {
		fmt.Fprintf(out, "%s VS Code 1.127 or newer is required for the Ollama extension; found %d.%d.%d. Upgrade VS Code, then run `copilot-local vscode` again.\n", red.Render("Can't continue:"), version[0], version[1], version[2])
		return 1
	}

	userDir, err := vscodeUserDir(e)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	profiles, err := vscode.Profiles(userDir)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}
	profile, err := selectVSCodeProfile(ctx, profiles, f.profile, f.yes)
	if err != nil {
		fmt.Fprintln(out, red.Render("Can't continue:"), err)
		return 2
	}
	plans, err := buildVSCodeSettingsPlans(userDir, profile)
	if err != nil {
		fmt.Fprintln(out, red.Render("Error:"), err)
		return 1
	}

	fmt.Fprintf(out, "VS Code: %d.%d.%d\nProfile: %s\n", version[0], version[1], version[2], profile.Name)
	for _, plan := range plans {
		fmt.Fprintln(out, "Settings:", plan.path)
	}
	fmt.Fprintln(out, "Extension: "+vscode.ExtensionID)
	changedSettings := 0
	for _, plan := range plans {
		changedSettings += len(plan.changes)
	}
	if changedSettings == 0 {
		fmt.Fprintln(out, "Settings: already configured")
	} else {
		fmt.Fprintf(out, "Settings to add or update: %d\n", changedSettings)
		for _, plan := range plans {
			for _, change := range plan.changes {
				if change.Conflict {
					fmt.Fprintf(out, "  %s (currently %v) in %s\n", change.Key, change.Current, plan.path)
				} else {
					fmt.Fprintf(out, "  %s in %s\n", change.Key, plan.path)
				}
			}
		}
	}
	if f.dryRun {
		fmt.Fprintln(out, "\nDry run: nothing was changed.")
		return 0
	}
	if !f.yes {
		if !interactive() {
			fmt.Fprintln(out, "\nRun again with --yes to apply these changes, or run it in an interactive terminal to review them.")
			return 2
		}
		ok, err := confirmVSCode(ctx, "Install the extension and update these VS Code settings?")
		if err != nil {
			fmt.Fprintln(out, red.Render("Couldn't confirm setup:"), err)
			return 1
		}
		if !ok {
			fmt.Fprintln(out, "Cancelled. Nothing was changed.")
			return 1
		}
	}

	for i := range plans {
		replace := make(map[string]bool)
		for _, change := range plans[i].changes {
			if !change.Conflict {
				continue
			}
			if f.yes {
				replace[change.Key] = true
				continue
			}
			question := fmt.Sprintf("Replace %q (%v) with %v?", change.Key, change.Current, change.Desired)
			ok, err := confirmVSCode(ctx, question)
			if err != nil {
				fmt.Fprintln(out, red.Render("Error:"), err)
				return 1
			}
			replace[change.Key] = ok
			if !ok {
				fmt.Fprintf(out, "Keeping the existing value for %q.\n", change.Key)
			}
		}
		plans[i].updated, err = vscode.ApplySettings(plans[i].current, plans[i].changes, replace)
		if err != nil {
			fmt.Fprintln(out, red.Render("Error:"), err)
			return 1
		}
	}

	installArgs := []string{}
	if profile.Location != "" {
		installArgs = append(installArgs, "--profile", profile.Name)
	}
	installArgs = append(installArgs, "--install-extension", vscode.ExtensionID)
	fmt.Fprintln(out, "\nInstalling the Ollama extension...")
	if _, err := e.Runner.Run(ctx, system.Cmd{Name: code, Args: installArgs}); err != nil {
		fmt.Fprintln(out, red.Render("Couldn't install the Ollama extension:"), err)
		return 1
	}
	for _, plan := range plans {
		backup, err := vscode.WriteSettings(plan.path, plan.current, plan.updated)
		if err != nil {
			fmt.Fprintln(out, red.Render("Couldn't update VS Code settings:"), err)
			return 1
		}
		if backup != "" {
			fmt.Fprintln(out, "Backup:", backup)
		}
	}
	if changedSettings == 0 {
		fmt.Fprintln(out, green.Render("Done."), "The Ollama extension is installed; VS Code settings were already configured.")
	} else {
		fmt.Fprintln(out, green.Render("Done."), "The Ollama extension is installed and the selected profile is configured for local generation and the documented privacy settings.")
	}
	fmt.Fprintln(out, "GitHub.com account-level privacy settings are separate; see the shared privacy section in the README.")
	return 0
}

func buildVSCodeSettingsPlans(userDir string, profile vscode.Profile) ([]vscodeSettingsPlan, error) {
	defaultPath := filepath.Join(userDir, "settings.json")
	profilePath, err := vscode.SettingsPath(userDir, profile)
	if err != nil {
		return nil, err
	}
	settingsByPath := map[string]map[string]any{
		defaultPath: vscode.ApplicationSettings(),
	}
	if profilePath == defaultPath {
		for key, value := range vscode.ProfileSettings() {
			settingsByPath[defaultPath][key] = value
		}
	} else {
		settingsByPath[profilePath] = vscode.ProfileSettings()
	}
	paths := make([]string, 0, len(settingsByPath))
	for path := range settingsByPath {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	plans := make([]vscodeSettingsPlan, 0, len(paths))
	for _, path := range paths {
		current, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("couldn't read VS Code settings %s: %w", path, err)
		}
		changes, err := vscode.AnalyzeSettings(current, settingsByPath[path])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		plans = append(plans, vscodeSettingsPlan{path: path, current: current, changes: changes})
	}
	return plans, nil
}

func selectVSCodeProfile(ctx context.Context, profiles []vscode.Profile, name string, yes bool) (vscode.Profile, error) {
	if name != "" {
		for _, profile := range profiles {
			if profile.Name == name {
				return profile, nil
			}
		}
		var available []string
		for _, profile := range profiles {
			available = append(available, profile.Name)
		}
		return vscode.Profile{}, fmt.Errorf("profile %q wasn't found; available profiles: %s", name, strings.Join(available, ", "))
	}
	if len(profiles) == 1 {
		return profiles[0], nil
	}
	if yes || !interactive() {
		return vscode.Profile{}, fmt.Errorf("more than one profile exists; choose one with --profile (available: %s)", profileNames(profiles))
	}
	selected, err := askVSCodeProfile(ctx, profiles)
	if err != nil {
		return vscode.Profile{}, fmt.Errorf("profile selection failed: %w", err)
	}
	for _, profile := range profiles {
		if profile.Name == selected {
			return profile, nil
		}
	}
	return vscode.Profile{}, fmt.Errorf("unknown profile selection %q", selected)
}

func profileNames(profiles []vscode.Profile) string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	return strings.Join(names, ", ")
}

func resolveVSCodeCommand(path, goos string) (string, error) {
	if goos != "windows" || !strings.EqualFold(filepath.Ext(path), ".cmd") {
		return path, nil
	}
	exe := filepath.Join(filepath.Dir(filepath.Dir(path)), "Code.exe")
	info, err := os.Stat(exe)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("found %s, but couldn't locate the VS Code executable beside it", path)
	}
	return exe, nil
}

func vscodeUserDir(e *ollama.Env) (string, error) {
	switch e.Info.OS {
	case "linux":
		config, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("find VS Code user settings directory: %w", err)
		}
		return filepath.Join(config, "Code", "User"), nil
	case "darwin":
		return filepath.Join(e.Home, "Library", "Application Support", "Code", "User"), nil
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(e.Home, "AppData", "Roaming")
		}
		return filepath.Join(appData, "Code", "User"), nil
	default:
		return "", fmt.Errorf("VS Code setup isn't supported on %s", e.Info.OS)
	}
}

func parseVSCodeVersion(output string) ([3]int, bool) {
	var version [3]int
	lines := strings.Fields(output)
	if len(lines) == 0 {
		return version, false
	}
	parts := strings.SplitN(lines[0], ".", 4)
	if len(parts) < 2 || len(parts) > 3 {
		return version, false
	}
	for i, part := range parts {
		if i > 2 {
			break
		}
		if dash := strings.IndexByte(part, '-'); dash >= 0 {
			part = part[:dash]
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version, false
		}
		version[i] = n
	}
	return version, version[0] > 0 && len(parts) >= 2
}
