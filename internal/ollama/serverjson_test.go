package ollama

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerJSONCreatesFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".ollama", "server.json")
	if err := setServerJSONCloud(p, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := cloudDisabledInServerJSON(p); !got {
		t.Fatalf("cloud not disabled: %s", readFile(t, p))
	}
	if _, err := os.Stat(p + ".copilot-local.bak"); err == nil {
		t.Error("no backup expected for a new file")
	}
}

func TestServerJSONKeepsOtherKeysAndBacksUp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.json")
	orig := `{"some_setting": {"nested": [1, 2]}, "flag": false}`
	os.WriteFile(p, []byte(orig), 0o644)
	if err := setServerJSONCloud(p, true); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, p)
	for _, want := range []string{`"some_setting"`, `"nested"`, `"flag": false`, `"disable_ollama_cloud": true`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if readFile(t, p+".copilot-local.bak") != orig {
		t.Error("backup doesn't match the original")
	}

	if err := setServerJSONCloud(p, false); err != nil {
		t.Fatal(err)
	}
	got = readFile(t, p)
	if strings.Contains(got, cloudKey) || !strings.Contains(got, `"some_setting"`) {
		t.Errorf("undo should remove only our key: %s", got)
	}
}

func TestServerJSONEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.json")
	os.WriteFile(p, []byte("  \n"), 0o644)
	if err := setServerJSONCloud(p, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := cloudDisabledInServerJSON(p); !got {
		t.Fatal("cloud not disabled")
	}
}

func TestServerJSONInvalidIsLeftAlone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.json")
	bad := `{"oops": `
	os.WriteFile(p, []byte(bad), 0o644)
	err := setServerJSONCloud(p, true)
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("want a clear error, got %v", err)
	}
	if readFile(t, p) != bad {
		t.Error("invalid file was modified")
	}
}

func TestServerJSONRemoveWithoutFileCreatesNothing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.json")
	if err := setServerJSONCloud(p, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("file should not be created when removing")
	}
}

func TestCloudDisabledFalseValue(t *testing.T) {
	p := filepath.Join(t.TempDir(), "server.json")
	os.WriteFile(p, []byte(`{"disable_ollama_cloud": false}`), 0o644)
	if got, _ := cloudDisabledInServerJSON(p); got {
		t.Error("false should read as not disabled")
	}
}
