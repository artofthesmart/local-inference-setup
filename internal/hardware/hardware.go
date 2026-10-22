// Package hardware reads the CPU, memory, GPUs, and free disk space, and works out how much memory a model can safely use.
package hardware

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/artofthesmart/local-inference-setup/internal/system"
)

const GiB = 1 << 30

// GPU is one graphics card or chip.
type GPU struct {
	Name       string
	Vendor     string // "nvidia", "amd", "intel", "apple", or ""
	VRAM       uint64 // dedicated memory in bytes; 0 when unknown or shared
	Integrated bool   // shares system RAM
}

// Hardware is what Detect found.
type Hardware struct {
	OS, Arch   string
	CPU        string
	Cores      int
	RAM        uint64 // installed memory in bytes
	VisibleRAM uint64 // Windows only: memory Windows can use, which can be less than installed
	GPUs       []GPU
	// AppleSilicon means one unified memory pool shared by CPU and GPU.
	AppleSilicon bool
	// IntelMac means Ollama runs on the CPU only.
	IntelMac bool
	// FreeDisk is free space where Ollama saves models; 0 when unknown.
	FreeDisk uint64
	ModelDir string
	Warnings []string
}

// Deps are the outside things Detect reads, so tests can fake them.
type Deps struct {
	Info     system.Info
	Runner   system.Runner
	ReadFile func(string) ([]byte, error)
	Glob     func(string) ([]string, error)
	Stat     func(string) (os.FileInfo, error)
	ModelDir string
}

// Detect inspects the machine. It never changes anything.
func Detect(ctx context.Context, d Deps) Hardware {
	if d.ReadFile == nil {
		d.ReadFile = os.ReadFile
	}
	if d.Glob == nil {
		d.Glob = filepath.Glob
	}
	if d.Stat == nil {
		d.Stat = os.Stat
	}
	h := Hardware{OS: d.Info.OS, Arch: d.Info.Arch, ModelDir: d.ModelDir}
	switch d.Info.OS {
	case "darwin":
		detectMac(ctx, d, &h)
	case "windows":
		detectWindows(ctx, d, &h)
	default:
		detectLinux(ctx, d, &h)
	}
	h.FreeDisk = freeDisk(ctx, d)
	if h.FreeDisk == 0 {
		h.Warnings = append(h.Warnings, "Couldn't read free disk space; make sure you have room for the download.")
	}
	return h
}

func detectLinux(ctx context.Context, d Deps, h *Hardware) {
	if b, err := d.ReadFile("/proc/cpuinfo"); err == nil {
		h.CPU, h.Cores = parseCPUInfo(string(b))
	}
	if h.CPU == "" {
		h.CPU = h.Arch + " CPU"
	}
	if b, err := d.ReadFile("/proc/meminfo"); err == nil {
		h.RAM = parseMemInfo(string(b))
	}
	h.GPUs = nvidiaGPUs(ctx, d, h)
	if len(h.GPUs) > 0 {
		return
	}
	out, err := d.Runner.Run(ctx, system.Cmd{Name: "lspci"})
	if err != nil {
		return
	}
	h.GPUs = parseLspci(out)
	// AMD's driver reports dedicated memory here.
	for i := range h.GPUs {
		if h.GPUs[i].Vendor != "amd" || h.GPUs[i].Integrated {
			continue
		}
		paths, _ := d.Glob("/sys/class/drm/card*/device/mem_info_vram_total")
		for _, p := range paths {
			if b, err := d.ReadFile(p); err == nil {
				if v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil && v > h.GPUs[i].VRAM {
					h.GPUs[i].VRAM = v
				}
			}
		}
	}
}

func detectMac(ctx context.Context, d Deps, h *Hardware) {
	sysctl := func(key string) string {
		out, _ := d.Runner.Run(ctx, system.Cmd{Name: "sysctl", Args: []string{"-n", key}})
		return strings.TrimSpace(out)
	}
	h.CPU = sysctl("machdep.cpu.brand_string")
	h.Cores, _ = strconv.Atoi(sysctl("hw.ncpu"))
	h.RAM, _ = strconv.ParseUint(sysctl("hw.memsize"), 10, 64)
	if h.Arch == "arm64" {
		h.AppleSilicon = true
		h.GPUs = []GPU{{Name: "Apple GPU (shares memory with the CPU)", Vendor: "apple", Integrated: true}}
	} else {
		h.IntelMac = true
		h.Warnings = append(h.Warnings, "Intel Macs run models on the CPU only, so expect slow answers. Start with a small model.")
	}
}

// WinScript prints CPU, memory, and GPU facts as JSON. Installed RAM is the sum of the memory sticks; AdapterRAM is never used because it caps at 4 GB.
const WinScript = `$ErrorActionPreference='SilentlyContinue'; $p=Get-CimInstance Win32_Processor | Select-Object -First 1; ` +
	`[pscustomobject]@{cpu=$p.Name; cores=[int]$p.NumberOfLogicalProcessors; ` +
	`installed=[uint64](Get-CimInstance Win32_PhysicalMemory | Measure-Object -Property Capacity -Sum).Sum; ` +
	`visible=[uint64](Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory; ` +
	`gpus=@(Get-CimInstance Win32_VideoController | ForEach-Object { $_.Name })} | ConvertTo-Json -Compress`

func detectWindows(ctx context.Context, d Deps, h *Hardware) {
	out, err := d.Runner.Run(ctx, system.Cmd{Name: "powershell", Args: []string{"-NoProfile", "-NonInteractive", "-Command", WinScript}})
	if err == nil {
		var w struct {
			CPU       string          `json:"cpu"`
			Cores     int             `json:"cores"`
			Installed uint64          `json:"installed"`
			Visible   uint64          `json:"visible"`
			GPUs      json.RawMessage `json:"gpus"`
		}
		if json.Unmarshal([]byte(out), &w) == nil {
			h.CPU, h.Cores, h.RAM, h.VisibleRAM = strings.TrimSpace(w.CPU), w.Cores, w.Installed, w.Visible
			for _, n := range stringList(w.GPUs) {
				h.GPUs = append(h.GPUs, classify(n))
			}
		}
	}
	if h.RAM == 0 && h.VisibleRAM > 0 {
		h.RAM = h.VisibleRAM
		h.Warnings = append(h.Warnings, "Couldn't read installed memory, so the memory Windows reports was used instead.")
	}
	if nv := nvidiaGPUs(ctx, d, h); len(nv) > 0 {
		// nvidia-smi knows the real VRAM, so it replaces the Windows entries for NVIDIA cards.
		kept := nv
		for _, g := range h.GPUs {
			if g.Vendor != "nvidia" {
				kept = append(kept, g)
			}
		}
		h.GPUs = kept
	}
}

// stringList accepts a JSON string or array; PowerShell sends a bare string when there's one item.
func stringList(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return []string{one}
	}
	return nil
}

func nvidiaGPUs(ctx context.Context, d Deps, h *Hardware) []GPU {
	out, err := d.Runner.Run(ctx, system.Cmd{Name: "nvidia-smi", Args: []string{"--query-gpu=name,memory.total", "--format=csv,noheader,nounits"}})
	if err != nil {
		if !strings.Contains(err.Error(), "executable file not found") {
			h.Warnings = append(h.Warnings, "nvidia-smi didn't answer, so NVIDIA video memory is unknown. Check your NVIDIA driver.")
		}
		return nil
	}
	return parseNvidiaSMI(out)
}

func parseNvidiaSMI(out string) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}
		mib, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			continue
		}
		gpus = append(gpus, GPU{Name: strings.TrimSpace(parts[0]), Vendor: "nvidia", VRAM: uint64(mib * (1 << 20))})
	}
	return gpus
}

func parseCPUInfo(s string) (name string, cores int) {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			cores++
		case "model name", "Model":
			if name == "" {
				name = v
			}
		}
	}
	return name, cores
}

func parseMemInfo(s string) uint64 {
	for _, line := range strings.Split(s, "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}

func parseLspci(out string) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(out, "\n") {
		for _, kind := range []string{"VGA compatible controller: ", "3D controller: ", "Display controller: "} {
			if _, name, ok := strings.Cut(line, kind); ok {
				gpus = append(gpus, classify(strings.TrimSpace(name)))
			}
		}
	}
	return gpus
}

// classify guesses the vendor and whether a GPU shares system memory, from its name.
func classify(name string) GPU {
	l := " " + strings.ToLower(name) + " "
	g := GPU{Name: name}
	switch {
	case strings.Contains(l, "nvidia"):
		g.Vendor = "nvidia"
	case strings.Contains(l, "amd") || strings.Contains(l, "radeon") || strings.Contains(l, " ati "):
		g.Vendor = "amd"
		// Discrete Radeon cards have an "RX", "Pro W", or "Instinct" model name; the rest are built into the CPU.
		g.Integrated = !(strings.Contains(l, " rx ") || strings.Contains(l, "pro w") || strings.Contains(l, "instinct"))
	case strings.Contains(l, "intel"):
		g.Vendor = "intel"
		g.Integrated = !strings.Contains(l, " arc(tm) a") && !strings.Contains(l, " arc a") && !strings.Contains(l, " arc b")
	default:
		// Virtual and basic display adapters have no memory of their own.
		g.Integrated = true
	}
	return g
}

func freeDisk(ctx context.Context, d Deps) uint64 {
	dir := d.ModelDir
	if dir == "" {
		return 0
	}
	if d.Info.OS == "windows" {
		// Parsed by hand so tests on other systems read Windows paths the same way.
		if len(dir) < 2 || dir[1] != ':' {
			return 0
		}
		out, err := d.Runner.Run(ctx, system.Cmd{Name: "powershell", Args: []string{"-NoProfile", "-NonInteractive", "-Command", "(Get-PSDrive " + strings.ToUpper(dir[:1]) + ").Free"}})
		if err != nil {
			return 0
		}
		v, _ := strconv.ParseUint(strings.TrimSpace(out), 10, 64)
		return v
	}
	// df needs a folder that exists, so walk up until one does.
	for dir != "/" && dir != "." {
		if _, err := d.Stat(dir); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	out, err := d.Runner.Run(ctx, system.Cmd{Name: "df", Args: []string{"-Pk", dir}})
	if err != nil {
		return 0
	}
	return parseDF(out)
}

func parseDF(out string) uint64 {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0
	}
	kb, err := strconv.ParseUint(f[3], 10, 64)
	if err != nil {
		return 0
	}
	return kb * 1024
}

// Budget is how much memory a model (weights plus context cache) may use, and which memory that is.
type Budget struct {
	Bytes uint64
	Pool  string // plain-English name, for the summary
	GPU   bool   // the model runs on a GPU
}

// Budget picks one memory pool; separate pools are never added together.
func (h Hardware) Budget() Budget {
	var best GPU
	for _, g := range h.GPUs {
		if !g.Integrated && g.VRAM > best.VRAM && (g.Vendor == "nvidia" || g.Vendor == "amd") {
			best = g
		}
	}
	switch {
	case best.VRAM > 0:
		return Budget{Bytes: best.VRAM * 9 / 10, Pool: fmt.Sprintf("%s video memory (%s)", best.Name, Size(best.VRAM)), GPU: true}
	case h.AppleSilicon:
		// macOS lets the GPU use about two thirds of memory on smaller Macs and three quarters on bigger ones.
		share := h.RAM * 2 / 3
		if h.RAM > 36*GiB {
			share = h.RAM * 3 / 4
		}
		return Budget{Bytes: share, Pool: fmt.Sprintf("shared memory (%s total)", Size(h.RAM)), GPU: true}
	case h.OS == "windows" && h.hasIntegratedGPU():
		return Budget{Bytes: h.RAM / 2, Pool: fmt.Sprintf("system memory shared with the built-in GPU (%s total)", Size(h.RAM)), GPU: true}
	default:
		headroom := uint64(4 * GiB)
		if q := h.RAM / 4; q > headroom {
			headroom = q
		}
		if h.RAM <= headroom {
			return Budget{Pool: "system memory"}
		}
		return Budget{Bytes: h.RAM - headroom, Pool: fmt.Sprintf("system memory, CPU only (%s total)", Size(h.RAM))}
	}
}

func (h Hardware) hasIntegratedGPU() bool {
	for _, g := range h.GPUs {
		if g.Integrated && (g.Vendor == "intel" || g.Vendor == "amd") {
			return true
		}
	}
	return false
}

// Size formats bytes as GB with one decimal, the way people read download sizes.
func Size(b uint64) string {
	return fmt.Sprintf("%.1f GB", float64(b)/GiB)
}

// FromWSL runs the Windows commands from inside WSL, where they need the .exe suffix. Use it with Info.OS "windows" to read the Windows host's hardware.
func FromWSL(r system.Runner) system.Runner { return wslRunner{r} }

type wslRunner struct{ system.Runner }

func (w wslRunner) Run(ctx context.Context, c system.Cmd) (string, error) {
	if !strings.HasSuffix(c.Name, ".exe") {
		c.Name += ".exe"
	}
	return w.Runner.Run(ctx, c)
}
