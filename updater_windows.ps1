param(
  [Parameter(Mandatory=$true)][string]$Target,
  [Parameter(Mandatory=$true)][string]$Staged,
  [Parameter(Mandatory=$true)][string]$ConfigDir,
  [Parameter(Mandatory=$true)][int]$ParentPid
)
$ErrorActionPreference = 'Stop'
$Log = Join-Path (Split-Path -Parent $Staged) 'update.log'
try { Start-Transcript -Path $Log -Append | Out-Null } catch {}
$Backup = "$Target.eznas-backup"
$PidFile = Join-Path $ConfigDir 'eznas.pid'
try {
  # Let the short-lived updater process exit so Windows releases its executable lock.
  for ($i=0; $i -lt 60; $i++) {
    if (-not (Get-Process -Id $ParentPid -ErrorAction SilentlyContinue)) { break }
    Start-Sleep -Milliseconds 500
  }
  if (Get-Process -Id $ParentPid -ErrorAction SilentlyContinue) { throw 'Updater process did not exit.' }

  # Stop an existing server only if its PID file points to a live process.
  if (Test-Path $PidFile) {
    $serverPid = 0
    [void][int]::TryParse((Get-Content $PidFile -Raw).Trim(), [ref]$serverPid)
    if ($serverPid -gt 0 -and $serverPid -ne $PID) {
      $p = Get-Process -Id $serverPid -ErrorAction SilentlyContinue
      if ($p) {
        # Ask ezNAS to shut down through its own stop command first.
        try { & $Target stop | Out-Null } catch {}
        for ($i=0; $i -lt 40; $i++) {
          if (-not (Get-Process -Id $serverPid -ErrorAction SilentlyContinue)) { break }
          Start-Sleep -Milliseconds 250
        }
        if (Get-Process -Id $serverPid -ErrorAction SilentlyContinue) {
          throw 'ezNAS did not stop gracefully. No executable was replaced; close the server and retry.'
        }
      }
    }
  }

  if (Test-Path $Backup) { Remove-Item $Backup -Force }
  Copy-Item -LiteralPath $Target -Destination $Backup
  try {
    Copy-Item -LiteralPath $Staged -Destination $Target -Force
    $p = Start-Process -FilePath $Target -ArgumentList 'start' -PassThru -WorkingDirectory (Split-Path -Parent $Target)
    Start-Sleep -Seconds 2
    if ($p.HasExited) { throw 'Updated ezNAS exited immediately after launch.' }
    Write-Output 'Update applied. Previous executable retained at:'
    Write-Output $Backup
    try { Stop-Transcript | Out-Null } catch {}
  } catch {
    Copy-Item -LiteralPath $Backup -Destination $Target -Force
    Start-Process -FilePath $Target -ArgumentList 'start' -WorkingDirectory (Split-Path -Parent $Target) | Out-Null
    throw "Update failed; previous executable restored. $($_.Exception.Message)"
  }
} catch {
  Write-Error $_
  try { Stop-Transcript | Out-Null } catch {}
  exit 1
}
