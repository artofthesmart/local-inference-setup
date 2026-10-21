// Package system detects the host platform and runs external commands.
package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Info describes the machine the tool is running on.
type Info struct {
	OS      string // runtime.GOOS value: "linux", "darwin", "windows"
	Arch    string
	WSL     bool
	Systemd bool // Linux only: systemd is PID 1
	Root    bool // Unix only: running as root, so sudo is not needed
}

// DetectInfo inspects the current machine.
func DetectInfo() Info {
	info := Info{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if info.OS == "linux" {
		if b, err := os.ReadFile("/proc/version"); err == nil {
			info.WSL = IsWSLVersion(string(b))
		}
		// sd_booted() checks this same directory.
		if st, err := os.Stat("/run/systemd/system"); err == nil && st.IsDir() {
			info.Systemd = true
		}
	}
	if info.OS != "windows" {
		info.Root = os.Geteuid() == 0
	}
	return info
}

// IsWSLVersion reports whether a /proc/version string belongs to WSL.
func IsWSLVersion(procVersion string) bool {
	return strings.Contains(strings.ToLower(procVersion), "microsoft")
}

// Cmd is an external command to run.
type Cmd struct {
	Name  string
	Args  []string
	Stdin string
	// Interactive connects the command to the user's terminal, for installers that prompt for a sudo password.
	Interactive bool
}

func (c Cmd) String() string {
	parts := append([]string{c.Name}, c.Args...)
	for i, p := range parts {
		if strings.ContainsAny(p, " \t\"'") {
			parts[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(parts, " ")
}

// Runner runs external commands. Tests and --dry-run swap in a fake.
type Runner interface {
	Run(ctx context.Context, c Cmd) (string, error)
}

const pipeWaitDelay = 3 * time.Second

// ExecRunner runs commands for real.
type ExecRunner struct {
	Out io.Writer // where interactive output goes; defaults to os.Stdout
}

func (r ExecRunner) Run(ctx context.Context, c Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	if c.Stdin != "" {
		cmd.Stdin = strings.NewReader(c.Stdin)
	}
	if c.Interactive {
		out := r.Out
		if out == nil {
			out = os.Stdout
		}
		if c.Stdin == "" {
			cmd.Stdin = os.Stdin
		}
		cmd.Stdout, cmd.Stderr = out, out
		return "", cmd.Run()
	}
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	// A started server can inherit our output pipe and keep it open forever, so stop waiting soon after the command itself exits.
	cmd.WaitDelay = pipeWaitDelay
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	out := strings.TrimSpace(buf.String())
	if err != nil {
		return out, fmt.Errorf("%s: %w: %s", c, err, out)
	}
	return out, nil
}

// Sudo prefixes a command with sudo unless the tool already runs as root.
func Sudo(info Info, c Cmd) Cmd {
	if info.Root || info.OS == "windows" {
		return c
	}
	return Cmd{Name: "sudo", Args: append([]string{c.Name}, c.Args...), Stdin: c.Stdin, Interactive: c.Interactive}
}
