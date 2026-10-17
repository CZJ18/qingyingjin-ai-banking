param([switch]$Integration, [string]$OutputDirectory='output/p4')
$ErrorActionPreference = 'Stop'
$serviceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$taskRepoRoot = [IO.Path]::GetFullPath((Join-Path $serviceRoot '../..'))
$env:UV_CACHE_DIR = Join-Path $taskRepoRoot 'output/uv-cache'
$env:UV_LINK_MODE = 'copy'
$env:PYTHONIOENCODING = 'utf-8'
if ($Integration) {
    $taskLocalEnv = Join-Path $taskRepoRoot 'deploy/banking-agent/.env.local'
    $taskValues = @{}
    foreach ($line in [IO.File]::ReadAllLines($taskLocalEnv)) {
        if ($line.StartsWith('#') -or -not $line.Contains('=')) { continue }
        $pair = $line.Split('=', 2)
        $taskValues[$pair[0]] = $pair[1]
    }
    $env:BANKING_AGENT_TEST_DATABASE_URL = $taskValues['BANKING_AGENT_DATABASE_URL']
    $env:BANKING_AGENT_TEST_ADMIN_URL = $taskValues['BANKING_AGENT_MIGRATION_URL']
}
Push-Location -LiteralPath $serviceRoot
try {
    New-Item -ItemType Directory -Force -Path $OutputDirectory | Out-Null
    & python -m uv run --locked ruff check . *> (Join-Path $OutputDirectory 'ruff.log')
    $taskRuffExit = $LASTEXITCODE
    & python -m uv run --locked pytest -q ("--junitxml=" + (Join-Path $OutputDirectory 'pytest.xml')) *> (Join-Path $OutputDirectory 'pytest.log')
    $taskTestExit = $LASTEXITCODE
    & python -m uv run --locked python -m compileall -q src scripts
    $taskCompileExit = $LASTEXITCODE
    @{ruff_exit=$taskRuffExit;test_exit=$taskTestExit;compile_exit=$taskCompileExit;integration=$Integration.IsPresent;time=(Get-Date).ToString('o')} | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $OutputDirectory 'checks.json')
    Get-Content (Join-Path $OutputDirectory 'ruff.log') -Tail 12
    Get-Content (Join-Path $OutputDirectory 'pytest.log') -Tail 14
    exit ([Math]::Max($taskRuffExit, [Math]::Max($taskTestExit, $taskCompileExit)))
} finally { Pop-Location }
