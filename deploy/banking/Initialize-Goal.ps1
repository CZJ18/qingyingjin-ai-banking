param([string]$UserID='42',[string]$BaseURL='http://127.0.0.1:8090')
$ErrorActionPreference='Stop'
$serviceRoot=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../services/banking-service'))
$envFile=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '.env.local'))
Push-Location -LiteralPath $serviceRoot
try {
    $userToken = & go run -buildvcs=false ./cmd/token -env-file $envFile -user-id $UserID
    if ($LASTEXITCODE -ne 0) { throw 'Cannot issue local test user credential' }
    $headers=@{Authorization=('Bearer '+$userToken.Trim());'Idempotency-Key'='module3-example-goal-v1';'X-Trace-ID'='module3-example-initialization'}
    $body=@{
        text='未来三个月，我想保持规律饮食和运动，每月储蓄200元，同时不影响正常生活。'
        financial_context=@{monthly_income_minor=500000;spending_budget_minor=400000;essential_expenses_minor=200000}
    } | ConvertTo-Json -Depth 8
    # Registers the simulated identity only; no account/funding operations.
    Invoke-RestMethod -Method Post -Uri ($BaseURL+'/api/v1/users/me') -Headers $headers -ContentType 'application/json' -Body '{"display_name":"Goal compiler simulation"}' | Out-Null
    Invoke-RestMethod -Method Post -Uri ($BaseURL+'/api/v1/goals') -Headers $headers -ContentType 'application/json; charset=utf-8' -Body ([System.Text.Encoding]::UTF8.GetBytes($body))
} finally { Pop-Location; $userToken=$null }
