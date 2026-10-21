package ollama

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnvList(t *testing.T) {
	got := parseEnvList(`PATH=/usr/bin OLLAMA_CONTEXT_LENGTH=65536 OLLAMA_NO_CLOUD=1 OLLAMA_HOST="0.0.0.0:11434" CUDA_VISIBLE_DEVICES=0`)
	want := map[string]string{"PATH": "/usr/bin", envContext: "65536", envNoCloud: "1", envHost: "0.0.0.0:11434", "CUDA_VISIBLE_DEVICES": "0"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestDropInContentParsesBack(t *testing.T) {
	env := parseEnvList(dropInContent(131072))
	if env[envContext] != "131072" || !truthy(env[envNoCloud]) {
		t.Fatalf("drop-in doesn't round-trip: %v", env)
	}
}

func TestProfileBlockKeepsUserContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".profile")
	orig := "# my stuff\nexport EDITOR=vim" // no trailing newline on purpose
	os.WriteFile(p, []byte(orig), 0o644)

	if err := setProfileBlock(p, 65536); err != nil {
		t.Fatal(err)
	}
	if got := readProfileContext(p); got != 65536 {
		t.Fatalf("context = %d", got)
	}
	// Changing the value replaces the block instead of adding a second one.
	if err := setProfileBlock(p, 32768); err != nil {
		t.Fatal(err)
	}
	s := readFile(t, p)
	if strings.Count(s, profileBegin) != 1 || readProfileContext(p) != 32768 {
		t.Fatalf("block not replaced:\n%s", s)
	}
	if !strings.HasPrefix(s, orig+"\n") {
		t.Fatalf("user content changed:\n%s", s)
	}

	if err := setProfileBlock(p, 0); err != nil {
		t.Fatal(err)
	}
	if s := readFile(t, p); strings.TrimRight(s, "\n") != orig {
		t.Fatalf("undo left changes:\n%q", s)
	}
}

func TestProfileBlockMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".profile")
	if err := setProfileBlock(p, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("removing from a missing file should not create it")
	}
	if err := setProfileBlock(p, 4096); err != nil || readProfileContext(p) != 4096 {
		t.Fatalf("create failed: %v", err)
	}
}

func TestLaunchAgentPlistParsesBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "agent.plist")
	os.WriteFile(p, []byte(launchAgentPlist(65536)), 0o644)
	if got := readLaunchAgentContext(p); got != 65536 {
		t.Fatalf("got %d", got)
	}
	if !strings.Contains(launchAgentPlist(1), "<key>RunAtLoad</key>") {
		t.Error("plist must run at login")
	}
}

func TestPowerShellEnvCommands(t *testing.T) {
	if got := psSetUserEnv(envContext, "65536").Args[3]; got != "[Environment]::SetEnvironmentVariable('OLLAMA_CONTEXT_LENGTH','65536','User')" {
		t.Errorf("set: %s", got)
	}
	if got := psSetUserEnv(envContext, "").Args[3]; !strings.Contains(got, ",$null,'User'") {
		t.Errorf("unset: %s", got)
	}
}

func TestIsExposed(t *testing.T) {
	for host, want := range map[string]bool{"0.0.0.0": true, "0.0.0.0:11434": true, "http://0.0.0.0:8080": true, "[::]:11434": true, ":11434": true, "127.0.0.1": false, "": false, "localhost:11434": false} {
		if got := isExposed(host); got != want {
			t.Errorf("isExposed(%q) = %v", host, got)
		}
	}
}

func TestParseVersion(t *testing.T) {
	if got := parseVersion("Warning: could not connect\nollama version is 0.40.7"); got != "0.40.7" {
		t.Errorf("got %q", got)
	}
}
