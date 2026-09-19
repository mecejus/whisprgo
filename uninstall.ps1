# Removes everything install.ps1 created: the logon entry, the binary and its
# folder, and the settings folder (API key, log, chimes). After this the PC is
# as it was before whisprgo was installed.
#
#   irm https://raw.githubusercontent.com/mecejus/whisprgo/main/uninstall.ps1 | iex
#
# Nothing here needs administrator rights, because nothing in the install
# needed them either: no service, no Program Files, no registry outside HKCU.

$ErrorActionPreference = 'Stop'

$installDir = Join-Path $env:LOCALAPPDATA 'whisprgo'
$configDir  = Join-Path $env:USERPROFILE '.config\whisprgo'
$runKey     = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'

Write-Host ''
Write-Host 'Removing whisprgo...'

Get-Process -Name 'whisprgo' -ErrorAction SilentlyContinue |
  Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 400

Remove-ItemProperty -Path $runKey -Name 'whisprgo' -ErrorAction SilentlyContinue

foreach ($dir in @($installDir, $configDir)) {
  if (Test-Path $dir) {
    Remove-Item -Path $dir -Recurse -Force -ErrorAction SilentlyContinue
  }
}

Write-Host ''
Write-Host 'whisprgo is gone. Your API key and settings were removed too.'
