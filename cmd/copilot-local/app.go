package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/artofthesmart/local-inference-setup/internal/hardware"
	"github.com/artofthesmart/local-inference-setup/internal/launch"
	"github.com/artofthesmart/local-inference-setup/internal/models"
	"github.com/artofthesmart/local-inference-setup/internal/ollama"
)

// These reach outside the program, so tests replace them.
var (
	findCopilot  = func() (string, error) { return launch.Find(exec.LookPath) }
	startCopilot = launch.Run
	newEnv       = ollama.NewEnv
)

// app holds what the model and launch commands share.
type app struct {
	out io.Writer
	e   *ollama.Env
	api models.Ollama
	reg models.Registry
}

func newApp(out io.Writer) (*app, error) {
	e, err := newEnv(out)
	if err != nil {
		return nil, err
	}
	reg := os.Getenv("COPILOT_LOCAL_REGISTRY_URL")
	if reg == "" {
		reg = models.DefaultRegistry
	}
	return &app{
		out: out,
		e:   e,
		api: models.Ollama{BaseURL: e.BaseURL, HTTP: &http.Client{}},
		reg: models.Registry{BaseURL: reg, HTTP: &http.Client{Timeout: 30 * time.Second}},
	}, nil
}

// shortlistPath is where a user's own model list goes.
func shortlistPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join("~", ".config", "copilot-local", "shortlist.txt")
	}
	return filepath.Join(dir, "copilot-local", "shortlist.txt")
}

// modelDir is where Ollama saves models, for the free-space check.
func (a *app) modelDir() string {
	if d := os.Getenv("OLLAMA_MODELS"); d != "" {
		return d
	}
	if a.e.Info.OS == "linux" && a.e.Info.Systemd && a.e.Exists("/usr/share/ollama/.ollama") {
		return "/usr/share/ollama/.ollama/models"
	}
	return filepath.Join(a.e.Home, ".ollama", "models")
}

func (a *app) hardware(ctx context.Context) hardware.Hardware {
	if a.e.Info.WSL {
		if s, err := ollama.Detect(ctx, a.e); err == nil && s.WindowsSideServer {
			// Ollama runs on Windows, so the Windows hardware is what matters.
			info := a.e.Info
			info.OS, info.WSL = "windows", false
			h := hardware.Detect(ctx, hardware.Deps{Info: info, Runner: hardware.FromWSL(a.e.Runner), ModelDir: `C:\`})
			if h.RAM > 0 {
				h.Warnings = append(h.Warnings, "Read from Windows, where Ollama runs. Free space is for drive C:.")
				return h
			}
		}
	}
	return hardware.Detect(ctx, hardware.Deps{Info: a.e.Info, Runner: a.e.Runner, ModelDir: a.modelDir()})
}

// contextTarget is the context size models must handle: the flag, else Ollama's setting, else the default.
func (a *app) contextTarget(ctx context.Context, flagVal int) int {
	if flagVal > 0 {
		return flagVal
	}
	if s, err := ollama.Detect(ctx, a.e); err == nil && s.ConfiguredContext > 0 {
		return s.ConfiguredContext
	}
	return ollama.DefaultContextLength
}

// installed lists downloaded models, waiting only briefly for the server.
func (a *app) installed(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return a.api.Installed(ctx)
}

func (a *app) line(label, value string) {
	fmt.Fprintf(a.out, "  %-14s %s\n", label, value)
}

func (a *app) printHardware(h hardware.Hardware) {
	cpu := h.CPU
	if h.Cores > 0 {
		cpu += fmt.Sprintf(" (%d threads)", h.Cores)
	}
	a.line("Processor:", cpu)
	mem := hardware.Size(h.RAM)
	if h.RAM == 0 {
		mem = "unknown"
	}
	a.line("Memory:", mem)
	var gpus []string
	for _, g := range h.GPUs {
		s := g.Name
		if g.VRAM > 0 {
			s += " (" + hardware.Size(g.VRAM) + ")"
		}
		gpus = append(gpus, s)
	}
	if len(gpus) == 0 {
		gpus = []string{"none found"}
	}
	a.line("Graphics:", strings.Join(gpus, "; "))
	b := h.Budget()
	a.line("For models:", fmt.Sprintf("about %s of %s", hardware.Size(b.Bytes), b.Pool))
	if h.FreeDisk > 0 {
		a.line("Free disk:", hardware.Size(h.FreeDisk))
	}
	for _, w := range h.Warnings {
		fmt.Fprintln(a.out, "  "+amber.Render("Note:")+" "+w)
	}
}
