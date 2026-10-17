[CmdletBinding()]
param(
    [switch] $LocalDist,
    [string] $ImageTag = 'qingrongjin-banking-web:p6',
    [string] $NodeBuildImage = 'node:22-alpine',
    [string] $NginxRuntimeImage = 'nginx:1.28-alpine'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$frontendRoot = Join-Path $repositoryRoot 'frontend\ai-banking'
if (-not (Test-Path -LiteralPath (Join-Path $frontendRoot 'package-lock.json') -PathType Leaf)) {
    throw 'Cannot locate the independent Vue app and package lock.'
}

if ($LocalDist) {
    # Host dependency installation remains explicit (npm ci). This option
    # performs a fresh type check/Vite build and never reuses an old dist.
    Push-Location -LiteralPath $frontendRoot
    try {
        & npm.cmd run build
        if ($LASTEXITCODE -ne 0) { throw 'Vue type check or production build failed.' }
    } finally { Pop-Location }

    $contextPath = Join-Path ([IO.Path]::GetTempPath()) ('qingrongjin-web-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $contextPath | Out-Null
    Copy-Item -LiteralPath (Join-Path $frontendRoot 'dist') -Destination $contextPath -Recurse
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'nginx.conf') -Destination $contextPath
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'Dockerfile.runtime') -Destination (Join-Path $contextPath 'Dockerfile')
    & docker build --build-arg "NGINX_RUNTIME_IMAGE=$NginxRuntimeImage" -t $ImageTag $contextPath
    if ($LASTEXITCODE -ne 0) { throw 'Static runtime image build failed.' }
    Write-Output "Built $ImageTag from a fresh local Vue build; public static-only context retained at $contextPath"
} else {
    Push-Location -LiteralPath $repositoryRoot
    try {
        & docker build --build-arg "NODE_BUILD_IMAGE=$NodeBuildImage" --build-arg "NGINX_RUNTIME_IMAGE=$NginxRuntimeImage" -f deploy/banking-web/Dockerfile -t $ImageTag .
        if ($LASTEXITCODE -ne 0) { throw 'Source image build failed. No containers were changed.' }
    } finally { Pop-Location }
}
