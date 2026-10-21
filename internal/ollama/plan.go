package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

// Step is one change the tool will make. Details say exactly what it touches, for --dry-run and the confirmation prompt.
type Step struct {
	Title   string
	Details []string
	// Install marks the install step. After it runs, the caller should detect and plan again.
	Install bool
	Apply   func(ctx context.Context) error
}

// ErrManualRestart means the settings were saved, but the user must restart Ollama themselves.
var ErrManualRestart = errors.New("restart Ollama yourself so it picks up the new settings")

// WindowsSideError explains what to do when WSL can reach a Windows install of Ollama.
type WindowsSideError struct{}

func (WindowsSideError) Error() string {
	return "Ollama isn't installed inside WSL, but a server answers at 127.0.0.1:11434, so it's probably your Windows install. Set it up from Windows instead: open PowerShell and run copilot-local there."
}

// record notes what the tool changed, so --undo removes only that.
type record struct {
	ServerJSONKeyAdded bool `json:"serverJsonKeyAdded,omitempty"`
	WindowsEnvSet      bool `json:"windowsEnvSet,omitempty"`
}

func (e *Env) recordPath() string { return filepath.Join(e.StateDir, "ollama.json") }

func (e *Env) loadRecord() record {
	var r record
	if data, err := os.ReadFile(e.recordPath()); err == nil {
		_ = json.Unmarshal(data, &r)
	}
	return r
}

func (e *Env) saveRecord(update func(*record)) error {
	r := e.loadRecord()
	update(&r)
	if err := os.MkdirAll(e.StateDir, 0o755); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(r, "", "  ")
	return writeFileAtomic(e.recordPath(), append(data, '\n'), 0o644)
}

// Plan lists the changes needed. An empty list means everything is already set up.
func Plan(e *Env, s *State, o Options) ([]Step, error) {
	if o.ContextLength <= 0 {
		o.ContextLength = DefaultContextLength
	}
	if s.WindowsSideServer {
		return nil, WindowsSideError{}
	}
	if !s.Installed() {
		if o.SkipInstall {
			return nil, errors.New("Ollama isn't installed. Install it from https://ollama.com/download, or run again without --skip-install")
		}
		return []Step{installStep(e)}, nil
	}

	var steps []Step
	want := o.ContextLength
	switch {
	case e.usesSystemd(s):
		ours := s.OurDropIn && s.DropInContext == want
		if !ours && (s.ConfiguredContext != want || !s.ConfiguredNoCloud) {
			steps = append(steps, dropInStep(e, want))
		}
	case e.Info.OS == "linux":
		if s.ConfiguredContext != want {
			steps = append(steps, profileStep(e, want))
		}
	case e.Info.OS == "darwin":
		if s.ConfiguredContext != want {
			steps = append(steps, launchAgentStep(e, want))
		}
	case e.Info.OS == "windows":
		if s.ConfiguredContext != want {
			steps = append(steps, windowsEnvStep(e, want))
		}
	}
	if !e.usesSystemd(s) && !s.ConfiguredNoCloud {
		steps = append(steps, serverJSONStep(e))
	}
	if len(steps) > 0 || !s.ServerUp {
		steps = append(steps, restartStep(e, s, want))
	}
	return steps, nil
}

// Undo lists the changes needed to remove everything this tool added. It never uninstalls Ollama.
func Undo(e *Env, s *State) []Step {
	var steps []Step
	rec := e.loadRecord()
	switch {
	case e.usesSystemd(s):
		if s.OurDropIn {
			steps = append(steps, Step{
				Title:   "Remove the systemd settings file",
				Details: []string{"sudo rm " + e.DropInFile, "sudo systemctl daemon-reload"},
				Apply: func(ctx context.Context) error {
					if _, err := e.Runner.Run(ctx, system.Sudo(e.Info, system.Cmd{Name: "rm", Args: []string{"-f", e.DropInFile}})); err != nil {
						return err
					}
					_, err := e.Runner.Run(ctx, system.Sudo(e.Info, system.Cmd{Name: "systemctl", Args: []string{"daemon-reload"}}))
					return err
				},
			})
		}
	case e.Info.OS == "linux":
		if readProfileContext(e.profilePath()) > 0 {
			steps = append(steps, Step{
				Title:   "Remove the Ollama block from ~/.profile",
				Details: []string{"Edit " + e.profilePath() + " (a backup is saved first)"},
				Apply:   func(context.Context) error { return setProfileBlock(e.profilePath(), 0) },
			})
		}
	case e.Info.OS == "darwin":
		if e.Exists(e.launchAgentPath()) {
			steps = append(steps, Step{
				Title:   "Remove the login item that sets the context length",
				Details: []string{"Delete " + e.launchAgentPath(), "launchctl unsetenv " + envContext},
				Apply: func(ctx context.Context) error {
					if err := os.Remove(e.launchAgentPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
						return err
					}
					_, err := e.Runner.Run(ctx, system.Cmd{Name: "launchctl", Args: []string{"unsetenv", envContext}})
					return err
				},
			})
		}
	case e.Info.OS == "windows":
		if rec.WindowsEnvSet {
			steps = append(steps, Step{
				Title:   "Remove the " + envContext + " user environment variable",
				Details: []string{psSetUserEnv(envContext, "").String()},
				Apply: func(ctx context.Context) error {
					if _, err := e.Runner.Run(ctx, psSetUserEnv(envContext, "")); err != nil {
						return err
					}
					return e.saveRecord(func(r *record) { r.WindowsEnvSet = false })
				},
			})
		}
	}
	if !e.usesSystemd(s) && rec.ServerJSONKeyAdded {
		steps = append(steps, Step{
			Title:   "Remove disable_ollama_cloud from server.json",
			Details: []string{"Edit " + e.serverJSONPath() + " (a backup is saved first)"},
			Apply: func(context.Context) error {
				if err := setServerJSONCloud(e.serverJSONPath(), false); err != nil {
					return err
				}
				return e.saveRecord(func(r *record) { r.ServerJSONKeyAdded = false })
			},
		})
	}
	if len(steps) > 0 && s.ServerUp {
		steps = append(steps, restartStep(e, s, 0))
	}
	return steps
}

func dropInStep(e *Env, want int) Step {
	content := dropInContent(want)
	return Step{
		Title: fmt.Sprintf("Set context length to %d and turn off Ollama cloud for the Ollama service", want),
		Details: []string{
			"Write " + e.DropInFile + " (needs sudo):",
			content,
			"sudo systemctl daemon-reload",
		},
		Apply: func(ctx context.Context) error {
			cmds := []system.Cmd{
				{Name: "mkdir", Args: []string{"-p", filepath.Dir(e.DropInFile)}},
				{Name: "tee", Args: []string{e.DropInFile}, Stdin: content},
				{Name: "systemctl", Args: []string{"daemon-reload"}},
			}
			for _, c := range cmds {
				if _, err := e.Runner.Run(ctx, system.Sudo(e.Info, c)); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func profileStep(e *Env, want int) Step {
	return Step{
		Title:   fmt.Sprintf("Set context length to %d in ~/.profile", want),
		Details: []string{"Add this block to " + e.profilePath() + " (a backup is saved first):", profileBlock(want)},
		Apply:   func(context.Context) error { return setProfileBlock(e.profilePath(), want) },
	}
}

func launchAgentStep(e *Env, want int) Step {
	return Step{
		Title: fmt.Sprintf("Set context length to %d for the Ollama app", want),
		Details: []string{
			fmt.Sprintf("launchctl setenv %s %d", envContext, want),
			"Write " + e.launchAgentPath() + " so the setting comes back after a reboot",
		},
		Apply: func(ctx context.Context) error {
			if err := os.MkdirAll(filepath.Dir(e.launchAgentPath()), 0o755); err != nil {
				return err
			}
			if err := writeFileAtomic(e.launchAgentPath(), []byte(launchAgentPlist(want)), 0o644); err != nil {
				return err
			}
			_, err := e.Runner.Run(ctx, system.Cmd{Name: "launchctl", Args: []string{"setenv", envContext, fmt.Sprint(want)}})
			return err
		},
	}
}

func windowsEnvStep(e *Env, want int) Step {
	c := psSetUserEnv(envContext, fmt.Sprint(want))
	return Step{
		Title:   fmt.Sprintf("Set context length to %d (user environment variable)", want),
		Details: []string{c.String()},
		Apply: func(ctx context.Context) error {
			if _, err := e.Runner.Run(ctx, c); err != nil {
				return err
			}
			return e.saveRecord(func(r *record) { r.WindowsEnvSet = true })
		},
	}
}

func serverJSONStep(e *Env) Step {
	return Step{
		Title:   "Turn off Ollama cloud",
		Details: []string{`Add "disable_ollama_cloud": true to ` + e.serverJSONPath() + " (other settings are kept, and a backup is saved first)"},
		Apply: func(context.Context) error {
			if err := setServerJSONCloud(e.serverJSONPath(), true); err != nil {
				return err
			}
			return e.saveRecord(func(r *record) { r.ServerJSONKeyAdded = true })
		},
	}
}
