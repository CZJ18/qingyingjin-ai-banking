[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$agentRoot = [IO.Path]::GetFullPath($PSScriptRoot)
$bankingRoot = [IO.Path]::GetFullPath((Join-Path $agentRoot '..\banking'))
$utf8 = [Text.UTF8Encoding]::new($false)
$targets = @(
    @{ Path = (Join-Path $bankingRoot '.env'); Name = 'BANKING_LANGGRAPH_SERVICE_TOKEN' }
    @{ Path = (Join-Path $bankingRoot '.env.local'); Name = 'BANKING_LANGGRAPH_SERVICE_TOKEN' }
    @{ Path = (Join-Path $agentRoot '.env'); Name = 'BANKING_AGENT_SERVICE_KEY' }
    @{ Path = (Join-Path $agentRoot '.env.local'); Name = 'BANKING_AGENT_SERVICE_KEY' }
)

# Banking credentials must already exist. This script never initializes or migrates
# Banking, rotates its JWT/PAT, or touches Docker containers, networks or volumes.
foreach ($target in $targets[0..1]) {
    if (-not (Test-Path -LiteralPath $target.Path -PathType Leaf)) {
        throw 'Initialize Banking .env and .env.local first. No Banking files were created.'
    }
}

# P4 initialization is safe to repeat: existing DB credentials and files survive.
& (Join-Path $agentRoot 'Setup-Local.ps1') | Out-Null

function Read-EnvironmentEntries {
    param([string] $Path)

    $entries = [Collections.Generic.List[object]]::new()
    $lines = [IO.File]::ReadAllLines($Path)
    for ($index = 0; $index -lt $lines.Length; $index++) {
        $trimmed = $lines[$index].Trim()
        if ($trimmed.StartsWith('#') -or -not $trimmed.Contains('=')) { continue }
        $separator = $trimmed.IndexOf('=')
        $name = $trimmed.Substring(0, $separator).Trim()
        $value = $trimmed.Substring($separator + 1).Trim()
        if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) {
            $value = $value.Substring(1, $value.Length - 2)
        }
        $entries.Add(@{ Index = $index; Name = $name; Value = $value })
    }
    return ,$entries
}

function Test-Placeholder {
    param([string] $Value)

    return [string]::IsNullOrWhiteSpace($Value) -or $Value -match '^(CHANGE_ME|replace-with-)'
}

$serviceKey = $null
$existingDocuments = [Collections.Generic.List[object]]::new()
$otherSecrets = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
foreach ($target in $targets) {
    $entries = Read-EnvironmentEntries -Path $target.Path
    $bridgeEntries = @($entries | Where-Object { $_.Name -eq $target.Name })
    if ($bridgeEntries.Count -gt 1) {
        throw "Duplicate $($target.Name) definitions found. Existing files were preserved."
    }
    foreach ($entry in $entries) {
        if ($entry.Name -ne $target.Name -and $entry.Name -match '(SECRET|TOKEN|PASSWORD|PAT|SERVICE_KEY)$' -and -not (Test-Placeholder -Value $entry.Value)) {
            [void] $otherSecrets.Add($entry.Value)
        }
    }
    if ($bridgeEntries.Count -eq 1 -and -not (Test-Placeholder -Value $bridgeEntries[0].Value)) {
        $candidate = $bridgeEntries[0].Value
        if ($candidate.Length -lt 32 -or $candidate -match '[^\x21-\x7e]' -or $candidate.Contains('$') -or $candidate.Contains('#') -or $candidate.Contains('"') -or $candidate.Contains("'")) {
            throw "Invalid $($target.Name). Existing files were preserved; use a separate 32-byte random hexadecimal credential."
        }
        if ($null -ne $serviceKey -and $serviceKey -cne $candidate) {
            throw 'Existing P5 service credentials disagree. Existing files were preserved; resolve the mismatch without rotating DB/JWT/PAT credentials.'
        }
        $serviceKey = $candidate
    }
    $existingDocuments.Add(@{ Target = $target; Entries = $bridgeEntries; Original = [IO.File]::ReadAllText($target.Path) })
}

if ($null -eq $serviceKey) {
    $bytes = [byte[]]::new(32)
    $random = [Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $random.GetBytes($bytes)
        $serviceKey = ([BitConverter]::ToString($bytes)).Replace('-', '').ToLowerInvariant()
    } finally {
        $random.Dispose()
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}
if ($otherSecrets.Contains($serviceKey)) {
    throw 'The P5 bridge credential must be independent of existing DB/JWT/PAT/tool credentials. Existing files were preserved.'
}

foreach ($document in $existingDocuments) {
    $target = $document.Target
    $original = $document.Original
    $newline = if ($original.Contains("`r`n")) { "`r`n" } else { "`n" }
    $line = "$($target.Name)=$serviceKey"
    if ($document.Entries.Count -eq 1) {
        # Update only the new bridge variable; all unrelated bytes remain intact.
        $pattern = '(?m)^[ \t]*' + [Regex]::Escape($target.Name) + '[ \t]*=[^\r\n]*'
        $updated = [Regex]::Replace($original, $pattern, [Text.RegularExpressions.MatchEvaluator]{ param($match) $line })
    } else {
        $separator = if ($original.Length -gt 0 -and -not $original.EndsWith("`n")) { $newline } else { '' }
        $updated = $original + $separator + $line + $newline
    }
    if ($updated -cne $original) {
        # Reject concurrent edits instead of clobbering another operator's changes.
        $stream = [IO.File]::Open($target.Path, [IO.FileMode]::Open, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
        try {
            $reader = [IO.StreamReader]::new($stream, $utf8, $true, 1024, $true)
            try { $current = $reader.ReadToEnd() } finally { $reader.Dispose() }
            if ($current -cne $original) { throw 'An environment file changed concurrently. Re-run Setup-P5.ps1 to finish synchronization.' }
            $content = $utf8.GetBytes($updated)
            $stream.Position = 0
            $stream.Write($content, 0, $content.Length)
            $stream.SetLength($content.Length)
            $stream.Flush()
        } finally {
            $stream.Dispose()
        }
    }
    Write-Output "$($target.Name) synchronized: $($target.Path)"
}
Write-Output 'P5 credential prepared. Existing DB/JWT/PAT credentials were preserved; no containers or database migrations were run.'
