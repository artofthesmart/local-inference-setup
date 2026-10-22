// Package launch starts GitHub Copilot CLI connected to a local model, offline.
package launch

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// OutputTokens is the most a single Copilot answer may use; the rest of the context is for the conversation.
const OutputTokens = 8192

// Settings is everything needed to start Copilot on a local model.
type Settings struct {
	BaseURL string // Ollama's address, without /v1
	Model   string
	Context int
}

// Vars returns the Copilot CLI settings, as NAME=value pairs. They apply to this one run only.
func (s Settings) Vars() []string {
	out := OutputTokens
	if s.Context < 4*OutputTokens {
		out = s.Context / 4
	}
	return []string{
		"COPILOT_OFFLINE=true",
		"COPILOT_PROVIDER_TYPE=openai",
		"COPILOT_PROVIDER_BASE_URL=" + strings.TrimRight(s.BaseURL, "/") + "/v1",
		"COPILOT_MODEL=" + s.Model,
		"COPILOT_PROVIDER_MAX_PROMPT_TOKENS=" + strconv.Itoa(s.Context-out),
		"COPILOT_PROVIDER_MAX_OUTPUT_TOKENS=" + strconv.Itoa(out),
	}
}

// Environ merges the settings into an existing environment, replacing any old values of the same names.
func (s Settings) Environ(base []string) []string {
	vars := s.Vars()
	names := map[string]bool{}
	for _, v := range vars {
		name, _, _ := strings.Cut(v, "=")
		names[strings.ToUpper(name)] = true
	}
	var out []string
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if !names[strings.ToUpper(name)] {
			out = append(out, kv)
		}
	}
	return append(out, vars...)
}

// InstallHint tells the user how to get Copilot CLI.
const InstallHint = `Copilot CLI isn't installed. Install it with one of these, then run copilot-local again:
  npm install -g @github/copilot
  brew install --cask copilot-cli
  winget install GitHub.Copilot`

// ErrNotInstalled means the copilot command wasn't found.
var ErrNotInstalled = errors.New(InstallHint)

// Find locates the copilot command.
func Find(lookPath func(string) (string, error)) (string, error) {
	p, err := lookPath("copilot")
	if err != nil {
		return "", ErrNotInstalled
	}
	return p, nil
}

// ProvidersWarning returns a warning when a Copilot providers file exists, because it overrides the settings this tool passes.
func ProvidersWarning(getenv func(string) string, home string, exists func(string) bool) string {
	path := getenv("COPILOT_PROVIDERS_CONFIG")
	if path == "" {
		dir := getenv("COPILOT_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".copilot")
		}
		path = filepath.Join(dir, "providers.json")
	}
	if !exists(path) {
		return ""
	}
	return fmt.Sprintf("%s exists and overrides the local model settings, so Copilot may not use your local model. Rename it if Copilot asks you to sign in or uses another model.", path)
}

// Run starts copilot and waits for it to finish, returning its exit code. Copilot gets the terminal; Ctrl+C goes to it.
func Run(path string, args []string, s Settings) (int, error) {
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = s.Environ(os.Environ())
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
