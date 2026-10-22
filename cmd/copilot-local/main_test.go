package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hermetic points the tool at an empty home folder and a port where nothing listens.
func hermetic(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(k, home)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// PowerShell writes its own cache under LOCALAPPDATA, so keep it outside the folder the tests inspect.
	appData := t.TempDir()
	t.Setenv("APPDATA", filepath.Join(appData, "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(appData, "Local"))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	t.Setenv("COPILOT_LOCAL_OLLAMA_URL", "http://"+addr)
	return home
}

func runCLI(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(t.Context(), args, &out)
	return code, out.String()
}

func TestVersion(t *testing.T) {
	if code, out := runCLI(t, "version"); code != 0 || !strings.Contains(out, "copilot-local") {
		t.Fatalf("%d %q", code, out)
	}
}

func TestHelp(t *testing.T) {
	cases := map[string][][]string{
		"Start Copilot with a specific model": {{"help"}, {"--help"}, {"-h"}, {"--help", "extra"}},
		"copilot-local model adds":            {{"help", "model"}, {"model", "--help"}, {"model", "-h"}},
		"copilot-local configure saves":       {{"help", "configure"}, {"configure", "--help"}},
		"copilot-local vscode installs":       {{"help", "vscode"}, {"vscode", "--help"}},
		"copilot-local verify checks":         {{"help", "verify"}, {"verify", "--help"}},
		"copilot-local ollama installs":       {{"help", "ollama"}, {"ollama", "--help"}},
	}
	for want, argSets := range cases {
		for _, args := range argSets {
			if code, out := runCLI(t, args...); code != 0 || !strings.Contains(out, want) || !strings.Contains(out, "Usage:") {
				t.Errorf("%q: %d %q", args, code, out)
			}
		}
	}
	if code, out := runCLI(t, "help", "bogus"); code != 2 || !strings.Contains(out, `No help for "bogus"`) {
		t.Errorf("help bogus: %d %q", code, out)
	}
}

func TestHelpListsEveryFlag(t *testing.T) {
	fs, _ := newLaunchFlags(io.Discard)
	if missing := undocumentedFlags(fs, topUsage); len(missing) > 0 {
		t.Errorf("copilot-local help is missing %v", missing)
	}
	fs, _ = newModelFlags(io.Discard)
	if missing := undocumentedFlags(fs, helpFor("model", "x")); len(missing) > 0 {
		t.Errorf("model help is missing %v", missing)
	}
	fs, _ = newOllamaFlags(io.Discard)
	if missing := undocumentedFlags(fs, helpFor("ollama", "")); len(missing) > 0 {
		t.Errorf("ollama help is missing %v", missing)
	}
	if code, out := runCLI(t, "vscode", "--help"); code != 0 {
		t.Errorf("vscode help: %d %q", code, out)
	} else {
		for _, flag := range []string{"--profile", "--yes", "--dry-run"} {
			if !strings.Contains(out, flag) {
				t.Errorf("vscode help is missing %s", flag)
			}
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	if code, out := runCLI(t, "bogus"); code != 2 || !strings.Contains(out, `Unknown command "bogus"`) {
		t.Fatalf("%d %q", code, out)
	}
}

func TestBadFlags(t *testing.T) {
	if code, _ := runCLI(t, "ollama", "--context", "10"); code != 2 {
		t.Errorf("small context: %d", code)
	}
	if code, _ := runCLI(t, "ollama", "--nope"); code != 2 {
		t.Errorf("unknown flag: %d", code)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	home := hermetic(t)
	code, out := runCLI(t, "ollama", "--dry-run")
	if code != 0 || !strings.Contains(out, "Dry run: nothing was changed.") {
		t.Fatalf("%d\n%s", code, out)
	}
	if !strings.Contains(out, "Here's what will change:") {
		t.Errorf("no plan shown:\n%s", out)
	}
	// PowerShell (used to read settings on Windows) creates empty AppData folders in a new home, so check for files only.
	filepath.WalkDir(home, func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			t.Errorf("dry run created %s", p)
		}
		return nil
	})
}

func TestChangesNeedYesWithoutTerminal(t *testing.T) {
	hermetic(t)
	code, out := runCLI(t, "ollama")
	if code != 2 || !strings.Contains(out, "Run again with --yes") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestUndoWithNothingToUndo(t *testing.T) {
	hermetic(t)
	if code, out := runCLI(t, "ollama", "--undo"); code != 0 || !strings.Contains(out, "Nothing to undo") {
		t.Fatalf("%d\n%s", code, out)
	}
}

func TestCheckReportsServerDown(t *testing.T) {
	hermetic(t)
	code, out := runCLI(t, "ollama", "--check")
	if code != 1 || !strings.Contains(out, "Ollama running") || !strings.Contains(out, "Fix:") {
		t.Fatalf("%d\n%s", code, out)
	}
}
