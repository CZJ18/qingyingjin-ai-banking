param()
$ErrorActionPreference = 'Stop'
$taskServiceRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$taskRepoRoot = [IO.Path]::GetFullPath((Join-Path $taskServiceRoot '../..'))
$taskOutput = Join-Path $taskServiceRoot 'output/p4'
$env:UV_CACHE_DIR = Join-Path $taskRepoRoot 'output/uv-cache'
$env:UV_LINK_MODE = 'copy'
$env:PYTHONIOENCODING = 'utf-8'
New-Item -ItemType Directory -Force -Path $taskOutput | Out-Null
function Invoke-LoggedCommand {
    param([string]$Command, [string[]]$Arguments, [string]$Log)
    # Windows PowerShell 5 treats redirected native stderr as ErrorRecord.
    # Docker progress uses stderr even on success; inspect the actual exit code.
    $taskPreviousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = 'Continue'
        & $Command @Arguments 2>&1 | Out-File -Encoding utf8 -FilePath $Log
        return $LASTEXITCODE
    } finally { $ErrorActionPreference = $taskPreviousPreference }
}
Push-Location -LiteralPath $taskRepoRoot
try {
    & python -m uv run --locked --project services/banking-agent-service python services/banking-agent-service/scripts/export_openapi.py
    if ($LASTEXITCODE) { throw 'OpenAPI export failed' }
    & powershell -NoProfile -File services/banking-agent-service/scripts/Run-Checks.ps1 -Integration
    if ($LASTEXITCODE) { throw 'Checks failed; integration configuration is required' }
    $taskBuildExit = Invoke-LoggedCommand -Command 'docker' -Arguments @('compose','--env-file','deploy/banking-agent/.env','-f','deploy/banking-agent/compose.yaml','-f','deploy/banking-agent/compose.test.yaml','build','banking-agent-service') -Log (Join-Path $taskOutput 'build.log')
    $taskImageId = if ($taskBuildExit -eq 0) { & docker image inspect qingrongjin-banking-agent-service:p4 --format '{{.Id}}' } else { $null }
    @{exit_code=$taskBuildExit;image_id=$taskImageId;time=(Get-Date).ToString('o')} | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $taskOutput 'build.json')
    if ($taskBuildExit) { throw 'Docker build failed; see build.log' }
    & docker compose --env-file deploy/banking-agent/.env -f deploy/banking-agent/compose.yaml -f deploy/banking-agent/compose.test.yaml up -d --no-build banking-agent-service
    if ($LASTEXITCODE) { throw 'Docker deployment failed' }
    & python -m uv run --locked --project services/banking-agent-service python services/banking-agent-service/scripts/verify_live.py
    if ($LASTEXITCODE) { throw 'Live verification failed' }
    & python -m uv run --locked --project services/banking-agent-service python services/banking-agent-service/scripts/verify_restart.py
    if ($LASTEXITCODE) { throw 'Actual restart verification failed' }
    $taskP3Exit = Invoke-LoggedCommand -Command 'python' -Arguments @('-X','utf8','services/banking-service/scripts/verify_model_live.py','--restart') -Log (Join-Path $taskOutput 'p3-regression.log')
    @{exit_code=$taskP3Exit;time=(Get-Date).ToString('o')} | ConvertTo-Json | Set-Content -Encoding utf8 (Join-Path $taskOutput 'p3-regression.json')
    if ($taskP3Exit) { throw 'P3 persisted results regression failed' }
    & python -m uv run --locked --project services/banking-agent-service python services/banking-agent-service/scripts/record_delivery.py
    if ($LASTEXITCODE) { throw 'Delivery evidence validation failed' }
} finally { Pop-Location }
