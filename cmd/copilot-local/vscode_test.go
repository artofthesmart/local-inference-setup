package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/artofthesmart/local-inference-setup/internal/system"
	"github.com/tailscale/hujson"
)

func fakeVSCode(t *testing.T) (string, *system.FakeRunner) {
	t.Helper()
	home, runner, _ := fakeWorld(t)
	oldFind := findVSCode
	findVSCode = func() (string, error) { return "/fake/code", nil }
	t.Cleanup(func() { findVSCode = oldFind })
	runner.Responses = map[string]string{"/fake/code --version": "1.143.0\ncommit\nx64"}
	return home, runner
}

func vscodeSettingsPath(home string) string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, "Code", "User", "settings.json")
}

func saveVSCodeProfile(t *testing.T, home, name, location string) {
	t.Helper()
	userDir := filepath.Dir(vscodeSettingsPath(home))
	path := filepath.Join(userDir, "globalStorage", "storage.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"userDataProfiles": []map[string]string{{"name": name, "location": location}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVSCodeSetupConfiguresDefaultProfileAndBacksUp(t *testing.T) {
	home, runner := fakeVSCode(t)
	path := vscodeSettingsPath(home)
	original := []byte("{\n  // Keep the user's note.\n  \"editor.fontSize\": 15\n}\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	code, out := runCLI(t, "vscode", "--yes")
	if code != 0 || !strings.Contains(out, "Backup:") || !strings.Contains(out, "selected profile is configured") {
		t.Fatalf("%d\n%s", code, out)
	}
	if got := runner.Lines(); len(got) != 2 || got[0] != "/fake/code --version" || got[1] != "/fake/code --install-extension Ollama.ollama" {
		t.Fatalf("commands: %v", got)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "// Keep the user's note.") {
		t.Error("JSONC comment was not preserved")
	}
	got := decodeVSCodeSettings(t, updated)
	for key, want := range map[string]any{
		"editor.fontSize":                   float64(15),
		"chat.agentHost.byokModels.enabled": true,
		"telemetry.telemetryLevel":          "off",
		"github.copilot.enable":             map[string]any{"*": false},
	} {
		if !reflect.DeepEqual(got[key], want) {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups: %v, %v", backups, err)
	}
	saved, err := os.ReadFile(backups[0])
	if err != nil || string(saved) != string(original) {
		t.Fatalf("backup contents: %q, %v", saved, err)
	}
}

func TestVSCodeSetupCanBeRerunWithoutAnotherBackup(t *testing.T) {
	home, _ := fakeVSCode(t)
	if code, out := runCLI(t, "vscode", "--yes"); code != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
	path := vscodeSettingsPath(home)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := runCLI(t, "vscode", "--yes"); code != 0 || !strings.Contains(out, "settings were already configured") {
		t.Fatalf("%d\n%s", code, out)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("rerun changed settings: %v", err)
	}
	backups, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(backups) != 0 {
		t.Fatalf("unexpected backups on rerun: %v, %v", backups, err)
	}
}

func TestVSCodeSetupRequiresConfirmationWithoutTerminal(t *testing.T) {
	_, runner := fakeVSCode(t)
	if code, out := runCLI(t, "vscode"); code != 2 || !strings.Contains(out, "Run again with --yes") {
		t.Fatalf("%d\n%s", code, out)
	}
	if len(runner.Lines()) != 1 {
		t.Fatalf("extension command ran before confirmation: %v", runner.Lines())
	}
}

func TestVSCodeSetupDryRunChangesNothing(t *testing.T) {
	home, runner := fakeVSCode(t)
	code, out := runCLI(t, "vscode", "--dry-run")
	if code != 0 || !strings.Contains(out, "Dry run: nothing was changed.") {
		t.Fatalf("%d\n%s", code, out)
	}
	if _, err := os.Stat(vscodeSettingsPath(home)); !os.IsNotExist(err) {
		t.Fatalf("dry run created settings: %v", err)
	}
	if len(runner.Lines()) != 1 {
		t.Fatalf("dry run installed the extension: %v", runner.Lines())
	}
}

func TestVSCodeSetupRejectsOldVersion(t *testing.T) {
	home, runner := fakeVSCode(t)
	runner.Responses["/fake/code --version"] = "1.126.9\ncommit\nx64"
	if code, out := runCLI(t, "vscode", "--yes"); code != 1 || !strings.Contains(out, "1.127 or newer") {
		t.Fatalf("%d\n%s", code, out)
	}
	if len(runner.Lines()) != 1 {
		t.Fatalf("installed extension despite old version: %v", runner.Lines())
	}
	if _, err := os.Stat(vscodeSettingsPath(home)); !os.IsNotExist(err) {
		t.Fatalf("old version changed settings: %v", err)
	}
}

func TestVSCodeSetupStopsWhenExtensionInstallFails(t *testing.T) {
	home, runner := fakeVSCode(t)
	runner.Failures = map[string]string{"/fake/code --install-extension": "marketplace unavailable"}
	if code, out := runCLI(t, "vscode", "--yes"); code != 1 || !strings.Contains(out, "Couldn't install the Ollama extension") {
		t.Fatalf("%d\n%s", code, out)
	}
	if _, err := os.Stat(vscodeSettingsPath(home)); !os.IsNotExist(err) {
		t.Fatalf("settings changed after failed install: %v", err)
	}
}

func TestVSCodeSetupRejectsInvalidJSONCBeforeInstalling(t *testing.T) {
	home, runner := fakeVSCode(t)
	path := vscodeSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"broken":}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runCLI(t, "vscode", "--yes"); code != 1 || !strings.Contains(out, "parse settings.json as JSONC") {
		t.Fatalf("%d\n%s", code, out)
	}
	if got := runner.Lines(); len(got) != 1 {
		t.Fatalf("installed extension despite invalid settings: %v", got)
	}
}

func TestVSCodeSetupUsesSelectedProfileAndDefaultForApplicationSettings(t *testing.T) {
	home, runner := fakeVSCode(t)
	saveVSCodeProfile(t, home, "Work", "profile-id")
	code, out := runCLI(t, "vscode", "--profile", "Work", "--yes")
	if code != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
	if got := runner.Lines(); len(got) != 2 || got[1] != "/fake/code --profile Work --install-extension Ollama.ollama" {
		t.Fatalf("commands: %v", got)
	}
	defaultSettings, err := os.ReadFile(vscodeSettingsPath(home))
	if err != nil {
		t.Fatal(err)
	}
	workSettingsPath := filepath.Join(filepath.Dir(vscodeSettingsPath(home)), "profiles", "profile-id", "settings.json")
	workSettings, err := os.ReadFile(workSettingsPath)
	if err != nil {
		t.Fatal(err)
	}
	defaultValues := decodeVSCodeSettings(t, defaultSettings)
	if defaultValues["telemetry.telemetryLevel"] != "off" {
		t.Errorf("application privacy setting missing from Default Profile:\n%s", defaultSettings)
	}
	workValues := decodeVSCodeSettings(t, workSettings)
	if _, ok := workValues["telemetry.telemetryLevel"]; ok {
		t.Errorf("application setting was placed in the named profile:\n%s", workSettings)
	}
	if workValues["chat.agentHost.byokModels.enabled"] != true {
		t.Errorf("profile setting missing from Work profile:\n%s", workSettings)
	}
}

func TestVSCodeSetupRequiresProfileWhenSelectionIsAmbiguous(t *testing.T) {
	home, runner := fakeVSCode(t)
	saveVSCodeProfile(t, home, "Work", "profile-id")
	code, out := runCLI(t, "vscode", "--yes")
	if code != 2 || !strings.Contains(out, "more than one profile exists") {
		t.Fatalf("%d\n%s", code, out)
	}
	if len(runner.Lines()) != 1 {
		t.Fatalf("extension command ran without a profile selection: %v", runner.Lines())
	}
}

func TestVSCodeSetupPromptsBeforeReplacingConflict(t *testing.T) {
	home, _ := fakeVSCode(t)
	path := vscodeSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"telemetry.telemetryLevel":"error"}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	oldInteractive, oldConfirm := interactive, confirmVSCode
	interactive = func() bool { return true }
	confirmVSCode = func(_ context.Context, question string) (bool, error) {
		return !strings.Contains(question, `"telemetry.telemetryLevel"`), nil
	}
	t.Cleanup(func() {
		interactive, confirmVSCode = oldInteractive, oldConfirm
	})
	code, out := runCLI(t, "vscode")
	if code != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeVSCodeSettings(t, updated)["telemetry.telemetryLevel"]; got != "error" {
		t.Fatalf("declined conflict was overwritten:\n%s", updated)
	}
}

func TestParseVSCodeVersion(t *testing.T) {
	for _, test := range []struct {
		output string
		want   [3]int
		ok     bool
	}{
		{"1.127.0\ncommit\nx64", [3]int{1, 127, 0}, true},
		{"1.127.0-insider\ncommit\nx64", [3]int{1, 127, 0}, true},
		{"1.126.9\ncommit\nx64", [3]int{1, 126, 9}, true},
		{"not a version", [3]int{}, false},
		{"", [3]int{}, false},
	} {
		got, ok := parseVSCodeVersion(test.output)
		if got != test.want || ok != test.ok {
			t.Errorf("parseVSCodeVersion(%q) = %v, %v; want %v, %v", test.output, got, ok, test.want, test.ok)
		}
	}
}

func TestResolveVSCodeCommandUsesExecutableOnWindows(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(binDir, "code.cmd")
	executable := filepath.Join(dir, "Code.exe")
	if err := os.WriteFile(executable, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := resolveVSCodeCommand(wrapper, "windows")
	if err != nil || got != executable {
		t.Fatalf("resolved command: %s, %v", got, err)
	}
}

func decodeVSCodeSettings(t *testing.T, data []byte) map[string]any {
	t.Helper()
	value, err := hujson.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	value.Standardize()
	var settings map[string]any
	if err := json.Unmarshal(value.Pack(), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}
