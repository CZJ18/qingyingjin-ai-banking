[CmdletBinding()]
param(
    [string] $ImageTag = 'qingrongjin-banking-agent-service:p7',
    [string] $RuntimeBaseImage = 'qingrongjin-banking-agent-service:p5'
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$sourceRoot = Join-Path $repositoryRoot 'services\banking-agent-service'
$contextPath = Join-Path ([IO.Path]::GetTempPath()) ('qingrongjin-agent-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $contextPath | Out-Null
# Only public application code enters this ASCII build context. Private .env
# files and the host virtualenv are excluded, rather than copied then deleted.
Copy-Item -LiteralPath (Join-Path $sourceRoot 'src') -Destination $contextPath -Recurse
Copy-Item -LiteralPath (Join-Path $sourceRoot 'migrations') -Destination $contextPath -Recurse
Copy-Item -LiteralPath (Join-Path $sourceRoot 'Dockerfile.runtime') -Destination (Join-Path $contextPath 'Dockerfile')
[IO.File]::WriteAllText((Join-Path $contextPath '.dockerignore'), "**/__pycache__`n**/*.pyc`n", [Text.UTF8Encoding]::new($false))
& docker build --build-arg "BANKING_AGENT_RUNTIME_BASE=$RuntimeBaseImage" -t $ImageTag $contextPath
if ($LASTEXITCODE -ne 0) { throw 'Agent source patch image build failed; no containers changed.' }
Write-Output "Built $ImageTag with unchanged base dependencies; public context retained at $contextPath"
