package ollama

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

// restartStep restarts Ollama so new settings take effect. want is the context length to pass when the tool starts `ollama serve` itself; 0 passes none.
func restartStep(e *Env, s *State, want int) Step {
	title := "Restart Ollama"
	if !s.ServerUp {
		title = "Start Ollama"
	}
	switch {
	case e.usesSystemd(s):
		c := system.Sudo(e.Info, system.Cmd{Name: "systemctl", Args: []string{"restart", "ollama"}})
		return Step{Title: title, Details: []string{c.String()}, Apply: func(ctx context.Context) error {
			_, err := e.Runner.Run(ctx, c)
			return err
		}}
	case e.Info.OS == "darwin" && s.MacApp:
		return Step{
			Title:   title,
			Details: []string{`osascript -e 'quit app "Ollama"'`, "open " + macAppPath},
			Apply: func(ctx context.Context) error {
				_, _ = e.Runner.Run(ctx, system.Cmd{Name: "osascript", Args: []string{"-e", `quit app "Ollama"`}})
				e.sleep(3 * time.Second)
				// Opening by path works even before macOS has registered a newly installed app.
				_, err := e.Runner.Run(ctx, system.Cmd{Name: "open", Args: []string{macAppPath}})
				if err == nil && waitFor(ctx, e, appStartWait) {
					return nil
				}
				// No desktop session (for example over SSH), so run the server directly.
				fmt.Fprintln(e.Out, "  The Ollama app didn't start the server, so starting `ollama serve` directly.")
				_, err = e.Runner.Run(ctx, serveInBackground(e, s, want))
				return err
			},
		}
	case e.Info.OS == "windows":
		c := powershell(windowsRestartScript(e, s))
		return Step{
			Title:   title,
			Details: []string{"Stop Ollama and start it again from " + windowsDir(e, s)},
			Apply: func(ctx context.Context) error {
				if _, err := e.Runner.Run(ctx, c); err != nil {
					return err
				}
				if waitFor(ctx, e, appStartWait) {
					return nil
				}
				// No desktop session (for example over SSH), so run the server directly.
				fmt.Fprintln(e.Out, "  The Ollama app didn't start the server, so starting `ollama serve` directly.")
				_, err := e.Runner.Run(ctx, powershell(windowsServeScript(e, s)))
				return err
			},
		}
	default:
		if s.ServerUp {
			return Step{
				Title:   title,
				Details: []string{"Ollama isn't managed by a service here, so you'll need to stop `ollama serve` and start it again in a new terminal."},
				Apply:   func(context.Context) error { return ErrManualRestart },
			}
		}
		c := serveInBackground(e, s, want)
		return Step{Title: title, Details: []string{c.Args[len(c.Args)-1]}, Apply: func(ctx context.Context) error {
			_, err := e.Runner.Run(ctx, c)
			return err
		}}
	}
}

// appStartWait is how long to give a desktop app to start its server before running `ollama serve` directly.
const appStartWait = 20 * time.Second

func waitFor(ctx context.Context, e *Env, d time.Duration) bool {
	saved := e.WaitTimeout
	defer func() { e.WaitTimeout = saved }()
	if d < e.WaitTimeout {
		e.WaitTimeout = d
	}
	return WaitForServer(ctx, e)
}

// serveInBackground starts `ollama serve` detached, logging to ~/.ollama/logs/server.log like the desktop apps do.
func serveInBackground(e *Env, s *State, want int) system.Cmd {
	log := e.serverLogPath()
	env := ""
	if want > 0 {
		env = fmt.Sprintf("%s=%d ", envContext, want)
	}
	script := fmt.Sprintf("mkdir -p %s && %snohup %s serve >> %s 2>&1 &", shQuote(filepath.Dir(log)), env, shQuote(s.BinaryPath), shQuote(log))
	return system.Cmd{Name: "sh", Args: []string{"-c", script}}
}

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func windowsDir(e *Env, s *State) string {
	if s.BinaryPath != "" {
		return filepath.Dir(s.BinaryPath)
	}
	return e.windowsInstallDir()
}

// windowsRestartScript stops Ollama, reloads the user variable into this session (child processes inherit it), and starts the tray app, or `ollama serve` if there is no tray app.
func windowsRestartScript(e *Env, s *State) string {
	dir := strings.ReplaceAll(windowsDir(e, s), "'", "''")
	return strings.Join([]string{
		"Get-Process -Name 'ollama app','ollama' -ErrorAction SilentlyContinue | Stop-Process -Force",
		"Start-Sleep -Seconds 2",
		fmt.Sprintf("$env:%s = [Environment]::GetEnvironmentVariable('%s','User')", envContext, envContext),
		fmt.Sprintf("$app = Join-Path '%s' 'ollama app.exe'", dir),
		fmt.Sprintf("if (Test-Path $app) { Start-Process -FilePath $app } else { Start-Process -FilePath (Join-Path '%s' 'ollama.exe') -ArgumentList 'serve' -WindowStyle Hidden }", dir),
	}, "; ")
}

// windowsServeScript starts `ollama.exe serve` hidden with the user's context setting, logging where the app would so verification can read it.
func windowsServeScript(e *Env, s *State) string {
	dir := strings.ReplaceAll(windowsDir(e, s), "'", "''")
	log := strings.ReplaceAll(e.serverLogPath(), "'", "''")
	return strings.Join([]string{
		fmt.Sprintf("$env:%s = [Environment]::GetEnvironmentVariable('%s','User')", envContext, envContext),
		fmt.Sprintf("$exe = Join-Path '%s' 'ollama.exe'", dir),
		fmt.Sprintf("New-Item -ItemType Directory -Force -Path (Split-Path '%s') | Out-Null", log),
		fmt.Sprintf("try { Start-Process -FilePath $exe -ArgumentList 'serve' -WindowStyle Hidden -RedirectStandardError '%s' -RedirectStandardOutput '%s.out' } catch { Start-Process -FilePath $exe -ArgumentList 'serve' -WindowStyle Hidden }", log, log),
	}, "; ")
}

func installStep(e *Env) Step {
	url := e.InstallScriptURL(e.Info.OS)
	return Step{
		Title:   "Install Ollama",
		Install: true,
		Details: []string{"Download Ollama's official installer from " + url + " and run it" + sudoNote(e)},
		Apply: func(ctx context.Context) error {
			path, err := download(ctx, e, url)
			if err != nil {
				return err
			}
			defer os.Remove(path)
			c := system.Cmd{Name: "sh", Args: []string{path}, Interactive: true}
			if e.Info.OS == "windows" {
				c = system.Cmd{Name: "powershell", Args: []string{"-NoProfile", "-ExecutionPolicy", "Bypass", "-File", path}, Interactive: true}
			}
			if _, err = e.Runner.Run(ctx, c); err != nil && e.findBinary() != "" {
				// Some installers fail on a last cosmetic step (like opening the app) after Ollama is in place.
				fmt.Fprintln(e.Out, "  The installer reported a problem, but Ollama is installed, so continuing.")
				return nil
			}
			return err
		},
	}
}

func sudoNote(e *Env) string {
	if e.Info.OS == "linux" && !e.Info.Root {
		return " (it asks for your sudo password)"
	}
	return ""
}

// download saves the installer to a temp file first, so a dropped connection can't run half a script.
func download(ctx context.Context, e *Env, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Minute, Transport: e.HTTP.Transport}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", url, resp.Status)
	}
	ext := ".sh"
	if e.Info.OS == "windows" {
		ext = ".ps1"
	}
	f, err := os.CreateTemp("", "ollama-install-*"+ext)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}
