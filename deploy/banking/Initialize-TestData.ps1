param([string]$UserID = '42')
$ErrorActionPreference = 'Stop'
$serviceRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../services/banking-service'))
$envFile = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '.env.local'))
Push-Location -LiteralPath $serviceRoot
try {
    & go run -buildvcs=false ./cmd/migrate -env-file $envFile
    if ($LASTEXITCODE -ne 0) { throw 'Database migration failed' }
    & go run -buildvcs=false ./cmd/seed -env-file $envFile -user-id $UserID -confirm-simulated-data
    if ($LASTEXITCODE -ne 0) { throw 'Test data initialization failed' }
} finally { Pop-Location }
