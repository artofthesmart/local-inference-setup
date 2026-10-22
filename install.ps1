# Downloads the right copilot-local program for this computer, checks it, and runs it.
# Nothing is installed: the program runs from a temporary folder that's deleted afterwards.
#   irm https://raw.githubusercontent.com/artofthesmart/local-inference-setup/main/install.ps1 | iex
# To pass arguments:
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/artofthesmart/local-inference-setup/main/install.ps1))) ollama --dry-run
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$base = if ($env:COPILOT_LOCAL_BASE_URL) { $env:COPILOT_LOCAL_BASE_URL } else { 'https://github.com/artofthesmart/local-inference-setup/releases/latest/download' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { throw "Sorry, $($env:PROCESSOR_ARCHITECTURE) computers aren't supported yet." }
}
$name = "copilot-local_windows_$arch.zip"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("copilot-local-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "Downloading copilot-local for windows/$arch..."
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile (Join-Path $tmp $name)
  Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

  $want = (Get-Content (Join-Path $tmp 'checksums.txt') | ForEach-Object {
      $p = $_ -split '\s+'
      if ($p[1] -eq $name) { $p[0] }
    }) | Select-Object -First 1
  $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $name)).Hash.ToLower()
  if (-not $want -or $want.ToLower() -ne $got) {
    throw "The download didn't match its checksum, so it wasn't run. Please try again."
  }

  Expand-Archive -Path (Join-Path $tmp $name) -DestinationPath $tmp
  & (Join-Path $tmp 'copilot-local.exe') @args
  $code = $LASTEXITCODE
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
if ($code) { Write-Host "copilot-local exited with code $code" }
