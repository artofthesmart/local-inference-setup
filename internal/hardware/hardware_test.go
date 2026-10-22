package hardware

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

const nvSMI = "nvidia-smi --query-gpu=name,memory.total --format=csv,noheader,nounits"

var winCmd = `powershell -NoProfile -NonInteractive -Command "$ErrorActionPreference`

func files(m map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if v, ok := m[p]; ok {
			return []byte(v), nil
		}
		return nil, os.ErrNotExist
	}
}

func statAll(string) (os.FileInfo, error) { return nil, nil }

const cpuinfo = "processor\t: 0\nmodel name\t: AMD Ryzen 9 7950X\nprocessor\t: 1\nmodel name\t: AMD Ryzen 9 7950X\n"
const meminfo = "MemTotal:       65536000 kB\nMemFree:  1 kB\n"
const dfOut = "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 1000000000 500 104857600 50% /\n"

func TestLinuxNvidia(t *testing.T) {
	r := &system.FakeRunner{
		Responses: map[string]string{nvSMI: "NVIDIA GeForce RTX 4090, 24564\n", "df -Pk": dfOut},
		Failures:  map[string]string{"lspci": "should not run"},
	}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "linux", Arch: "amd64"}, Runner: r, ReadFile: files(map[string]string{"/proc/cpuinfo": cpuinfo, "/proc/meminfo": meminfo}), Stat: statAll, ModelDir: "/home/u/.ollama/models"})
	if h.CPU != "AMD Ryzen 9 7950X" || h.Cores != 2 || h.RAM != 65536000*1024 {
		t.Fatalf("cpu/ram: %+v", h)
	}
	if len(h.GPUs) != 1 || h.GPUs[0].VRAM != 24564<<20 || h.GPUs[0].Vendor != "nvidia" {
		t.Fatalf("gpus: %+v", h.GPUs)
	}
	if h.FreeDisk != 100*GiB {
		t.Errorf("disk: %d", h.FreeDisk)
	}
	b := h.Budget()
	if !b.GPU || b.Bytes != (24564<<20)*9/10 || !strings.Contains(b.Pool, "RTX 4090") {
		t.Errorf("budget: %+v", b)
	}
	if len(h.Warnings) != 0 {
		t.Errorf("warnings: %v", h.Warnings)
	}
}

func TestLinuxAMDFromLspci(t *testing.T) {
	r := &system.FakeRunner{
		Responses: map[string]string{"lspci": "03:00.0 VGA compatible controller: Advanced Micro Devices, Inc. [AMD/ATI] Navi 31 [Radeon RX 7900 XTX]\n", "df -Pk": dfOut},
		Failures:  map[string]string{"nvidia-smi": "exec: \"nvidia-smi\": executable file not found in $PATH"},
	}
	fs := map[string]string{"/proc/cpuinfo": cpuinfo, "/proc/meminfo": meminfo, "/sys/class/drm/card1/device/mem_info_vram_total": "25753026560\n"}
	glob := func(string) ([]string, error) {
		return []string{"/sys/class/drm/card1/device/mem_info_vram_total"}, nil
	}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "linux"}, Runner: r, ReadFile: files(fs), Glob: glob, Stat: statAll, ModelDir: "/m"})
	if len(h.GPUs) != 1 || h.GPUs[0].Vendor != "amd" || h.GPUs[0].Integrated || h.GPUs[0].VRAM != 25753026560 {
		t.Fatalf("%+v", h.GPUs)
	}
	if !h.Budget().GPU {
		t.Error("expected GPU budget")
	}
	if len(h.Warnings) != 0 {
		t.Errorf("a missing nvidia-smi isn't worth a warning: %v", h.Warnings)
	}
}

func TestLinuxNvidiaSMIFails(t *testing.T) {
	r := &system.FakeRunner{
		Responses: map[string]string{"lspci": "01:00.0 3D controller: NVIDIA Corporation AD102 [GeForce RTX 4090]\n", "df -Pk": dfOut},
		Failures:  map[string]string{"nvidia-smi": "NVIDIA-SMI has failed because it couldn't communicate with the NVIDIA driver"},
	}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "linux"}, Runner: r, ReadFile: files(map[string]string{"/proc/meminfo": meminfo}), Stat: statAll, ModelDir: "/m"})
	if len(h.Warnings) != 1 || !strings.Contains(h.Warnings[0], "NVIDIA driver") {
		t.Fatalf("warnings: %v", h.Warnings)
	}
	// VRAM unknown, so plan on the CPU.
	if b := h.Budget(); b.GPU || !strings.Contains(b.Pool, "CPU only") {
		t.Errorf("budget: %+v", b)
	}
	if h.CPU != " CPU" && !strings.HasSuffix(h.CPU, "CPU") {
		t.Errorf("cpu fallback: %q", h.CPU)
	}
}

func TestAppleSilicon(t *testing.T) {
	r := &system.FakeRunner{Responses: map[string]string{
		"sysctl -n machdep.cpu.brand_string": "Apple M3 Pro",
		"sysctl -n hw.ncpu":                  "12",
		"sysctl -n hw.memsize":               "38654705664",
		"df -Pk":                             dfOut,
	}}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "darwin", Arch: "arm64"}, Runner: r, Stat: statAll, ModelDir: "/Users/u/.ollama/models"})
	if !h.AppleSilicon || h.CPU != "Apple M3 Pro" || h.Cores != 12 || h.RAM != 36*GiB {
		t.Fatalf("%+v", h)
	}
	if b := h.Budget(); b.Bytes != 36*GiB*2/3 || !b.GPU {
		t.Errorf("budget: %+v", b)
	}
	small := Hardware{AppleSilicon: true, RAM: 16 * GiB}
	if b := small.Budget(); b.Bytes != 16*GiB*2/3 {
		t.Errorf("16 GB Mac budget: %+v", b)
	}
	for _, l := range r.Lines() {
		if strings.HasPrefix(l, "nvidia-smi") {
			t.Error("Macs have no NVIDIA tools")
		}
	}
}

func TestIntelMac(t *testing.T) {
	r := &system.FakeRunner{Responses: map[string]string{"sysctl -n hw.memsize": "17179869184", "df -Pk": dfOut}}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "darwin", Arch: "amd64"}, Runner: r, Stat: statAll, ModelDir: "/m"})
	if !h.IntelMac || len(h.Warnings) == 0 || !strings.Contains(h.Warnings[0], "CPU only") {
		t.Fatalf("%+v", h)
	}
	if b := h.Budget(); b.GPU || b.Bytes != 12*GiB {
		t.Errorf("budget: %+v", b)
	}
}

func TestWindowsIntegratedGPU(t *testing.T) {
	r := &system.FakeRunner{
		Responses: map[string]string{
			winCmd: `{"cpu":"Intel(R) Core(TM) Ultra 7 155H ","cores":22,"installed":34359738368,"visible":33895002112,"gpus":"Intel(R) Arc(TM) Graphics"}`,
			`powershell -NoProfile -NonInteractive -Command "(Get-PSDrive C).Free"`: "107374182400",
		},
		Failures: map[string]string{"nvidia-smi": "exec: \"nvidia-smi\": executable file not found in %PATH%"},
	}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "windows"}, Runner: r, ModelDir: `C:\Users\u\.ollama\models`})
	if h.RAM != 32*GiB || h.VisibleRAM != 33895002112 || h.CPU != "Intel(R) Core(TM) Ultra 7 155H" || h.Cores != 22 {
		t.Fatalf("%+v", h)
	}
	if len(h.GPUs) != 1 || !h.GPUs[0].Integrated || h.GPUs[0].Vendor != "intel" {
		t.Fatalf("gpus: %+v", h.GPUs)
	}
	if h.FreeDisk != 100*GiB {
		t.Errorf("disk: %d", h.FreeDisk)
	}
	if b := h.Budget(); b.Bytes != 16*GiB || !strings.Contains(b.Pool, "built-in GPU") {
		t.Errorf("budget: %+v", b)
	}
}

func TestWindowsNvidiaReplacesAdapterEntry(t *testing.T) {
	r := &system.FakeRunner{Responses: map[string]string{
		winCmd: `{"cpu":"AMD Ryzen 7","cores":16,"installed":68719476736,"visible":68000000000,"gpus":["NVIDIA GeForce RTX 3060","AMD Radeon(TM) Graphics"]}`,
		nvSMI:  "NVIDIA GeForce RTX 3060, 12288",
	}}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "windows"}, Runner: r, ModelDir: `D:\models`})
	if len(h.GPUs) != 2 || h.GPUs[0].VRAM != 12*GiB || h.GPUs[1].Vendor != "amd" || !h.GPUs[1].Integrated {
		t.Fatalf("%+v", h.GPUs)
	}
	if b := h.Budget(); b.Bytes != 12*GiB*9/10 {
		t.Errorf("never add pools together; budget: %+v", b)
	}
}

func TestWindowsFallsBackToVisibleRAM(t *testing.T) {
	r := &system.FakeRunner{Responses: map[string]string{
		winCmd: `{"cpu":"x","cores":4,"installed":0,"visible":17179869184,"gpus":[]}`,
	}, Failures: map[string]string{"nvidia-smi": "executable file not found"}}
	h := Detect(t.Context(), Deps{Info: system.Info{OS: "windows"}, Runner: r, ModelDir: `C:\m`})
	if h.RAM != 16*GiB || len(h.Warnings) != 2 {
		t.Fatalf("%+v", h)
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		vendor     string
		integrated bool
	}{
		"AMD Radeon RX 7800 XT":            {"amd", false},
		"AMD Radeon(TM) 780M":              {"amd", true},
		"Intel(R) UHD Graphics 770":        {"intel", true},
		"Intel(R) Arc(TM) A770 Graphics":   {"intel", false},
		"Microsoft Basic Display Adapter":  {"", true},
		"NVIDIA GeForce RTX 4070 Laptop":   {"nvidia", false},
		"Advanced Micro Devices [AMD/ATI]": {"amd", true},
	}
	for name, want := range cases {
		g := classify(name)
		if g.Vendor != want.vendor || g.Integrated != want.integrated {
			t.Errorf("%s: %+v", name, g)
		}
	}
}

func TestTinyMachineHasNoBudget(t *testing.T) {
	if b := (Hardware{RAM: 4 * GiB}).Budget(); b.Bytes != 0 {
		t.Errorf("%+v", b)
	}
}

func TestDiskWalksUpToExistingFolder(t *testing.T) {
	r := &system.FakeRunner{Responses: map[string]string{"df -Pk": dfOut}}
	stat := func(p string) (os.FileInfo, error) {
		if p == "/home/u" {
			return nil, nil
		}
		return nil, errors.New("missing")
	}
	h := Hardware{}
	h.FreeDisk = freeDisk(t.Context(), Deps{Info: system.Info{OS: "linux"}, Runner: r, Stat: stat, ModelDir: "/home/u/.ollama/models"})
	if h.FreeDisk != 100*GiB || r.Lines()[0] != "df -Pk /home/u" {
		t.Fatalf("%d %v", h.FreeDisk, r.Lines())
	}
}

func TestParseDFBad(t *testing.T) {
	if parseDF("") != 0 || parseDF("header\nshort line") != 0 {
		t.Error("bad df output should give 0")
	}
}

func TestBigMacGetsThreeQuarters(t *testing.T) {
	if b := (Hardware{AppleSilicon: true, RAM: 64 * GiB}).Budget(); b.Bytes != 48*GiB {
		t.Errorf("%+v", b)
	}
}

func TestFromWSLAddsExe(t *testing.T) {
	r := &system.FakeRunner{Responses: map[string]string{
		`powershell.exe -NoProfile`: `{"cpu":"Ryzen","cores":32,"installed":137438953472,"gpus":"AMD Radeon 8060S"}`,
	}}
	h := Detect(context.Background(), Deps{Info: system.Info{OS: "windows"}, Runner: FromWSL(r), ModelDir: `C:\`})
	if h.CPU != "Ryzen" || h.RAM != 128*GiB {
		t.Fatalf("%+v", h)
	}
	for _, l := range r.Lines() {
		if !strings.Contains(strings.Fields(l)[0], ".exe") {
			t.Errorf("ran %s without .exe", l)
		}
	}
}
