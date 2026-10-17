[CmdletBinding()]
param(
    [string] $BaseUrl = 'http://127.0.0.1:8088',
    [string] $OutputPath = '',
    [string] $ContainerName = 'qingrongjin-web-banking-web-1'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Net.Http

$origin = [Uri]$BaseUrl
if (-not $origin.IsAbsoluteUri -or -not $origin.IsLoopback -or $origin.Scheme -ne 'http' -or $origin.AbsolutePath -ne '/') {
    throw 'This simulation deployment verifier requires an HTTP loopback origin with no path.'
}

$handler = [Net.Http.HttpClientHandler]::new()
$handler.AllowAutoRedirect = $false
$client = [Net.Http.HttpClient]::new($handler)
$client.Timeout = [TimeSpan]::FromSeconds(8)
$checks = [Collections.Generic.List[object]]::new()

function Get-PublicResponse {
    param([string] $Path)
    $response = $client.GetAsync([Uri]::new($origin, $Path)).GetAwaiter().GetResult()
    try {
        $body = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        $headers = @{}
        foreach ($header in $response.Headers) { $headers[$header.Key.ToLowerInvariant()] = $header.Value -join ', ' }
        foreach ($header in $response.Content.Headers) { $headers[$header.Key.ToLowerInvariant()] = $header.Value -join ', ' }
        return @{ status = [int]$response.StatusCode; body = $body; headers = $headers }
    } finally { $response.Dispose() }
}

function Assert-Check {
    param([string] $Name, [bool] $Condition)
    $checks.Add(@{name=$Name; passed=$Condition})
    if (-not $Condition) { throw "Web verification failed: $Name" }
}

$failure = ''
try {
    $page = Get-PublicResponse '/agents'
    Assert-Check 'SPA history route serves built Vue index' ($page.status -eq 200 -and $page.body.Contains('<div id="app"></div>') -and $page.body.Contains('/assets/'))
    Assert-Check 'CSP restricts scripts and network to same origin' ($page.headers['content-security-policy'].Contains("script-src 'self'") -and $page.headers['content-security-policy'].Contains("connect-src 'self'"))
    Assert-Check 'HTML is not cached' ($page.headers['cache-control'].Contains('no-store'))
    Assert-Check 'Frame and MIME protections' ($page.headers['x-frame-options'] -eq 'DENY' -and $page.headers['x-content-type-options'] -eq 'nosniff')

    foreach ($path in @('/web-healthz', '/healthz', '/readyz', '/product/healthz', '/product/readyz')) {
        $response = Get-PublicResponse $path
        Assert-Check ("GET $path returns 200") ($response.status -eq 200)
    }

    $goApi = Get-PublicResponse '/openapi.yaml'
    Assert-Check 'Go OpenAPI is proxied, not SPA' ($goApi.status -eq 200 -and $goApi.body.Contains('openapi:') -and -not $goApi.body.Contains('<div id="app">'))
    $productApi = Get-PublicResponse '/product/openapi.json'
    $document = ConvertFrom-Json $productApi.body
    Assert-Check 'Product OpenAPI contains current LangGraph task route' ($productApi.status -eq 200 -and $null -ne $document.paths.PSObject.Properties['/api/v1/agent-tasks'])

    foreach ($path in @('/api/v1/accounts', '/product/api/v1/me')) {
        $response = Get-PublicResponse $path
        Assert-Check ("Anonymous GET $path is rejected by backend") ($response.status -eq 401 -and -not $response.body.Contains('<div id="app">'))
    }
    foreach ($path in @('/api/unregistered', '/api/v1/unregistered', '/product/unregistered', '/product/api/v1/unregistered', '/assets/missing.js', '/.env')) {
        $response = Get-PublicResponse $path
        Assert-Check ("GET $path remains 404, not SPA") ($response.status -eq 404 -and -not $response.body.Contains('<div id="app">'))
    }

    # Inspect only selected immutable/runtime fields; never return container env.
    $raw = & docker inspect --format '{{json .}}' $ContainerName
    if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect the expected Web container.' }
    $container = ConvertFrom-Json ($raw -join "`n")
    Assert-Check 'Container uses non-root user 101' ($container.Config.User -eq '101:101')
    Assert-Check 'Runtime root filesystem is read-only' ([bool]$container.HostConfig.ReadonlyRootfs)
    Assert-Check 'All Linux capabilities dropped' ($container.HostConfig.CapDrop -contains 'ALL')
    Assert-Check 'No-new-privileges enabled' (($container.HostConfig.SecurityOpt -join ' ').Contains('no-new-privileges'))
    $environmentNames = @($container.Config.Env | ForEach-Object { ($_ -split '=', 2)[0] })
    Assert-Check 'Web runtime has no banking/model credential environment' (-not ($environmentNames | Where-Object { $_ -match '(?:^|_)(?:TOKEN|SECRET|PASSWORD|PAT|JWT|DEEPSEEK|API_KEY|SERVICE_KEY)(?:_|$)' }))
    Assert-Check 'No database or bind-mounted data volume' (@($container.Mounts | Where-Object { $_.Type -ne 'tmpfs' }).Count -eq 0)
} catch {
    # Static fixed failure names contain no response bodies or credentials.
    $failure = if ($_.Exception.Message.StartsWith('Web verification failed:')) { $_.Exception.Message } else { 'Web deployment check could not complete; inspect service health and commands.' }
} finally {
    $client.Dispose()
    $handler.Dispose()
}

$result = @{base_url=$BaseUrl; checks=@($checks.ToArray()); passed=($failure -eq ''); failure=$failure; methods=@('GET', 'docker inspect'); financial_writes=$false}
$json = ConvertTo-Json $result -Depth 8
if ($OutputPath) {
    $absolute = [IO.Path]::GetFullPath($OutputPath)
    [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($absolute)) | Out-Null
    [IO.File]::WriteAllText($absolute, $json + "`n", [Text.UTF8Encoding]::new($false))
}
Write-Output $json
if ($failure) { exit 1 }
