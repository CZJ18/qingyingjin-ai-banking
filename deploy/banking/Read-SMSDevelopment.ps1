[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][ValidatePattern('^(\+86)?1[3-9][0-9]{9}$')][string]$Phone,
    [Parameter(Mandatory=$true)][ValidateSet('register','verify_phone','password_reset')][string]$Purpose
)
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$repoRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
Push-Location -LiteralPath (Join-Path $repoRoot 'services/banking-service')
try {
    # Prints a short-lived local development code only on explicit invocation.
    # Never configure this command as an Agent Tool or public HTTP endpoint.
    & go run -buildvcs=false ./cmd/sms-inbox -env-file ../../deploy/banking-postgres/finance.app.env.local -env-file ../../deploy/banking/sms.env.local -phone $Phone -purpose $Purpose -allow-development-code
    if ($LASTEXITCODE -ne 0) { throw 'Cannot read local development code; send a fresh code from the webpage and check its purpose.' }
} finally { Pop-Location }
