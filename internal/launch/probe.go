package launch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const probeReply = "COPILOT_LOCAL_OK"

// Probe tests a fixed prompt without loading the user's workspace, credentials, hooks or MCP configuration.
func Probe(ctx context.Context, path string, settings Settings) (err error) {
	dir, err := os.MkdirTemp("", "copilot-local-verify-")
	if err != nil {
		return fmt.Errorf("create temporary workspace: %w", err)
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(dir))
	}()
	cmd, err := probeCommand(ctx, path, settings, dir, os.Environ())
	if err != nil {
		return err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// Don't echo arbitrary CLI output: it may include credentials or unrelated diagnostic data.
		return fmt.Errorf("offline Copilot test failed: %w; run `copilot-local -- -p \"Reply with OK\"` to diagnose", err)
	}
	if strings.TrimSpace(stdout.String()) != probeReply {
		return errors.New("Copilot didn't return the expected test reply; re-test the model with `copilot-local model --check`")
	}
	return nil
}

func probeCommand(ctx context.Context, path string, settings Settings, dir string, environ []string) (*exec.Cmd, error) {
	home := filepath.Join(dir, "home")
	work := filepath.Join(dir, "work")
	for _, p := range []string{home, work} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, fmt.Errorf("create isolated test directory: %w", err)
		}
	}
	// Keep only platform essentials. Inherited provider commands, tokens, Node options,
	// telemetry exporters and Copilot settings must not influence this test.
	var env []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "LANG", "LC_ALL":
			env = append(env, kv)
		}
	}
	env = append(env,
		"HOME="+home, "USERPROFILE="+home,
		"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"COPILOT_HOME="+filepath.Join(home, ".copilot"),
		"COPILOT_AUTO_UPDATE=false",
		"TMPDIR="+dir, "TMP="+dir, "TEMP="+dir,
	)
	// An empty --available-tools value means "use defaults", not "no tools".
	cmd := exec.CommandContext(ctx, path,
		"--no-auto-update", "--disable-client-telemetry", "--disable-builtin-mcps",
		"--no-custom-instructions", "--available-tools=none", "--no-ask-user",
		"--silent", "--prompt", "Reply with exactly "+probeReply+". Do not use tools.",
	)
	cmd.Dir = work
	cmd.Env = settings.Environ(env)
	cmd.WaitDelay = 3 * time.Second
	return cmd, nil
}
