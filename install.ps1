# whisprgo installer for Windows.
#
#   irm https://raw.githubusercontent.com/mecejus/whisprgo/main/install.ps1 | iex
#
# The macOS half of this project signs the binary it downloads, because macOS
# ties the Accessibility grant to a code signature. Windows needs no such
# grant and no such trick: a low-level keyboard hook just works. So this is
# the whole install — download, register a logon entry, start it.

$ErrorActionPreference = 'Stop'

# Invoke-WebRequest renders a progress bar that costs more time than the
# download on Windows PowerShell 5.1.
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo      = 'mecejus/whisprgo'
$installDir = Join-Path $env:LOCALAPPDATA 'whisprgo'
$exePath   = Join-Path $installDir 'whisprgo.exe'
# Matches config.Dir() in the binary: Go's UserHomeDir is %USERPROFILE%.
$configDir = Join-Path $env:USERPROFILE '.config\whisprgo'
$logFile   = Join-Path $configDir 'whisprgo.log'
$runKey    = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'

function Step($text) { Write-Host $text }

# A 32-bit PowerShell on a 64-bit machine reports x86 in PROCESSOR_ARCHITECTURE
# and the real architecture in PROCESSOR_ARCHITEW6432.
$arch = $env:PROCESSOR_ARCHITEW6432
if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
switch ($arch) {
  'AMD64' { $asset = 'whisprgo-windows-amd64.exe' }
  'ARM64' { $asset = 'whisprgo-windows-arm64.exe' }
  default {
    Write-Error "whisprgo needs a 64-bit Windows PC. This one reports '$arch'."
    return
  }
}

Write-Host ''

# A running copy holds a lock on its own .exe, so it has to go first. This is
# also what makes re-running the installer the way to upgrade.
$wasRunning = $false
Get-Process -Name 'whisprgo' -ErrorAction SilentlyContinue | ForEach-Object {
  $wasRunning = $true
  $_ | Stop-Process -Force -ErrorAction SilentlyContinue
}
if ($wasRunning) { Start-Sleep -Milliseconds 400 }

$firstRun = -not (Test-Path (Join-Path $configDir 'config.json'))

Step 'Downloading whisprgo...'
# /releases/latest/download/ follows the rolling-release tag, so this is
# always the current build with no API call and no tag to resolve.
$url = "https://github.com/$repo/releases/latest/download/$asset"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("whisprgo-" + [guid]::NewGuid().ToString('N') + '.exe')
try {
  Invoke-WebRequest -Uri $url -OutFile $tmp -UseBasicParsing
} catch {
  Write-Error "Could not download whisprgo from GitHub. Check your internet connection and try again.`n$_"
  return
}

# Clear the mark-of-the-web so Windows does not treat the binary as untrusted
# downloaded content every time it starts.
Unblock-File -Path $tmp -ErrorAction SilentlyContinue

New-Item -ItemType Directory -Force -Path $installDir | Out-Null
New-Item -ItemType Directory -Force -Path $configDir  | Out-Null

# Even after Stop-Process, antivirus can hold the old file open for a moment.
$moved = $false
foreach ($attempt in 1..10) {
  try {
    Move-Item -Path $tmp -Destination $exePath -Force
    $moved = $true
    break
  } catch {
    Start-Sleep -Milliseconds 300
  }
}
if (-not $moved) {
  Remove-Item $tmp -Force -ErrorAction SilentlyContinue
  Write-Error "Could not replace $exePath. Close whisprgo and run this again."
  return
}
Step "Installed to $exePath."

# Windows has no launchd. The per-user Run key starts whisprgo at logon
# without needing administrator rights, which is the whole reason this
# installer never asks for any.
Set-ItemProperty -Path $runKey -Name 'whisprgo' -Value "`"$exePath`" --background"
Step 'Set to start when you log in.'

# Remember where this launch's output begins: the log is appended to, and
# earlier runs have already printed the ready line.
# @(...) forces an array: Get-Content hands back a bare string for a
# one-line file, and indexing into that would count characters.
$logStart = 0
if (Test-Path $logFile) { $logStart = @(Get-Content $logFile -ErrorAction SilentlyContinue).Count }

# -PassThru so the wait loop below can notice if whisprgo gives up.
$proc = Start-Process -FilePath $exePath -ArgumentList '--background' -PassThru

Write-Host ''
if ($firstRun) {
  Write-Host 'Almost done. A box will pop up asking for your Groq API key.'
  Write-Host 'Get a free one at https://console.groq.com, paste it in, click OK.'
  Write-Host ''
  $waitMsg = 'Waiting for you to do that (leave this window open)'
} else {
  $waitMsg = 'Starting whisprgo'
}

# Poll this launch's log lines for the ready marker, four times a second.
# The API key dialog is the slow part, so allow ten minutes.
$spinner = '|', '/', '-', '\'
$ready = $false
$stopped = $false
for ($ticks = 0; $ticks -lt 2400; $ticks++) {
  if (Test-Path $logFile) {
    $lines = @(Get-Content $logFile -ErrorAction SilentlyContinue)
    if ($lines.Count -gt $logStart) {
      $fresh = $lines[$logStart..($lines.Count - 1)]
      if ($fresh -match 'whisprgo ready') { $ready = $true; break }
    }
  }
  # whisprgo exits when it has no API key to work with, and cancelling the
  # dialog is the usual way that happens. Checked after the log, so a run
  # that printed the ready line still counts as ready. Without this the
  # installer spins for the full ten minutes waiting on a process that is
  # already gone.
  if ($proc -and $proc.HasExited) { $stopped = $true; break }
  Write-Host -NoNewline ("`r{0} {1}... {2}s   " -f $spinner[$ticks % 4], $waitMsg, [int]($ticks / 4))
  Start-Sleep -Milliseconds 250
}

Write-Host "`r$(' ' * 70)`r" -NoNewline
if ($ready) {
  Write-Host 'All set. Hold ctrl and the Windows key together, talk, let go. Your words appear where you were typing.'
  Write-Host ''
  Write-Host 'To watch it work, run it in a terminal instead:'
  Write-Host "  & `"$exePath`""
} elseif ($stopped) {
  Write-Host 'whisprgo stopped before it was ready.'

  # fatal() writes the reason to stderr as well as showing it in a box, so
  # the last line of this launch's log says what went wrong.
  $reason = ''
  if (Test-Path $logFile) {
    $lines = @(Get-Content $logFile -ErrorAction SilentlyContinue)
    if ($lines.Count -gt $logStart) {
      $reason = $lines[$logStart..($lines.Count - 1)] |
        Where-Object { $_.Trim() } | Select-Object -Last 1
    }
  }
  if ($reason) { Write-Host "  $reason" }

  Write-Host ''
  Write-Host 'If you cancelled the API key box, that is all this is. whisprgo asks'
  Write-Host 'again next time you log in, or run the install line again now.'
} else {
  Write-Host 'Still waiting, and that is fine. whisprgo is running in the background'
  Write-Host 'and will be ready as soon as it has your API key.'
  Write-Host "If something looks wrong, the log is at $logFile."
}
