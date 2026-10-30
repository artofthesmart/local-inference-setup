# Prints a hardware-aware prompt; optionally copies it on ENTER. No network calls.
# Compatible with Windows PowerShell 5.1 and PowerShell 7.
& {
  $ErrorActionPreference = 'Stop'
  function Format-Memory($Bytes) {
    if ($Bytes -gt 0) { '{0:F1} GiB' -f ($Bytes / 1GB) } else { 'unknown' }
  }
  function Copy-QueryOnEnter($Query) {
    Write-Host "`n============================================================"
    $null = Read-Host 'Press ENTER to copy the query to your clipboard (Ctrl+C to skip)'
    try {
      Set-Clipboard -Value $Query
      Write-Host 'Copied! Paste into Gemini.'
    } catch {
      Write-Host "Couldn't access the clipboard. Select and copy the query above manually."
    }
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

  $query = @"
Recommend the best LOCAL CHAT and AGENTIC models to download from Ollama as of $today.
Use current official Ollama library/tag pages and model documentation, not a stale shortlist.
Consider the full range of locally downloadable Ollama chat/generation models, not just tool-capable or coding models.

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
"@
  $query
  if ([Environment]::UserInteractive -and -not [Console]::IsOutputRedirected) {
    Copy-QueryOnEnter $query
  }
}
