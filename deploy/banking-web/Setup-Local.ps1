[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$examplePath = Join-Path $PSScriptRoot '.env.example'
$targetPath = Join-Path $PSScriptRoot '.env'
if (-not (Test-Path -LiteralPath $targetPath)) {
    # CreateNew prevents overwriting an existing file, including races.
    $bytes = [IO.File]::ReadAllBytes($examplePath)
    $stream = [IO.File]::Open($targetPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try { $stream.Write($bytes, 0, $bytes.Length) } finally { $stream.Dispose() }
}

Write-Output 'Banking Web local configuration is ready. No credentials were created or modified.'
