param([ValidateSet('Prepare', 'Verify', 'Apply')][string]$Mode = 'Prepare')
$ErrorActionPreference = 'Stop'
# Credentials are read by Node from the ignored file, never from command-line arguments.
$bankingScript = Join-Path $PSScriptRoot 'configure-coze-deepseek.mjs'
& node $bankingScript $Mode
if ($LASTEXITCODE -ne 0) { throw 'DeepSeek configuration did not complete; see the preceding sanitized error code.' }
