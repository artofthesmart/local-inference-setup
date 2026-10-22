// Package ollama installs Ollama and tunes it for GitHub Copilot: a large context window and Ollama cloud turned off.
package ollama

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

const (
	DefaultBaseURL       = "http://localhost:11434"
	DefaultContextLength = 65536

	DropInDir        = "/etc/systemd/system/ollama.service.d"
	DropInPath       = DropInDir + "/50-copilot-local.conf"
	LaunchAgentLabel = "com.copilot-local.ollama-env"

	envContext = "OLLAMA_CONTEXT_LENGTH"
	envNoCloud = "OLLAMA_NO_CLOUD"
	envHost    = "OLLAMA_HOST"
)

// Env holds everything the Ollama step touches, so tests can point it at temp folders and fakes.
type Env struct {
	Info         system.Info
	Runner       system.Runner
	HTTP         *http.Client
	BaseURL      string
	Home         string
	LocalAppData string // Windows only
	StateDir     string // where the tool records what it changed, for --undo
	Out          io.Writer
	Sleep        func(time.Duration)
	// InstallScriptURL returns the official Ollama installer for an OS.
	InstallScriptURL func(goos string) string
	// WaitTimeout is how long Verify waits for the server to answer.
	WaitTimeout time.Duration
	// DropInFile is the systemd drop-in path. Tests point it elsewhere.
	DropInFile string
	// LookPath and Exists find programs and files. Tests replace them so a real Ollama on the test machine is ignored.
	LookPath func(string) (string, error)
	Exists   func(string) bool
}

// NewEnv builds an Env for the real machine.
func NewEnv(out io.Writer) (*Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		cfgDir = filepath.Join(home, ".config")
	}
	base := os.Getenv("COPILOT_LOCAL_OLLAMA_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	return &Env{
		Info:             system.DetectInfo(),
		Runner:           system.ExecRunner{Out: out},
		HTTP:             &http.Client{Timeout: 5 * time.Second},
		BaseURL:          base,
		Home:             home,
		LocalAppData:     os.Getenv("LOCALAPPDATA"),
		StateDir:         filepath.Join(cfgDir, "copilot-local"),
		Out:              out,
		Sleep:            time.Sleep,
		InstallScriptURL: OfficialInstallScriptURL,
		WaitTimeout:      30 * time.Second,
		DropInFile:       DropInPath,
		LookPath:         exec.LookPath,
		Exists:           fileExists,
	}, nil
}

// OfficialInstallScriptURL returns Ollama's own installer script for an OS.
func OfficialInstallScriptURL(goos string) string {
	if goos == "windows" {
		return "https://ollama.com/install.ps1"
	}
	return "https://ollama.com/install.sh"
}

// Options are the user's choices.
type Options struct {
	ContextLength int
	SkipInstall   bool
}

// usesSystemd reports whether Ollama runs as a systemd service. Its service runs as the "ollama" user, so it never reads the signed-in user's ~/.ollama/server.json; cloud must be turned off with an environment variable instead.
func (e *Env) usesSystemd(s *State) bool {
	return e.Info.OS == "linux" && e.Info.Systemd && s.SystemdUnit
}

func (e *Env) serverJSONPath() string {
	return filepath.Join(e.Home, ".ollama", "server.json")
}

func (e *Env) profilePath() string {
	return filepath.Join(e.Home, ".profile")
}

func (e *Env) launchAgentPath() string {
	return filepath.Join(e.Home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
}

func (e *Env) serverLogPath() string {
	if e.Info.OS == "windows" {
		return filepath.Join(e.LocalAppData, "Ollama", "server.log")
	}
	return filepath.Join(e.Home, ".ollama", "logs", "server.log")
}

func (e *Env) windowsInstallDir() string {
	return filepath.Join(e.LocalAppData, "Programs", "Ollama")
}

func (e *Env) sleep(d time.Duration) {
	if e.Sleep != nil {
		e.Sleep(d)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
