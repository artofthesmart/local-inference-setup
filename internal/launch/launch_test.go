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
