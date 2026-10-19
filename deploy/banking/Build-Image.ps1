param([switch]$LocalBinaries, [string]$ImageTag = "qingyingjin-banking-service:module11", [string]$RuntimeBaseImage = "gcr.io/distroless/static-debian12:nonroot")
$ErrorActionPreference = 'Stop'
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
if ($LocalBinaries) {
    $contextDirectory = Join-Path ([System.IO.Path]::GetTempPath()) ('qingyingjin-runtime-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $contextDirectory | Out-Null
    $oldGOOS = $env:GOOS; $oldGOARCH = $env:GOARCH; $oldCGO = $env:CGO_ENABLED
    Push-Location -LiteralPath (Join-Path $repoRoot 'services/banking-service')
    try {
        $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
        foreach ($command in @('server', 'migrate', 'seed', 'storage-migrate', 'storage-control', 'model-reconcile', 'model-gateway', 'model-spend-reconcile')) {
            $binary = if ($command -eq 'server') { 'banking-service' } else { 'banking-' + $command }
            & go build -buildvcs=false -trimpath -o (Join-Path $contextDirectory $binary) ('./cmd/' + $command)
            if ($LASTEXITCODE -ne 0) { throw "Failed to compile $command" }
        }
        Copy-Item -LiteralPath (Join-Path $repoRoot 'services/banking-service/Dockerfile.runtime') -Destination (Join-Path $contextDirectory 'Dockerfile')
        & docker build --build-arg "BANKING_RUNTIME_BASE=$RuntimeBaseImage" -t $ImageTag $contextDirectory
        if ($LASTEXITCODE -ne 0) { throw 'Runtime image build failed' }
    } finally {
        $env:GOOS = $oldGOOS; $env:GOARCH = $oldGOARCH; $env:CGO_ENABLED = $oldCGO
        Pop-Location
    }
    Write-Output "Built image $ImageTag; reproducible local binaries retained at $contextDirectory"
} else {
    # BuildKit session headers cannot contain this workspace's Chinese path.
    # A temporary drive alias changes neither repository contents nor data.
    $driveLetter = @('Q','R','S','T','U','V','W','X','Y','Z') | Where-Object { -not (Get-PSDrive -Name $_ -ErrorAction SilentlyContinue) } | Select-Object -First 1
    if (-not $driveLetter) { throw 'No free drive letter; use -LocalBinaries' }
    & subst.exe "${driveLetter}:" $repoRoot
    if ($LASTEXITCODE -ne 0) { throw 'Cannot create ASCII path alias' }
    try {
        & docker build -t $ImageTag -f "${driveLetter}:\services\banking-service\Dockerfile" "${driveLetter}:\"
        if ($LASTEXITCODE -ne 0) { throw 'Source image build failed; if Docker Hub unavailable use -LocalBinaries' }
    } finally { & subst.exe "${driveLetter}:" /D }
}
