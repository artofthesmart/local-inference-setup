package ollama

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

// fakeOllama mimics the parts of the Ollama API the tool calls.
type fakeOllama struct {
	mu            sync.Mutex
	up            bool
	cloudDisabled bool
	noStatusAPI   bool // older Ollama without /api/status
	srv           *httptest.Server
}

func newFakeOllama(t *testing.T) *fakeOllama {
	f := &fakeOllama{up: true}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.up {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/api/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "0.40.7"})
		case "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{}})
		case "/install":
			w.Write([]byte("echo installing\n"))
		case "/api/status":
			if f.noStatusAPI {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"cloud": map[string]any{"disabled": f.cloudDisabled, "source": "config"}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOllama) set(fn func(*fakeOllama)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

const fakeBin = "/fake/bin/ollama"

type testEnv struct {
	*Env
	runner *system.FakeRunner
	server *fakeOllama
	exists map[string]bool
}

// newTestEnv builds an Env in a temp folder. installed controls whether the fake Ollama binary is found.
func newTestEnv(t *testing.T, info system.Info, installed bool) *testEnv {
	t.Helper()
	home := t.TempDir()
	srv := newFakeOllama(t)
	fr := &system.FakeRunner{
		Responses: map[string]string{fakeBin + " --version": "ollama version is 0.40.7"},
		Failures:  map[string]string{},
	}
	te := &testEnv{runner: fr, server: srv, exists: map[string]bool{}}
	te.Env = &Env{
		Info:             info,
		Runner:           fr,
		HTTP:             srv.srv.Client(),
		BaseURL:          srv.srv.URL,
		Home:             home,
		LocalAppData:     filepath.Join(home, "AppData", "Local"),
		StateDir:         filepath.Join(home, "state"),
		Out:              io.Discard,
		Sleep:            func(time.Duration) {},
		InstallScriptURL: func(string) string { return srv.srv.URL + "/install" },
		WaitTimeout:      0,
		DropInFile:       filepath.Join(home, "systemd", "ollama.service.d", "50-copilot-local.conf"),
		LookPath: func(string) (string, error) {
			if installed {
				return fakeBin, nil
			}
			return "", errors.New("not found")
		},
	}
	te.Env.Exists = func(p string) bool {
		if v, ok := te.exists[p]; ok {
			return v
		}
		_, err := os.Stat(p)
		return err == nil && strings.HasPrefix(p, home)
	}
	return te
}

func titles(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Title
	}
	return out
}

func applyAll(t *testing.T, steps []Step) {
	t.Helper()
	for _, s := range steps {
		if err := s.Apply(t.Context()); err != nil {
			t.Fatalf("apply %q: %v", s.Title, err)
		}
	}
}

func mustDetect(t *testing.T, e *Env) *State {
	t.Helper()
	s, err := Detect(t.Context(), e)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	return s
}

func mustPlan(t *testing.T, e *Env, want int) []Step {
	t.Helper()
	steps, err := Plan(e, mustDetect(t, e), Options{ContextLength: want})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return steps
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func newServer(t *testing.T, h http.Handler) *httptest.Server {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}
