[CmdletBinding(SupportsShouldProcess=$true, ConfirmImpact='Medium')]
param([switch]$IncludeDownloadCache)
$ErrorActionPreference='Stop'
$taskRepoRoot=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$taskModelRoot=[IO.Path]::GetFullPath((Join-Path $taskRepoRoot 'output/local-model'))
if (!$taskModelRoot.StartsWith($taskRepoRoot+[IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Invalid model directory' }
$taskNames=@('qwen-complete.gguf','qwen-cpu.gguf','qwen2.5-1.5b-instruct-q4_k_m.gguf')
if ($IncludeDownloadCache) { $taskNames += @('parts','parts4') }
$taskTargets=@()
foreach ($taskName in $taskNames) {
    $taskPath=[IO.Path]::GetFullPath((Join-Path $taskModelRoot $taskName))
    if (!$taskPath.StartsWith($taskModelRoot+[IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Target is outside the model directory' }
    if (!(Test-Path -LiteralPath $taskPath)) { continue }
    $taskItem=Get-Item -LiteralPath $taskPath -Force
    if (($taskItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Model deletion cannot follow links' }
    if ($taskItem.PSIsContainer) {
        foreach ($taskChild in @(Get-ChildItem -LiteralPath $taskPath -Force -Recurse)) {
            if ($taskChild.PSIsContainer -or ($taskChild.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0 -or $taskChild.Name -notmatch '^\d+\.(bin|headers)(\.tail)?$') { throw 'Unexpected model cache content' }
            if ($taskChild.Name -match '\.headers$' -and [IO.File]::ReadAllText($taskChild.FullName) -notmatch 'content-range:\s*bytes\s+\d+-\d+/1117320736') { throw 'Cache is not the verified 1.5B model' }
        }
    }
    $taskTargets += $taskItem
}
# All paths are checked before any removal; shared binaries and the 3B directory are outside this list.
foreach ($taskItem in $taskTargets) {
    if ($PSCmdlet.ShouldProcess($taskItem.FullName, 'Delete the requested local Qwen2.5-1.5B model')) {
        if ($taskItem.PSIsContainer) { Remove-Item -LiteralPath $taskItem.FullName -Recurse -Force }
        else { Remove-Item -LiteralPath $taskItem.FullName -Force }
    }
}
[pscustomobject]@{
    what_if=$WhatIfPreference
    model_files_remaining=@($taskNames | Where-Object { Test-Path -LiteralPath (Join-Path $taskModelRoot $_) })
} | ConvertTo-Json -Compress
