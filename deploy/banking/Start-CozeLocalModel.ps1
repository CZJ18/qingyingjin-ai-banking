param([ValidateSet('1.5b', '3b')][string]$ModelSize = '1.5b')
$ErrorActionPreference = 'Stop'
$bankingRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$modelRelative = if ($ModelSize -eq '3b') { 'output/local-model/3b/qwen-complete.gguf' } else { 'output/local-model/qwen-complete.gguf' }
$expectedHash = if ($ModelSize -eq '3b') { '626b4a6678b86442240e33df819e00132d3ba7dddfe1cdc4fbb18e0a9615c62d' } else { '6a1a2eb6d15622bf3c96857206351ba97e1af16c30d7a74ee38970e434e9407e' }
$modelFile = Join-Path $bankingRoot $modelRelative
$binary = Join-Path $bankingRoot 'output/local-model/bin/llama-server.exe'
$statePath = Join-Path $bankingRoot 'output/coze-local-private.json'
if (!(Test-Path -LiteralPath $modelFile) -or !(Test-Path -LiteralPath $binary)) { throw 'Download the official CPU binary and verified Qwen model first.' }
if ((Get-FileHash -LiteralPath $modelFile -Algorithm SHA256).Hash.ToLowerInvariant() -ne $expectedHash) { throw 'Model SHA256 mismatch' }
$state = Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
if (Get-NetTCPConnection -LocalPort 11435 -State Listen -ErrorAction SilentlyContinue) {
  throw 'Port 11435 already has a listener. Reuse the running model or stop that specific process before restarting.'
}
if (!$state.PSObject.Properties['modelConnection']) {
  $keyBytes = [byte[]]::new(32)
  [Security.Cryptography.RandomNumberGenerator]::Fill($keyBytes)
  $state | Add-Member -NotePropertyName modelConnection -NotePropertyValue @{
    base_url = 'http://host.docker.internal:11435/v1'
    api_key = [Convert]::ToHexString($keyBytes).ToLowerInvariant()
    model = 'qwen2.5:1.5b'
  }
  [IO.File]::WriteAllText($statePath, ($state | ConvertTo-Json -Depth 15))
}
$state.modelConnection.model = 'qwen2.5:' + $ModelSize
[IO.File]::WriteAllText($statePath, ($state | ConvertTo-Json -Depth 15))
$modelArgs = @('-m', ('"' + $modelFile + '"'), '--alias', $state.modelConnection.model, '--host', '0.0.0.0', '--port', '11435', '--api-key', $state.modelConnection.api_key, '--ctx-size', '8192', '--threads', '8', '--parallel', '1', '--n-gpu-layers', '0', '--no-webui')
$process = Start-Process -FilePath $binary -ArgumentList $modelArgs -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $bankingRoot 'output/local-model/server.stdout.log') -RedirectStandardError (Join-Path $bankingRoot 'output/local-model/server.stderr.log')
Write-Output ('Local Qwen CPU server started, PID ' + $process.Id + '; API key remains in ignored local state.')
