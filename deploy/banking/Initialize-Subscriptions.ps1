param([string]$UserID='42',[string]$BaseURL='http://127.0.0.1:8090')
$ErrorActionPreference='Stop'
$serviceRoot=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../services/banking-service'))
$envFile=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '.env.local'))
Push-Location -LiteralPath $serviceRoot
try {
    $userToken=& go run -buildvcs=false ./cmd/token -env-file $envFile -user-id $UserID
    if ($LASTEXITCODE -ne 0){throw 'Cannot issue simulation user credential'}
    $headers=@{Authorization=('Bearer '+$userToken.Trim());'X-Trace-ID'='module5-fixture';'X-Task-ID'='module5-demo'}
    function Invoke-SimulationAPI([string]$Path,[hashtable]$Body,[string]$Key) {
        $headers['Idempotency-Key']=$Key
        $json=$Body | ConvertTo-Json -Depth 12 -Compress
        Invoke-RestMethod -Method Post -Uri ($BaseURL+$Path) -Headers $headers -ContentType 'application/json; charset=utf-8' -Body ([System.Text.Encoding]::UTF8.GetBytes($json))
    }
    Invoke-SimulationAPI '/api/v1/users/me' @{display_name='Subscription simulation'} '' | Out-Null
    $account=Invoke-SimulationAPI '/api/v1/simulation/accounts' @{display_name='Module5 subscription evidence';initial_balance_minor=0} 'module5-evidence-account-v1'
    $dates=@('2026-01-31T12:00:00Z','2026-02-28T12:00:00Z','2026-03-31T12:00:00Z','2026-03-31T12:01:00Z')
    for ($index=0;$index -lt $dates.Count;$index++){
        $amount=if($index -lt 2){2000}else{2200}
        $at=[DateTimeOffset]::Parse($dates[$index]).ToUnixTimeMilliseconds()
        Invoke-SimulationAPI '/api/v1/transactions/import' @{bank_account_id=$account.data.id;external_transaction_id=('module5-payment-'+$index);direction='expense';amount_minor=$amount;category='sports';merchant_name='Module5 Local Gym';occurred_at=$at;effect_mode='record_only'} ('module5-payment-'+$index+'-v1') | Out-Null
    }
    Invoke-SimulationAPI '/api/v1/subscriptions/detect_subscriptions' @{from='2026-01-01T00:00:00Z';to='2026-04-01T00:00:00Z';account_id=$account.data.id} ''
    # Stops at evidence/candidates. No adoption, approval or cancellation.
} finally {Pop-Location;$userToken=$null;$headers=$null}
