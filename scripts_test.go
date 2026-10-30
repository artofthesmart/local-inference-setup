package setup_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func checkQuery(t *testing.T, output string, facts ...string) {
	t.Helper()
	for _, want := range append([]string{
		"1. SPEED:", "2. BALANCED:", "3. INTELLIGENCE:",
		"remain usable for interactive local agent work",
		"65536 context tokens", "weights plus cache", "at least 128 declared tools",
		"Say NONE for any tier", "do not invent hardware benchmarks",
		"ollama pull", "current official Ollama library/tag pages",
	}, facts...) {
		if !strings.Contains(output, want) {
			t.Errorf("prompt missing %q:\n%s", want, output)
		}
	}
}

func TestShellQuery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script is for macOS and Linux")
	}
	for _, tt := range []struct {
		name, mocks, nvidia string
		facts               []string
	}{
		{
			name: "linux-nvidia",
			mocks: `
uname() { case "$1" in -s) echo Linux;; -m) echo x86_64;; esac; }
lspci() { echo '01:00.0 VGA compatible controller: NVIDIA Test GPU'; }
df() { printf 'Filesystem 1024-blocks Used Available Capacity Mounted\nfixture 209715200 0 104857600 50%% /\n'; }
`,
			nvidia: "echo 'NVIDIA Test GPU, 24576'",
			facts:  []string{"OS: Linux; architecture: x86_64", "NVIDIA Test GPU, 24576", "Free disk: 100.0 GiB"},
		},
		{
			name: "apple-unified-memory",
			mocks: `
uname() { case "$1" in -s) echo Darwin;; -m) echo arm64;; esac; }
sysctl() { case "$2" in machdep.cpu.brand_string) echo 'Apple Test CPU';; hw.ncpu) echo 12;; hw.memsize) echo 34359738368;; esac; }
system_profiler() { printf '    Chipset Model: Apple Test GPU\n    Metal Support: Metal 3\n    Serial Number: PRIVATE-SERIAL\n'; }
df() { return 1; }
`,
			nvidia: "exit 1",
			facts:  []string{"CPU: Apple Test CPU", "Logical CPU threads: 12", "Physical RAM: 32.0 GiB", "Chipset Model: Apple Test GPU", "ONE RAM pool", "Free disk: unknown"},
		},
		{
			name: "unavailable-probes",
			mocks: `
uname() { case "$1" in -s) echo Darwin;; -m) echo x86_64;; esac; }
sysctl() { return 1; }
system_profiler() { return 1; }
df() { return 1; }
`,
			nvidia: "echo 'partial output'; exit 1",
			facts:  []string{"CPU: unknown", "Logical CPU threads: unknown", "Physical RAM: unknown", "Intel Mac:", "Free disk: unknown", "NVIDIA GPU name, dedicated VRAM in MiB (may describe the same GPUs listed above):\nunknown"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			bin := t.TempDir()
			if err := os.WriteFile(filepath.Join(bin, "nvidia-smi"), []byte("#!/bin/sh\n"+tt.nvidia+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := exec.Command("sh", "-c", tt.mocks+"\n. ./gemini-query.sh").CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v\n%s", err, out)
			}
			checkQuery(t, string(out), tt.facts...)
			if strings.Contains(string(out), "PRIVATE-SERIAL") {
				t.Fatal("system profiler serial number leaked into prompt")
			}
		})
	}
	t.Run("unsupported-platform", func(t *testing.T) {
		out, err := exec.Command("sh", "-c", "uname() { echo Unsupported; }; . ./gemini-query.sh").CombinedOutput()
		if err == nil || !strings.Contains(string(out), "use gemini-query.ps1") {
			t.Fatalf("expected actionable unsupported-platform error: %v\n%s", err, out)
		}
	})
}

func TestPowerShellQuery(t *testing.T) {
	ps, err := exec.LookPath("pwsh")
	if err != nil {
		ps, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("PowerShell not installed; Windows CI runs these tests")
	}
	for _, tt := range []struct {
		name, mocks string
		facts       []string
	}{
		{
			name: "windows-hardware",
			mocks: `
function Get-CimInstance {
  param($ClassName)
  switch ($ClassName) {
    'Win32_Processor' { [pscustomobject]@{Name='Test CPU'; NumberOfLogicalProcessors=24} }
    'Win32_PhysicalMemory' { [pscustomobject]@{Capacity=32GB}; [pscustomobject]@{Capacity=16GB} }
    'Win32_ComputerSystem' { [pscustomobject]@{TotalPhysicalMemory=45.5GB} }
    'Win32_VideoController' { [pscustomobject]@{Name='Test NVIDIA GPU'; AdapterRAM=4GB}; [pscustomobject]@{Name='Test integrated GPU'} }
    default { throw 'Unexpected probe' }
  }
}
function nvidia-smi { $global:LASTEXITCODE=0; 'Test NVIDIA GPU, 24576' }
`,
			facts: []string{"CPU: Test CPU", "Logical CPU threads: 24", "Installed physical RAM: 48.0 GiB", "Windows-visible physical RAM: 45.5 GiB", "Test NVIDIA GPU, 24576", "Test integrated GPU"},
		},
		{
			name: "unavailable-probes",
			mocks: `
function Get-CimInstance { throw 'Unavailable' }
function nvidia-smi { $global:LASTEXITCODE=1 }
function Get-Item { throw 'Unavailable' }
`,
			facts: []string{"CPU: unknown", "Installed physical RAM: unknown", "Windows-visible physical RAM: unknown", "Free disk: unknown"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			command := tt.mocks + `
$env:PROCESSOR_ARCHITECTURE='AMD64'
$env:PROCESSOR_ARCHITEW6432=''
$before=$ErrorActionPreference
. ./gemini-query.ps1
if ($ErrorActionPreference -ne $before) { throw 'Script changed caller error preference' }
`
			out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", command).CombinedOutput()
			if err != nil {
				t.Fatalf("script failed: %v\n%s", err, out)
			}
			checkQuery(t, string(out), append(tt.facts, "OS: Windows; architecture: AMD64")...)
		})
	}
}
