param([string]$ToolUserId = '42', [string]$CozeUserId = '')
$ErrorActionPreference = 'Stop'
if ($ToolUserId -notmatch '^[1-9][0-9]*$' -or ($CozeUserId -ne '' -and $CozeUserId -notmatch '^[1-9][0-9]*$')) { throw 'Use positive, operator-verified user IDs.' }
$targetPath = Join-Path $PSScriptRoot '.env'
$localPath = Join-Path $PSScriptRoot '.env.local'
if ((Test-Path -LiteralPath $targetPath) -or (Test-Path -LiteralPath $localPath)) { throw 'Environment files already exist; refusing to overwrite credentials.' }
function New-Secret {
    $secretBytes = New-Object byte[] 32
    $random = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $random.GetBytes($secretBytes) } finally { $random.Dispose() }
    return [System.BitConverter]::ToString($secretBytes).Replace('-', '').ToLowerInvariant()
}
$content = Get-Content -LiteralPath (Join-Path $PSScriptRoot '.env.example') -Raw
$replacements = @{
    'CHANGE_ME_MYSQL_PASSWORD' = (New-Secret)
    'CHANGE_ME_ROOT_PASSWORD' = (New-Secret)
    'CHANGE_ME_REDIS_PASSWORD' = (New-Secret)
    'CHANGE_ME_USER_SECRET_AT_LEAST_32_BYTES' = (New-Secret)
    'CHANGE_ME_TOOL_SECRET_AT_LEAST_32_BYTES' = (New-Secret)
}
foreach ($placeholder in $replacements.Keys) { $content = $content.Replace($placeholder, $replacements[$placeholder]) }
$content = $content.Replace('BANKING_TOOL_USER_ID=42', "BANKING_TOOL_USER_ID=$ToolUserId")
$content = $content.Replace("BANKING_TOOL_COZE_USER_ID=", "BANKING_TOOL_COZE_USER_ID=$CozeUserId")
$localContent = $content.Replace('BANKING_HTTP_ADDR=0.0.0.0:8090','BANKING_HTTP_ADDR=127.0.0.1:8090').Replace('BANKING_MYSQL_HOST=banking-mysql','BANKING_MYSQL_HOST=127.0.0.1').Replace('BANKING_MYSQL_PORT=3306','BANKING_MYSQL_PORT=13306').Replace('BANKING_REDIS_ADDR=banking-redis:6379','BANKING_REDIS_ADDR=127.0.0.1:16379')
[System.IO.File]::WriteAllText($targetPath, $content)
[System.IO.File]::WriteAllText($localPath, $localContent)
Write-Output 'Created Banking .env and .env.local; credentials were not printed.'
