# ezNAS Windows Network + SMB setup
# Run from an elevated PowerShell terminal. This uses Windows' built-in SMB server.
$ErrorActionPreference = "Stop"
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw "Please reopen PowerShell with Run as administrator, then run this script again." }
$identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
$configPath = if ($env:EZNAS_CONFIG) { $env:EZNAS_CONFIG } else { Join-Path $env:APPDATA "eznas\config.json" }
$configDir = Split-Path -Parent $configPath
New-Item -ItemType Directory -Path $configDir -Force | Out-Null
$config = @{ host = "0.0.0.0"; port = 8080; storage = (Join-Path $PSScriptRoot "eznas-data") }
if (Test-Path $configPath) {
    try {
        $loaded = Get-Content -Raw -Path $configPath | ConvertFrom-Json
        if ($loaded.host) { $config.host = [string]$loaded.host }
        if ($loaded.port) { $config.port = [int]$loaded.port }
        if ($loaded.storage) {
            $existingStorage = [string]$loaded.storage
            if ([IO.Path]::IsPathRooted($existingStorage)) { $config.storage = $existingStorage }
            else {
                Write-Host "Current storage path is relative: $existingStorage"
                $resolved = Read-Host "Enter its intended absolute path (or enter a new storage path)"
                if (-not $resolved) { throw "An absolute storage path is required for SMB sharing." }
                $config.storage = $resolved
            }
        }
        foreach ($key in @("password_hash", "password_salt")) {
            if ($loaded.PSObject.Properties.Name -contains $key) { $config[$key] = $loaded.$key }
        }
    } catch { throw "Could not read ezNAS config at $configPath. Fix the JSON before enabling sharing. $($_.Exception.Message)" }
}
Write-Host ""
Write-Host "ezNAS Network + File Explorer setup" -ForegroundColor Cyan
Write-Host "Windows account that will receive share access: $identity"
Write-Host "The SMB login uses your Windows account credentials, not the ezNAS web password."
Write-Host "Current storage path: $($config.storage)"
$storageInput = Read-Host "Storage folder (press Enter to keep this path)"
if ($storageInput) { $config.storage = $storageInput }
if (-not [IO.Path]::IsPathRooted([string]$config.storage)) { throw "Storage path must be absolute. Run setup again and enter an absolute path." }
$storagePath = [IO.Path]::GetFullPath([string]$config.storage)
New-Item -ItemType Directory -Path $storagePath -Force | Out-Null
if (-not (Test-Path $storagePath -PathType Container)) { throw "Storage directory is not available: $storagePath" }
$portInput = Read-Host "Web port (default $($config.port))"
if ($portInput) {
    $parsedPort = 0
    if (-not [int]::TryParse($portInput, [ref]$parsedPort) -or $parsedPort -lt 1 -or $parsedPort -gt 65535) { throw "Port must be between 1 and 65535." }
    $config.port = $parsedPort
}
$config.storage = $storagePath
$config.host = "0.0.0.0"
$config | ConvertTo-Json -Depth 4 | Set-Content -Path $configPath -Encoding UTF8
Write-Host "Saved configuration: $configPath"
foreach ($serviceName in @("FDResPub", "fdPHost")) {
    $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
    if ($service) {
        Set-Service -Name $serviceName -StartupType Automatic
        if ($service.Status -ne "Running") { Start-Service -Name $serviceName }
    } else { Write-Warning "Windows discovery service $serviceName is not present on this edition." }
}
$rules = @(
    @{ Name = "ezNAS-SMB-Private"; Display = "ezNAS SMB (Private LAN only)"; Protocol = "TCP"; Port = "445" },
    @{ Name = "ezNAS-WSD-Private"; Display = "ezNAS WS-Discovery (Private LAN only)"; Protocol = "UDP"; Port = "3702" },
    @{ Name = "ezNAS-WSD-Metadata-Private"; Display = "ezNAS WSD metadata (Private LAN only)"; Protocol = "TCP"; Port = "5357" }
)
foreach ($rule in $rules) {
    Get-NetFirewallRule -Name $rule.Name -ErrorAction SilentlyContinue | Remove-NetFirewallRule
    New-NetFirewallRule -Name $rule.Name -DisplayName $rule.Display -Direction Inbound -Action Allow -Protocol $rule.Protocol -LocalPort $rule.Port -RemoteAddress LocalSubnet -Profile Private | Out-Null
}
$shareName = "ezNAS"
$existingShare = Get-SmbShare -Name $shareName -ErrorAction SilentlyContinue
if ($existingShare -and ($existingShare.Path -ne $storagePath)) { throw "An SMB share named '$shareName' already points to '$($existingShare.Path)'. Rename or remove it manually before retrying; no existing share was changed." }
if (-not $existingShare) {
    New-SmbShare -Name $shareName -Path $storagePath -Description "ezNAS file storage" -FullAccess $identity -FolderEnumerationMode AccessBased | Out-Null
} else { Grant-SmbShareAccess -Name $shareName -AccountName $identity -AccessRight Full -Force | Out-Null }
Write-Host ""
Write-Host "Setup complete." -ForegroundColor Green
Write-Host "Web interface: http://localhost:$($config.port)"
Write-Host "SMB share: \\$env:COMPUTERNAME\$shareName"
Write-Host "Only this Windows account was granted share access: $identity"
Write-Host "On other PCs, sign in to SMB with this Windows account's credentials."
Write-Host "If Network discovery is off, set this PC's network profile to Private and enable Network discovery in Windows Settings."
Write-Host "Restart ezNAS to apply any changed storage or port settings. Its first run will print a generated web password if one is not already configured."
