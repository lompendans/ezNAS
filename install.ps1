$ErrorActionPreference = "Stop"
$Repo = "lompendans/ezNAS"
$InstallDir = if ($env:EZNAS_INSTALL_DIR) { [IO.Path]::GetFullPath($env:EZNAS_INSTALL_DIR) } else { Join-Path $env:LOCALAPPDATA "Programs\ezNAS" }
$Arch = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture -eq "Arm64") { "arm64" } else { "amd64" }
$Asset = "eznas-windows-$Arch.zip"
$Api = "https://api.github.com/repos/$Repo/releases/latest"
$Temp = Join-Path ([System.IO.Path]::GetTempPath()) ("eznas-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $Temp | Out-Null
try {
    $Release = Invoke-RestMethod -Uri $Api
    if (-not $Release.tag_name) { throw "No published ezNAS release found yet." }
    $Base = "https://github.com/$Repo/releases/download/$($Release.tag_name)"
    Invoke-WebRequest -Uri "$Base/$Asset" -OutFile (Join-Path $Temp $Asset)
    Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile (Join-Path $Temp "checksums.txt")
    $Line = Get-Content (Join-Path $Temp "checksums.txt") | Where-Object { $_ -match "\s+$([regex]::Escape($Asset))$" } | Select-Object -First 1
    if (-not $Line) { throw "Checksum entry for $Asset was not found." }
    $Expected = ($Line -split "\s+")[0].Trim()
    $Actual = (Get-FileHash (Join-Path $Temp $Asset) -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($Actual -ne $Expected.ToLowerInvariant()) { throw "Checksum verification failed; refusing to install." }
    Expand-Archive -Path (Join-Path $Temp $Asset) -DestinationPath (Join-Path $Temp "unpack") -Force
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item (Join-Path $Temp "unpack\eznas.exe") (Join-Path $InstallDir "eznas.exe") -Force

    # Add the install directory to the current user's PATH without duplicates.
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $PathEntries = @($UserPath -split ";" | Where-Object { $_ -and $_.TrimEnd("\") -ine $InstallDir.TrimEnd("\") })
    $PathEntries += $InstallDir
    [Environment]::SetEnvironmentVariable("Path", (($PathEntries -join ";").Trim(";")), "User")
    $env:Path = "$InstallDir;$env:Path"

    Write-Host "ezNAS $($Release.tag_name) installed to $InstallDir" -ForegroundColor Green
    Write-Host "PATH configured for your Windows user." -ForegroundColor Green
    Write-Host "You can run: eznas help"

    $autoStart = Read-Host "Start ezNAS automatically whenever you sign in to Windows? [y/N]"
    if ($autoStart -match "^(y|yes)$") {
        $taskName = "ezNAS"
        $action = New-ScheduledTaskAction -Execute (Join-Path $InstallDir "eznas.exe") -Argument "start" -WorkingDirectory $InstallDir
        $trigger = New-ScheduledTaskTrigger -AtLogOn -User ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name)
        $principal = New-ScheduledTaskPrincipal -UserId ([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) -LogonType Interactive -RunLevel Limited
        $settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero)
        Register-ScheduledTask -TaskName $taskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Description "Starts the ezNAS server when this Windows user signs in." -Force | Out-Null
        Write-Host "Automatic startup enabled for the current Windows user." -ForegroundColor Green
    } else {
        Write-Host "Automatic startup skipped. You can run 'eznas start' whenever you want."
    }

    Write-Host ""
    $configureNetwork = Read-Host "Configure Windows Network discovery and File Explorer (SMB) sharing now? This requires Administrator approval. [y/N]"
    if ($configureNetwork -match "^(y|yes)$") {
        $NetworkScript = Join-Path $Temp "setup-network.ps1"
        $NetworkUri = "https://raw.githubusercontent.com/$Repo/main/setup-network.ps1"
        Invoke-WebRequest -Uri $NetworkUri -OutFile $NetworkScript
        $ConfigPath = Join-Path $env:APPDATA "eznas\config.json"
        $CurrentIdentity = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
        $ElevatedArgs = "-NoProfile -ExecutionPolicy Bypass -File '$NetworkScript' -ConfigPath '$ConfigPath' -TargetIdentity '$CurrentIdentity'"
        Start-Process -FilePath "powershell.exe" -Verb RunAs -Wait -ArgumentList $ElevatedArgs
        Write-Host "Windows network setup finished. If you changed storage or port, restart ezNAS." -ForegroundColor Green
    } else {
        Write-Host "Network setup skipped. You can run setup-network.ps1 later from the ezNAS repository in Administrator PowerShell."
    }
    Write-Host "Open a new terminal for PATH changes to be visible in all existing shells."
} finally {
    Remove-Item $Temp -Recurse -Force -ErrorAction SilentlyContinue
}
