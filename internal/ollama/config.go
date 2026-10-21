package ollama

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

const (
	profileBegin = "# >>> copilot-local ollama >>>"
	profileEnd   = "# <<< copilot-local ollama <<<"
)

// dropInContent is the systemd drop-in that sets both variables for the Ollama service.
func dropInContent(ctx int) string {
	return fmt.Sprintf(`# Managed by copilot-local. Remove with: copilot-local ollama --undo
[Service]
Environment="%s=%d"
Environment="%s=1"
`, envContext, ctx, envNoCloud)
}

var envAssign = regexp.MustCompile(`\b([A-Z][A-Z0-9_]*)=("[^"]*"|\S+)`)

// parseEnvList reads KEY=VALUE pairs from systemd output such as `systemctl show -p Environment --value` or a drop-in file.
func parseEnvList(s string) map[string]string {
	m := map[string]string{}
	for _, match := range envAssign.FindAllStringSubmatch(s, -1) {
		m[match[1]] = strings.Trim(match[2], `"`)
	}
	return m
}

func truthy(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

func atoi(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

// profileBlock is appended to ~/.profile on Linux without systemd, where the user runs `ollama serve` themselves.
func profileBlock(ctx int) string {
	return fmt.Sprintf("%s\nexport %s=%d\n%s\n", profileBegin, envContext, ctx, profileEnd)
}

// readProfileContext returns the context length in our ~/.profile block, or 0.
func readProfileContext(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	block, ok := extractBlock(string(data))
	if !ok {
		return 0
	}
	return atoi(parseEnvList(block)[envContext])
}

func extractBlock(s string) (string, bool) {
	i := strings.Index(s, profileBegin)
	j := strings.Index(s, profileEnd)
	if i < 0 || j < i {
		return "", false
	}
	return s[i : j+len(profileEnd)], true
}

// setProfileBlock writes our block into ~/.profile, replacing any old one. ctx 0 removes it.
func setProfileBlock(path string, ctx int) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s := string(data)
	if old, ok := extractBlock(s); ok {
		s = strings.Replace(s, old+"\n", "", 1)
		s = strings.Replace(s, old, "", 1)
	} else if ctx == 0 {
		return nil
	}
	if ctx > 0 {
		if s != "" && !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += profileBlock(ctx)
	}
	if data != nil {
		if err := backup(path); err != nil {
			return err
		}
	}
	return writeFileAtomic(path, []byte(s), 0o644)
}

// launchAgentPlist re-applies the variable at every login, because `launchctl setenv` alone is lost on reboot.
func launchAgentPlist(ctx int) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- Managed by copilot-local. Remove with: copilot-local ollama --undo -->
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/launchctl</string>
    <string>setenv</string>
    <string>%s</string>
    <string>%d</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>
`, LaunchAgentLabel, envContext, ctx)
}

var plistCtx = regexp.MustCompile(`<string>` + envContext + `</string>\s*<string>(\d+)</string>`)

func readLaunchAgentContext(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if m := plistCtx.FindSubmatch(data); m != nil {
		return atoi(string(m[1]))
	}
	return 0
}

// PowerShell commands for the Windows user environment. SetEnvironmentVariable with 'User' writes HKCU\Environment and notifies running apps, like setx but without its 1024-character limit.
func psGetUserEnv(name string) system.Cmd {
	return powershell(fmt.Sprintf("[Environment]::GetEnvironmentVariable('%s','User')", name))
}

func psSetUserEnv(name, value string) system.Cmd {
	v := "$null"
	if value != "" {
		v = "'" + value + "'"
	}
	return powershell(fmt.Sprintf("[Environment]::SetEnvironmentVariable('%s',%s,'User')", name, v))
}

func powershell(script string) system.Cmd {
	return system.Cmd{Name: "powershell", Args: []string{"-NoProfile", "-NonInteractive", "-Command", script}}
}
