param([Parameter(Mandatory = $true)][string]$LanAddress, [ValidateRange(1024,65535)][int]$Port = 8089)
$ErrorActionPreference = 'Stop'
$taskIp = $null
if (-not [Net.IPAddress]::TryParse($LanAddress, [ref]$taskIp) -or $taskIp.AddressFamily -ne [Net.Sockets.AddressFamily]::InterNetwork) {
    throw 'Use the private IPv4 address of this computer.'
}
$taskBytes = $taskIp.GetAddressBytes()
if (-not ($taskBytes[0] -eq 10 -or ($taskBytes[0] -eq 192 -and $taskBytes[1] -eq 168) -or
    ($taskBytes[0] -eq 172 -and $taskBytes[1] -ge 16 -and $taskBytes[1] -le 31))) { throw 'Only private LAN IPv4 is supported.' }
if (-not (Get-NetIPAddress -AddressFamily IPv4 | Where-Object IPAddress -eq $LanAddress)) { throw 'Address is not assigned to this computer.' }
$taskOldIp = $env:ANDROID_BIND_IP; $taskOldPort = $env:ANDROID_PORT
try {
    $env:ANDROID_BIND_IP = $LanAddress; $env:ANDROID_PORT = "$Port"
    docker compose -f "$PSScriptRoot\compose.yaml" up -d --no-build
    if ($LASTEXITCODE -ne 0) { throw 'Mobile entry startup failed' }
    Write-Output "Android server address: http://$($LanAddress):$Port"
} finally { $env:ANDROID_BIND_IP = $taskOldIp; $env:ANDROID_PORT = $taskOldPort }
