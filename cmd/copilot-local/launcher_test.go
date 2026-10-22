package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/hardware"
	"github.com/artofthesmart/local-inference-setup/internal/launch"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/ollama"
	"github.com/artofthesmart/local-inference-setup/internal/system"
)

// gguf builds a model file header with just an architecture and a context length.
func gguf(ctx uint64) []byte {
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { w(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	w(uint32(3))
	w(uint64(0))
	w(uint64(2))
	str("general.architecture")
	w(uint32(8))
	str("llama")
	str("llama.context_length")
	w(uint32(10))
	w(ctx)
	return b.Bytes()
}

// fakeRegistry knows the given models, each a 1 GiB download.
func fakeRegistry(t *testing.T, contexts map[string]uint64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for tag, ctx := range contexts {
			name, version, _ := strings.Cut(tag, ":")
			switch r.URL.Path {
			case "/v2/library/" + name + "/manifests/" + version:
				json.NewEncoder(w).Encode(map[string]any{"layers": []map[string]any{
					{"mediaType": "application/vnd.ollama.image.model", "digest": "sha256:" + name, "size": hardware.GiB},
				}})
				return
			case "/v2/library/" + name + "/blobs/sha256:" + name:
				w.WriteHeader(http.StatusPartialContent)
				w.Write(gguf(ctx))
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("COPILOT_LOCAL_REGISTRY_URL", srv.URL)
	return srv
}

// fakeOllama is an Ollama server whose models pass every test unless listed in noTools.
type fakeOllama struct {
	mu        sync.Mutex
	installed []string
	pulled    []string
	noTools   []string
	ctx       uint64
}

func (f *fakeOllama) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var req struct {
			Model string `json:"model"`
		}
		if r.Body != nil {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &req)
		}
		switch r.URL.Path {
		case "/api/version":
			io.WriteString(w, `{"version":"0.12.0"}`)
		case "/api/tags":
			var list []map[string]string
			for _, m := range f.installed {
				list = append(list, map[string]string{"name": m})
			}
			json.NewEncoder(w).Encode(map[string]any{"models": list})
		case "/api/pull":
			f.pulled = append(f.pulled, req.Model)
			f.installed = append(f.installed, req.Model)
			io.WriteString(w, `{"status":"pulling manifest"}`+"\n"+`{"status":"downloading","total":100,"completed":50}`+"\n"+`{"status":"success"}`+"\n")
		case "/api/show":
			if !slices.Contains(f.installed, req.Model) {
				http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
				return
			}
			caps := []string{"completion", "tools"}
			if slices.Contains(f.noTools, req.Model) {
				caps = caps[:1]
			}
			json.NewEncoder(w).Encode(map[string]any{"capabilities": caps, "model_info": map[string]any{"general.architecture": "llama", "llama.context_length": 131072}})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_temperature","arguments":"{\"city\":\"New York\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		case "/api/ps":
			var list []map[string]any
			for _, m := range f.installed {
				list = append(list, map[string]any{"name": m, "size": 100, "size_vram": 100, "context_length": f.ctx})
			}
			json.NewEncoder(w).Encode(map[string]any{"models": list})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("COPILOT_LOCAL_OLLAMA_URL", srv.URL)
	return srv
}

// launched records how the fake Copilot was started.
type launched struct {
	path     string
	args     []string
	settings launch.Settings
	calls    int
}

// fakeWorld isolates the CLI: a temp home, a Linux machine with nothing installed, a fake Copilot, and no terminal.
func fakeWorld(t *testing.T) (home string, runner *system.FakeRunner, got *launched) {
	t.Helper()
	home = hermetic(t)
	runner = &system.FakeRunner{}
	got = &launched{}
	oldEnv, oldFind, oldStart, oldInteractive, oldCopy := newEnv, findCopilot, startCopilot, interactive, copyText
	t.Cleanup(func() {
		newEnv, findCopilot, startCopilot, interactive, copyText = oldEnv, oldFind, oldStart, oldInteractive, oldCopy
	})
	newEnv = func(out io.Writer) (*ollama.Env, error) {
		e, err := ollama.NewEnv(out)
		if err != nil {
			return nil, err
		}
		e.Info = system.Info{OS: "linux", Arch: "amd64"}
		e.Runner = runner
		e.WaitTimeout = 0
		e.Sleep = func(time.Duration) {}
		e.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
		return e, nil
	}
	findCopilot = func() (string, error) { return "/fake/copilot", nil }
	startCopilot = func(path string, args []string, s launch.Settings) (int, error) {
		got.path, got.args, got.settings = path, args, s
		got.calls++
		return 7, nil
	}
	interactive = func() bool { return false }
	copyText = func(string) error { return nil }
	return home, runner, got
}

func writeShortlist(t *testing.T, tags ...string) {
	t.Helper()
	p := shortlistPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(tags, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func saveState(t *testing.T, st models.State) {
	t.Helper()
	e, err := newEnv(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(e.StateDir); err != nil {
		t.Fatal(err)
	}
}

func loadState(t *testing.T) models.State {
	t.Helper()
	e, err := newEnv(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	st, err := models.LoadState(e.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestLaunchUsesLastModelWithoutTerminal(t *testing.T) {
	_, _, got := fakeWorld(t)
	o := &fakeOllama{installed: []string{"llama3.1:8b", "gpt-oss:20b"}, ctx: 65536}
	srv := o.serve(t)
	saveState(t, models.State{LastUsed: "gpt-oss:20b", Models: []models.Qualified{{Tag: "llama3.1:8b", Context: 65536}, {Tag: "gpt-oss:20b", Context: 32768}}})

	code, out := runCLI(t, "--", "-p", "fix the tests")
	if code != 7 {
		t.Fatalf("Copilot's exit code wasn't passed on: %d\n%s", code, out)
	}
	if got.settings.Model != "gpt-oss:20b" || got.settings.Context != 32768 || got.settings.BaseURL != srv.URL {
		t.Errorf("settings: %+v", got.settings)
	}
	if !slices.Equal(got.args, []string{"-p", "fix the tests"}) || got.path != "/fake/copilot" {
		t.Errorf("args: %q %q", got.path, got.args)
	}
	vars := strings.Join(got.settings.Vars(), " ")
	for _, want := range []string{"COPILOT_OFFLINE=true", "COPILOT_PROVIDER_BASE_URL=" + srv.URL + "/v1", "COPILOT_MODEL=gpt-oss:20b"} {
		if !strings.Contains(vars, want) {
			t.Errorf("missing %s in %s", want, vars)
		}
	}
}

func TestLaunchForgetsRemovedModels(t *testing.T) {
	_, _, got := fakeWorld(t)
	(&fakeOllama{installed: []string{"llama3.1:8b"}, ctx: 65536}).serve(t)
	saveState(t, models.State{LastUsed: "gpt-oss:20b", Models: []models.Qualified{{Tag: "llama3.1:8b", Context: 65536}, {Tag: "gpt-oss:20b", Context: 65536}}})

	if code, out := runCLI(t); code != 7 || got.settings.Model != "llama3.1:8b" {
		t.Fatalf("%d %+v\n%s", code, got.settings, out)
	}
	st := loadState(t)
	if _, ok := st.Get("gpt-oss:20b"); ok || st.LastUsed != "llama3.1:8b" {
		t.Errorf("state: %+v", st)
	}
}

func TestLaunchNamedModelDownloadsTestsAndStarts(t *testing.T) {
	_, _, got := fakeWorld(t)
	o := &fakeOllama{installed: []string{"llama3.1:8b"}, ctx: 65536}
	o.serve(t)
	fakeRegistry(t, map[string]uint64{"tiny:1b": 131072})
	saveState(t, models.State{Models: []models.Qualified{{Tag: "llama3.1:8b", Context: 65536}}})

	code, out := runCLI(t, "--model", "tiny:1b", "--yes")
	if code != 7 || got.settings.Model != "tiny:1b" {
		t.Fatalf("%d %+v\n%s", code, got.settings, out)
	}
	if !slices.Equal(o.pulled, []string{"tiny:1b"}) {
		t.Errorf("pulled %v", o.pulled)
	}
	for _, want := range []string{"Downloading", "Tool-call test", "tiny:1b is ready for Copilot."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if st := loadState(t); st.LastUsed != "tiny:1b" {
		t.Errorf("state: %+v", st)
	}
}

func TestLaunchFirstRunShowsOnePlanAndAsks(t *testing.T) {
	_, _, got := fakeWorld(t)
	fakeRegistry(t, map[string]uint64{"tiny:1b": 131072, "short:1b": 4096})
	writeShortlist(t, "tiny:1b A tiny model", "short:1b Too little context")

	code, out := runCLI(t, "--context", "8192")
	if code != 2 || got.calls != 0 {
		t.Fatalf("%d calls=%d\n%s", code, got.calls, out)
	}
	for _, want := range []string{"Welcome!", "Set up Ollama:", "Install Ollama", "Model:", "tiny:1b", "Run again with --yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "Skipped short:1b") {
		t.Errorf("the model with too little context wasn't skipped:\n%s", out)
	}
}

func TestLaunchWithoutCopilot(t *testing.T) {
	fakeWorld(t)
	findCopilot = func() (string, error) { return "", launch.ErrNotInstalled }
	code, out := runCLI(t)
	if code != 1 || !strings.Contains(out, "Copilot CLI isn't installed") || !strings.Contains(out, "copilot-local model") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestLaunchWarnsAboutProvidersFile(t *testing.T) {
	home, _, _ := fakeWorld(t)
	t.Setenv("COPILOT_PROVIDERS_CONFIG", "")
	t.Setenv("COPILOT_HOME", "")
	os.MkdirAll(filepath.Join(home, ".copilot"), 0o755)
	os.WriteFile(filepath.Join(home, ".copilot", "providers.json"), []byte("{}"), 0o644)
	(&fakeOllama{installed: []string{"llama3.1:8b"}, ctx: 65536}).serve(t)
	saveState(t, models.State{Models: []models.Qualified{{Tag: "llama3.1:8b", Context: 65536}}})
	if code, out := runCLI(t); code != 7 || !strings.Contains(out, "providers.json exists") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestModelYesPicksDownloadsAndRecords(t *testing.T) {
	fakeWorld(t)
	o := &fakeOllama{ctx: 8192}
	o.serve(t)
	fakeRegistry(t, map[string]uint64{"tiny:1b": 131072})
	writeShortlist(t, "tiny:1b")

	code, out := runCLI(t, "model", "--yes", "--context", "8192")
	if code != 0 || !slices.Equal(o.pulled, []string{"tiny:1b"}) {
		t.Fatalf("%d %v\n%s", code, o.pulled, out)
	}
	if q, ok := loadState(t).Get("tiny:1b"); !ok || q.Context != 8192 {
		t.Errorf("not recorded: %+v", q)
	}
}

func TestModelFailsWithoutTools(t *testing.T) {
	fakeWorld(t)
	o := &fakeOllama{installed: []string{"tiny:1b"}, noTools: []string{"tiny:1b"}, ctx: 65536}
	o.serve(t)
	fakeRegistry(t, map[string]uint64{"tiny:1b": 131072})

	code, out := runCLI(t, "model", "--model", "tiny:1b", "--yes")
	if code != 1 || !strings.Contains(out, "can't call tools") || len(o.pulled) != 0 {
		t.Fatalf("%d\n%s", code, out)
	}
	if _, ok := loadState(t).Get("tiny:1b"); ok {
		t.Error("a failed model was recorded")
	}
}

func TestModelCheckCatchesSmallContext(t *testing.T) {
	fakeWorld(t)
	(&fakeOllama{installed: []string{"tiny:1b"}, ctx: 4096}).serve(t)
	saveState(t, models.State{Models: []models.Qualified{{Tag: "tiny:1b", Context: 65536}}})

	code, out := runCLI(t, "model", "--check", "--context", "65536")
	if code != 1 || !strings.Contains(out, "Ollama gave the model 4096 tokens") || !strings.Contains(out, "copilot-local ollama") {
		t.Fatalf("%d\n%s", code, out)
	}
	if _, ok := loadState(t).Get("tiny:1b"); ok {
		t.Error("a model that failed the re-test is still offered")
	}
}

func TestModelRefusesTooSmallContext(t *testing.T) {
	fakeWorld(t)
	(&fakeOllama{ctx: 65536}).serve(t)
	fakeRegistry(t, map[string]uint64{"short:1b": 4096})
	if code, out := runCLI(t, "model", "--model", "short:1b", "--yes", "--context", "65536"); code != 1 || !strings.Contains(out, "supports only 4096") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestModelDryRunDownloadsNothing(t *testing.T) {
	fakeWorld(t)
	o := &fakeOllama{ctx: 65536}
	o.serve(t)
	fakeRegistry(t, map[string]uint64{"tiny:1b": 131072})
	writeShortlist(t, "tiny:1b")
	code, out := runCLI(t, "model", "--dry-run")
	if code != 0 || len(o.pulled) != 0 || !strings.Contains(out, "Dry run") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestModelGeminiWithoutTerminal(t *testing.T) {
	home, _, _ := fakeWorld(t)
	os.MkdirAll(filepath.Join(home, "Documents"), 0o755)
	(&fakeOllama{ctx: 65536}).serve(t)
	fakeRegistry(t, map[string]uint64{"tiny:1b": 131072})
	writeShortlist(t, "tiny:1b")

	code, out := runCLI(t, "model", "--ask-gemini")
	if code != 0 || !strings.Contains(out, "MODEL:") || !strings.Contains(out, "copilot-local model --model") {
		t.Fatalf("%d\n%s", code, out)
	}
	saved, err := os.ReadFile(filepath.Join(home, "Documents", "copilot-local-gemini-prompt.txt"))
	if err != nil || !strings.Contains(string(saved), "tiny:1b") {
		t.Errorf("prompt not saved: %v", err)
	}
}

func TestModelGeminiDryRunSavesNothing(t *testing.T) {
	home, _, _ := fakeWorld(t)
	(&fakeOllama{ctx: 65536}).serve(t)
	fakeRegistry(t, map[string]uint64{"tiny:1b": 131072})
	writeShortlist(t, "tiny:1b")
	code, out := runCLI(t, "model", "--ask-gemini", "--dry-run")
	if code != 0 || !strings.Contains(out, "wasn't saved") {
		t.Fatalf("%d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(home, "copilot-local-gemini-prompt.txt")); err == nil {
		t.Error("dry run saved the prompt")
	}
}

func TestModelGeminiRefusesYes(t *testing.T) {
	fakeWorld(t)
	if code, out := runCLI(t, "model", "--ask-gemini", "--yes"); code != 2 || !strings.Contains(out, "can't be used with --yes") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestModelNeedsOllama(t *testing.T) {
	fakeWorld(t)
	if code, out := runCLI(t, "model", "--yes"); code != 1 || !strings.Contains(out, "copilot-local ollama") {
		t.Fatalf("%d\n%s", code, out)
	}
}
