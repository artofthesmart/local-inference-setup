package launch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProbeCommandIsolated(t *testing.T) {
	dir := t.TempDir()
	settings := Settings{BaseURL: "http://127.0.0.1:11434", Model: "m:1", Context: 65536}
	cmd, err := probeCommand(t.Context(), "copilot", settings, dir, []string{
		"PATH=" + os.Getenv("PATH"), "SystemRoot=C:\\Windows", "LANG=C",
		"GH_TOKEN=secret", "COPILOT_PROVIDER_API_KEY=secret",
		"COPILOT_PROVIDER_API_KEY_COMMAND=do-not-run", "COPILOT_PROVIDER_WIRE_MODEL=cloud",
		"COPILOT_PROVIDERS_CONFIG=/private/providers.json", "COPILOT_HOME=/private",
		"NODE_OPTIONS=--require=/private/inject.js", "OTEL_EXPORTER_OTLP_ENDPOINT=https://example.com",
		"COPILOT_ALLOW_ALL=true", "COPILOT_OFFLINE=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != filepath.Join(dir, "work") || cmd.Stdin != nil {
		t.Fatalf("workspace/stdin not isolated: %s %v", cmd.Dir, cmd.Stdin)
	}
	for _, flag := range []string{"--available-tools=none", "--no-custom-instructions", "--disable-builtin-mcps", "--disable-client-telemetry", "--no-auto-update"} {
		if !slices.Contains(cmd.Args, flag) {
			t.Errorf("missing %s in %q", flag, cmd.Args)
		}
	}
	for _, kv := range settings.Vars() {
		if !slices.Contains(cmd.Env, kv) {
			t.Errorf("missing %s", kv)
		}
	}
	for _, kv := range cmd.Env {
		if strings.Contains(kv, "secret") || strings.Contains(kv, "/private") || strings.Contains(kv, "do-not-run") || strings.HasPrefix(kv, "NODE_OPTIONS=") || strings.HasPrefix(kv, "OTEL_") || strings.HasPrefix(kv, "COPILOT_ALLOW_ALL=") {
			t.Errorf("unsafe inherited environment: %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
	if !slices.Contains(cmd.Env, "COPILOT_HOME="+filepath.Join(dir, "home", ".copilot")) ||
		!slices.Contains(cmd.Env, "HOME="+filepath.Join(dir, "home")) ||
		!slices.Contains(cmd.Env, "SystemRoot=C:\\Windows") {
		t.Error("isolated home or platform essentials missing")
	}
}

func TestProbeCommandDirectoryError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "home"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := probeCommand(t.Context(), "copilot", Settings{}, dir, nil); err == nil {
		t.Fatal("expected directory creation error")
	}
}

func TestProbeMissingBinaryCleansUp(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	if err := Probe(t.Context(), filepath.Join(dir, "missing-copilot"), Settings{}); err == nil {
		t.Fatal("missing executable passed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary probe files left behind: %v, %v", entries, err)
	}
}

func TestProbeCancellationCleansUp(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Probe(ctx, path, Settings{}); err == nil {
		t.Fatal("cancelled probe passed")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary probe files left behind: %v, %v", entries, err)
	}
}

// TestProbeWithInstalledCLI validates the real CLI wiring against a local mock, with no model download.
func TestProbeWithInstalledCLI(t *testing.T) {
	if os.Getenv("COPILOT_LOCAL_CLI_TEST") != "1" {
		t.Skip("set COPILOT_LOCAL_CLI_TEST=1 to test installed Copilot against a mock")
	}
	path, err := exec.LookPath("copilot")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		reply string
		fail  bool
	}{
		{"success", probeReply, false},
		{"wrong reply", "WRONG_REPLY", true},
		{"empty reply", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/chat/completions" {
					http.NotFound(w, r)
					return
				}
				var req struct {
					Model    string `json:"model"`
					Messages []struct {
						Content any `json:"content"`
					} `json:"messages"`
					Tools []any `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("decode request: %v", err)
				}
				if req.Model != "m:1" || len(req.Tools) != 0 {
					t.Errorf("unexpected model/tools: %s, %d tools", req.Model, len(req.Tools))
				}
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				data, _ := json.Marshal(map[string]any{"id": "test", "object": "chat.completion.chunk", "choices": []any{
					map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": test.reply}, "finish_reason": nil},
				}})
				fmt.Fprintf(w, "data: %s\n\n", data)
				io.WriteString(w, "data: "+`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
			}))
			defer srv.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			err := Probe(ctx, path, Settings{BaseURL: srv.URL, Model: "m:1", Context: 65536})
			if (err != nil) != test.fail {
				t.Fatalf("probe error = %v; expected failure %v", err, test.fail)
			}
			if requests.Load() == 0 {
				t.Fatal("Copilot never called the local /v1/chat/completions endpoint")
			}
		})
	}
}
