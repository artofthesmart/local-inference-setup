package system

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestIsWSLVersion(t *testing.T) {
	if !IsWSLVersion("Linux version 6.6.36.6-microsoft-standard-WSL2") {
		t.Error("WSL2 not detected")
	}
	if IsWSLVersion("Linux version 6.8.0-45-generic (buildd@lcy02-amd64-075)") {
		t.Error("plain Linux detected as WSL")
	}
}

func TestSudo(t *testing.T) {
	c := Cmd{Name: "tee", Args: []string{"/etc/x"}, Stdin: "data"}
	got := Sudo(Info{OS: "linux"}, c)
	if got.String() != "sudo tee /etc/x" || got.Stdin != "data" {
		t.Errorf("got %q stdin %q", got, got.Stdin)
	}
	if Sudo(Info{OS: "linux", Root: true}, c).Name != "tee" {
		t.Error("root should not use sudo")
	}
	if Sudo(Info{OS: "windows"}, c).Name != "tee" {
		t.Error("windows has no sudo")
	}
}

func TestCmdStringQuotes(t *testing.T) {
	c := Cmd{Name: "osascript", Args: []string{"-e", `quit app "Ollama"`}}
	if got := c.String(); got != `osascript -e "quit app \"Ollama\""` {
		t.Errorf("got %s", got)
	}
}

func TestFakeRunnerLongestPrefix(t *testing.T) {
	f := &FakeRunner{
		Responses: map[string]string{"systemctl": "short", "systemctl show ollama": "long"},
		Failures:  map[string]string{"systemctl cat": "no unit"},
	}
	ctx := context.Background()
	if out, _ := f.Run(ctx, Cmd{Name: "systemctl", Args: []string{"show", "ollama", "-p", "Environment"}}); out != "long" {
		t.Errorf("got %q", out)
	}
	if _, err := f.Run(ctx, Cmd{Name: "systemctl", Args: []string{"cat", "ollama"}}); err == nil {
		t.Error("want failure")
	}
	if len(f.Lines()) != 2 {
		t.Error("calls not recorded")
	}
}

func TestExecRunnerReportsOutputOnError(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), Cmd{Name: "definitely-not-a-real-command-xyz"})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestExecRunnerDoesNotHangOnBackgroundChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	start := time.Now()
	if _, err := (ExecRunner{}).Run(t.Context(), Cmd{Name: "sh", Args: []string{"-c", "sleep 30 &"}}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 15*time.Second {
		t.Fatalf("Run waited %v for a background child", d)
	}
}
