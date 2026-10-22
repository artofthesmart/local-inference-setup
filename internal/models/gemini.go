package models

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/artofthesmart/local-inference-setup/internal/hardware"
)

// GeminiURL is where the user pastes the prompt.
const GeminiURL = "https://gemini.google.com/app"

// The wording comes from artofthesmart/dotfiles-bootstrap's setup-local-ai wizard, which was tested against real Gemini answers.
var promptTemplate = template.Must(template.New("prompt").Funcs(template.FuncMap{"size": hardware.Size}).Parse(`Recommend a practical LOCAL Ollama model for GitHub Copilot agent mode (VS Code and Copilot CLI).
OS: {{.OS}}; architecture: {{.Arch}}
CPU: {{.CPU}}{{if .Cores}} ({{.Cores}} threads){{end}}
{{- if eq .OS "Windows"}}
Installed physical RAM: {{size .RAM}} (sum of memory-device capacities)
Windows-visible physical RAM: {{size .VisibleRAM}} (not installed capacity or currently free RAM)
{{- else}}
RAM: {{size .RAM}}
{{- end}}
GPU: {{.GPUText}}
{{- if .AppleSilicon}}
Memory model: Apple Silicon unified memory. The GPU shares the RAM above; it is one pool, not extra VRAM.
{{- else if .IntelMac}}
Acceleration: Intel Mac. Ollama runs on the CPU only, so agent work may be very slow.
{{- else if .WindowsIGPU}}
Windows iGPU planning ceiling: up to 50% of installed RAM ({{size .HalfRAM}}), shared dynamically; actual availability unverified.
{{- end}}
Free space where models are saved: {{if .FreeDisk}}{{size .FreeDisk}}{{else}}unknown{{end}}

TOOL-VERIFIED EXACT-TAG CANDIDATES:
{{- range .Candidates}}
- {{.Tag}} | DOWNLOAD {{size .Size}} | VERIFIED_CONTEXT_TOKENS {{.Context}}
{{- end}}

These are the ONLY allowed Ollama Library recommendations for this run. The
setup tool read each exact tag's registry layers and GGUF context metadata directly.
Choose MODEL exactly as written from this list, or use MODEL: NONE. Do not output
another tag, alias, quantization, family member or Hugging Face model in the
copy-back line. Candidate size and verified context are metadata for your
analysis; the tool already has them. Tool use is still unverified until the
tool's post-download local metadata and structured tool-call checks pass.

TOOL CALLING STATUS:
The shortlist is limited to models currently presented as tool-capable by
Ollama, but that label is not proof. Do not claim success; the tool must see
an actual structured tool_calls response from the downloaded exact tag.
Prefer candidates with current primary-source or reproducible evidence that they
accept at least 128 tool definitions in one request, follow complex JSON schemas,
and reliably choose among many tools. Basic single-tool support is not enough
evidence. If a candidate's tool-definition limit or large-tool-set reliability
is unknown, say so explicitly rather than guessing.

REQUIRED RESPONSE FORMAT: Start your response with this copy-back line, before
any greeting, explanation, heading, or analysis:
MODEL: <exact local Ollama name:tag; no command, URL, backticks, or cloud model>
This is the only value the user must copy into the setup tool. The tool
already knows the exact registry size and fixes agent context at {{.Context}} tokens.
Put ALL analysis, links, caveats and fallback choices AFTER this line.
Do not invent a runnable recommendation if none is suitable:
use MODEL: NONE and explain the blocker afterward.

Compare ONLY the verified candidates above and explain which best fits this
hardware. Recommend a primary and smaller fallback from that same list, with
official model-page links, license/commercial-use caveats, realistic speed
expectations, and memory headroom for the OS, the editor or terminal, model
weights AND KV cache.
Account for shared versus dedicated memory; do not add GPUs' VRAM together.
Do not suggest cloud inference. Do not claim structured tool calling is verified:
the tool will test the exact downloaded model locally.
Treat support for at least 128 declared tools, complex schemas and reliable
selection from a large tool set as important compatibility criteria.
For integrated or unified-memory GPUs, treat RAM as one physical pool. Do not add
shared graphics memory to RAM as extra capacity or infer a fixed GPU reservation.
MANDATORY FOR THIS SETUP: Copilot's agents need at least {{.Context}} tokens of
context. Recommend only models whose supported context window is at least
{{.Context}} AND whose weights plus KV cache at that context fit this hardware
with OS headroom. A small download alone is not enough.
Do not recommend a smaller-context model and simply raise num_ctx; setting a
larger number does not establish model support. Do not lower context to make it fit.
If no suitable model fits, use MODEL: NONE and explain the hardware/support blocker.
State uncertainties and ask about missing hardware details instead of guessing.
`))

// Prompt builds the Gemini prompt. It contains only hardware facts and the checked model list: no names, paths, or code.
func Prompt(h hardware.Hardware, candidates []Info, contextLen int) string {
	var gpus []string
	igpu := false
	for _, g := range h.GPUs {
		s := g.Name
		switch {
		case g.VRAM > 0:
			s += fmt.Sprintf(" (%s dedicated video memory)", hardware.Size(g.VRAM))
		case g.Integrated:
			s += " (built in; shares system RAM)"
			igpu = igpu || g.Vendor == "intel" || g.Vendor == "amd"
		}
		gpus = append(gpus, s)
	}
	gpuText := "none found"
	if len(gpus) > 0 {
		gpuText = strings.Join(gpus, "; ") + ". Separate GPUs have separate memory pools."
	}
	osName := map[string]string{"linux": "Linux", "darwin": "macOS", "windows": "Windows"}[h.OS]
	if osName == "" {
		osName = h.OS
	}
	data := struct {
		hardware.Hardware
		OS          string
		GPUText     string
		WindowsIGPU bool
		HalfRAM     uint64
		Candidates  []Info
		Context     int
	}{h, osName, gpuText, h.OS == "windows" && igpu, h.RAM / 2, candidates, contextLen}
	var b bytes.Buffer
	if err := promptTemplate.Execute(&b, data); err != nil {
		panic(err) // the template is fixed and tested
	}
	return b.String()
}

// ErrNone means Gemini said nothing on the list fits.
var ErrNone = errors.New("Gemini says none of the models fit this computer")

var modelLine = regexp.MustCompile(`(?im)^\W*model\W*:\s*(.+)$`)

// ParseAnswer reads the model tag out of whatever the user pasted: the MODEL: line, a bare tag, or a tag in backticks. The tag must be one of allowed.
func ParseAnswer(text string, allowed []string) (string, error) {
	text = strings.TrimSpace(text)
	if m := modelLine.FindStringSubmatch(text); m != nil {
		text = m[1]
	} else if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	text = strings.Trim(strings.TrimSpace(text), "`*_\"' .")
	if strings.EqualFold(text, "none") {
		return "", ErrNone
	}
	if err := ValidTag(text); err != nil {
		return "", err
	}
	tag := Canonical(text)
	if !slices.Contains(allowed, tag) {
		return "", fmt.Errorf("%s isn't on the checked list (%s)", tag, strings.Join(allowed, ", "))
	}
	return tag, nil
}
