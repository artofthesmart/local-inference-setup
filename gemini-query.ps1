# Prints a hardware-aware prompt to paste into Gemini; no network calls or changes.
# Compatible with Windows PowerShell 5.1 and PowerShell 7.
& {
  $ErrorActionPreference = 'Stop'
  function Format-Memory($Bytes) {
    if ($Bytes -gt 0) { '{0:F1} GiB' -f ($Bytes / 1GB) } else { 'unknown' }
  }

  $cpu = 'unknown'
  $threads = 'unknown'
  $installed = 'unknown'
  $visible = 'unknown'
  $gpu = 'unknown'
  $nvidia = 'unknown'
  $disk = 'unknown'
  try {
    $processors = @(Get-CimInstance -ClassName Win32_Processor)
    $cpu = ($processors.Name | Select-Object -Unique) -join '; '
    $threads = ($processors | Measure-Object -Property NumberOfLogicalProcessors -Sum).Sum
  } catch {}
  try {
    $installed = Format-Memory ((Get-CimInstance -ClassName Win32_PhysicalMemory | Measure-Object -Property Capacity -Sum).Sum)
  } catch {}
  try {
    $visible = Format-Memory ((Get-CimInstance -ClassName Win32_ComputerSystem).TotalPhysicalMemory)
  } catch {}
  try {
    # AdapterRAM can truncate large VRAM values; report names, not misleading sizes.
    $names = @(Get-CimInstance -ClassName Win32_VideoController | ForEach-Object { $_.Name })
    if ($names.Count -gt 0) { $gpu = $names -join "`n" }
  } catch {}
  if (Get-Command nvidia-smi -ErrorAction SilentlyContinue) {
    try {
      $output = & nvidia-smi '--query-gpu=name,memory.total' '--format=csv,noheader,nounits' 2>$null
      if ($LASTEXITCODE -eq 0 -and $output) { $nvidia = $output -join "`n" }
    } catch {}
  }
  try {
    $modelDir = if ($env:OLLAMA_MODELS) { $env:OLLAMA_MODELS } else { $HOME }
    while (-not (Test-Path -LiteralPath $modelDir -PathType Container)) {
      $parent = Split-Path -Parent $modelDir
      if (-not $parent -or $parent -eq $modelDir) { break }
      $modelDir = $parent
    }
    $item = Get-Item -LiteralPath $modelDir
    if ($null -ne $item.PSDrive.Free) { $disk = Format-Memory $item.PSDrive.Free }
  } catch {}
  $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } elseif ($env:PROCESSOR_ARCHITECTURE) { $env:PROCESSOR_ARCHITECTURE } else { 'unknown' }
  $today = Get-Date -Format 'yyyy-MM-dd'

  @"
Recommend the best LOCAL agentic coding models to download from Ollama as of $today.
Use current official Ollama library/tag pages and model documentation, not a stale shortlist.

HARDWARE (automatically detected; unknown means unverified):
OS: Windows; architecture: $arch
CPU: $cpu
Logical CPU threads: $threads
Installed physical RAM: $installed (sum of memory-device capacities)
Windows-visible physical RAM: $visible (not currently free RAM)
GPU inventory (names alone do not prove acceleration or dedicated VRAM):
$gpu
NVIDIA GPU name, dedicated VRAM in MiB (may describe the same GPUs listed above):
$nvidia
Memory/acceleration notes: Integrated GPUs share system RAM dynamically, not extra dedicated VRAM. Actual usable GPU memory and Ollama acceleration support must be verified.
Free disk: $disk (OLLAMA_MODELS filesystem, or home filesystem if unset; the Ollama service may store models elsewhere)

TASK:
Give THREE choices for GitHub Copilot agent mode in VS Code and Copilot CLI:
1. SPEED: prioritize responsive interactive agent loops and low latency, while retaining useful coding and tool-calling ability.
2. BALANCED: the best everyday tradeoff between speed and coding/reasoning quality on this hardware.
3. INTELLIGENCE: prioritize coding/reasoning quality over speed, but remain usable for interactive local agent work on this hardware. Do not just choose the largest model that can barely load.

For EACH choice provide:
- An exact, currently downloadable local Ollama name:tag and its ollama pull command.
- Official source links verifying that exact tag, quantization, download size, supported context, tool calling, and license/commercial-use caveats.
- Estimated model-weight memory PLUS KV-cache/runtime memory at 65536 tokens, with headroom for the OS, editor, and other apps; distinguish disk size from runtime memory.
- Expected acceleration/offloading, time to first token, generation speed, and practical agent-loop latency. Label estimates and unknowns; do not invent hardware benchmarks. Explain what makes the intelligence option still usable.
- Evidence for reliable structured tool calls, complex JSON schemas, and choosing among at least 128 declared tools. A tools label alone is not proof; state what still needs local testing.
- The main tradeoff and a short local validation plan, including checking loaded context and structured tool_calls.

REQUIREMENTS:
Only local inference with models downloadable from Ollama; no cloud tags, hosted APIs, or Hugging Face-only models.
Each choice must natively support at least 65536 context tokens AND fit weights plus cache at that context; raising num_ctx does not establish support. Do not lower context just to make a model fit.
Do not add shared GPU memory to RAM, sum separate GPUs' VRAM, assume iGPU support, or treat all system RAM as usable VRAM. Account for memory bandwidth, CPU offloading, and MoE total weights, not just active parameters.
Prefer three distinct choices, but reuse a model with an explanation if hardware leaves fewer suitable options. Say NONE for any tier with no practical fit rather than forcing an unusable recommendation.
Start with a comparison table (tier, exact tag, memory at 64k, estimated speed, tradeoff), then explain the choices with dated sources. If browsing or hardware facts are unavailable, say so and make recommendations conditional rather than claiming verification.
"@
}
