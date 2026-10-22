package vscode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tailscale/hujson"
)

func TestRequiredSettingsAreSplitByScope(t *testing.T) {
	required := RequiredSettings()
	combined := make(map[string]any)
	for key, value := range ApplicationSettings() {
		combined[key] = value
	}
	for key, value := range ProfileSettings() {
		if _, exists := combined[key]; exists {
			t.Fatalf("setting %q is assigned to multiple scopes", key)
		}
		combined[key] = value
	}
	if !reflect.DeepEqual(combined, required) {
		t.Fatalf("scope split differs from required settings:\ncombined=%v\nrequired=%v", combined, required)
	}
}

func TestAnalyzeAndApplyJSONCSettings(t *testing.T) {
	input := []byte(`{
		// Keep this note and the unrelated setting.
		"editor.fontSize": 14,
		"telemetry.telemetryLevel": "all",
		"url": "https://example.test/path//name",
	}`)
	required := map[string]any{
		"editor.fontSize":                   16,
		"telemetry.telemetryLevel":          "off",
		"chat.agentHost.byokModels.enabled": true,
	}
	changes, err := AnalyzeSettings(input, required)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || changes[0].Key != "chat.agentHost.byokModels.enabled" || changes[1].Key != "editor.fontSize" || !changes[1].Conflict || changes[2].Key != "telemetry.telemetryLevel" || !changes[2].Conflict {
		t.Fatalf("unexpected changes: %+v", changes)
	}
	updated, err := ApplySettings(input, changes, map[string]bool{"editor.fontSize": true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "// Keep this note") {
		t.Errorf("updated JSONC lost the comment:\n%s", updated)
	}
	var got map[string]any
	standard, err := hujsonStandardize(updated)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(standard, &got); err != nil {
		t.Fatal(err)
	}
	if got["editor.fontSize"] != float64(16) || got["telemetry.telemetryLevel"] != "all" || got["chat.agentHost.byokModels.enabled"] != true || got["url"] != "https://example.test/path//name" {
		t.Fatalf("unexpected merged settings: %#v", got)
	}
	again, err := AnalyzeSettings(updated, required)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 2 || again[0].Key != "editor.fontSize" || !again[0].Conflict || again[1].Key != "telemetry.telemetryLevel" {
		t.Fatalf("rerun changes: %+v", again)
	}
}

func TestApplySettingsKeepsDeclinedConflict(t *testing.T) {
	input := []byte(`{"telemetry.telemetryLevel":"error"}`)
	changes, err := AnalyzeSettings(input, map[string]any{"telemetry.telemetryLevel": "off"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := ApplySettings(input, changes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != string(input) {
		t.Fatalf("declining conflict changed settings: %s", updated)
	}
}

func TestAnalyzeSettingsRejectsMalformedAndDuplicateJSONC(t *testing.T) {
	for _, input := range []string{
		`{"ok": }`,
		`{"telemetry.telemetryLevel":"error","telemetry.telemetryLevel":"off"}`,
		`[]`,
	} {
		if _, err := AnalyzeSettings([]byte(input), map[string]any{"telemetry.telemetryLevel": "off"}); err == nil {
			t.Errorf("accepted invalid settings %q", input)
		}
	}
}

func TestProfilesAndSettingsPath(t *testing.T) {
	userDir := t.TempDir()
	path := filepath.Join(userDir, "globalStorage", "storage.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	state := map[string]any{
		"userDataProfiles": []map[string]any{
			{"name": "Work", "location": "profile-id"},
			{"name": "Shared", "location": "shared-id", "useDefaultFlags": map[string]bool{"settings": true}},
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := Profiles(userDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 || profiles[0].Name != "Default" || profiles[1].Name != "Work" || profiles[2].Name != "Shared" || !profiles[2].UsesDefaultSettings {
		t.Fatalf("profiles: %+v", profiles)
	}
	settings, err := SettingsPath(userDir, profiles[1])
	if err != nil {
		t.Fatal(err)
	}
	if settings != filepath.Join(userDir, "profiles", "profile-id", "settings.json") {
		t.Fatalf("settings path: %s", settings)
	}
	if _, err := SettingsPath(userDir, Profile{Name: "bad", Location: "../outside"}); err == nil {
		t.Fatal("accepted path traversal")
	}
	inherited, err := SettingsPath(userDir, Profile{Name: "Shared", Location: "shared-id", UsesDefaultSettings: true})
	if err != nil || inherited != filepath.Join(userDir, "settings.json") {
		t.Fatalf("inherited profile settings path: %s, %v", inherited, err)
	}
}

func TestWriteSettingsBacksUpOnlyActualChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte("{\n  // original comment\n  \"editor.fontSize\": 14\n}\n")
	updated := []byte("{\n  // original comment\n  \"editor.fontSize\": 16\n}\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	oldTimeStamp := timeStamp
	timeStamp = func() string { return "test-time" }
	t.Cleanup(func() { timeStamp = oldTimeStamp })
	backup, err := WriteSettings(path, original, updated)
	if err != nil {
		t.Fatal(err)
	}
	if backup != path+".bak.test-time" {
		t.Fatalf("backup path: %s", backup)
	}
	backedUp, err := os.ReadFile(backup)
	if err != nil || !reflect.DeepEqual(backedUp, original) {
		t.Fatalf("backup contents: %q, %v", backedUp, err)
	}
	mode, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && mode.Mode().Perm() != 0o640 {
		t.Fatalf("settings permissions: %v", mode.Mode().Perm())
	}
	if _, err := WriteSettings(path, updated, updated); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(path + ".bak.*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("rerun created a backup: %v, %v", matches, err)
	}
}

func TestWriteSettingsRefusesConcurrentChangesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	current := []byte(`{"editor.fontSize":16}`)
	if err := os.WriteFile(path, []byte(`{"editor.fontSize":18}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSettings(path, current, []byte(`{"editor.fontSize":20}`)); err == nil {
		t.Fatal("overwrote settings changed after planning")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "real.json")
	if err := os.WriteFile(target, current, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := WriteSettings(path, current, []byte(`{"editor.fontSize":20}`)); err == nil {
		t.Fatal("replaced a symlink")
	}
}

func hujsonStandardize(data []byte) ([]byte, error) {
	value, err := hujson.Parse(data)
	if err != nil {
		return nil, err
	}
	value.Standardize()
	return value.Pack(), nil
}
