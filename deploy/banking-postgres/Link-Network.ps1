[CmdletBinding()]
param(
    [string] $PostgresContainer = 'qingrongjin-agent-banking-agent-postgres-1',
    [string] $Network = 'qingyingjin_banking-egress'
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if ($PostgresContainer -notmatch '^[a-zA-Z0-9_.-]+$' -or $Network -notmatch '^[a-zA-Z0-9_.-]+$') { throw 'Invalid Docker resource name.' }
$networkName = & docker network inspect --format '{{.Name}}' $Network 2>$null
if ($LASTEXITCODE -ne 0 -or $networkName -ne $Network) { throw 'Existing Banking network was not found; no network was created.' }
$state = & docker inspect --format '{{json .NetworkSettings.Networks}}' $PostgresContainer 2>$null
if ($LASTEXITCODE -ne 0) { throw 'Existing product PostgreSQL container was not found.' }
$networks = $state | ConvertFrom-Json
$present = $null -ne ($networks.PSObject.Properties | Where-Object Name -eq $Network)
if (-not $present) {
    $null = & docker network connect --alias banking-agent-postgres $Network $PostgresContainer 2>$null
    if ($LASTEXITCODE -ne 0) { throw 'Could not add the existing Banking network; original networks were preserved.' }
}
[ordered]@{ linked = $true; already_linked = $present; container = $PostgresContainer; network = $Network; container_restarted = $false } | ConvertTo-Json -Compress
