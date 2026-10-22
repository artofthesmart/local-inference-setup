package launch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestVars(t *testing.T) {
	got := Settings{BaseURL: "http://127.0.0.1:11434/", Model: "gpt-oss:20b", Context: 65536}.Vars()
	want := []string{
		"COPILOT_OFFLINE=true",
		"COPILOT_PROVIDER_TYPE=openai",
		"COPILOT_PROVIDER_BASE_URL=http://127.0.0.1:11434/v1",
		"COPILOT_MODEL=gpt-oss:20b",
		"COPILOT_PROVIDER_MAX_PROMPT_TOKENS=57344",
		"COPILOT_PROVIDER_MAX_OUTPUT_TOKENS=8192",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%q", got)
	}
	small := Settings{BaseURL: "x", Model: "m", Context: 16384}.Vars()
	if small[4] != "COPILOT_PROVIDER_MAX_PROMPT_TOKENS=12288" || small[5] != "COPILOT_PROVIDER_MAX_OUTPUT_TOKENS=4096" {
		t.Errorf("small context: %q", small)
	}
}

func TestEnvironReplacesOldValues(t *testing.T) {
	base := []string{"PATH=/bin", "COPILOT_MODEL=old", "copilot_offline=false", "HOME=/h"}
	got := Settings{BaseURL: "u", Model: "new", Context: 65536}.Environ(base)
	if !slices.Contains(got, "PATH=/bin") || !slices.Contains(got, "HOME=/h") || slices.Contains(got, "COPILOT_MODEL=old") || slices.Contains(got, "copilot_offline=false") || !slices.Contains(got, "COPILOT_MODEL=new") {
		t.Fatalf("%q", got)
	}
}

func TestUpdateShellProfiles(t *testing.T) {
	home := t.TempDir()
	settings := Settings{BaseURL: "http://localhost:11434", Model: "model'with-quote", Context: 65536}
	if err := UpdateShellProfiles(home, settings); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".bashrc", ".zshrc"} {
		content, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		for _, want := range []string{
			profileStart,
			"export COPILOT_OFFLINE='true'",
			"export COPILOT_PROVIDER_BASE_URL='http://localhost:11434/v1'",
			`export COPILOT_MODEL='model'"'"'with-quote'`,
			"export COPILOT_PROVIDER_MAX_PROMPT_TOKENS='57344'",
			"export COPILOT_PROVIDER_MAX_OUTPUT_TOKENS='8192'",
			profileEnd,
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q in:\n%s", name, want, text)
			}
		}
	}
}

func TestUpdateShellProfilesReplacesOnlyManagedBlock(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".bashrc")
	initial := "export KEEP_ME=1\n" + profileStart + "\nexport COPILOT_MODEL='old'\n" + profileEnd + "\nsource ~/.custom\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	settings := Settings{BaseURL: "http://localhost:11434", Model: "new", Context: 32768}
	if err := updateShellProfile(path, settings); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := updateShellProfile(path, settings); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(second)
	if string(first) != text {
		t.Errorf("re-running changed the profile:\n%s", text)
	}
	if strings.Count(text, profileStart) != 1 || strings.Count(text, profileEnd) != 1 || !strings.Contains(text, "export KEEP_ME=1") || !strings.Contains(text, "source ~/.custom") || strings.Contains(text, "COPILOT_MODEL='old'") {
		t.Errorf("managed block was not safely replaced:\n%s", text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("profile permissions changed: %o", info.Mode().Perm())
	}
}

func TestUpdateShellProfilesPreservesCRLF(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(path, []byte("export KEEP_ME=1\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateShellProfile(path, Settings{BaseURL: "u", Model: "m", Context: 65536}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "export KEEP_ME=1\r\n"+profileStart+"\r\n") {
		t.Errorf("CRLF profile was not preserved: %q", content)
	}
}

func TestUpdateShellProfilesRejectsIncompleteBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(path, []byte(profileStart+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := updateShellProfile(path, Settings{BaseURL: "u", Model: "m", Context: 65536}); err == nil {
		t.Fatal("expected an incomplete block error")
	}
}

func TestFind(t *testing.T) {
	if _, err := Find(func(string) (string, error) { return "", exec.ErrNotFound }); !errors.Is(err, ErrNotInstalled) || !strings.Contains(err.Error(), "npm install -g @github/copilot") {
		t.Errorf("%v", err)
	}
	if p, err := Find(func(string) (string, error) { return "/bin/copilot", nil }); err != nil || p != "/bin/copilot" {
		t.Errorf("%s %v", p, err)
	}
}

func TestProvidersWarning(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	var checked string
	exists := func(p string) bool { checked = p; return true }
	if w := ProvidersWarning(getenv, "/h", exists); !strings.Contains(w, "overrides") || checked != filepath.Join("/h", ".copilot", "providers.json") {
		t.Errorf("%q %s", w, checked)
	}
	env["COPILOT_HOME"] = "/ch"
	ProvidersWarning(getenv, "/h", exists)
	if checked != filepath.Join("/ch", "providers.json") {
		t.Errorf("COPILOT_HOME: %s", checked)
	}
	env["COPILOT_PROVIDERS_CONFIG"] = "/p.json"
	ProvidersWarning(getenv, "/h", exists)
	if checked != "/p.json" {
		t.Errorf("COPILOT_PROVIDERS_CONFIG: %s", checked)
	}
	if w := ProvidersWarning(getenv, "/h", func(string) bool { return false }); w != "" {
		t.Errorf("no file: %q", w)
	}
}

// TestRun starts a real child process (this test binary) and checks it gets the settings and its exit code comes back.
func TestRun(t *testing.T) {
	if os.Getenv("LAUNCH_CHILD") == "1" {
		if os.Getenv("COPILOT_MODEL") == "m:1" && os.Getenv("COPILOT_OFFLINE") == "true" && len(os.Args) > 0 && os.Args[len(os.Args)-1] == "extra" {
			os.Exit(7)
		}
		os.Exit(3)
	}
	t.Setenv("LAUNCH_CHILD", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code, err := Run(exe, []string{"-test.run=^TestRun$", "--", "extra"}, Settings{BaseURL: "u", Model: "m:1", Context: 65536})
	if err != nil || code != 7 {
		t.Fatalf("code %d, err %v", code, err)
	}
	missing := filepath.Join(t.TempDir(), "nope")
	if runtime.GOOS == "windows" {
		missing += ".exe"
	}
	if _, err := Run(missing, nil, Settings{}); err == nil {
		t.Error("missing program should fail")
	}
}
