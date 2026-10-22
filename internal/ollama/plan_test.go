package ollama

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

var (
	linuxSystemd = system.Info{OS: "linux", Arch: "amd64", Systemd: true}
	linuxPlain   = system.Info{OS: "linux", Arch: "amd64"}
	macOS        = system.Info{OS: "darwin", Arch: "arm64"}
	windows      = system.Info{OS: "windows", Arch: "amd64"}
)

const showEnv = "systemctl show ollama -p Environment --value"

func TestLinuxSystemdSetup(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	steps := mustPlan(t, te.Env, 65536)
	if got, want := len(steps), 2; got != want {
		t.Fatalf("steps = %v", titles(steps))
	}
	applyAll(t, steps)

	calls := te.runner.Calls
	var tee *system.Cmd
	for i := range calls {
		if calls[i].Name == "sudo" && calls[i].Args[0] == "tee" {
			tee = &calls[i]
		}
	}
	if tee == nil || tee.Args[1] != te.DropInFile {
		t.Fatalf("drop-in not written with sudo tee: %v", te.runner.Lines())
	}
	env := parseEnvList(tee.Stdin)
	if env[envContext] != "65536" || env[envNoCloud] != "1" {
		t.Errorf("drop-in content wrong:\n%s", tee.Stdin)
	}
	lines := te.runner.Lines()
	for _, want := range []string{"sudo mkdir -p " + filepath.Dir(te.DropInFile), "sudo systemctl daemon-reload", "sudo systemctl restart ollama"} {
		if !containsLine(lines, want) {
			t.Errorf("missing %q in %v", want, lines)
		}
	}
	// systemd never touches the user's server.json: the service runs as the ollama user and doesn't read it.
	if _, err := os.Stat(te.serverJSONPath()); !os.IsNotExist(err) {
		t.Error("server.json should not be written on systemd")
	}
}

func TestLinuxSystemdAlreadyConfiguredIsNoOp(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	te.runner.Responses[showEnv] = "OLLAMA_CONTEXT_LENGTH=65536 OLLAMA_NO_CLOUD=1"
	if steps := mustPlan(t, te.Env, 65536); len(steps) != 0 {
		t.Fatalf("want no steps, got %v", titles(steps))
	}
}

func TestLinuxSystemdAsRootSkipsSudo(t *testing.T) {
	info := linuxSystemd
	info.Root = true
	te := newTestEnv(t, info, true)
	applyAll(t, mustPlan(t, te.Env, 65536))
	for _, l := range te.runner.Lines() {
		if strings.HasPrefix(l, "sudo ") {
			t.Errorf("root should not use sudo: %s", l)
		}
	}
}

func TestLinuxSystemdOverrideWinsIsReported(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	os.MkdirAll(filepath.Dir(te.DropInFile), 0o755)
	os.WriteFile(te.DropInFile, []byte(dropInContent(65536)), 0o644)
	te.runner.Responses[showEnv] = "OLLAMA_CONTEXT_LENGTH=8192 OLLAMA_NO_CLOUD=1"
	te.server.set(func(f *fakeOllama) { f.cloudDisabled = true })

	if steps := mustPlan(t, te.Env, 65536); len(steps) != 0 {
		t.Fatalf("rewriting our file can't help, so no steps expected: %v", titles(steps))
	}
	r := Verify(t.Context(), te.Env, 65536)
	c := find(t, r, "Context length 65536")
	if c.Status != Fail || !strings.Contains(c.Fix, "Another systemd override") {
		t.Fatalf("want override hint, got %+v", c)
	}
}

func TestLinuxSystemdUndo(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	os.MkdirAll(filepath.Dir(te.DropInFile), 0o755)
	os.WriteFile(te.DropInFile, []byte(dropInContent(65536)), 0o644)
	applyAll(t, Undo(te.Env, mustDetect(t, te.Env)))
	lines := te.runner.Lines()
	for _, want := range []string{"sudo rm -f " + te.DropInFile, "sudo systemctl daemon-reload", "sudo systemctl restart ollama"} {
		if !containsLine(lines, want) {
			t.Errorf("missing %q in %v", want, lines)
		}
	}
}

func TestLinuxWithoutSystemdSetupIsIdempotentAndUndoable(t *testing.T) {
	te := newTestEnv(t, linuxPlain, true)
	profile := te.profilePath()
	os.WriteFile(profile, []byte("export EDITOR=vim\n"), 0o644)
	te.server.set(func(f *fakeOllama) { f.up = false })

	steps := mustPlan(t, te.Env, 65536)
	if want := []string{"Set context length to 65536 in ~/.profile", "Turn off Ollama cloud", "Start Ollama"}; !reflect.DeepEqual(titles(steps), want) {
		t.Fatalf("steps = %v", titles(steps))
	}
	applyAll(t, steps)

	last := te.runner.Calls[len(te.runner.Calls)-1]
	if last.Name != "sh" || !strings.Contains(last.Args[1], "OLLAMA_CONTEXT_LENGTH=65536 nohup '"+fakeBin+"' serve >> ") {
		t.Errorf("serve command wrong: %v", last)
	}
	if readProfileContext(profile) != 65536 || !strings.HasPrefix(readFile(t, profile), "export EDITOR=vim\n") {
		t.Errorf("profile wrong:\n%s", readFile(t, profile))
	}

	// Second run with the server up: nothing left to do.
	te.server.set(func(f *fakeOllama) { f.up = true })
	if steps := mustPlan(t, te.Env, 65536); len(steps) != 0 {
		t.Fatalf("second run should be a no-op, got %v", titles(steps))
	}

	undo := Undo(te.Env, mustDetect(t, te.Env))
	applyAll(t, withoutManualRestart(undo))
	if readProfileContext(profile) != 0 || readFile(t, profile) != "export EDITOR=vim\n" {
		t.Errorf("profile not restored:\n%q", readFile(t, profile))
	}
	if off, _ := cloudDisabledInServerJSON(te.serverJSONPath()); off {
		t.Error("cloud key not removed")
	}
	// A running, unmanaged server can't be restarted for the user, so undo asks them to.
	if err := undo[len(undo)-1].Apply(t.Context()); !errors.Is(err, ErrManualRestart) {
		t.Errorf("want ErrManualRestart, got %v", err)
	}
	if len(Undo(te.Env, mustDetect(t, te.Env))) != 0 {
		t.Error("undo should be a no-op the second time")
	}
}

func TestUndoKeepsCloudKeyTheUserSetThemselves(t *testing.T) {
	te := newTestEnv(t, linuxPlain, true)
	os.MkdirAll(filepath.Dir(te.serverJSONPath()), 0o755)
	os.WriteFile(te.serverJSONPath(), []byte(`{"disable_ollama_cloud": true}`), 0o644)
	applyAll(t, withoutManualRestart(mustPlan(t, te.Env, 65536)))
	applyAll(t, withoutManualRestart(Undo(te.Env, mustDetect(t, te.Env))))
	if off, _ := cloudDisabledInServerJSON(te.serverJSONPath()); !off {
		t.Error("undo removed a setting the user had before")
	}
}

func TestMacOSSetup(t *testing.T) {
	te := newTestEnv(t, macOS, true)
	te.exists[macAppPath] = true
	steps := mustPlan(t, te.Env, 65536)
	if want := []string{"Set context length to 65536 for the Ollama app", "Turn off Ollama cloud", "Restart Ollama"}; !reflect.DeepEqual(titles(steps), want) {
		t.Fatalf("steps = %v", titles(steps))
	}
	applyAll(t, steps)
	if readLaunchAgentContext(te.launchAgentPath()) != 65536 {
		t.Error("LaunchAgent not written")
	}
	lines := te.runner.Lines()
	for _, want := range []string{"launchctl setenv OLLAMA_CONTEXT_LENGTH 65536", `osascript -e "quit app \"Ollama\""`, "open /Applications/Ollama.app"} {
		if !containsLine(lines, want) {
			t.Errorf("missing %q in %v", want, lines)
		}
	}

	te.runner.Responses["launchctl getenv OLLAMA_CONTEXT_LENGTH"] = "65536"
	if steps := mustPlan(t, te.Env, 65536); len(steps) != 0 {
		t.Fatalf("second run should be a no-op, got %v", titles(steps))
	}

	applyAll(t, Undo(te.Env, mustDetect(t, te.Env)))
	if _, err := os.Stat(te.launchAgentPath()); !os.IsNotExist(err) {
		t.Error("LaunchAgent not removed")
	}
	if !containsLine(te.runner.Lines(), "launchctl unsetenv OLLAMA_CONTEXT_LENGTH") {
		t.Error("launchctl unsetenv not run")
	}
}

func TestMacOSPlistWithoutLiveValueIsReapplied(t *testing.T) {
	te := newTestEnv(t, macOS, true)
	os.MkdirAll(filepath.Dir(te.launchAgentPath()), 0o755)
	os.WriteFile(te.launchAgentPath(), []byte(launchAgentPlist(65536)), 0o644)
	steps := mustPlan(t, te.Env, 65536)
	if len(steps) == 0 || !strings.HasPrefix(steps[0].Title, "Set context length") {
		t.Fatalf("live launchd value missing, so it should be set again: %v", titles(steps))
	}
}

func TestWindowsSetup(t *testing.T) {
	te := newTestEnv(t, windows, true)
	steps := mustPlan(t, te.Env, 65536)
	if want := []string{"Set context length to 65536 (user environment variable)", "Turn off Ollama cloud", "Restart Ollama"}; !reflect.DeepEqual(titles(steps), want) {
		t.Fatalf("steps = %v", titles(steps))
	}
	applyAll(t, steps)
	var scripts []string
	for _, c := range te.runner.Calls {
		if c.Name == "powershell" {
			scripts = append(scripts, c.Args[len(c.Args)-1])
		}
	}
	joined := strings.Join(scripts, "\n")
	for _, want := range []string{
		"SetEnvironmentVariable('OLLAMA_CONTEXT_LENGTH','65536','User')",
		"Stop-Process -Force",
		"$env:OLLAMA_CONTEXT_LENGTH = [Environment]::GetEnvironmentVariable('OLLAMA_CONTEXT_LENGTH','User')",
		"'ollama app.exe'",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	if !te.loadRecord().WindowsEnvSet || !te.loadRecord().ServerJSONKeyAdded {
		t.Error("changes not recorded for undo")
	}

	te.runner.Responses["powershell -NoProfile -NonInteractive -Command \"[Environment]::GetEnvironmentVariable('OLLAMA_CONTEXT_LENGTH','User')\""] = "65536"
	if steps := mustPlan(t, te.Env, 65536); len(steps) != 0 {
		t.Fatalf("second run should be a no-op, got %v", titles(steps))
	}

	applyAll(t, Undo(te.Env, mustDetect(t, te.Env)))
	found := false
	for _, c := range te.runner.Calls {
		if c.Name == "powershell" && strings.Contains(c.Args[3], "SetEnvironmentVariable('OLLAMA_CONTEXT_LENGTH',$null,'User')") {
			found = true
		}
	}
	if !found {
		t.Error("undo didn't remove the variable")
	}
	if rec := te.loadRecord(); rec.WindowsEnvSet || rec.ServerJSONKeyAdded {
		t.Errorf("record not cleared: %+v", rec)
	}
}

func TestWindowsUndoLeavesUserSetVariable(t *testing.T) {
	te := newTestEnv(t, windows, true)
	te.runner.Responses["powershell -NoProfile -NonInteractive -Command \"[Environment]::GetEnvironmentVariable"] = "65536"
	if steps := Undo(te.Env, mustDetect(t, te.Env)); len(steps) != 0 {
		t.Fatalf("variable wasn't set by us, so undo should leave it: %v", titles(steps))
	}
}

func TestNotInstalledPlansInstall(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, false)
	steps := mustPlan(t, te.Env, 65536)
	if len(steps) != 1 || !steps[0].Install {
		t.Fatalf("want only the install step, got %v", titles(steps))
	}
	if _, err := Plan(te.Env, mustDetect(t, te.Env), Options{SkipInstall: true}); err == nil {
		t.Error("--skip-install with no Ollama should fail")
	}
}

func TestInstallRunsDownloadedScript(t *testing.T) {
	for _, info := range []system.Info{linuxSystemd, windows} {
		te := newTestEnv(t, info, false)
		steps := mustPlan(t, te.Env, 65536)
		applyAll(t, steps)
		c := te.runner.Calls[len(te.runner.Calls)-1]
		if !c.Interactive {
			t.Errorf("%s: installer must be interactive (sudo prompt)", info.OS)
		}
		script := c.Args[len(c.Args)-1]
		if info.OS == "windows" {
			if c.Name != "powershell" || !strings.HasSuffix(script, ".ps1") {
				t.Errorf("windows installer: %v", c)
			}
		} else if c.Name != "sh" || !strings.HasSuffix(script, ".sh") {
			t.Errorf("unix installer: %v", c)
		}
		if _, err := os.Stat(script); !os.IsNotExist(err) {
			t.Errorf("%s: temp installer not cleaned up", info.OS)
		}
	}
}

func TestInstallDownloadFailureIsReported(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, false)
	te.InstallScriptURL = func(string) string { return te.BaseURL + "/missing" }
	steps := mustPlan(t, te.Env, 65536)
	before := len(te.runner.Calls)
	if err := steps[0].Apply(t.Context()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want a 404 error, got %v", err)
	}
	if len(te.runner.Calls) != before {
		t.Error("nothing should run when the download fails")
	}
}

func TestWSLWithWindowsServer(t *testing.T) {
	info := linuxPlain
	info.WSL = true
	te := newTestEnv(t, info, false)
	_, err := Plan(te.Env, mustDetect(t, te.Env), Options{})
	var ws WindowsSideError
	if !errors.As(err, &ws) {
		t.Fatalf("want WindowsSideError, got %v", err)
	}
}

func TestWSLIgnoresWindowsBinaryOnPath(t *testing.T) {
	info := linuxPlain
	info.WSL = true
	te := newTestEnv(t, info, false)
	te.LookPath = func(string) (string, error) { return "/mnt/c/Users/me/AppData/Local/Programs/Ollama/ollama.exe", nil }
	if s := mustDetect(t, te.Env); s.Installed() {
		t.Error("a Windows ollama.exe shouldn't count as installed inside WSL")
	}
}

func withoutManualRestart(steps []Step) []Step {
	var out []Step
	for _, s := range steps {
		if !strings.Contains(strings.Join(s.Details, " "), "start it again in a new terminal") {
			out = append(out, s)
		}
	}
	return out
}

func TestMacOSFallsBackToServeWhenAppDoesNotStartServer(t *testing.T) {
	te := newTestEnv(t, macOS, true)
	te.exists[macAppPath] = true
	te.server.set(func(f *fakeOllama) { f.up = false })
	applyAll(t, mustPlan(t, te.Env, 65536))
	last := te.runner.Calls[len(te.runner.Calls)-1]
	if last.Name != "sh" || !strings.Contains(last.String(), "OLLAMA_CONTEXT_LENGTH=65536 nohup '"+fakeBin+"' serve") {
		t.Fatalf("want a direct `ollama serve` fallback, got %v", last)
	}
}

func TestMacOSNoFallbackWhenAppStartsServer(t *testing.T) {
	te := newTestEnv(t, macOS, true)
	te.exists[macAppPath] = true
	applyAll(t, mustPlan(t, te.Env, 65536))
	for _, c := range te.runner.Calls {
		if c.Name == "sh" {
			t.Fatalf("server was up, so no fallback expected: %v", c)
		}
	}
}

func TestWindowsFallsBackToServeWhenAppDoesNotStartServer(t *testing.T) {
	te := newTestEnv(t, windows, true)
	te.server.set(func(f *fakeOllama) { f.up = false })
	applyAll(t, mustPlan(t, te.Env, 65536))
	last := te.runner.Calls[len(te.runner.Calls)-1]
	script := last.Args[len(last.Args)-1]
	for _, want := range []string{"-ArgumentList 'serve'", "-RedirectStandardError '" + te.serverLogPath() + "'", "$env:OLLAMA_CONTEXT_LENGTH = "} {
		if !strings.Contains(script, want) {
			t.Errorf("missing %q in fallback:\n%s", want, script)
		}
	}
}

func TestInstallerErrorIsToleratedWhenOllamaGotInstalled(t *testing.T) {
	for _, installedAfter := range []bool{true, false} {
		te := newTestEnv(t, macOS, false)
		te.runner.Failures["sh "] = "Unable to find application named 'Ollama'"
		steps := mustPlan(t, te.Env, 65536)
		if installedAfter {
			te.LookPath = func(string) (string, error) { return fakeBin, nil }
		}
		err := steps[0].Apply(t.Context())
		if installedAfter && err != nil {
			t.Errorf("Ollama is installed, so the installer's error should be tolerated: %v", err)
		}
		if !installedAfter && err == nil {
			t.Error("Ollama isn't installed, so the installer's error must be reported")
		}
	}
}
