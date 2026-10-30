package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/launch"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/ollama"
	"github.com/artofthesmart/local-inference-setup/internal/system"
	"github.com/artofthesmart/local-inference-setup/internal/vscode"
)

type verificationProbe struct {
	calls    int
	settings launch.Settings
	err      error
}

func verificationWorld(t *testing.T, overrides map[string]string) (string, *system.FakeRunner, *verificationProbe) {
	t.Helper()
	home, runner := fakeVSCode(t)
	for _, kv := range (launch.Settings{}).Vars() {
		key, _, _ := strings.Cut(kv, "=")
		t.Setenv(key, "")
	}
	t.Setenv("COPILOT_PROVIDERS_CONFIG", "")
	t.Setenv("COPILOT_HOME", "")
	for _, key := range []string{
		"COPILOT_PROVIDER_API_KEY_COMMAND", "COPILOT_PROVIDER_HEADERS",
		"COPILOT_PROVIDER_WIRE_MODEL", "COPILOT_PROVIDER_MODEL_ID",
		"COPILOT_PROVIDER_WIRE_API", "COPILOT_PROVIDER_TRANSPORT",
	} {
		t.Setenv(key, "")
	}
	responses := map[string]string{
		"/api/version":         `{"version":"0.12.0"}`,
		"/api/status":          `{"cloud":{"disabled":true}}`,
		"/v1/models":           `{"data":[]}`,
		"/api/show":            `{"capabilities":["completion","tools"],"model_info":{"general.architecture":"llama","llama.context_length":131072}}`,
		"/api/ps":              `{"models":[{"name":"m:1","size":100,"size_vram":100,"context_length":65536}]}`,
		"/v1/chat/completions": "data: " + `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_temperature","arguments":"{\"city\":\"New York\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n",
	}
	for path, value := range overrides {
		responses[path] = value
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := responses[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			if _, overridden := overrides[r.URL.Path]; !overridden {
				var request struct {
					Tools []any `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode chat request: %v", err)
				}
				if len(request.Tools) == 0 {
					body = "data: " + `{"choices":[{"index":0,"delta":{"role":"assistant","content":"COPILOT_LOCAL_OK"},"finish_reason":null}]}` + "\n\ndata: " + `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"
				}
			}
			w.Header().Set("Content-Type", "text/event-stream")
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("COPILOT_LOCAL_OLLAMA_URL", srv.URL)
	baseEnv := newEnv
	newEnv = func(out io.Writer) (*ollama.Env, error) {
		e, err := baseEnv(out)
		if err != nil {
			return nil, err
		}
		e.LookPath = func(string) (string, error) { return "/fake/ollama", nil }
		e.Exists = func(path string) bool {
			if !strings.HasPrefix(path, home+string(filepath.Separator)) {
				return false
			}
			_, err := os.Stat(path)
			return err == nil
		}
		return e, nil
	}
	probe := &verificationProbe{}
	oldProbe := probeCopilot
	t.Cleanup(func() { probeCopilot = oldProbe })
	probeCopilot = func(_ context.Context, _ string, settings launch.Settings) error {
		probe.calls++
		probe.settings = settings
		return probe.err
	}
	saveState(t, models.State{LastUsed: "m:1", Models: []models.Qualified{{Tag: "m:1", Context: 65536}}})
	runner.Responses["/fake/ollama --version"] = "ollama version is 0.12.0"
	runner.Responses["/fake/code --list-extensions"] = "GitHub.copilot\nollama.ollama\n"
	settings, err := json.Marshal(vscode.RequiredSettings())
	if err != nil {
		t.Fatal(err)
	}
	path := vscodeSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, settings, 0o600); err != nil {
		t.Fatal(err)
	}
	return home, runner, probe
}

func TestVerifyWholeSetupWithoutChangingSettings(t *testing.T) {
	home, runner, probe := verificationWorld(t, nil)
	path := vscodeSettingsPath(home)
	before, _ := os.ReadFile(path)
	e, err := newEnv(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(e.StateDir, models.StateFile)
	modelBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	code, out := runCLI(t, "verify")
	if code != 0 || probe.calls != 1 {
		t.Fatalf("%d, probe calls %d\n%s", code, probe.calls, out)
	}
	for _, want := range []string{"Automated checks passed", "Copilot offline prompt", "Loaded model context", "VS Code Ollama extension", "VS Code local/privacy settings", "conflict resolution", "turn off usage stats", "WARN", "not a network-privacy audit"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	after, _ := os.ReadFile(path)
	modelAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || string(modelBefore) != string(modelAfter) {
		t.Error("verification changed settings or model state")
	}
	if backups, _ := filepath.Glob(path + ".bak.*"); len(backups) != 0 {
		t.Error("verification created settings backups")
	}
	for _, command := range runner.Lines() {
		if strings.Contains(command, "install-extension") || strings.Contains(command, "config autoUpdate") || strings.Contains(command, "setx") {
			t.Errorf("verification ran a configuration command: %s", command)
		}
	}
	if probe.settings.Model != "m:1" || probe.settings.Context != 65536 {
		t.Errorf("unexpected probe settings: %+v", probe.settings)
	}
}

func TestVerifyFailureCases(t *testing.T) {
	for _, test := range []struct {
		name      string
		responses map[string]string
		want      string
		noProbe   bool
	}{
		{"server down", map[string]string{"/api/version": "invalid JSON"}, "Ollama running", true},
		{"cloud on", map[string]string{"/api/status": `{"cloud":{"disabled":false}}`}, "Ollama cloud off", true},
		{"cloud model", map[string]string{"/api/show": `{"remote_host":"https://cloud.example.com"}`}, "cloud model", true},
		{"no tools", map[string]string{"/api/show": `{"capabilities":["completion"]}`}, "Supports tool calling", true},
		{"tool test fails", map[string]string{"/v1/chat/completions": "data: [DONE]\n\n"}, "Tool-call test", true},
		{"short context", map[string]string{"/api/ps": `{"models":[{"name":"m:1","context_length":4096}]}`}, "Context in use", true},
		{"unknown context", map[string]string{"/api/ps": `{"models":[{"name":"m:1"}]}`}, "Loaded model context", false},
		{"not loaded", map[string]string{"/api/ps": `{"models":[]}`}, "Loaded model context", false},
		{"ps API error", map[string]string{"/api/ps": "invalid JSON"}, "Loaded model context", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, probe := verificationWorld(t, test.responses)
			code, out := runCLI(t, "verify", "--skip-vscode")
			if code != 1 || !strings.Contains(out, test.want) || !strings.Contains(out, "Fix:") || strings.Contains(out, "Automated checks passed") {
				t.Fatalf("%d\n%s", code, out)
			}
			if test.noProbe && probe.calls != 0 {
				t.Fatal("unsafe or broken model was sent to Copilot")
			}
		})
	}
}

func TestVerifyEnvironmentAndProviderConflicts(t *testing.T) {
	home, _, probe := verificationWorld(t, nil)
	path := filepath.Join(home, ".copilot", "providers.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COPILOT_PROVIDER_BASE_URL", "https://secret:password@example.com")
	t.Setenv("COPILOT_OFFLINE", "false")
	code, out := runCLI(t, "verify", "--skip-vscode")
	if code != 1 || !strings.Contains(out, "provider overrides") || !strings.Contains(out, "Does not match") || strings.Contains(out, "password") {
		t.Fatalf("%d\n%s", code, out)
	}
	if probe.calls != 1 || probe.settings.BaseURL != os.Getenv("COPILOT_LOCAL_OLLAMA_URL") {
		t.Fatal("isolated probe didn't use launcher settings")
	}
}

func TestVerifyMatchingEnvironment(t *testing.T) {
	_, _, _ = verificationWorld(t, nil)
	settings := launch.Settings{BaseURL: os.Getenv("COPILOT_LOCAL_OLLAMA_URL"), Model: "m:1", Context: 65536}
	for _, variable := range settings.Vars() {
		key, value, _ := strings.Cut(variable, "=")
		t.Setenv(key, value)
	}
	code, out := runCLI(t, "verify", "--skip-vscode")
	if code != 0 || strings.Contains(out, "Unset in this terminal") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVerifyAdvancedProviderOverrides(t *testing.T) {
	for _, variable := range []string{"COPILOT_PROVIDER_API_KEY_COMMAND", "COPILOT_PROVIDER_HEADERS", "COPILOT_PROVIDER_WIRE_MODEL", "COPILOT_PROVIDER_MODEL_ID", "COPILOT_PROVIDER_WIRE_API", "COPILOT_PROVIDER_TRANSPORT"} {
		t.Run(variable, func(t *testing.T) {
			verificationWorld(t, nil)
			t.Setenv(variable, "private-value")
			code, out := runCLI(t, "verify", "--skip-vscode")
			if code != 1 || !strings.Contains(out, variable) || strings.Contains(out, "private-value") {
				t.Fatalf("%d\n%s", code, out)
			}
		})
	}
}

func TestVerifyWithInstalledCLI(t *testing.T) {
	if os.Getenv("COPILOT_LOCAL_CLI_TEST") != "1" {
		t.Skip("set COPILOT_LOCAL_CLI_TEST=1 to run verification through the installed CLI against a mock")
	}
	actualFind := findCopilot
	verificationWorld(t, nil)
	findCopilot = actualFind
	probeCopilot = launch.Probe
	code, out := runCLI(t, "verify")
	if code != 0 || !strings.Contains(out, "Fixed test reply received") || !strings.Contains(out, "Automated checks passed") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVerifyProbeFailure(t *testing.T) {
	_, _, probe := verificationWorld(t, nil)
	probe.err = errors.New("test prompt timed out")
	code, out := runCLI(t, "verify", "--skip-vscode")
	if code != 1 || !strings.Contains(out, "test prompt timed out") || !strings.Contains(out, "--timeout 5m") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVerifyMissingCopilot(t *testing.T) {
	_, _, probe := verificationWorld(t, nil)
	findCopilot = func() (string, error) { return "", launch.ErrNotInstalled }
	code, out := runCLI(t, "verify", "--skip-vscode")
	if code != 1 || !strings.Contains(out, "Copilot CLI isn't installed") || probe.calls != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVerifyVSCodeFailures(t *testing.T) {
	for _, failure := range []string{"missing command", "old version", "missing extension", "extension listing error", "bad settings", "conflicting settings", "ambiguous profiles", "missing profile"} {
		t.Run(failure, func(t *testing.T) {
			home, runner, _ := verificationWorld(t, nil)
			args := []string{"verify"}
			switch failure {
			case "missing command":
				findVSCode = func() (string, error) { return "", os.ErrNotExist }
			case "old version":
				runner.Responses["/fake/code --version"] = "1.126.0"
			case "missing extension":
				runner.Responses["/fake/code --list-extensions"] = "GitHub.copilot"
			case "extension listing error":
				runner.Failures = map[string]string{"/fake/code --list-extensions": "can't list extensions"}
			case "bad settings":
				if err := os.WriteFile(vscodeSettingsPath(home), []byte("{bad"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "conflicting settings":
				if err := os.WriteFile(vscodeSettingsPath(home), []byte(`{"telemetry.telemetryLevel":"all"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "ambiguous profiles":
				saveVSCodeProfile(t, home, "Work", "work")
			case "missing profile":
				args = append(args, "--profile", "Missing")
			}
			code, out := runCLI(t, args...)
			if code != 1 || !strings.Contains(out, "VS Code") || !strings.Contains(out, "Fix:") {
				t.Fatalf("%d\n%s", code, out)
			}
		})
	}
}

func TestVerifySelectedVSCodeProfile(t *testing.T) {
	home, runner, _ := verificationWorld(t, nil)
	saveVSCodeProfile(t, home, "Local AI", "local")
	path := filepath.Join(filepath.Dir(vscodeSettingsPath(home)), "profiles", "local", "settings.json")
	data, _ := json.Marshal(vscode.ProfileSettings())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := runCLI(t, "verify", "--profile", "Local AI")
	if code != 0 || !strings.Contains(out, "Automated checks passed") {
		t.Fatalf("%d\n%s", code, out)
	}
	if !strings.Contains(strings.Join(runner.Lines(), "\n"), `--list-extensions --profile "Local AI"`) {
		t.Fatalf("extension listing didn't target the chosen profile: %v", runner.Lines())
	}
}

func TestVerifyMissingModelStillChecksVSCode(t *testing.T) {
	_, _, probe := verificationWorld(t, nil)
	saveState(t, models.State{})
	code, out := runCLI(t, "verify")
	if code != 1 || !strings.Contains(out, "no saved model") || !strings.Contains(out, "VS Code Ollama extension") || probe.calls != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestVerificationModelSelection(t *testing.T) {
	dir := t.TempDir()
	state := models.State{LastUsed: "m:1", Models: []models.Qualified{{Tag: "m:1", Context: 32768}, {Tag: "other:latest", Context: 65536}}}
	if err := state.Save(dir); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		f    verifyFlags
		tag  string
		want int
	}{
		{verifyFlags{}, "m:1", 32768},
		{verifyFlags{model: "other:latest"}, "other:latest", 65536},
		{verifyFlags{model: "new:latest"}, "new:latest", 65536},
		{verifyFlags{context: 16384}, "m:1", 16384},
	} {
		got, err := verificationModel(dir, test.f)
		if err != nil || got.Tag != test.tag || got.Context != test.want {
			t.Errorf("%+v: %+v, %v", test.f, got, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, models.StateFile), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verificationModel(dir, verifyFlags{}); err == nil {
		t.Fatal("malformed saved state passed")
	}
}

func TestVerifyArgumentValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--context", "12"}, {"--context", "-1"}, {"--model", "../../bad"},
		{"--timeout", "0s"}, {"--timeout", "-1s"}, {"--timeout", "bad"},
		{"--profile", "Default", "--skip-vscode"}, {"extra"}, {"--nope"},
	} {
		if code, out := runCLI(t, append([]string{"verify"}, args...)...); code != 2 {
			t.Errorf("%q: %d\n%s", args, code, out)
		}
	}
	fs, f := newVerifyFlags(io.Discard)
	if f.timeout != 3*time.Minute {
		t.Fatal("unexpected timeout default")
	}
	if missing := undocumentedFlags(fs, helpFor("verify", "")); len(missing) > 0 {
		t.Errorf("missing flags: %v", missing)
	}
}
