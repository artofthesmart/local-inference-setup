package launch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	profileStart = "# BEGIN copilot-local Copilot CLI settings"
	profileEnd   = "# END copilot-local Copilot CLI settings"
)

// UpdateShellProfiles adds or replaces the settings block in both supported shell profiles.
func UpdateShellProfiles(home string, s Settings) error {
	var errs []error
	for _, name := range []string{".bashrc", ".zshrc"} {
		path := filepath.Join(home, name)
		if err := updateShellProfile(path, s); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func updateShellProfile(path string, s Settings) error {
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	original := string(content)
	newline := "\n"
	if strings.Contains(original, "\r\n") {
		newline = "\r\n"
	}
	block := profileBlock(s, newline)
	updated, err := replaceProfileBlock(original, block, newline)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return os.WriteFile(path, []byte(updated), mode)
}

func profileBlock(s Settings, newline string) string {
	var b strings.Builder
	b.WriteString(profileStart + newline)
	for _, variable := range s.Vars() {
		name, value, _ := strings.Cut(variable, "=")
		fmt.Fprintf(&b, "export %s=%s%s", name, shellQuote(value), newline)
	}
	b.WriteString(profileEnd + newline)
	return b.String()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func replaceProfileBlock(content, block, newline string) (string, error) {
	lines := strings.SplitAfter(content, "\n")
	start, end := -1, -1
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		switch line {
		case profileStart:
			if start >= 0 {
				return "", errors.New("profile contains more than one copilot-local settings block")
			}
			start = i
		case profileEnd:
			if end >= 0 {
				return "", errors.New("profile contains more than one copilot-local settings block")
			}
			end = i
		}
	}
	if start >= 0 && (end < 0 || end < start) || end >= 0 && start < 0 {
		return "", errors.New("profile contains an incomplete copilot-local settings block")
	}
	if start >= 0 {
		return strings.Join(lines[:start], "") + block + strings.Join(lines[end+1:], ""), nil
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += newline
	}
	return content + block, nil
}
