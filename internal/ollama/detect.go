package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

const macAppPath = "/Applications/Ollama.app"

// State is what Detect found on the machine.
type State struct {
	BinaryPath    string
	Version       string
	ServerUp      bool
	ServerVersion string
	SystemdUnit   bool // Linux: an ollama.service unit exists

	// Cloud reports what the running server says, when it supports /api/status.
	CloudKnown    bool
	CloudDisabled bool

	// Configured values: what the settings say. They take effect after a restart.
	ConfiguredContext int
	ConfiguredNoCloud bool
	OurDropIn         bool // systemd: our drop-in file exists
	DropInContext     int  // systemd: the value in our drop-in file
	ExposedHost       string
	// MacApp is true when Ollama.app exists, so it can be restarted as an app.
	MacApp bool
	// WindowsSideServer: inside WSL, Ollama is not installed but a server answers, so it is the Windows install.
	WindowsSideServer bool
}

// Installed reports whether an Ollama binary was found.
func (s *State) Installed() bool { return s.BinaryPath != "" }

// Detect inspects the machine. It never changes anything.
func Detect(ctx context.Context, e *Env) (*State, error) {
	s := &State{}
	s.BinaryPath = e.findBinary()
	if s.Installed() {
		if out, err := e.Runner.Run(ctx, system.Cmd{Name: s.BinaryPath, Args: []string{"--version"}}); err == nil || out != "" {
			s.Version = parseVersion(out)
		}
	}
	s.ServerVersion, s.ServerUp = e.serverVersion(ctx)
	if s.ServerUp {
		s.CloudKnown, s.CloudDisabled = e.cloudStatus(ctx)
	}
	if e.Info.OS == "darwin" {
		s.MacApp = e.Exists(macAppPath)
	}
	if e.Info.WSL && !s.Installed() && s.ServerUp {
		s.WindowsSideServer = true
	}
	if host := os.Getenv(envHost); isExposed(host) {
		s.ExposedHost = host
	}

	switch {
	case e.Info.OS == "linux" && e.Info.Systemd:
		if _, err := e.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"cat", "ollama"}}); err == nil {
			s.SystemdUnit = true
		}
		if s.SystemdUnit {
			out, _ := e.Runner.Run(ctx, system.Cmd{Name: "systemctl", Args: []string{"show", "ollama", "-p", "Environment", "--value"}})
			env := parseEnvList(out)
			s.ConfiguredContext = atoi(env[envContext])
			s.ConfiguredNoCloud = truthy(env[envNoCloud])
			if isExposed(env[envHost]) {
				s.ExposedHost = env[envHost]
			}
			if data, err := os.ReadFile(e.DropInFile); err == nil {
				s.OurDropIn = true
				s.DropInContext = atoi(parseEnvList(string(data))[envContext])
			}
			break
		}
		fallthrough
	case e.Info.OS == "linux":
		s.ConfiguredContext = readProfileContext(e.profilePath())
	case e.Info.OS == "darwin":
		s.ConfiguredContext = readLaunchAgentContext(e.launchAgentPath())
		if s.ConfiguredContext > 0 {
			// The plist only applies at login, so also check the live launchd value.
			out, _ := e.Runner.Run(ctx, system.Cmd{Name: "launchctl", Args: []string{"getenv", envContext}})
			if atoi(out) != s.ConfiguredContext {
				s.ConfiguredContext = 0
			}
		}
	case e.Info.OS == "windows":
		out, _ := e.Runner.Run(ctx, psGetUserEnv(envContext))
		s.ConfiguredContext = atoi(out)
	}

	if !e.usesSystemd(s) {
		off, err := cloudDisabledInServerJSON(e.serverJSONPath())
		if err != nil {
			return s, err
		}
		s.ConfiguredNoCloud = off
	}
	return s, nil
}

func (e *Env) findBinary() string {
	name := "ollama"
	if e.Info.OS == "windows" {
		name = "ollama.exe"
	}
	if p, err := e.LookPath(name); err == nil && !isWindowsBinaryFromWSL(e, p) {
		return p
	}
	var candidates []string
	switch e.Info.OS {
	case "windows":
		candidates = []string{filepath.Join(e.windowsInstallDir(), name)}
	case "darwin":
		candidates = []string{macAppPath + "/Contents/Resources/ollama", "/usr/local/bin/ollama", "/opt/homebrew/bin/ollama"}
	default:
		candidates = []string{"/usr/local/bin/ollama", "/usr/bin/ollama"}
	}
	for _, c := range candidates {
		if e.Exists(c) {
			return c
		}
	}
	return ""
}

// isWindowsBinaryFromWSL ignores ollama.exe found on a WSL PATH that includes Windows folders.
func isWindowsBinaryFromWSL(e *Env, p string) bool {
	return e.Info.WSL && (strings.HasPrefix(p, "/mnt/") || strings.HasSuffix(p, ".exe"))
}

func parseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "version is "); i >= 0 {
			return strings.TrimSpace(line[i+len("version is "):])
		}
	}
	return strings.TrimSpace(out)
}

func isExposed(host string) bool {
	h := strings.TrimSpace(host)
	h = strings.TrimPrefix(strings.TrimPrefix(h, "http://"), "https://")
	return strings.HasPrefix(h, "0.0.0.0") || strings.HasPrefix(h, "[::]") || strings.HasPrefix(h, ":")
}

func (e *Env) get(ctx context.Context, path string, into any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(e.BaseURL, "/")+path, nil)
	if err != nil {
		return 0, err
	}
	resp, err := e.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			return resp.StatusCode, fmt.Errorf("GET %s: %w", path, err)
		}
	}
	return resp.StatusCode, nil
}

func (e *Env) serverVersion(ctx context.Context) (string, bool) {
	var v struct {
		Version string `json:"version"`
	}
	if _, err := e.get(ctx, "/api/version", &v); err != nil {
		return "", false
	}
	return v.Version, true
}

// cloudStatus asks the server whether cloud is off. Older servers have no /api/status, so known is false.
func (e *Env) cloudStatus(ctx context.Context) (known, disabled bool) {
	var st struct {
		Cloud struct {
			Disabled bool   `json:"disabled"`
			Source   string `json:"source"`
		} `json:"cloud"`
	}
	if _, err := e.get(ctx, "/api/status", &st); err != nil {
		return false, false
	}
	return true, st.Cloud.Disabled
}
