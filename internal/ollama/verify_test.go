package ollama

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func find(t *testing.T, r Report, name string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, r.Checks)
	return Check{}
}

func writeLog(t *testing.T, e *Env, lines ...string) {
	t.Helper()
	p := e.serverLogPath()
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func serverConfigLine(ctx string) string {
	return `time=2026-10-21T10:00:00.000-07:00 level=INFO source=routes.go:2450 msg="server config" env="map[CUDA_VISIBLE_DEVICES: OLLAMA_CONTEXT_LENGTH:` + ctx + ` OLLAMA_DEBUG:INFO OLLAMA_HOST:http://127.0.0.1:11434]"`
}

func TestVerifyAllGood(t *testing.T) {
	te := newTestEnv(t, macOS, true)
	te.server.set(func(f *fakeOllama) { f.cloudDisabled = true })
	writeLog(t, te.Env, serverConfigLine("4096"), "level=INFO msg=other", serverConfigLine("65536"), `level=INFO msg="Ollama cloud disabled: true"`)
	r := Verify(t.Context(), te.Env, 65536)
	if !r.OK() {
		t.Fatalf("want OK, got %+v", r.Checks)
	}
	for _, c := range r.Checks {
		if c.Status != Pass {
			t.Errorf("%s: %s", c.Name, c.Status)
		}
	}
}

func TestVerifyWrongContextFromOldStart(t *testing.T) {
	te := newTestEnv(t, windows, true)
	te.server.set(func(f *fakeOllama) { f.cloudDisabled = true })
	writeLog(t, te.Env, serverConfigLine("0"))
	c := find(t, Verify(t.Context(), te.Env, 65536), "Context length 65536")
	if c.Status != Fail || !strings.Contains(c.Fix, "system tray") {
		t.Fatalf("got %+v", c)
	}
}

func TestVerifyNoLogIsAWarning(t *testing.T) {
	te := newTestEnv(t, linuxPlain, true)
	te.server.set(func(f *fakeOllama) { f.cloudDisabled = true })
	r := Verify(t.Context(), te.Env, 65536)
	if c := find(t, r, "Context length 65536"); c.Status != Warn || !strings.Contains(c.Fix, "ollama ps") {
		t.Fatalf("got %+v", c)
	}
	if !r.OK() {
		t.Error("a warning alone shouldn't fail the run")
	}
}

func TestVerifyCloudStillOn(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	te.runner.Responses[showEnv] = "OLLAMA_CONTEXT_LENGTH=65536"
	r := Verify(t.Context(), te.Env, 65536)
	if c := find(t, r, "Ollama cloud off"); c.Status != Fail || !strings.Contains(c.Fix, "systemctl restart") {
		t.Fatalf("got %+v", c)
	}
	if find(t, r, "Context length 65536").Status != Pass {
		t.Error("context from systemd should pass")
	}
}

func TestVerifyOldOllamaWithoutStatusAPI(t *testing.T) {
	te := newTestEnv(t, linuxPlain, true)
	te.server.set(func(f *fakeOllama) { f.noStatusAPI = true })
	setServerJSONCloud(te.serverJSONPath(), true)
	if c := find(t, Verify(t.Context(), te.Env, 65536), "Ollama cloud off"); c.Status != Warn {
		t.Fatalf("got %+v", c)
	}
}

func TestVerifyServerDown(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	te.server.set(func(f *fakeOllama) { f.up = false })
	r := Verify(t.Context(), te.Env, 65536)
	c := find(t, r, "Ollama running")
	if c.Status != Fail || !strings.Contains(c.Fix, "systemctl start ollama") {
		t.Fatalf("got %+v", c)
	}
	if r.OK() {
		t.Error("server down must fail")
	}
}

func TestVerifyWarnsWhenExposed(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	te.runner.Responses[showEnv] = `OLLAMA_CONTEXT_LENGTH=65536 OLLAMA_NO_CLOUD=1 OLLAMA_HOST=0.0.0.0`
	te.server.set(func(f *fakeOllama) { f.cloudDisabled = true })
	if c := find(t, Verify(t.Context(), te.Env, 65536), "Only reachable from this computer"); c.Status != Warn {
		t.Fatalf("got %+v", c)
	}
}

func TestVerifyMissingV1(t *testing.T) {
	te := newTestEnv(t, linuxSystemd, true)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"version":"0.1.0"}`)) })
	srv := newServer(t, mux)
	te.BaseURL, te.HTTP = srv.URL, srv.Client()
	if c := find(t, Verify(t.Context(), te.Env, 65536), "Copilot-compatible API (/v1)"); c.Status != Fail {
		t.Fatalf("got %+v", c)
	}
}

func TestLogContextLargeFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.log")
	f, _ := os.Create(p)
	f.WriteString(serverConfigLine("1") + "\n")
	pad := strings.Repeat("x", 1023) + "\n"
	for i := 0; i < 5*1024; i++ {
		f.WriteString(pad)
	}
	f.WriteString(serverConfigLine("65536") + "\n")
	f.Close()
	if n, ok := logContext(p); !ok || n != 65536 {
		t.Fatalf("got %d %v", n, ok)
	}
}
