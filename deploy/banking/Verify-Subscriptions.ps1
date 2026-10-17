param([string]$BaseURL='http://127.0.0.1:8090',[switch]$AfterRestart)
$ErrorActionPreference='Stop'
$repoRoot=[System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$fixture=Get-Content -LiteralPath (Join-Path $repoRoot 'docs/banking-module5-fixture.json') -Raw | ConvertFrom-Json
$candidate=$fixture.data.candidates | Where-Object merchant_name -eq 'Module5 Local Gym' | Select-Object -First 1
if (-not $candidate){throw 'Run Initialize-Subscriptions first'}
$settings=@{}
Get-Content -LiteralPath (Join-Path $PSScriptRoot '.env.local') | ForEach-Object {
    if ($_ -match '^([A-Z][A-Z0-9_]*)=(.*)$'){$settings[$matches[1]]=$matches[2].Trim().Trim('"')}
}
$toolHeaders=@{'X-Banking-Tool-Key'=$settings['BANKING_TOOL_TOKEN'];'X-Trace-ID'='module5-runtime-verification';'X-Task-ID'='module5-runtime-test'}
Push-Location -LiteralPath (Join-Path $repoRoot 'services/banking-service')
try {
    $userToken=& go run -buildvcs=false ./cmd/token -env-file (Join-Path $PSScriptRoot '.env.local') -user-id $fixture.data.user_id
    if ($LASTEXITCODE -ne 0){throw 'Cannot issue simulation user credential'}
    $userHeaders=@{Authorization=('Bearer '+$userToken.Trim())}
    $balanceBefore=Invoke-RestMethod -Uri ($BaseURL+'/api/v1/accounts/'+$candidate.account_id+'/balance') -Headers $userHeaders
    $results=@{}
    foreach($tool in @('detect_subscriptions','get_subscription_details','analyze_subscription_cost','suggest_subscription_actions','create_cancel_intent','get_subscription_status')){
        $body=switch($tool){
            'detect_subscriptions' {@{from='2026-01-01T00:00:00Z';to='2026-04-01T00:00:00Z';account_id=$candidate.account_id}}
            'get_subscription_details' {@{subscription_id=$candidate.subscription_id}}
            'get_subscription_status' {@{subscription_id=$candidate.subscription_id}}
            'create_cancel_intent' {@{subscription_id=$candidate.subscription_id;expected_version=$results['get_subscription_details'].response.data.subscription.version}}
            default {$null}
        }
        $toolHeaders['Idempotency-Key']=[guid]::NewGuid().ToString()
        $request=@{Method='Post';Uri=($BaseURL+'/tools/v1/subscriptions/'+$tool);Headers=$toolHeaders;ContentType='application/json';SkipHttpErrorCheck=$true}
        if($null -ne $body){$request.Body=($body | ConvertTo-Json -Compress)}
        $response=Invoke-WebRequest @request
        $decoded=$response.Content | ConvertFrom-Json
        $expected=if($tool -eq 'create_cancel_intent'){422}else{200}
        if([int]$response.StatusCode -ne $expected){throw ('Unexpected tool status: '+$tool)}
        $results[$tool]=@{http_status=[int]$response.StatusCode;response=$decoded}
    }
    # This script checks the candidate gate, and never adopts or confirms a cancellation.
    if($results['create_cancel_intent'].response.code -ne 'RISK_REJECTED'){throw 'Candidate gate did not reject cancellation'}
     $stored=Invoke-RestMethod -Uri ($BaseURL+'/api/v1/subscriptions/analyses/'+$fixture.data.id) -Headers $userHeaders
    if ($stored.data.id -ne $fixture.data.id){throw 'Persisted analysis missing'}
    $balanceAfter=Invoke-RestMethod -Uri ($BaseURL+'/api/v1/accounts/'+$candidate.account_id+'/balance') -Headers $userHeaders
    if($balanceBefore.data.balance_minor -ne $balanceAfter.data.balance_minor){throw 'Read/proposal tools modified funds'}
    $evidence=@{tools=$results;balance_before=$balanceBefore.data;balance_after=$balanceAfter.data;funds_unchanged=$true;scope='Real Docker API; candidate cancellation correctly rejected; no user adoption/confirmation'}
    $evidence | ConvertTo-Json -Depth 40 | Set-Content -LiteralPath (Join-Path $repoRoot 'docs/banking-module5-docker-api.json') -Encoding utf8
    $health=@{restart_persistence_verified=([bool]$AfterRestart.IsPresent);stored_report_user_id=$stored.data.user_id;healthz=(Invoke-RestMethod ($BaseURL+'/healthz'));readyz=(Invoke-RestMethod ($BaseURL+'/readyz'));persisted_report_id=$fixture.data.id;persisted_subscription_id=$candidate.subscription_id;image_id=(& docker image inspect qingyingjin-banking-service:module5 --format '{{.Id}}')}
    $health | ConvertTo-Json -Depth 15 | Set-Content -LiteralPath (Join-Path $repoRoot 'docs/banking-module5-health.json') -Encoding utf8
    Write-Output 'Docker subscription tools verified; candidate gate rejected cancellation and balances are unchanged.'
} finally {Pop-Location;$userToken=$null;$userHeaders=$null;$toolHeaders=$null;$settings=$null}
