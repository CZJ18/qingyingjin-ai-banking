[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$deploymentRoot = [IO.Path]::GetFullPath($PSScriptRoot)
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $deploymentRoot '..\..'))
$envPath = Join-Path $deploymentRoot '.env'
$localEnvPath = Join-Path $deploymentRoot '.env.local'
$utf8 = [Text.UTF8Encoding]::new($false)

if (-not (Test-Path -LiteralPath (Join-Path $repositoryRoot 'services\banking-agent-service\Dockerfile') -PathType Leaf)) {
    throw 'Cannot locate the banking-agent-service Dockerfile relative to this script.'
}

function New-IndependentPassword {
    $bytes = [byte[]]::new(32)
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($bytes)
        return ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
    } finally {
        $random.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

function Write-NewEnvironmentFile {
    param([string] $Path, [string[]] $Lines)

    # CreateNew refuses to replace an existing file, including a concurrent creation.
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $content = $utf8.GetBytes(($Lines -join "`n") + "`n")
        $stream.Write($content, 0, $content.Length)
    } finally {
        $stream.Dispose()
    }
}

function Read-EnvironmentValue {
    param([string] $Path, [string] $Name)

    foreach ($line in [IO.File]::ReadAllLines($Path)) {
        $trimmed = $line.Trim()
        if ($trimmed.StartsWith('#') -or -not $trimmed.Contains('=')) { continue }
        $separator = $trimmed.IndexOf('=')
        if ($trimmed.Substring(0, $separator).Trim() -ne $Name) { continue }
        $value = $trimmed.Substring($separator + 1).Trim()
        if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        if ([string]::IsNullOrWhiteSpace($value)) { break }
        return $value
    }
    throw "The existing environment file must define $Name. Existing files were preserved."
}

if (-not (Test-Path -LiteralPath $envPath)) {
    $adminPassword = New-IndependentPassword
    $appPassword = New-IndependentPassword
    $redisPassword = New-IndependentPassword
    $adminUrlPassword = [Uri]::EscapeDataString($adminPassword)
    $appUrlPassword = [Uri]::EscapeDataString($appPassword)
    $redisUrlPassword = [Uri]::EscapeDataString($redisPassword)
    Write-NewEnvironmentFile -Path $envPath -Lines @(
        '# Local credentials generated once. Keep this file private.'
        "BANKING_AGENT_POSTGRES_ADMIN_PASSWORD=$adminPassword"
        "BANKING_AGENT_POSTGRES_APP_PASSWORD=$appPassword"
        "BANKING_AGENT_REDIS_PASSWORD=$redisPassword"
        "BANKING_AGENT_MIGRATION_URL=postgresql://postgres:${adminUrlPassword}@banking-agent-postgres:5432/qingrongjin_agent"
        "BANKING_AGENT_DATABASE_URL=postgresql://banking_agent_app:${appUrlPassword}@banking-agent-postgres:5432/qingrongjin_agent"
        "BANKING_AGENT_REDIS_URL=redis://:${redisUrlPassword}@banking-agent-redis:6379/0"
        'BANKING_AGENT_BANKING_URL=http://banking-service:8090'
    )
}

if (-not (Test-Path -LiteralPath $localEnvPath)) {
    $adminUrlPassword = [Uri]::EscapeDataString((Read-EnvironmentValue -Path $envPath -Name 'BANKING_AGENT_POSTGRES_ADMIN_PASSWORD'))
    $appUrlPassword = [Uri]::EscapeDataString((Read-EnvironmentValue -Path $envPath -Name 'BANKING_AGENT_POSTGRES_APP_PASSWORD'))
    $redisUrlPassword = [Uri]::EscapeDataString((Read-EnvironmentValue -Path $envPath -Name 'BANKING_AGENT_REDIS_PASSWORD'))
    Write-NewEnvironmentFile -Path $localEnvPath -Lines @(
        '# Host integration connections. Use compose.yaml with compose.test.yaml.'
        "BANKING_AGENT_MIGRATION_URL=postgresql://postgres:${adminUrlPassword}@127.0.0.1:15432/qingrongjin_agent"
        "BANKING_AGENT_DATABASE_URL=postgresql://banking_agent_app:${appUrlPassword}@127.0.0.1:15432/qingrongjin_agent"
        "BANKING_AGENT_REDIS_URL=redis://:${redisUrlPassword}@127.0.0.1:16380/0"
        'BANKING_AGENT_BANKING_URL=http://127.0.0.1:8090'
        'BANKING_AGENT_API_URL=http://127.0.0.1:8091'
    )
}

Write-Output "Environment file (existing contents preserved): $envPath"
Write-Output "Host integration environment (existing contents preserved): $localEnvPath"
Write-Output 'For P5, run Setup-P5.ps1 to synchronize the independent Banking bridge credential.'
Write-Output "docker compose --env-file `"$envPath`" -f `"$(Join-Path $deploymentRoot 'compose.yaml')`" up --build -d"
Write-Output "docker compose --env-file `"$envPath`" -f `"$(Join-Path $deploymentRoot 'compose.yaml')`" -f `"$(Join-Path $deploymentRoot 'compose.test.yaml')`" up --build -d"
