$ErrorActionPreference = "Stop"
$Repo = "lompendans/ezNAS"
$InstallDir = if ($env:EZNAS_INSTALL_DIR) { $env:EZNAS_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA "Programs\ezNAS" }
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
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if (($UserPath -split ";") -notcontains $InstallDir) {
        [Environment]::SetEnvironmentVariable("Path", (($UserPath.TrimEnd(";") + ";" + $InstallDir).Trim(";")), "User")
    }
    Write-Host "ezNAS $($Release.tag_name) installed to $InstallDir" -ForegroundColor Green
    Write-Host "Open a new terminal, then run: eznas"
} finally {
    Remove-Item $Temp -Recurse -Force -ErrorAction SilentlyContinue
}
