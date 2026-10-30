#!/bin/sh
# Prints a hardware-aware prompt to paste into Gemini; no network calls or changes.
set -eu

os=$(uname -s)
arch=$(uname -m)
cpu=unknown
threads=unknown
ram=unknown
gpu=unknown
memory_note="GPU acceleration and dedicated VRAM are unverified unless reported below."

case "$os" in
  Linux)
    if [ -r /proc/cpuinfo ]; then
      cpu=$(awk -F ': *' '/^(model name|Model)[[:space:]]*:/ {print $2; exit}' /proc/cpuinfo)
      threads=$(awk '/^processor[[:space:]]*:/ {n++} END {if (n) print n}' /proc/cpuinfo)
    fi
    if [ -r /proc/meminfo ]; then
      ram=$(awk '/^MemTotal:/ {printf "%.1f GiB (kernel-visible, not necessarily installed capacity)", $2 / 1048576}' /proc/meminfo)
    fi
    if grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null; then
      memory_note="$memory_note WSL detected: RAM and CPU describe the Linux guest. If Ollama runs on Windows, use gemini-query.ps1 on the Windows host instead."
    fi
    ;;
  Darwin)
    cpu=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || true)
    threads=$(sysctl -n hw.ncpu 2>/dev/null || true)
    ram=$(sysctl -n hw.memsize 2>/dev/null | awk '$1 ~ /^[0-9]+$/ {printf "%.1f GiB", $1 / 1073741824}' || true)
    gpu=$(system_profiler SPDisplaysDataType 2>/dev/null | awk '/Chipset Model:|VRAM.*:|Metal Support:/ {sub(/^[[:space:]]+/, ""); print}' || true)
    case "$arch" in
      arm64) memory_note="Apple Silicon unified memory: CPU and GPU share ONE RAM pool; do not add VRAM to RAM." ;;
      *) memory_note="Intel Mac: do not assume the listed GPU is supported by Ollama; evaluate CPU-only usability." ;;
    esac
    ;;
  *)
    printf '%s\n' "This script supports Linux and macOS. On Windows, use gemini-query.ps1." >&2
    exit 1
    ;;
esac

if [ "$os" = Linux ]; then
  if command -v lspci >/dev/null 2>&1; then
    gpu=$(lspci 2>/dev/null | awk '/VGA compatible controller:|3D controller:|Display controller:/ {sub(/^.*(VGA compatible controller:|3D controller:|Display controller:) */, ""); print}' || true)
  fi
fi
nvidia=
if command -v nvidia-smi >/dev/null 2>&1; then
  nvidia=$(nvidia-smi --query-gpu=name,memory.total --format=csv,noheader,nounits 2>/dev/null) || nvidia=
fi

disk_dir=${OLLAMA_MODELS:-${HOME:-/}}
while [ ! -d "$disk_dir" ] && [ "$disk_dir" != / ] && [ "$disk_dir" != . ]; do
  disk_dir=$(dirname "$disk_dir")
done
disk=$(df -Pk "$disk_dir" 2>/dev/null | awk 'END {if (NR > 1 && $4 ~ /^[0-9]+$/) printf "%.1f GiB", $4 / 1048576}' || true)
today=$(date +%Y-%m-%d)

cat <<EOF
Recommend the best LOCAL CHAT and AGENTIC models to download from Ollama as of $today.
Use current official Ollama library/tag pages and model documentation, not a stale shortlist.
Consider the full range of locally downloadable Ollama chat/generation models, not just tool-capable or coding models.

HARDWARE (automatically detected; unknown means unverified):
OS: $os; architecture: $arch
CPU: ${cpu:-unknown}
Logical CPU threads: ${threads:-unknown}
Physical RAM: ${ram:-unknown}
GPU inventory (names alone do not prove acceleration or dedicated VRAM):
${gpu:-unknown}
NVIDIA GPU name, dedicated VRAM in MiB (may describe the same GPUs listed above):
${nvidia:-unknown}
Memory/acceleration notes: $memory_note
Free disk: ${disk:-unknown} (OLLAMA_MODELS filesystem, or home filesystem if unset; the Ollama service may store models elsewhere)

TASK:
Give THREE choices in EACH of these two use-case groups (six recommendations total).
These are use cases, not mutually exclusive model classes; a model may appear in both groups.

CHAT: Conversation, explanations, writing, reasoning, and occasional coding without tool calls or autonomous actions.
1. SPEED: prioritize responsive conversation and low latency while retaining useful answer quality.
2. BALANCED: the best everyday tradeoff between speed and general answer quality on this hardware.
3. INTELLIGENCE: prioritize reasoning and answer quality over speed, while remaining usable for interactive local chat.
Tool calling is NOT required for CHAT. Recommend an explicit practical working context for each choice, starting around 8192-16384 tokens if appropriate for the hardware and task; larger contexts are welcome when useful and feasible.
Do not exclude CHAT candidates for lacking tool support or a 128k context window. Chat-only behavior comes from the client not supplying or executing tools, not from choosing a model that cannot call tools.

AGENTIC: GitHub Copilot agent mode in VS Code and Copilot CLI, with reliable structured tool calling and multi-step coding work.
1. SPEED: prioritize responsive interactive agent loops and low latency, while retaining useful coding and tool-calling ability.
2. BALANCED: the best everyday tradeoff between speed and coding/reasoning quality on this hardware.
3. INTELLIGENCE: prioritize coding/reasoning quality over speed, but remain usable for interactive local agent work on this hardware. Do not just choose the largest model that can barely load.
Use 131072 tokens (128k, above 100k) as the required AGENTIC working context.

For EACH choice provide:
- An exact, currently downloadable local Ollama name:tag and its ollama pull command.
- Official source links verifying that exact tag, quantization, download size, supported context, and license/commercial-use caveats; also verify tool calling for AGENTIC.
- The recommended working context and estimated model-weight memory PLUS KV-cache/runtime memory at that context (131072 tokens for AGENTIC), with headroom for the OS, editor, and other apps; distinguish disk size from runtime memory.
- Expected acceleration/offloading, time to first token, generation speed, and practical chat or agent-loop latency as appropriate. Label estimates and unknowns; do not invent hardware benchmarks. Explain what makes each intelligence option still usable.
- For AGENTIC only: evidence for reliable structured tool calls, complex JSON schemas, and choosing among at least 128 declared tools. A tools label alone is not proof; state what still needs local testing.
- The main tradeoff and a short local validation plan: check loaded context and representative chat quality for CHAT; check loaded context and structured tool_calls for AGENTIC.

REQUIREMENTS:
Only local inference with models downloadable from Ollama; no cloud tags, hosted APIs, or Hugging Face-only models.
Each AGENTIC choice must natively support at least 131072 context tokens AND fit weights plus cache at that context; raising num_ctx does not establish support. Do not lower the AGENTIC context just to make a model fit.
Each CHAT choice must support its recommended working context AND fit weights plus cache at that context. Do not apply AGENTIC tool-calling or context requirements to CHAT.
Do not add shared GPU memory to RAM, sum separate GPUs' VRAM, assume iGPU support, or treat all system RAM as usable VRAM. Account for memory bandwidth, CPU offloading, and MoE total weights, not just active parameters.
Prefer three distinct choices within each group, but reuse a model with an explanation if hardware leaves fewer suitable options. Say NONE for any tier with no practical fit rather than forcing an unusable recommendation. AGENTIC may have no practical fit even when CHAT does.
Start with separate CHAT and AGENTIC comparison tables (tier, exact tag, working context, memory at that context, estimated speed, tradeoff), then explain the choices with dated sources. If browsing or hardware facts are unavailable, say so and make recommendations conditional rather than claiming verification.
EOF
