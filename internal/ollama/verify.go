package ollama

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// Status is the result of one check.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
)

func (s Status) String() string {
	return [...]string{"PASS", "WARN", "FAIL"}[s]
}

// Check is one line of the final report.
type Check struct {
	Name   string
	Status Status
	Detail string
	Fix    string
}

// Report is the pass/fail list shown at the end.
type Report struct {
	Checks []Check
}

// OK is true when nothing failed. Warnings are allowed.
func (r Report) OK() bool {
	for _, c := range r.Checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

func (r *Report) add(name string, st Status, detail, fix string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: st, Detail: detail, Fix: fix})
}

// WaitForServer polls /api/version until the server answers or the timeout passes.
func WaitForServer(ctx context.Context, e *Env) bool {
	deadline := time.Now().Add(e.WaitTimeout)
	for {
		if _, ok := e.serverVersion(ctx); ok {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		e.sleep(time.Second)
	}
}

// Verify checks that Ollama is installed, running, and set up as wanted.
func Verify(ctx context.Context, e *Env, want int) Report {
	var r Report
	up := WaitForServer(ctx, e)
	s, err := Detect(ctx, e)
	if err != nil {
		r.add("Read settings", Fail, err.Error(), "")
		return r
	}

	if s.Installed() {
		r.add("Ollama installed", Pass, strings.TrimSpace(s.Version+" at "+s.BinaryPath), "")
	} else if s.WindowsSideServer {
		r.add("Ollama installed", Warn, "Not inside WSL, but your Windows install is reachable", "Run copilot-local from Windows PowerShell to configure it.")
	} else {
		r.add("Ollama installed", Fail, "Not found", "Run `copilot-local ollama` again, or install from https://ollama.com/download")
	}

	if !up {
		r.add("Ollama running", Fail, "No answer at "+e.BaseURL, startFix(e, s))
		return r
	}
	r.add("Ollama running", Pass, "Version "+s.ServerVersion+" at "+e.BaseURL, "")

	if _, err := e.get(ctx, "/v1/models", nil); err != nil {
		r.add("Copilot-compatible API (/v1)", Fail, err.Error(), "Update Ollama to a recent version from https://ollama.com/download")
	} else {
		r.add("Copilot-compatible API (/v1)", Pass, e.BaseURL+"/v1", "")
	}

	switch {
	case s.CloudKnown && s.CloudDisabled:
		r.add("Ollama cloud off", Pass, "The server reports cloud is disabled", "")
	case s.CloudKnown:
		r.add("Ollama cloud off", Fail, "The server reports cloud is still on", restartFix(e, s))
	case s.ConfiguredNoCloud:
		r.add("Ollama cloud off", Warn, "Turned off in settings, but this Ollama version can't confirm it", "Update Ollama to confirm.")
	default:
		r.add("Ollama cloud off", Fail, "Not turned off", "Run `copilot-local ollama` again.")
	}

	r.Checks = append(r.Checks, contextCheck(e, s, want))

	if s.ExposedHost != "" {
		r.add("Only reachable from this computer", Warn, "OLLAMA_HOST is "+s.ExposedHost+", so other machines on your network can use Ollama", "Remove OLLAMA_HOST unless you meant to share Ollama.")
	}
	return r
}

func contextCheck(e *Env, s *State, want int) Check {
	name := fmt.Sprintf("Context length %d", want)
	got, source := 0, ""
	if e.usesSystemd(s) {
		got, source = s.ConfiguredContext, "the Ollama service settings"
	} else if n, ok := logContext(e.serverLogPath()); ok {
		got, source = n, "Ollama's log"
	} else {
		return Check{Name: name, Status: Warn, Detail: "Couldn't read Ollama's log to confirm", Fix: "Load a model, then run `ollama ps`. The CONTEXT column shows the value in use."}
	}
	if got == want {
		return Check{Name: name, Status: Pass, Detail: "Confirmed in " + source}
	}
	fix := restartFix(e, s)
	if e.usesSystemd(s) && s.OurDropIn && s.DropInContext == want {
		fix = fmt.Sprintf("Another systemd override sets %s=%d and wins over ours. Run `systemctl cat ollama` to find it, then remove that line.", envContext, got)
	}
	detail := fmt.Sprintf("%s shows %d", source, got)
	if got == 0 {
		detail = source + " shows it isn't set, so Ollama uses its small default"
	}
	return Check{Name: name, Status: Fail, Detail: detail, Fix: fix}
}

var logCtx = regexp.MustCompile(envContext + `:(\d+)`)

// logContext reads the context length from the newest "server config" line Ollama logs at startup.
func logContext(path string) (int, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	// Only the tail matters; logs can be large.
	if st, err := f.Stat(); err == nil && st.Size() > 4<<20 {
		_, _ = f.Seek(-4<<20, io.SeekEnd)
	}
	found, val := false, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "server config") {
			continue
		}
		if m := logCtx.FindStringSubmatch(line); m != nil {
			found, val = true, atoi(m[1])
		}
	}
	return val, found
}

func startFix(e *Env, s *State) string {
	switch {
	case e.usesSystemd(s):
		return "Run `sudo systemctl start ollama`, then `journalctl -u ollama -n 50` to see why it stopped."
	case e.Info.OS == "darwin" && s.MacApp:
		return "Run `open -a Ollama`."
	case e.Info.OS == "windows":
		return "Start Ollama from the Start menu."
	default:
		return "Run `ollama serve` in a new terminal."
	}
}

func restartFix(e *Env, s *State) string {
	switch {
	case e.usesSystemd(s):
		return "Run `sudo systemctl restart ollama`."
	case e.Info.OS == "darwin" && s.MacApp:
		return "Quit Ollama from the menu bar, then run `open -a Ollama`."
	case e.Info.OS == "windows":
		return "Quit Ollama from the system tray, then start it again from the Start menu."
	default:
		return "Stop `ollama serve`, open a new terminal, and run `ollama serve` again."
	}
}
